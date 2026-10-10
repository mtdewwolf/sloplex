package library

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// applier rebuilds one section's items from scan results while preserving
// IDs. It runs with the store's write lock held.
type applier struct {
	s     *Store
	sec   *Section
	ts    int64
	prev  map[string]fileRef // previous parts of this section, by file path
	old   []*Item            // previous items of this section
	kept  []*Item            // items of other sections
	out   []*Item            // new items of this section
	taken map[int]bool       // previous leaf IDs reused so far
	stats ScanStats
}

func newApplier(s *Store, sec *Section, now time.Time) *applier {
	a := &applier{s: s, sec: sec, ts: now.Unix(), prev: map[string]fileRef{}, taken: map[int]bool{}}
	for _, it := range s.d.Items {
		if it.SectionID != sec.ID {
			a.kept = append(a.kept, it)
			continue
		}
		a.old = append(a.old, it)
	}
	for k, ref := range s.files {
		if k.section == sec.ID {
			a.prev[k.path] = ref
		}
	}
	return a
}

func (a *applier) finish() {
	for _, it := range a.old {
		if IsLeaf(it.Type) && !a.taken[it.ID] {
			a.stats.Removed++
		}
	}
	a.s.d.Items = append(a.kept, a.out...)
}

// groupBy buckets files by key and returns the buckets in key order, each
// sorted by path.
func groupBy[K cmp.Ordered](files []ScannedFile, key func(ScannedFile) K) ([]K, map[K][]ScannedFile) {
	groups := map[K][]ScannedFile{}
	for _, f := range files {
		k := key(f)
		groups[k] = append(groups[k], f)
	}
	keys := make([]K, 0, len(groups))
	for k, g := range groups {
		keys = append(keys, k)
		slices.SortFunc(g, func(x, y ScannedFile) int { return strings.Compare(x.Path, y.Path) })
	}
	slices.Sort(keys)
	return keys, groups
}

func titleKey(title string, year int) string {
	return strings.ToLower(title) + "\x00" + strconv.Itoa(year)
}

// leaf builds a leaf item from the files that make up its versions. it must
// have everything but ID, timestamps and Media set. It reports whether the
// item is new or differs from its previous state.
func (a *applier) leaf(it *Item, group []ScannedFile) bool {
	var base *Item
	for _, f := range group {
		if ref, ok := a.prev[f.Path]; ok && !a.taken[ref.item.ID] {
			base = ref.item
			break
		}
	}
	changed := false
	if base != nil {
		a.taken[base.ID] = true
		it.ID, it.AddedAt, it.UpdatedAt, it.Images = base.ID, base.AddedAt, base.UpdatedAt, base.Images
	} else {
		it.ID, it.AddedAt = next(&a.s.d.NextItemID), a.ts
		a.stats.Added++
		changed = true
	}

	for _, f := range group {
		m := Media{Info: f.Info}
		m.Info.Streams = nil // kept on the part, with IDs
		pt := Part{File: f.Path, Size: f.Size, ModTime: f.ModTime, Duration: f.Info.Duration, Container: f.Info.Container}
		var prevStreams []Stream
		if ref, ok := a.prev[f.Path]; ok {
			m.ID, pt.ID = ref.media.ID, ref.part.ID
			prevStreams = ref.part.Streams
			changed = changed || ref.item.ID != it.ID
		} else {
			m.ID, pt.ID = next(&a.s.d.NextMediaID), next(&a.s.d.NextPartID)
			changed = true
		}
		pt.Streams = a.streams(f, prevStreams)
		changed = changed || f.Changed
		m.Parts = []Part{pt}
		it.Media = append(it.Media, m)
	}
	if base != nil {
		changed = changed || len(base.Media) != len(it.Media) || base.Title != it.Title ||
			base.Year != it.Year || base.ParentID != it.ParentID || base.Index != it.Index
		if changed {
			a.stats.Updated++
		}
	}
	if changed {
		it.UpdatedAt = a.ts
	}
	a.out = append(a.out, it)
	return changed
}

// streamKey identifies a stream within its part across rescans.
func streamKey(st Stream) string {
	if st.File != "" {
		return "f:" + st.File
	}
	return fmt.Sprintf("i%d:%s", st.Index, st.Codec)
}

// streams builds a part's stream list, keeping the IDs of streams that
// were already known.
func (a *applier) streams(f ScannedFile, prev []Stream) []Stream {
	ids := make(map[string]int, len(prev))
	for _, st := range prev {
		ids[streamKey(st)] = st.ID
	}
	out := make([]Stream, 0, len(f.Info.Streams)+len(f.Sidecars))
	for _, ps := range f.Info.Streams {
		out = append(out, Stream{Stream: ps})
	}
	out = append(out, f.Sidecars...)
	for i := range out {
		if id, ok := ids[streamKey(out[i])]; ok {
			out[i].ID = id
		} else {
			out[i].ID = next(&a.s.d.NextStreamID)
		}
	}
	return out
}

// container reuses a previous show or season (or allocates a new one) and
// reports whether it is new or renamed.
func (a *applier) container(it *Item, base *Item) bool {
	a.out = append(a.out, it)
	if base == nil {
		it.ID, it.AddedAt, it.UpdatedAt = next(&a.s.d.NextItemID), a.ts, a.ts
		return true
	}
	it.ID, it.AddedAt, it.UpdatedAt, it.Images = base.ID, base.AddedAt, base.UpdatedAt, base.Images
	return base.Title != it.Title || base.Year != it.Year
}

func (a *applier) movies(files []ScannedFile) {
	a.stats.Files = len(files)
	keys, groups := groupBy(files, func(f ScannedFile) string { return titleKey(f.Title, f.Year) })
	for _, k := range keys {
		g := groups[k]
		a.leaf(&Item{SectionID: a.sec.ID, Type: TypeMovie, Title: g[0].Title, Year: g[0].Year}, g)
	}
}

func (a *applier) shows(files []ScannedFile) {
	a.stats.Files = len(files)

	type seasonKey struct{ show, index int }
	oldShows := map[string]*Item{}
	oldSeasons := map[seasonKey]*Item{}
	oldKids := map[int]int{}
	for _, it := range a.old {
		switch it.Type {
		case TypeShow:
			oldShows[titleKey(it.Title, it.Year)] = it
		case TypeSeason:
			oldSeasons[seasonKey{it.ParentID, it.Index}] = it
		}
		if it.ParentID != 0 {
			oldKids[it.ParentID]++
		}
	}

	showKeys, shows := groupBy(files, func(f ScannedFile) string { return titleKey(f.Show, f.ShowYear) })
	for _, sk := range showKeys {
		sf := shows[sk]
		show := &Item{SectionID: a.sec.ID, Type: TypeShow, Title: sf[0].Show, Year: sf[0].ShowYear}
		showChanged := a.container(show, oldShows[sk])

		seasonNums, seasons := groupBy(sf, func(f ScannedFile) int { return f.Season })
		for _, n := range seasonNums {
			season := &Item{SectionID: a.sec.ID, Type: TypeSeason, ParentID: show.ID, Index: n, Title: SeasonTitle(n)}
			seasonChanged := a.container(season, oldSeasons[seasonKey{show.ID, n}])

			epNums, eps := groupBy(seasons[n], func(f ScannedFile) int { return f.Episode })
			for _, e := range epNums {
				g := eps[e]
				title := fmt.Sprintf("Episode %d", e)
				for _, f := range g {
					if f.Title != "" {
						title = f.Title
						break
					}
				}
				ep := &Item{SectionID: a.sec.ID, Type: TypeEpisode, ParentID: season.ID, Index: e, Title: title}
				seasonChanged = a.leaf(ep, g) || seasonChanged
			}
			if seasonChanged || oldKids[season.ID] != len(epNums) {
				season.UpdatedAt = a.ts
				showChanged = true
			}
		}
		if showChanged || oldKids[show.ID] != len(seasonNums) {
			show.UpdatedAt = a.ts
		}
	}
}
