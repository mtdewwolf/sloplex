package server

import (
	"encoding/xml"
	"errors"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PlayQueueInfo is flattened into the XML and JSON MediaContainer envelope.
type PlayQueueInfo struct {
	ID                     int    `xml:"playQueueID,attr" json:"playQueueID"`
	QueueVersion           int    `xml:"playQueueVersion,attr" json:"playQueueVersion"`
	TotalCount             int    `xml:"playQueueTotalCount,attr" json:"playQueueTotalCount"`
	SelectedItemID         int    `xml:"playQueueSelectedItemID,attr" json:"playQueueSelectedItemID"`
	SelectedMetadataItemID int    `xml:"playQueueSelectedMetadataItemID,attr" json:"playQueueSelectedMetadataItemID"`
	SelectedItemOffset     int    `xml:"playQueueSelectedItemOffset,attr" json:"playQueueSelectedItemOffset"`
	Shuffled               Bool   `xml:"playQueueShuffled,attr" json:"playQueueShuffled"`
	SourceURI              string `xml:"playQueueSourceURI,attr,omitempty" json:"playQueueSourceURI,omitempty"`
	LastAddedItemID        int    `xml:"playQueueLastAddedItemID,attr,omitempty" json:"playQueueLastAddedItemID,omitempty"`
}

func (q *PlayQueueInfo) xmlAttrs() []xml.Attr {
	var attrs []xml.Attr
	for _, field := range []struct {
		name  string
		value int
	}{
		{"playQueueID", q.ID}, {"playQueueVersion", q.QueueVersion},
		{"playQueueTotalCount", q.TotalCount}, {"playQueueSelectedItemID", q.SelectedItemID},
		{"playQueueSelectedMetadataItemID", q.SelectedMetadataItemID}, {"playQueueSelectedItemOffset", q.SelectedItemOffset},
	} {
		attrs = append(attrs, xml.Attr{Name: xml.Name{Local: field.name}, Value: strconv.Itoa(field.value)})
	}
	shuffled := "0"
	if q.Shuffled {
		shuffled = "1"
	}
	attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "playQueueShuffled"}, Value: shuffled})
	if q.SourceURI != "" {
		attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "playQueueSourceURI"}, Value: q.SourceURI})
	}
	if q.LastAddedItemID != 0 {
		attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "playQueueLastAddedItemID"}, Value: strconv.Itoa(q.LastAddedItemID)})
	}
	return attrs
}

type queueEntry struct{ ID, ItemID int }
type playQueue struct {
	ID, Owner, Version, Selected int
	LastAdded                    int
	Source                       string
	Shuffled                     bool
	Entries, Natural             []queueEntry
	Touched                      time.Time
}

func (q playQueue) clone() playQueue {
	q.Entries = slices.Clone(q.Entries)
	q.Natural = slices.Clone(q.Natural)
	return q
}

// Queues are ephemeral, bounded, and protected by one lock. A mutation validates
// and edits a copy, so a failed request cannot partially change the live queue.
type queueStore struct {
	mu                   sync.Mutex
	nextQueue, nextEntry int
	queues               map[int]playQueue
}

func newQueueStore() *queueStore { return &queueStore{queues: make(map[int]playQueue)} }

const maxQueueItems = 10000
const maxAccountQueues = 32
const maxServerQueues = 256
const queueIdleLifetime = time.Hour

var errQueueMissing = errors.New("play queue or entry not found")
var errQueueLimit = errors.New("play queue may contain at most 10000 items")
var errTimelineSave = errors.New("timeline playback state could not be saved")

// commitTimeline validates an occurrence and saves playstate before publishing
// selection. No other path takes the queue lock while holding a playstate lock.
func (s *queueStore) commitTimeline(owner, queueID, entryID, itemID int, save func() bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.expire(now)
	var queue playQueue
	found := false
	for id, candidate := range s.queues {
		if candidate.Owner != owner || queueID != 0 && id != queueID {
			continue
		}
		if pos := queueIndex(candidate.Entries, entryID); pos >= 0 {
			if candidate.Entries[pos].ItemID != itemID {
				return errors.New("playQueueItemID does not match the reported metadata item")
			}
			queue, found = candidate, true
			break
		}
	}
	if !found {
		return errQueueMissing
	}
	if !save() {
		return errTimelineSave
	}
	if queue.Selected != entryID {
		queue.Selected = entryID
	}
	if queueIndex(queue.Entries, entryID) > queueIndex(queue.Entries, queue.LastAdded) {
		queue.LastAdded = 0
	}
	queue.Touched = now
	s.queues[queue.ID] = queue
	return nil
}

func (s *Server) reportQueueTimeline(w http.ResponseWriter, r *http.Request) bool {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		queueError(w, r, errors.New("invalid timeline query encoding"))
		return false
	}
	if !query.Has("playQueueItemID") && !query.Has("playQueueID") {
		return s.reportPosition(w, r, false)
	}
	for key, values := range query {
		if len(values) != 1 {
			queueError(w, r, errors.New("duplicate timeline parameter: "+key))
			return false
		}
	}
	entryID, err := strconv.Atoi(query.Get("playQueueItemID"))
	if err != nil || entryID <= 0 {
		queueError(w, r, errors.New("playQueueItemID must identify a queue occurrence"))
		return false
	}
	queueID := 0
	if query.Has("playQueueID") {
		queueID, err = strconv.Atoi(query.Get("playQueueID"))
		if err != nil || queueID <= 0 {
			queueError(w, r, errors.New("invalid playQueueID"))
			return false
		}
	}
	if state := query.Get("state"); state != "" && !timelineStates[state] {
		queueError(w, r, errors.New("invalid timeline state"))
		return false
	}
	item, ok := s.playTarget(w, r)
	if !ok {
		return false
	}
	if item.Unavailable {
		http.NotFound(w, r)
		return false
	}
	err = s.queues.commitTimeline(s.account(r), queueID, entryID, item.ID, func() bool {
		return s.reportPosition(w, r, false)
	})
	if errors.Is(err, errTimelineSave) {
		return false // reportPosition already wrote the error.
	}
	if err != nil {
		queueError(w, r, err)
		return false
	}
	return true
}

func (s *queueStore) expire(now time.Time) {
	for id, q := range s.queues {
		if now.Sub(q.Touched) >= queueIdleLifetime {
			delete(s.queues, id)
		}
	}
}
func (s *queueStore) create(owner int, source string, ids []int, selected int, shuffle bool) (playQueue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(ids) > maxQueueItems {
		return playQueue{}, errQueueLimit
	}
	now := time.Now()
	s.expire(now)
	count := 0
	oldestAccount, oldestAll := 0, 0
	for id, q := range s.queues {
		if oldestAll == 0 || q.Touched.Before(s.queues[oldestAll].Touched) {
			oldestAll = id
		}
		if q.Owner == owner {
			count++
			if oldestAccount == 0 || q.Touched.Before(s.queues[oldestAccount].Touched) {
				oldestAccount = id
			}
		}
	}
	if count >= maxAccountQueues {
		delete(s.queues, oldestAccount)
	}
	if len(s.queues) >= maxServerQueues {
		delete(s.queues, oldestAll)
	}
	s.nextQueue++
	q := playQueue{ID: s.nextQueue, Owner: owner, Version: 1, Source: source, Touched: now}
	for _, id := range ids {
		s.nextEntry++
		q.Entries = append(q.Entries, queueEntry{s.nextEntry, id})
	}
	if len(q.Entries) > 0 {
		q.Selected = q.Entries[0].ID
		for _, e := range q.Entries {
			if e.ItemID == selected {
				q.Selected = e.ID
				break
			}
		}
	}
	q.Natural = slices.Clone(q.Entries)
	if shuffle {
		rand.Shuffle(len(q.Entries), func(i, j int) { q.Entries[i], q.Entries[j] = q.Entries[j], q.Entries[i] })
		q.Shuffled = true
	}
	s.queues[q.ID] = q
	return q.clone(), nil
}
func (s *queueStore) edit(owner, id int, change func(*playQueue) error) (playQueue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.expire(now)
	q, ok := s.queues[id]
	if !ok || q.Owner != owner {
		return playQueue{}, errQueueMissing
	}
	q = q.clone()
	if change != nil {
		if err := change(&q); err != nil {
			return playQueue{}, err
		}
		q.Version++
	}
	q.Touched = now
	s.queues[id] = q
	return q.clone(), nil
}
func queueIndex(entries []queueEntry, id int) int {
	return slices.IndexFunc(entries, func(e queueEntry) bool { return e.ID == id })
}
func moveQueueEntry(entries []queueEntry, id, after int) []queueEntry {
	pos := queueIndex(entries, id)
	entry := entries[pos]
	entries = slices.Delete(entries, pos, pos+1)
	at := 0
	if after != 0 {
		at = queueIndex(entries, after) + 1
	}
	return slices.Insert(entries, at, entry)
}
func (s *Server) playQueueRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /playQueues", s.handleCreateQueue)
	mux.HandleFunc("GET /playQueues/{id}", s.handleGetQueue)
	mux.HandleFunc("PUT /playQueues/{id}", s.handleAddQueue)
	mux.HandleFunc("DELETE /playQueues/{id}/items", s.handleClearQueue)
	mux.HandleFunc("DELETE /playQueues/{id}/items/{entry}", s.handleRemoveQueue)
	mux.HandleFunc("PUT /playQueues/{id}/items/{entry}/move", s.handleMoveQueue)
	mux.HandleFunc("PUT /playQueues/{id}/reset", s.handleResetQueue)
	mux.HandleFunc("PUT /playQueues/{id}/shuffle", s.handleShuffleQueue)
	mux.HandleFunc("PUT /playQueues/{id}/unshuffle", s.handleUnshuffleQueue)
}

func queueQuery(r *http.Request) (url.Values, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, errors.New("invalid query encoding")
	}
	for k, v := range q {
		if len(v) != 1 {
			return nil, errors.New("duplicate parameter: " + k)
		}
	}
	allowed := ""
	switch {
	case r.Method == http.MethodPost:
		allowed = "type uri playlistID key shuffle repeat continuous extrasPrefixCount recursive onDeck"
	case r.Method == http.MethodGet:
		allowed = "own center window includeBefore includeAfter"
	case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/move"):
		allowed = "after"
	case r.Method == http.MethodPut && r.Pattern == "PUT /playQueues/{id}":
		allowed = "uri playlistID next"
	}
	for _, key := range strings.Fields("type uri playlistID key shuffle repeat continuous extrasPrefixCount recursive onDeck own center window includeBefore includeAfter next after") {
		if q.Has(key) && !slices.Contains(strings.Fields(allowed), key) {
			return nil, errors.New(key + " is not supported on this operation")
		}
	}
	for _, key := range []string{"shuffle", "repeat", "continuous", "recursive", "onDeck", "own", "includeBefore", "includeAfter", "next"} {
		if v, ok := q[key]; ok && v[0] != "0" && v[0] != "1" {
			return nil, errors.New(key + " must be 0 or 1")
		}
	}
	for _, key := range []string{"repeat", "continuous", "onDeck", "own"} {
		if q.Get(key) == "1" {
			return nil, errors.New(key + " is not supported")
		}
	}
	if q.Get("includeBefore") == "0" && q.Get("includeAfter") == "0" {
		return nil, errors.New("includeBefore and includeAfter cannot both be disabled")
	}
	if q.Has("extrasPrefixCount") && q.Get("extrasPrefixCount") != "0" {
		return nil, errors.New("trailers are not supported")
	}
	return q, nil
}

// queueSource resolves only local metadata URIs and the caller's playlists;
// remote authorities and arbitrary URLs are never fetched.
func (s *Server) queueSource(r *http.Request, q url.Values) ([]int, string, error) {
	uri, playlist := q.Get("uri"), q.Get("playlistID")
	if (uri == "") == (playlist == "") {
		return nil, "", errors.New("specify exactly one of uri or playlistID")
	}
	if playlist != "" {
		id, err := strconv.Atoi(playlist)
		if err != nil {
			return nil, "", errors.New("invalid playlistID")
		}
		p, err := s.playlists.Get(s.account(r), id)
		if err != nil {
			return nil, "", errQueueMissing
		}
		var ids []int
		for _, e := range s.visibleEntries(r, p) {
			if !e.item.Unavailable {
				ids = append(ids, e.item.ID)
			}
		}
		return ids, "", nil
	}
	u, err := url.Parse(uri)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, "", errors.New("invalid local content URI")
	}
	section := 0
	path := u.Path
	switch u.Scheme {
	case "":
		if u.Host != "" {
			return nil, "", errors.New("remote content URI is not supported")
		}
	case "server":
		if u.Host != s.opts.MachineID || !strings.HasPrefix(path, "/com.plexapp.plugins.library/") {
			return nil, "", errors.New("URI must identify this server's library")
		}
		path = strings.TrimPrefix(path, "/com.plexapp.plugins.library")
	case "library":
		for _, sec := range s.store.Sections() {
			if sec.UUID == u.Host {
				section = sec.ID
				break
			}
		}
		if section == 0 || !strings.HasPrefix(path, "/item/") {
			return nil, "", errors.New("URI must identify a local library item")
		}
		path = strings.TrimPrefix(path, "/item/")
	default:
		return nil, "", errors.New("remote content URI is not supported")
	}
	if !strings.HasPrefix(path, "/library/metadata/") {
		return nil, "", errors.New("URI must identify library metadata")
	}
	raw := strings.TrimPrefix(path, "/library/metadata/")
	parts := strings.Split(raw, ",")
	if len(parts) > maxQueueItems {
		return nil, "", errQueueLimit
	}
	for _, v := range parts {
		id, err := strconv.Atoi(v)
		if err != nil || id <= 0 {
			return nil, "", errors.New("invalid metadata ID")
		}
		it, err := s.store.Item(id)
		if err != nil || !s.visibility(r)(it.SectionID) || it.Unavailable || section != 0 && it.SectionID != section {
			return nil, "", errQueueMissing
		}
	}
	copyRequest := r.Clone(r.Context())
	copyURL := *r.URL
	copyRequest.URL = &copyURL
	copyRequest.URL.RawQuery = url.Values{"ids": {raw}}.Encode()
	ids, err := s.playlistItems(copyRequest)
	if err != nil {
		return nil, "", err
	}
	playable := ids[:0]
	for _, id := range ids {
		if it, err := s.store.Item(id); err == nil && !it.Unavailable {
			playable = append(playable, id)
		}
	}
	if len(playable) > maxQueueItems {
		return nil, "", errQueueLimit
	}
	return playable, uri, nil
}
func queueError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errQueueMissing) {
		http.NotFound(w, r)
	} else {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}
func (s *Server) handleCreateQueue(w http.ResponseWriter, r *http.Request) {
	q, err := queueQuery(r)
	if err != nil {
		queueError(w, r, err)
		return
	}
	if q.Get("type") != "video" {
		queueError(w, r, errors.New("only video play queues are supported"))
		return
	}
	ids, source, err := s.queueSource(r, q)
	if err != nil {
		queueError(w, r, err)
		return
	}
	selected := 0
	if key := q.Get("key"); key != "" {
		selected, err = strconv.Atoi(strings.TrimPrefix(key, "/library/metadata/"))
		if err != nil || !slices.Contains(ids, selected) {
			queueError(w, r, errors.New("key must select an item in the queue"))
			return
		}
	}
	if len(ids) == 0 {
		queueError(w, r, errors.New("source contains no playable items"))
		return
	}
	pq, err := s.queues.create(s.account(r), source, ids, selected, q.Get("shuffle") == "1")
	if err != nil {
		queueError(w, r, err)
		return
	}
	s.writeQueue(w, r, pq)
}
func (s *Server) editQueue(w http.ResponseWriter, r *http.Request, change func(*playQueue) error) {
	if _, err := queueQuery(r); err != nil {
		queueError(w, r, err)
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	pq, err := s.queues.edit(s.account(r), id, change)
	if err != nil {
		queueError(w, r, err)
		return
	}
	s.writeQueue(w, r, pq)
}
func (s *Server) handleGetQueue(w http.ResponseWriter, r *http.Request) {
	q, err := queueQuery(r)
	if err != nil {
		queueError(w, r, err)
		return
	}
	for _, key := range []string{"center", "window"} {
		if q.Has(key) {
			n, err := strconv.Atoi(q.Get(key))
			if err != nil || n < 0 || key == "center" && n == 0 {
				queueError(w, r, errors.New("invalid "+key))
				return
			}
		}
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pq, err := s.queues.edit(s.account(r), id, nil)
	if err != nil {
		queueError(w, r, err)
		return
	}
	if q.Has("center") {
		center, _ := strconv.Atoi(q.Get("center"))
		pos := queueIndex(pq.Entries, center)
		if pos < 0 {
			http.NotFound(w, r)
			return
		}
		it, err := s.store.Item(pq.Entries[pos].ItemID)
		if err != nil || !s.visibility(r)(it.SectionID) || it.Unavailable {
			http.NotFound(w, r)
			return
		}
	}
	s.writeQueue(w, r, pq)
}
func (s *Server) handleAddQueue(w http.ResponseWriter, r *http.Request) {
	q, err := queueQuery(r)
	if err != nil {
		queueError(w, r, err)
		return
	}
	// Check ownership before resolving a source or assigning any entry IDs.
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err = s.queues.edit(s.account(r), id, nil); err != nil {
		queueError(w, r, err)
		return
	}
	ids, _, err := s.queueSource(r, q)
	if err != nil {
		queueError(w, r, err)
		return
	}
	if len(ids) == 0 {
		queueError(w, r, errors.New("source contains no playable items"))
		return
	}
	s.editQueue(w, r, func(pq *playQueue) error {
		if len(pq.Entries)+len(ids) > maxQueueItems {
			return errQueueLimit
		}
		entries := make([]queueEntry, 0, len(ids))
		for _, item := range ids {
			s.queues.nextEntry++
			entries = append(entries, queueEntry{s.queues.nextEntry, item})
		}
		anchor := pq.Selected
		if q.Get("next") != "1" && queueIndex(pq.Entries, pq.LastAdded) > queueIndex(pq.Entries, anchor) {
			anchor = pq.LastAdded
		}
		at := queueIndex(pq.Entries, anchor) + 1
		naturalAt := queueIndex(pq.Natural, anchor) + 1
		pq.Entries = slices.Insert(pq.Entries, at, entries...)
		pq.Natural = slices.Insert(pq.Natural, naturalAt, entries...)
		if q.Get("next") != "1" || queueIndex(pq.Entries, pq.LastAdded) <= queueIndex(pq.Entries, pq.Selected) {
			pq.LastAdded = entries[len(entries)-1].ID
		}
		if pq.Selected == 0 {
			pq.Selected = entries[0].ID
		}
		return nil
	})
}
func (s *Server) handleClearQueue(w http.ResponseWriter, r *http.Request) {
	s.editQueue(w, r, func(q *playQueue) error {
		q.LastAdded = 0
		if selected := queueIndex(q.Entries, q.Selected); selected >= 0 {
			q.Entries = []queueEntry{q.Entries[selected]}
			q.Natural = slices.Clone(q.Entries)
		} else {
			q.Entries, q.Natural = nil, nil
			q.Selected = 0
		}
		return nil
	})
}
func (s *Server) handleRemoveQueue(w http.ResponseWriter, r *http.Request) {
	entry, err := strconv.Atoi(r.PathValue("entry"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.editQueue(w, r, func(q *playQueue) error {
		pos := queueIndex(q.Entries, entry)
		if pos < 0 {
			return errQueueMissing
		}
		if q.LastAdded == entry {
			q.LastAdded = 0
			if pos > 0 && pos-1 > queueIndex(q.Entries, q.Selected) {
				q.LastAdded = q.Entries[pos-1].ID
			}
		}
		q.Entries = slices.Delete(q.Entries, pos, pos+1)
		q.Natural = slices.DeleteFunc(q.Natural, func(e queueEntry) bool { return e.ID == entry })
		if q.Selected == entry {
			q.Selected = 0
			if len(q.Entries) > 0 {
				q.Selected = q.Entries[min(pos, len(q.Entries)-1)].ID
			}
		}
		return nil
	})
}
func (s *Server) handleMoveQueue(w http.ResponseWriter, r *http.Request) {
	entry, err := strconv.Atoi(r.PathValue("entry"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	q, err := queueQuery(r)
	if err != nil {
		queueError(w, r, err)
		return
	}
	after := 0
	if q.Has("after") {
		after, err = strconv.Atoi(q.Get("after"))
		if err != nil || after < 0 {
			queueError(w, r, errors.New("invalid after"))
			return
		}
	}
	s.editQueue(w, r, func(pq *playQueue) error {
		if queueIndex(pq.Entries, entry) < 0 {
			return errQueueMissing
		}
		if after == entry || after != 0 && queueIndex(pq.Entries, after) < 0 {
			return errors.New("after must identify another queue entry")
		}
		pq.Entries = moveQueueEntry(pq.Entries, entry, after)
		pq.Natural = moveQueueEntry(pq.Natural, entry, after)
		return nil
	})
}
func (s *Server) handleResetQueue(w http.ResponseWriter, r *http.Request) {
	s.editQueue(w, r, func(q *playQueue) error {
		q.Selected = 0
		if len(q.Entries) > 0 {
			q.Selected = q.Entries[0].ID
		}
		return nil
	})
}
func (s *Server) handleShuffleQueue(w http.ResponseWriter, r *http.Request) {
	s.editQueue(w, r, func(q *playQueue) error {
		rand.Shuffle(len(q.Entries), func(i, j int) { q.Entries[i], q.Entries[j] = q.Entries[j], q.Entries[i] })
		q.Shuffled = true
		return nil
	})
}
func (s *Server) handleUnshuffleQueue(w http.ResponseWriter, r *http.Request) {
	s.editQueue(w, r, func(q *playQueue) error { q.Entries = slices.Clone(q.Natural); q.Shuffled = false; return nil })
}

func (s *Server) writeQueue(w http.ResponseWriter, r *http.Request, q playQueue) {
	info := &PlayQueueInfo{ID: q.ID, QueueVersion: q.Version, SourceURI: q.Source, Shuffled: Bool(q.Shuffled)}
	mc := MediaContainer{PlayQueueInfo: info}
	see := s.visibility(r)
	acct := s.account(r)
	for _, e := range q.Entries {
		it, err := s.store.Item(e.ItemID)
		if err != nil || !see(it.SectionID) || it.Unavailable {
			continue
		}
		md := s.metadata(it, acct)
		md.PlayQueueItemID = e.ID
		mc.Metadata = append(mc.Metadata, md)
		if e.ID == q.LastAdded {
			info.LastAddedItemID = e.ID
		}
		if e.ID == q.Selected {
			info.SelectedItemID = e.ID
			info.SelectedMetadataItemID = it.ID
			info.SelectedItemOffset = len(mc.Metadata) - 1
		}
	}
	if len(mc.Metadata) > 0 && info.SelectedItemID == 0 {
		info.SelectedItemID = mc.Metadata[0].PlayQueueItemID
		info.SelectedMetadataItemID, _ = strconv.Atoi(mc.Metadata[0].RatingKey)
	}
	info.TotalCount = len(mc.Metadata)
	start, end := 0, len(mc.Metadata)
	query := r.URL.Query()
	center := info.SelectedItemOffset
	if query.Has("center") {
		id, _ := strconv.Atoi(query.Get("center"))
		center = slices.IndexFunc(mc.Metadata, func(md Metadata) bool { return md.PlayQueueItemID == id })
		if center < 0 {
			http.NotFound(w, r)
			return
		}
	}
	if query.Has("window") {
		window, _ := strconv.Atoi(query.Get("window"))
		start = max(0, center-min(window, center))
		end = min(len(mc.Metadata), center+1+min(window, max(0, len(mc.Metadata)-center-1)))
		if window == 0 {
			end = start
		}
	}
	if query.Get("includeBefore") == "0" {
		start = min(center+1, len(mc.Metadata))
	}
	if query.Get("includeAfter") == "0" {
		end = max(0, center)
	}
	if start > end {
		start = end
	}
	mc.Metadata = mc.Metadata[start:end]
	// Container paging applies within the requested queue window.
	a, b, paged := pageBounds(r, len(mc.Metadata), -1)
	mc.Metadata = mc.Metadata[a:b]
	if paged || query.Has("window") || query.Has("includeBefore") || query.Has("includeAfter") {
		total := info.TotalCount
		offset := start + a
		mc.TotalSize = &total
		mc.Offset = &offset
	}
	writeContainer(w, r, mc)
}
