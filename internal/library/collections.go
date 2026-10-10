package library

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// Collection sources; see docs/spec/collections.md.
const (
	CollectionNFO    = "nfo"
	CollectionManual = "manual"
)

// MaxCollectionItems caps the members of a manual collection. It is a
// variable so tests can lower it.
var MaxCollectionItems = 1000

// Collection groups movies or shows of one section.
type Collection struct {
	ID        int    `json:"id"`
	SectionID int    `json:"sectionId"`
	Title     string `json:"title"`
	Source    string `json:"source"`
	ItemIDs   []int  `json:"itemIds,omitempty"`
	AddedAt   int64  `json:"addedAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

var (
	// ErrReadOnly is returned when changing an automatic collection.
	ErrReadOnly = errors.New("this collection comes from .nfo files; edit them instead")
	// ErrBadMember is wrapped for items that cannot join a collection.
	ErrBadMember = errors.New("item cannot be in this collection")
)

func (c *Collection) clone() Collection {
	out := *c
	out.ItemIDs = slices.Clone(c.ItemIDs)
	return out
}

// memberType is the item type a section's collections hold.
func memberType(sec *Section) string {
	if sec.Type == TypeShow {
		return TypeShow
	}
	return TypeMovie
}

// Collections returns a section's collections ordered by sort title.
func (s *Store) Collections(sectionID int) []Collection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Collection
	for _, c := range s.d.Collections {
		if c.SectionID == sectionID {
			out = append(out, c.clone())
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return SortTitle(out[i].Title) < SortTitle(out[j].Title) })
	return out
}

// Collection returns a collection by ID.
func (s *Store) Collection(id int) (Collection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if c := s.collectionLocked(id); c != nil {
		return c.clone(), nil
	}
	return Collection{}, ErrNotFound
}

// CollectionTitles returns the titles of the collections an item belongs
// to, in sort order.
func (s *Store) CollectionTitles(itemID int) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for _, c := range s.memberOf[itemID] {
		out = append(out, c.Title)
	}
	sort.SliceStable(out, func(i, j int) bool { return SortTitle(out[i]) < SortTitle(out[j]) })
	return out
}

// reindexCollectionsLocked rebuilds memberOf after collections change.
func (s *Store) reindexCollectionsLocked() {
	s.memberOf = map[int][]*Collection{}
	for _, c := range s.d.Collections {
		for _, id := range c.ItemIDs {
			s.memberOf[id] = append(s.memberOf[id], c)
		}
	}
}

func (s *Store) collectionLocked(id int) *Collection {
	for _, c := range s.d.Collections {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// checkMembersLocked validates items for a collection of sec.
func (s *Store) checkMembersLocked(sec *Section, ids []int) error {
	want := memberType(sec)
	for _, id := range ids {
		it := s.byID[id]
		if it == nil || it.SectionID != sec.ID || it.Type != want {
			return fmt.Errorf("%w: %d is not a %s of section %q", ErrBadMember, id, want, sec.Title)
		}
	}
	return nil
}

// addMembers appends ids not already present, keeping order.
func addMembers(have, ids []int) []int {
	for _, id := range ids {
		if !slices.Contains(have, id) {
			have = append(have, id)
		}
	}
	return have
}

// CreateCollection adds a manual collection.
func (s *Store) CreateCollection(sectionID int, title string, itemIDs []int, now time.Time) (Collection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sec := s.sectionLocked(sectionID)
	if sec == nil {
		return Collection{}, ErrNotFound
	}
	if err := s.checkMembersLocked(sec, itemIDs); err != nil {
		return Collection{}, err
	}
	members := addMembers(nil, itemIDs)
	if len(members) > MaxCollectionItems {
		return Collection{}, fmt.Errorf("%w: more than %d members", ErrBadMember, MaxCollectionItems)
	}
	c := &Collection{ID: next(&s.d.NextItemID), SectionID: sectionID, Title: title, Source: CollectionManual,
		ItemIDs: members, AddedAt: now.Unix(), UpdatedAt: now.Unix()}
	s.d.Collections = append(s.d.Collections, c)
	s.reindexCollectionsLocked()
	return c.clone(), s.saveLocked()
}

// editCollection applies f to a manual collection and saves.
func (s *Store) editCollection(id int, now time.Time, f func(*Collection, *Section) error) (Collection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.collectionLocked(id)
	if c == nil {
		return Collection{}, ErrNotFound
	}
	if c.Source != CollectionManual {
		return Collection{}, ErrReadOnly
	}
	sec := s.sectionLocked(c.SectionID)
	if sec == nil {
		return Collection{}, ErrNotFound
	}
	if err := f(c, sec); err != nil {
		return Collection{}, err
	}
	c.UpdatedAt = now.Unix()
	s.reindexCollectionsLocked()
	return c.clone(), s.saveLocked()
}

// AddToCollection adds items to a manual collection.
func (s *Store) AddToCollection(id int, itemIDs []int, now time.Time) (Collection, error) {
	return s.editCollection(id, now, func(c *Collection, sec *Section) error {
		if err := s.checkMembersLocked(sec, itemIDs); err != nil {
			return err
		}
		members := addMembers(slices.Clone(c.ItemIDs), itemIDs)
		if len(members) > MaxCollectionItems {
			return fmt.Errorf("%w: more than %d members", ErrBadMember, MaxCollectionItems)
		}
		c.ItemIDs = members
		return nil
	})
}

// RemoveFromCollection removes one member of a manual collection.
func (s *Store) RemoveFromCollection(id, itemID int, now time.Time) (Collection, error) {
	return s.editCollection(id, now, func(c *Collection, _ *Section) error {
		i := slices.Index(c.ItemIDs, itemID)
		if i < 0 {
			return ErrNotFound
		}
		c.ItemIDs = slices.Delete(slices.Clone(c.ItemIDs), i, i+1)
		return nil
	})
}

// RenameCollection retitles a manual collection.
func (s *Store) RenameCollection(id int, title string, now time.Time) (Collection, error) {
	return s.editCollection(id, now, func(c *Collection, _ *Section) error {
		c.Title = title
		return nil
	})
}

// DeleteCollection removes a manual collection.
func (s *Store) DeleteCollection(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.collectionLocked(id)
	if c == nil {
		return ErrNotFound
	}
	if c.Source != CollectionManual {
		return ErrReadOnly
	}
	s.d.Collections = slices.DeleteFunc(s.d.Collections, func(x *Collection) bool { return x.ID == id })
	s.reindexCollectionsLocked()
	return s.saveLocked()
}

// syncCollectionsLocked rebuilds a section's automatic collections from
// .nfo sets and drops manual members that left the section. It runs after
// a scan, with indexes current.
func (s *Store) syncCollectionsLocked(sec *Section, ts int64) {
	old := map[string]*Collection{}
	var keep []*Collection
	for _, c := range s.d.Collections {
		switch {
		case c.SectionID != sec.ID:
			keep = append(keep, c)
		case c.Source == CollectionNFO:
			old[strings.ToLower(c.Title)] = c
		default:
			want := memberType(sec)
			c.ItemIDs = slices.DeleteFunc(c.ItemIDs, func(id int) bool {
				it := s.byID[id]
				return it == nil || it.SectionID != sec.ID || it.Type != want
			})
			keep = append(keep, c)
		}
	}

	// Members in item order (by ID), so the result is stable.
	var order []string
	sets := map[string]*Collection{}
	items := slices.Clone(s.d.Items)
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	for _, it := range items {
		if it.SectionID != sec.ID || it.Type != TypeMovie || it.Meta == nil {
			continue
		}
		for _, name := range it.Meta.Collections {
			k := strings.ToLower(name)
			c := sets[k]
			if c == nil {
				c = &Collection{SectionID: sec.ID, Title: name, Source: CollectionNFO, AddedAt: ts, UpdatedAt: ts}
				if prev := old[k]; prev != nil {
					c.ID, c.Title, c.AddedAt, c.UpdatedAt = prev.ID, prev.Title, prev.AddedAt, prev.UpdatedAt
				} else {
					c.ID = next(&s.d.NextItemID)
				}
				sets[k] = c
				order = append(order, k)
			}
			c.ItemIDs = addMembers(c.ItemIDs, []int{it.ID})
		}
	}
	for _, k := range order {
		c := sets[k]
		if prev := old[k]; prev != nil && !slices.Equal(prev.ItemIDs, c.ItemIDs) {
			c.UpdatedAt = ts
		}
		keep = append(keep, c)
	}
	s.d.Collections = keep
	s.reindexCollectionsLocked()
}
