package library

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/playlists"
	"github.com/mtdewwolf/sloplex/internal/playstate"
	"github.com/mtdewwolf/sloplex/internal/probe"
)

func TestTrashRetainsAndRevivesHierarchy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncSections([]SectionDef{{Title: "TV", Type: TypeShow, Paths: []string{"/tv"}}}, t0); err != nil {
		t.Fatal(err)
	}
	sec := s.Sections()[0].ID
	f := episodeFile("/tv/Example/S01E01.mkv", "Example", 1, 1, "Pilot")
	f.NFO = &Meta{Title: "Known episode", Summary: "Retained summary"}
	f.ShowNFO = &Meta{Title: "Display show", Summary: "Known show summary"}
	if _, err := s.ApplyScan(sec, []ScannedFile{f}, t0); err != nil {
		t.Fatal(err)
	}
	before := s.Items(sec)
	c, err := s.CreateCollection(sec, "Keep", []int{s.Items(sec, TypeShow)[0].ID}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyScan(sec, nil, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, old := range before {
		if got, err := s.Item(old.ID); err != nil || !got.Unavailable {
			t.Errorf("missing file deleted %s ID %d: %v", old.Type, old.ID, err)
		}
	}
	got, _ := s.Collection(c.ID)
	if !reflect.DeepEqual(got.ItemIDs, c.ItemIDs) {
		t.Errorf("lost manual collection membership: %+v", got)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyScan(sec, []ScannedFile{f}, t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, old := range before {
		got, err := s.Item(old.ID)
		if err != nil || got.Title != old.Title || got.Unavailable || !reflect.DeepEqual(got.Meta, old.Meta) {
			t.Errorf("revival lost %s ID %d: %+v, %v", old.Type, old.ID, got, err)
		}
	}
}

func TestTrashMixedVersionsAndExplicitPurge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.json")
	s, sec := newStore(t, path)
	a, b := file("/m/a.mkv", "Film", 2000), file("/m/b.mkv", "Film", 2000)
	a.NFO = &Meta{Title: "Display title", Summary: "Known summary", Collections: []string{"Set"}}
	a.Info.Width = 3840
	a.Info.Streams = []probe.Stream{{Index: 0, Type: probe.StreamVideo, Codec: "hevc"}}
	a.Sidecars = []Stream{{Stream: probe.Stream{Type: probe.StreamSubtitle, Codec: "srt"}, File: "/m/a.en.srt"}}
	if _, err := s.ApplyScan(sec, []ScannedFile{a, b}, t0); err != nil {
		t.Fatal(err)
	}
	before := s.Items(sec)[0]
	c, err := s.CreateCollection(sec, "Manual", []int{before.ID}, t0)
	if err != nil {
		t.Fatal(err)
	}
	b.Changed = false
	if _, err := s.ApplyScan(sec, []ScannedFile{b}, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	mixed, _ := s.Item(before.ID)
	if mixed.Unavailable || len(mixed.Media) != 2 || mixed.Media[0].ID != before.Media[1].ID || !mixed.Media[0].Available() || mixed.Media[1].Available() {
		t.Fatalf("missing version hid healthy version: %+v", mixed)
	}
	if mixed.Title != before.Title || !reflect.DeepEqual(mixed.Meta, before.Meta) || mixed.Media[1].Width != 3840 || !reflect.DeepEqual(mixed.Media[1].Parts[0].Streams, before.Media[0].Parts[0].Streams) {
		t.Fatal("missing version lost known metadata/streams")
	}
	if s.PartAvailable(mixed.Media[1].Parts[0].ID) || !s.PartAvailable(mixed.Media[0].Parts[0].ID) {
		t.Fatal("part availability did not follow version")
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a.Changed = false
	if _, err := s.ApplyScan(sec, []ScannedFile{a, b}, t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	revived, _ := s.Item(before.ID)
	for i, m := range revived.Media {
		if m.ID != before.Media[i].ID || m.Parts[0].ID != before.Media[i].Parts[0].ID || !m.Available() || !reflect.DeepEqual(m.Parts[0].Streams, before.Media[i].Parts[0].Streams) {
			t.Errorf("version IDs/streams changed on revival: %+v", m)
		}
	}
	if _, err := s.ApplyScan(sec, []ScannedFile{b}, t0.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if stats, err := s.EmptyTrash(sec, t0.Add(4*time.Hour)); err != nil || stats.Removed != 0 || stats.Updated != 1 {
		t.Fatalf("partial purge: %+v, %v", stats, err)
	}
	healthy, _ := s.Item(before.ID)
	if len(healthy.Media) != 1 || healthy.Media[0].ID != before.Media[1].ID {
		t.Fatal("purge lost healthy version")
	}
	if got, _ := s.Collection(c.ID); !reflect.DeepEqual(got.ItemIDs, c.ItemIDs) {
		t.Fatal("partial purge lost collection membership")
	}
	if _, err := s.ApplyScan(sec, nil, t0.Add(5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if stats, err := s.EmptyTrash(sec, t0.Add(6*time.Hour)); err != nil || stats.Removed != 1 {
		t.Fatalf("final purge: %+v, %v", stats, err)
	}
	if _, err := s.Item(before.ID); err != ErrNotFound {
		t.Fatal("purged leaf still resolves")
	}
	if got, err := s.Collection(c.ID); err != nil || len(got.ItemIDs) != 0 {
		t.Fatal("manual collection identity/membership after purge")
	}
	if _, _, err := s.Part(before.Media[1].Parts[0].ID); err != ErrNotFound {
		t.Fatal("purged part still resolves")
	}
	if _, err := s.ApplyScan(sec, []ScannedFile{b}, t0.Add(7*time.Hour)); err != nil {
		t.Fatal(err)
	}
	fresh := s.Items(sec)[0]
	if fresh.ID == before.ID || fresh.Media[0].ID == before.Media[1].ID || fresh.Media[0].Parts[0].ID == before.Media[1].Parts[0].ID {
		t.Fatal("purged IDs reused")
	}
}

func TestTrashPurgeHierarchyAndSectionIsolation(t *testing.T) {
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncSections([]SectionDef{{Title: "TV", Type: TypeShow, Paths: []string{"/tv"}}, {Title: "Movies", Type: TypeMovie, Paths: []string{"/m"}}}, t0); err != nil {
		t.Fatal(err)
	}
	sec, other := s.Sections()[0].ID, s.Sections()[1].ID
	files := []ScannedFile{episodeFile("/tv/Show/S01E01.mkv", "Show", 1, 1, "A"), episodeFile("/tv/Show/S02E01.mkv", "Show", 2, 1, "B")}
	if _, err := s.ApplyScan(sec, files, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyScan(other, []ScannedFile{file("/m/movie.mkv", "Movie", 2000)}, t0); err != nil {
		t.Fatal(err)
	}
	otherBefore := s.Items(other)
	show := s.Items(sec, TypeShow)[0]
	if _, err := s.ApplyScan(sec, files[:1], t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if showNow, _ := s.Item(show.ID); showNow.Unavailable || len(s.Children(show.ID)) != 2 {
		t.Fatal("partial outage hid hierarchy")
	}
	if _, err := s.EmptyTrash(sec, t0); err != nil {
		t.Fatal(err)
	}
	if len(s.Children(show.ID)) != 1 || len(s.Items(sec, TypeEpisode)) != 1 {
		t.Fatal("empty season was not purged")
	}
	if _, err := s.ApplyScan(sec, nil, t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EmptyTrash(sec, t0); err != nil {
		t.Fatal(err)
	}
	if len(s.Items(sec)) != 0 {
		t.Fatal("empty hierarchy not purged")
	}
	if !reflect.DeepEqual(s.Items(other), otherBefore) {
		t.Fatal("purge changed another section")
	}
	if stats, err := s.EmptyTrash(sec, t0); err != nil || stats.Removed != 0 {
		t.Fatal("repeat purge not idempotent")
	}
	if _, err := s.EmptyTrash(999, t0); err != ErrNotFound {
		t.Fatal("unknown section accepted")
	}
}

func TestTrashPersistenceFailureDoesNotPublish(t *testing.T) {
	s, sec := newStore(t, "")
	f := file("/m/a.mkv", "A", 2000)
	if _, err := s.ApplyScan(sec, []ScannedFile{f}, t0); err != nil {
		t.Fatal(err)
	}
	before := s.Items(sec)
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	s.path = filepath.Join(blocker, "library.json")
	if _, err := s.ApplyScan(sec, nil, t0.Add(time.Hour)); err == nil {
		t.Fatal("scan accepted failed persistence")
	}
	if !reflect.DeepEqual(s.Items(sec), before) {
		t.Fatal("failed scan published unavailable state")
	}
	s.path = ""
	if _, err := s.ApplyScan(sec, nil, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	before = s.Items(sec)
	s.path = filepath.Join(blocker, "library.json")
	if _, err := s.EmptyTrash(sec, t0); err == nil {
		t.Fatal("purge accepted failed persistence")
	}
	if !reflect.DeepEqual(s.Items(sec), before) {
		t.Fatal("failed purge published deletion")
	}
}

func TestTrashRestartPreservesPlaylistAndPlaystateReferences(t *testing.T) {
	root := t.TempDir()
	s, sec := newStore(t, filepath.Join(root, "library.json"))
	f := file("/m/a.mkv", "A", 2000)
	f.Info.Streams = []probe.Stream{{Index: 1, Type: probe.StreamAudio, Codec: "aac"}}
	if _, err := s.ApplyScan(sec, []ScannedFile{f}, t0); err != nil {
		t.Fatal(err)
	}
	before := s.Items(sec)[0]
	part := before.Media[0].Parts[0]
	playPath, listPath := filepath.Join(root, "playstate.json"), filepath.Join(root, "playlists.json")
	play, err := playstate.Open(playPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := play.Update(42, []int{before.ID}, func(st *playstate.State) { st.ViewCount = 2; st.ViewOffset = 700; st.LastViewedAt = t0.Unix() }); err != nil {
		t.Fatal(err)
	}
	if err := play.SetSelection(42, part.ID, func(sel *playstate.Selection) { sel.Audio = part.Streams[0].ID; sel.Subtitle = playstate.NoSubtitle }); err != nil {
		t.Fatal(err)
	}
	lists, err := playlists.Open(listPath)
	if err != nil {
		t.Fatal(err)
	}
	list, err := lists.Create(42, "Keep", []int{before.ID}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyScan(sec, nil, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	s, err = Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	play, err = playstate.Open(playPath)
	if err != nil {
		t.Fatal(err)
	}
	lists, err = playlists.Open(listPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := lists.Get(42, list.ID); err != nil || !reflect.DeepEqual(got, list) {
		t.Fatal("restart lost playlist entries")
	}
	if it, err := s.Item(list.Entries[0].ItemID); err != nil || !it.Unavailable {
		t.Fatal("playlist no longer resolves unavailable item")
	}
	if _, err := s.ApplyScan(sec, []ScannedFile{f}, t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	revived, _ := s.Item(before.ID)
	if revived.Media[0].Parts[0].ID != part.ID || revived.Media[0].Parts[0].Streams[0].ID != part.Streams[0].ID {
		t.Fatal("revival lost stream selection identities")
	}
	if got := play.Get(42, before.ID); got.ViewCount != 2 || got.ViewOffset != 700 || got.LastViewedAt != t0.Unix() {
		t.Fatal("revival lost watched/resume state")
	}
	if got := play.Selection(42, part.ID); got.Audio != part.Streams[0].ID || got.Subtitle != playstate.NoSubtitle {
		t.Fatal("revival lost stream selections")
	}
}

// The scanner normally models each file as its own version. Existing stored
// multipart versions must nevertheless retain distinct ordered parts and IDs.
func TestTrashExistingMultipartVersion(t *testing.T) {
	s, sec := newStore(t, "")
	a, b := file("/m/a.mkv", "Film", 2000), file("/m/b.mkv", "Film", 2000)
	if _, err := s.ApplyScan(sec, []ScannedFile{a, b}, t0); err != nil {
		t.Fatal(err)
	}
	it := s.d.Items[0]
	it.Media[0].Parts = append(it.Media[0].Parts, it.Media[1].Parts...)
	it.Media = it.Media[:1]
	s.reindexLocked()
	before := s.Items(sec)[0]
	// The first part disappears, while the later part remains readable.
	if _, err := s.ApplyScan(sec, []ScannedFile{b}, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	missing := s.Items(sec)[0]
	if !missing.Unavailable || len(missing.Media) != 1 || len(missing.Media[0].Parts) != 2 {
		t.Fatalf("multipart retention: %+v", missing)
	}
	for i, p := range missing.Media[0].Parts {
		if p.ID != before.Media[0].Parts[i].ID {
			t.Fatal("missing part reordered multipart sequence")
		}
	}
	if s.PartAvailable(before.Media[0].Parts[1].ID) {
		t.Fatal("incomplete multipart version was playable")
	}
	if _, err := s.ApplyScan(sec, []ScannedFile{a, b}, t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	revived := s.Items(sec)[0]
	if revived.Unavailable || len(revived.Media) != 1 || revived.Media[0].ID != before.Media[0].ID || len(revived.Media[0].Parts) != 2 {
		t.Fatalf("multipart revival duplicated version: %+v", revived)
	}
	for i, p := range revived.Media[0].Parts {
		if p.ID != before.Media[0].Parts[i].ID || p.Unavailable {
			t.Fatal("multipart ID/order changed")
		}
	}
}
