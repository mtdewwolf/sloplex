package preferences

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var boot = Bootstrap{FriendlyName: "host", ScanInterval: time.Hour, Watch: true, Discovery: true}

func open(t *testing.T, path string, b Bootstrap) *Store {
	t.Helper()
	s, err := Open(path, b)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDefaultsFollowBootstrap(t *testing.T) {
	s := open(t, "", boot)
	want := map[string]any{
		FriendlyName: "", ScheduledLibraryUpdatesEnabled: true, ScheduledLibraryUpdateInterval: int64(3600),
		FSEventLibraryUpdatesEnabled: true, GdmEnabled: true, LogDebug: false,
	}
	all := s.All()
	if len(all) != len(want) {
		t.Fatalf("got %d settings, want %d", len(all), len(want))
	}
	for _, st := range all {
		if st.Default != want[st.ID] || st.Value != want[st.ID] {
			t.Errorf("%s: default %v value %v, want %v", st.ID, st.Default, st.Value, want[st.ID])
		}
	}
	if s.FriendlyName() != "host" || s.ScanInterval() != time.Hour || !s.Watch() || !s.Discovery() || s.Debug() {
		t.Fatalf("effective values wrong")
	}

	off := open(t, "", Bootstrap{FriendlyName: "host", Debug: true})
	if st, _ := off.Get(ScheduledLibraryUpdatesEnabled); st.Value != false {
		t.Errorf("scanInterval 0 should disable scheduled scans, got %v", st.Value)
	}
	if st, _ := off.Get(ScheduledLibraryUpdateInterval); st.Value != int64(3600) {
		t.Errorf("interval default with scans off = %v, want 3600", st.Value)
	}
	if off.ScanInterval() != 0 || !off.Debug() || off.Watch() || off.Discovery() {
		t.Errorf("bootstrap flags not applied")
	}
}

func TestSetPersistsAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	s := open(t, path, boot)
	err := s.Set(map[string]string{
		FriendlyName: "Den", ScheduledLibraryUpdateInterval: "900", GdmEnabled: "0", LogDebug: "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.FriendlyName() != "Den" || s.ScanInterval() != 15*time.Minute || s.Discovery() || !s.Debug() {
		t.Fatalf("values not applied in memory")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}

	r := open(t, path, boot)
	for id, want := range map[string]any{FriendlyName: "Den", ScheduledLibraryUpdateInterval: int64(900),
		GdmEnabled: false, LogDebug: true, FSEventLibraryUpdatesEnabled: true} {
		if st, _ := r.Get(id); st.Value != want {
			t.Errorf("reopened %s = %#v, want %#v", id, st.Value, want)
		}
	}
	// Stored values win over a changed config; unstored ones follow it.
	r2 := open(t, path, Bootstrap{FriendlyName: "other", ScanInterval: 2 * time.Hour, Watch: false, Discovery: true})
	if r2.FriendlyName() != "Den" || r2.ScanInterval() != 15*time.Minute || r2.Watch() || r2.Discovery() {
		t.Errorf("precedence wrong after config change")
	}
}

func TestSetIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	s := open(t, path, boot)
	if err := s.Set(map[string]string{FriendlyName: "Den"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	cases := []struct {
		raw     map[string]string
		unknown bool
	}{
		{map[string]string{FriendlyName: "New", "foo": "1"}, true},
		{map[string]string{FriendlyName: "New", "sendCrashReports": "1"}, true}, // documented PMS ID, not supported
		{map[string]string{FriendlyName: "New", "friendlyname": "x"}, true},     // IDs are case-sensitive
		{map[string]string{FriendlyName: "New", GdmEnabled: "yes"}, false},
		{map[string]string{FriendlyName: "New", ScheduledLibraryUpdateInterval: "1000"}, false},
		{map[string]string{FriendlyName: "New", ScheduledLibraryUpdateInterval: "1e3"}, false},
		{map[string]string{FriendlyName: "bad\nname"}, false},
		{map[string]string{FriendlyName: strings.Repeat("x", 256)}, false},
		{map[string]string{FriendlyName: "\xff"}, false},
	}
	for _, c := range cases {
		err := s.Set(c.raw)
		var u *UnknownError
		var inv *InvalidError
		if c.unknown && !errors.As(err, &u) || !c.unknown && !errors.As(err, &inv) {
			t.Errorf("Set(%q) = %v", c.raw, err)
		}
	}
	if s.FriendlyName() != "Den" {
		t.Errorf("rejected update changed memory: %q", s.FriendlyName())
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Errorf("rejected update changed the file")
	}
	if err := s.Set(map[string]string{"foo": "1"}); err.Error() != "cannot set preference value for unknown preference foo" {
		t.Errorf("unknown message = %q", err)
	}
}

func TestPersistFailureLeavesMemory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "preferences.json")
	s := open(t, path, boot)
	// A file where the parent directory should be makes the write fail.
	if err := os.WriteFile(filepath.Join(dir, "sub"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(map[string]string{FriendlyName: "Den"}); err == nil {
		t.Fatal("Set succeeded without storage")
	}
	if s.FriendlyName() != "host" {
		t.Errorf("failed Set changed memory")
	}
}

func TestOpenRejectsBadFiles(t *testing.T) {
	for name, body := range map[string]string{
		"syntax":   `{`,
		"version":  `{"version":2,"values":{}}`,
		"type":     `{"version":1,"values":{"GdmEnabled":"1"}}`,
		"invalid":  `{"version":1,"values":{"ScheduledLibraryUpdateInterval":5}}`,
		"fraction": `{"version":1,"values":{"ScheduledLibraryUpdateInterval":900.5}}`,
		"control":  `{"version":1,"values":{"FriendlyName":"a\u0007"}}`,
	} {
		path := filepath.Join(t.TempDir(), "preferences.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(path, boot); err == nil {
			t.Errorf("%s: Open accepted %s", name, body)
		}
	}
}

func TestUnknownStoredIDsAreKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"values":{"FutureThing":{"a":1}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := open(t, path, boot)
	if _, ok := s.Get("FutureThing"); ok {
		t.Fatal("unknown stored ID is exposed")
	}
	if err := s.Set(map[string]string{GdmEnabled: "false"}); err != nil {
		t.Fatal(err)
	}
	var f file
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	var kept bytes.Buffer
	if err := json.Compact(&kept, f.Values["FutureThing"]); err != nil {
		t.Fatal(err)
	}
	if kept.String() != `{"a":1}` || string(f.Values[GdmEnabled]) != "false" {
		t.Errorf("file = %s", b)
	}
}

func TestConfigOnlyIntervalFollowsConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	b := Bootstrap{FriendlyName: "h", ScanInterval: 45 * time.Minute}
	s := open(t, path, b)
	if st, _ := s.Get(ScheduledLibraryUpdateInterval); st.Value != int64(2700) {
		t.Fatalf("value %v", st.Value)
	}
	// Writing back the config value is allowed but not pinned.
	if err := s.Set(map[string]string{ScheduledLibraryUpdateInterval: "2700"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(map[string]string{ScheduledLibraryUpdateInterval: "2400"}); err == nil {
		t.Fatal("non-choice value accepted")
	}
	r := open(t, path, Bootstrap{FriendlyName: "h", ScanInterval: 30 * time.Minute})
	if r.ScanInterval() != 30*time.Minute {
		t.Errorf("interval %v, want the new config value", r.ScanInterval())
	}
}

func TestBlankFriendlyNameFallsBack(t *testing.T) {
	s := open(t, "", boot)
	if err := s.Set(map[string]string{FriendlyName: "Den"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(map[string]string{FriendlyName: ""}); err != nil {
		t.Fatal(err)
	}
	if s.FriendlyName() != "host" {
		t.Errorf("blank name = %q, want the bootstrap name", s.FriendlyName())
	}
}

func TestConcurrentSetAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	s := open(t, path, boot)
	var wg sync.WaitGroup
	names := []string{"a", "b", "c", "d"}
	for _, n := range names {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 20 {
				// Both values of a request are seen together.
				if err := s.Set(map[string]string{FriendlyName: n, LogDebug: map[string]string{"a": "1", "b": "0", "c": "1", "d": "0"}[n]}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			for range 100 {
				_ = s.All()
				_ = s.FriendlyName()
			}
		}()
	}
	wg.Wait()
	r := open(t, path, boot)
	all := r.All()
	name, debug := all[0].Value.(string), all[5].Value.(bool)
	if want := name == "a" || name == "c"; debug != want {
		t.Errorf("file mixes requests: name %q debug %v", name, debug)
	}
	if name != s.FriendlyName() {
		t.Errorf("file %q and memory %q disagree", name, s.FriendlyName())
	}
}

func TestRunScanSchedule(t *testing.T) {
	old := scheduleUnit
	scheduleUnit = time.Millisecond / 100 // 900 units = 9 ms
	defer func() { scheduleUnit = old }()

	s := open(t, "", Bootstrap{FriendlyName: "h"}) // interval scans off
	ctx, cancel := context.WithCancel(context.Background())
	scans := make(chan struct{}, 100)
	done := make(chan struct{})
	go func() {
		RunScanSchedule(ctx, s, func() { scans <- struct{}{} })
		close(done)
	}()
	select {
	case <-scans:
		t.Fatal("scan while interval scans are off")
	case <-time.After(100 * time.Millisecond):
	}
	if err := s.Set(map[string]string{ScheduledLibraryUpdatesEnabled: "1", ScheduledLibraryUpdateInterval: "900"}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		select {
		case <-scans:
		case <-time.After(2 * time.Second):
			t.Fatal("no scheduled scan after enabling")
		}
	}
	if err := s.Set(map[string]string{ScheduledLibraryUpdatesEnabled: "0"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // let the schedule see the change
	for len(scans) > 0 {
		<-scans
	}
	select {
	case <-scans:
		t.Fatal("scan after disabling")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunScanSchedule did not return")
	}
}

func TestFollowLogLevel(t *testing.T) {
	s := open(t, "", boot)
	var level slog.LevelVar
	level.Set(slog.LevelError)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go FollowLogLevel(ctx, s, &level)
	waitLevel := func(want slog.Level) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for level.Level() != want {
			if time.Now().After(deadline) {
				t.Fatalf("level %v, want %v", level.Level(), want)
			}
			time.Sleep(time.Millisecond)
		}
	}
	waitLevel(slog.LevelInfo)
	if err := s.Set(map[string]string{LogDebug: "1"}); err != nil {
		t.Fatal(err)
	}
	waitLevel(slog.LevelDebug)
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, &slog.HandlerOptions{Level: &level}))
	if !logger.Enabled(ctx, slog.LevelDebug) {
		t.Error("debug logging not enabled")
	}
	if err := s.Set(map[string]string{LogDebug: "0"}); err != nil {
		t.Fatal(err)
	}
	waitLevel(slog.LevelInfo)
}
