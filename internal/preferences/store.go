package preferences

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/mtdewwolf/sloplex/internal/atomicfile"
)

const fileVersion = 1

type file struct {
	Version int                        `json:"version"`
	Values  map[string]json.RawMessage `json:"values"`
}

// Store holds stored preference values over the bootstrap defaults. It is
// safe for concurrent use. With an empty path values are kept in memory.
type Store struct {
	path string
	boot Bootstrap
	// writeFile is an optional per-store writer; nil uses atomicfile.Write.
	writeFile func(string, []byte, os.FileMode) error

	mu     sync.RWMutex
	values map[string]any // stored values of registry IDs
	// unknown are stored IDs this version does not support, kept as is.
	unknown map[string]json.RawMessage

	subMu sync.Mutex
	subs  map[chan struct{}]struct{}
}

// Open loads the values stored at path over the defaults from b.
func Open(path string, b Bootstrap) (*Store, error) {
	s := &Store{path: path, boot: b, values: map[string]any{}, unknown: map[string]json.RawMessage{},
		subs: map[chan struct{}]struct{}{}}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Version != fileVersion {
		return nil, fmt.Errorf("%s: unsupported version %d", path, f.Version)
	}
	for id, raw := range f.Values {
		d, ok := lookup(id)
		if !ok {
			s.unknown[id] = raw
			continue
		}
		v, err := d.decode(raw)
		if err == nil {
			err = d.check(v, b)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: preference %s: %w", path, id, err)
		}
		s.values[id] = v
	}
	return s, nil
}

// decode reads a stored JSON value of the preference's type.
func (d *def) decode(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	switch d.Type {
	case Bool:
		if b, ok := v.(bool); ok {
			return b, nil
		}
	case Int:
		if n, ok := v.(json.Number); ok {
			if i, err := n.Int64(); err == nil {
				return i, nil
			}
		}
	case Text:
		if s, ok := v.(string); ok {
			return s, nil
		}
	}
	return nil, fmt.Errorf("stored value %s is not of type %s", raw, d.Type)
}

func (s *Store) setting(d *def) Setting {
	out := d.Setting
	out.Default = d.defaultOf(s.boot)
	out.Value = out.Default
	if v, ok := s.values[d.ID]; ok {
		out.Value = v
	}
	return out
}

// All returns every supported preference in registry order.
func (s *Store) All() []Setting {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Setting, len(registry))
	for i := range registry {
		out[i] = s.setting(&registry[i])
	}
	return out
}

// Get returns one preference.
func (s *Store) Get(id string) (Setting, bool) {
	d, ok := lookup(id)
	if !ok {
		return Setting{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.setting(d), true
}

func (s *Store) value(id string) any {
	d, _ := lookup(id)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.values[id]; ok {
		return v
	}
	return d.defaultOf(s.boot)
}

// Set validates and stores the wire values in raw, keyed by ID. Either all
// of them become visible together. Validation and failures before replacement
// publish none. An atomicfile.CommittedError publishes and notifies subscribers
// about the installed values while returning the persistence error. Bad input
// returns an *UnknownError or *InvalidError.
func (s *Store) Set(raw map[string]string) error {
	ids := make([]string, 0, len(raw))
	for id := range raw {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	parsed := make(map[string]any, len(raw))
	for _, id := range ids {
		d, ok := lookup(id)
		if !ok {
			return &UnknownError{ID: id}
		}
		v, err := d.parse(raw[id], s.boot)
		if err != nil {
			return &InvalidError{ID: id, Reason: err.Error()}
		}
		parsed[id] = v
	}

	s.mu.Lock()
	next := make(map[string]any, len(s.values)+len(parsed))
	for id, v := range s.values {
		next[id] = v
	}
	for id, v := range parsed {
		if d, _ := lookup(id); !d.choice(v) {
			// Only the config-derived default is accepted outside the
			// choices; it keeps following the config.
			delete(next, id)
			continue
		}
		next[id] = v
	}
	err := s.save(next)
	var committed *atomicfile.CommittedError
	if err != nil && !errors.As(err, &committed) {
		s.mu.Unlock()
		return err
	}
	s.values = next
	s.mu.Unlock()
	s.notify()
	return err
}

// save writes values and the kept unknown entries; s.mu is held.
func (s *Store) save(values map[string]any) error {
	if s.path == "" {
		return nil
	}
	f := file{Version: fileVersion, Values: make(map[string]json.RawMessage, len(values)+len(s.unknown))}
	for id, raw := range s.unknown {
		f.Values[id] = raw
	}
	for id, v := range values {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		f.Values[id] = b
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	writeFile := s.writeFile
	if writeFile == nil {
		writeFile = atomicfile.Write
	}
	return writeFile(s.path, append(b, '\n'), 0o600)
}

// Subscribe returns a channel that receives after each published Set,
// including an installed replacement with unconfirmed directory durability.
// Notifications coalesce: a receiver should read the values it needs
// afresh. cancel stops them.
func (s *Store) Subscribe() (ch <-chan struct{}, cancel func()) {
	c := make(chan struct{}, 1)
	s.subMu.Lock()
	s.subs[c] = struct{}{}
	s.subMu.Unlock()
	return c, func() {
		s.subMu.Lock()
		delete(s.subs, c)
		s.subMu.Unlock()
	}
}

func (s *Store) notify() {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for c := range s.subs {
		select {
		case c <- struct{}{}:
		default:
		}
	}
}

// FriendlyName is the server name in effect: the preference if it is not
// blank, else the bootstrap name.
func (s *Store) FriendlyName() string {
	return cmp.Or(s.value(FriendlyName).(string), s.boot.FriendlyName)
}

// ScanInterval is how often every library is scanned; 0 means never.
func (s *Store) ScanInterval() time.Duration {
	if !s.value(ScheduledLibraryUpdatesEnabled).(bool) {
		return 0
	}
	return time.Duration(s.value(ScheduledLibraryUpdateInterval).(int64)) * scheduleUnit
}

// Watch says whether to watch library folders. It applies at startup.
func (s *Store) Watch() bool { return s.value(FSEventLibraryUpdatesEnabled).(bool) }

// Discovery says whether to run GDM discovery. It applies at startup.
func (s *Store) Discovery() bool { return s.value(GdmEnabled).(bool) }

// Debug says whether to log at debug level.
func (s *Store) Debug() bool { return s.value(LogDebug).(bool) }
