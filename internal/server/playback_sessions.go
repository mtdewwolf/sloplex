package server

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/transcode"
)

// Timeline-created sessions describe client-reported playback, independently
// of active transcodes. See docs/spec/playback-sessions.md for evidence/limits.
type PlaybackUser struct {
	ID    int    `xml:"id,attr" json:"id"`
	Title string `xml:"title,attr,omitempty" json:"title,omitempty"`
}

type PlaybackPlayer struct {
	MachineIdentifier string `xml:"machineIdentifier,attr" json:"machineIdentifier"`
	State             string `xml:"state,attr" json:"state"`
	Product           string `xml:"product,attr,omitempty" json:"product,omitempty"`
	Platform          string `xml:"platform,attr,omitempty" json:"platform,omitempty"`
	Version           string `xml:"version,attr,omitempty" json:"version,omitempty"`
	Title             string `xml:"title,attr,omitempty" json:"title,omitempty"`
	UserID            int    `xml:"userID,attr" json:"userID"`
}

type playbackIdentity struct {
	Account         int
	Session, Client string
}
type playbackEntry struct {
	Key                                                         int
	Identity                                                    playbackIdentity
	Owner                                                       transcode.Owner
	ItemID, MediaIndex, PartIndex, MediaID, PartID, QueueItemID int
	Position, Duration                                          int64
	HasPosition                                                 bool
	Player                                                      PlaybackPlayer
	Touched                                                     time.Time
}

type playbackStore struct {
	mu      sync.Mutex
	next    int
	entries map[playbackIdentity]playbackEntry
}

const playbackLifetime = 2 * time.Minute
const maxPlaybackSessions = 256
const maxAccountPlaybackSessions = 32

func newPlaybackStore() *playbackStore {
	return &playbackStore{entries: make(map[playbackIdentity]playbackEntry)}
}

func (s *playbackStore) expire(now time.Time) {
	for key, entry := range s.entries {
		if now.Sub(entry.Touched) >= playbackLifetime {
			delete(s.entries, key)
		}
	}
}

// commit serializes playback updates around playstate and queue publication.
// Lock order is playback -> queue -> playstate. Failed saves publish nothing.
func (s *playbackStore) commit(update playbackEntry, save func() bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !save() {
		return false
	}
	now := time.Now()
	s.expire(now)
	previous, exists := s.entries[update.Identity]
	if update.Player.State == "stopped" {
		if exists && previous.ItemID == update.ItemID {
			delete(s.entries, update.Identity)
		}
		return true
	}
	if exists {
		update.Key = previous.Key
		if !update.HasPosition && update.ItemID == previous.ItemID {
			update.Position = previous.Position
		}
	} else {
		count := 0
		var oldestAccount, oldestAll *playbackEntry
		for _, entry := range s.entries {
			if oldestAll == nil || entry.Touched.Before(oldestAll.Touched) {
				e := entry
				oldestAll = &e
			}
			if entry.Identity.Account == update.Identity.Account {
				count++
				if oldestAccount == nil || entry.Touched.Before(oldestAccount.Touched) {
					e := entry
					oldestAccount = &e
				}
			}
		}
		if count >= maxAccountPlaybackSessions {
			delete(s.entries, oldestAccount.Identity)
		}
		if len(s.entries) >= maxPlaybackSessions {
			delete(s.entries, oldestAll.Identity)
		}
		s.next++
		update.Key = s.next
	}
	update.Touched = now
	s.entries[update.Identity] = update
	return true
}

func (s *playbackStore) list(allowed func(playbackEntry) bool) []playbackEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire(time.Now())
	var entries []playbackEntry
	for key, entry := range s.entries {
		if !allowed(entry) {
			delete(s.entries, key)
			continue
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b playbackEntry) int { return a.Key - b.Key })
	return entries
}

func plexClientField(r *http.Request, query url.Values, name string) string {
	if value := r.Header.Get(name); value != "" {
		return value
	}
	return query.Get(name)
}

func (s *Server) reportTimeline(w http.ResponseWriter, r *http.Request) bool {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, "invalid timeline query", http.StatusBadRequest)
		return false
	}
	// Only client-identified reports with a known state create sessions; the
	// legacy GET contract ignores unknown states, so those keep the old path.
	state := query.Get("state")
	client := plexClientField(r, query, "X-Plex-Client-Identifier")
	if !timelineStates[state] || client == "" {
		return s.reportQueueTimeline(w, r)
	}
	for key, values := range query {
		if len(values) != 1 {
			http.Error(w, "duplicate timeline parameter: "+key, http.StatusBadRequest)
			return false
		}
	}
	fields := make(map[string]string)
	for _, name := range []string{"X-Plex-Client-Identifier", "X-Plex-Session-Identifier", "X-Plex-Product", "X-Plex-Platform", "X-Plex-Version", "X-Plex-Device-Name"} {
		value := plexClientField(r, query, name)
		if len(value) > 256 || len(r.Header.Values(name)) > 1 {
			http.Error(w, "invalid "+name, http.StatusBadRequest)
			return false
		}
		fields[name] = value
	}
	item, ok := s.playTarget(w, r)
	if !ok {
		return false
	}
	if item.Unavailable {
		http.NotFound(w, r)
		return false
	}
	if !library.IsLeaf(item.Type) {
		http.Error(w, "playback sessions require movies or episodes", http.StatusBadRequest)
		return false
	}
	caller, _ := principalOf(r)
	update := playbackEntry{Owner: caller.owner(), ItemID: item.ID, Identity: playbackIdentity{Account: caller.AccountID, Client: client}}
	if session := fields["X-Plex-Session-Identifier"]; session != "" {
		update.Identity.Session = session
		update.Identity.Client = ""
	}
	update.Player = PlaybackPlayer{MachineIdentifier: client, State: state, Product: fields["X-Plex-Product"], Platform: fields["X-Plex-Platform"], Version: fields["X-Plex-Version"], Title: fields["X-Plex-Device-Name"], UserID: caller.AccountID}
	if query.Has("time") {
		update.Position, err = strconv.ParseInt(query.Get("time"), 10, 64)
		if err != nil || update.Position < 0 {
			http.Error(w, "invalid timeline time", http.StatusBadRequest)
			return false
		}
		update.HasPosition = true
	}
	for _, field := range []struct {
		name string
		dest *int
	}{{"mediaIndex", &update.MediaIndex}, {"partIndex", &update.PartIndex}} {
		if query.Has(field.name) {
			*field.dest, err = strconv.Atoi(query.Get(field.name))
			if err != nil || *field.dest < 0 {
				http.Error(w, "invalid "+field.name, http.StatusBadRequest)
				return false
			}
		}
	}
	if update.MediaIndex >= len(item.Media) || update.PartIndex >= len(item.Media[update.MediaIndex].Parts) {
		http.Error(w, "invalid playback media or part index", http.StatusBadRequest)
		return false
	}
	update.MediaID = item.Media[update.MediaIndex].ID
	update.PartID = item.Media[update.MediaIndex].Parts[update.PartIndex].ID
	if !s.store.PartAvailable(update.PartID) {
		http.NotFound(w, r)
		return false
	}
	update.Duration = item.Media[update.MediaIndex].Duration
	if value := query.Get("duration"); value != "" {
		duration, err := strconv.ParseInt(value, 10, 64)
		if err != nil || duration < 0 {
			http.Error(w, "invalid timeline duration", http.StatusBadRequest)
			return false
		}
		if duration > 0 {
			update.Duration = duration
		}
	}
	update.QueueItemID, _ = strconv.Atoi(query.Get("playQueueItemID"))
	return s.playback.commit(update, func() bool { return s.reportQueueTimeline(w, r) })
}

func (s *Server) handlePlaybackSessions(w http.ResponseWriter, r *http.Request) {
	entries := s.playback.list(func(entry playbackEntry) bool {
		return s.sessionAllowed(entry.Owner, entry.ItemID) && s.store.PartAvailable(entry.PartID)
	})
	mc := MediaContainer{}
	for _, entry := range entries {
		item, err := s.store.Item(entry.ItemID)
		if err != nil || item.Unavailable {
			continue
		}
		account, err := s.accounts.Account(entry.Owner.Account)
		if err != nil || !account.CanSee(item.SectionID) {
			continue
		}
		md := s.metadata(item, entry.Owner.Account)
		md.SessionKey = strconv.Itoa(entry.Key)
		md.ViewOffset, md.Duration = entry.Position, entry.Duration
		md.PlayQueueItemID = entry.QueueItemID
		md.User = &PlaybackUser{ID: account.ID, Title: account.Name}
		player := entry.Player
		md.Player = &player
		mi := slices.IndexFunc(md.Media, func(media Media) bool { return media.ID == entry.MediaID })
		if mi < 0 {
			continue
		}
		pi := slices.IndexFunc(md.Media[mi].Part, func(part Part) bool { return part.ID == entry.PartID })
		if pi >= 0 {
			media := md.Media[mi]
			media.Part = []Part{media.Part[pi]}
			md.Media = []Media{media}
		} else {
			continue
		}
		mc.Metadata = append(mc.Metadata, md)
	}
	writeContainer(w, r, mc)
}
