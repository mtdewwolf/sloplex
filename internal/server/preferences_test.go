package server

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/accounts"
	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/preferences"
)

var prefsBoot = preferences.Bootstrap{FriendlyName: "config-name", ScanInterval: time.Hour, Watch: true, Discovery: true}

// prefsServer serves a Server whose preferences are stored at path.
func prefsServer(t *testing.T, path string) (*fixture, *preferences.Store) {
	t.Helper()
	prefs, err := preferences.Open(path, prefsBoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := library.Open("")
	if err != nil {
		t.Fatal(err)
	}
	accts, err := accounts.Open("")
	if err != nil {
		t.Fatal(err)
	}
	sc := library.NewScanner(store, fakeProber{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); sc.Wait() })
	opts := Options{FriendlyName: "config-name", MachineID: "abc123", Version: "0.0.1", Tokens: []string{testToken}}
	srv := httptest.NewServer(New(ctx, opts, Deps{Store: store, Scanner: sc, Play: mustPlay(t), Accounts: accts, Prefs: prefs}).Handler())
	t.Cleanup(srv.Close)
	return &fixture{srv: srv, store: store}, prefs
}

type jsonSetting struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Summary    string `json:"summary"`
	Type       string `json:"type"`
	Default    any    `json:"default"`
	Value      any    `json:"value"`
	Hidden     *bool  `json:"hidden"`
	Advanced   *bool  `json:"advanced"`
	Group      string `json:"group"`
	EnumValues string `json:"enumValues"`
}

type xmlSetting struct {
	ID         string `xml:"id,attr"`
	Type       string `xml:"type,attr"`
	Default    string `xml:"default,attr"`
	Value      string `xml:"value,attr"`
	Hidden     string `xml:"hidden,attr"`
	Advanced   string `xml:"advanced,attr"`
	Group      string `xml:"group,attr"`
	EnumValues string `xml:"enumValues,attr"`
}

func prefsJSON(t *testing.T, f *fixture, path string) (int, []jsonSetting) {
	t.Helper()
	resp := f.get(t, path, map[string]string{"Accept": "application/json"})
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	var body struct {
		MediaContainer struct {
			Size    int           `json:"size"`
			Setting []jsonSetting `json:"Setting"`
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.MediaContainer.Size != len(body.MediaContainer.Setting) {
		t.Fatalf("size %d with %d settings", body.MediaContainer.Size, len(body.MediaContainer.Setting))
	}
	return resp.StatusCode, body.MediaContainer.Setting
}

func prefsXML(t *testing.T, f *fixture) []xmlSetting {
	t.Helper()
	resp := f.get(t, "/:/prefs", nil)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/xml") {
		t.Fatalf("GET /:/prefs XML: %s %s", resp.Status, resp.Header.Get("Content-Type"))
	}
	var mc struct {
		XMLName xml.Name     `xml:"MediaContainer"`
		Size    int          `xml:"size,attr"`
		Setting []xmlSetting `xml:"Setting"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&mc); err != nil {
		t.Fatal(err)
	}
	if mc.Size != len(mc.Setting) {
		t.Fatalf("size %d with %d settings", mc.Size, len(mc.Setting))
	}
	return mc.Setting
}

func (f *fixture) putPrefs(t *testing.T, token string, q url.Values) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, f.srv.URL+"/:/prefs?"+q.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set(tokenHeader, token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func rootName(t *testing.T, f *fixture) string {
	t.Helper()
	return f.getJSON(t, "/").FriendlyName
}

func TestPrefsListJSON(t *testing.T) {
	f, _ := prefsServer(t, "")
	code, settings := prefsJSON(t, f, "/:/prefs")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	byID := map[string]jsonSetting{}
	var ids []string
	for _, s := range settings {
		byID[s.ID] = s
		ids = append(ids, s.ID)
		if s.Hidden == nil || s.Advanced == nil || s.Label == "" || s.Group == "" {
			t.Errorf("%s: missing attributes: %+v", s.ID, s)
		}
	}
	if got := strings.Join(ids, ","); got != "FriendlyName,ScheduledLibraryUpdatesEnabled,ScheduledLibraryUpdateInterval,FSEventLibraryUpdatesEnabled,GdmEnabled,logDebug" {
		t.Fatalf("ids = %s", got)
	}
	fn := byID["FriendlyName"]
	if fn.Type != "text" || fn.Default != "" || fn.Value != "" || fn.Group != "general" || *fn.Hidden || *fn.Advanced {
		t.Errorf("FriendlyName = %+v", fn)
	}
	iv := byID["ScheduledLibraryUpdateInterval"]
	if iv.Type != "int" || iv.Default != float64(3600) || iv.Value != float64(3600) || iv.Group != "library" ||
		iv.EnumValues != "900:every 15 minutes|1800:every 30 minutes|3600:hourly|7200:every 2 hours|21600:every 6 hours|43200:every 12 hours|86400:daily" {
		t.Errorf("interval = %+v", iv)
	}
	if b := byID["GdmEnabled"]; b.Type != "bool" || b.Value != true || b.Default != true || b.EnumValues != "" {
		t.Errorf("GdmEnabled = %+v", b)
	}
	if d := byID["logDebug"]; d.Value != false || !*d.Advanced {
		t.Errorf("logDebug = %+v", d)
	}
}

func TestPrefsDoNotLeakConfig(t *testing.T) {
	f, _ := prefsServer(t, filepath.Join(t.TempDir(), "preferences.json"))
	for _, hdr := range []map[string]string{nil, {"Accept": "application/json"}} {
		b, _ := io.ReadAll(f.get(t, "/:/prefs", hdr).Body)
		for _, secret := range []string{testToken, "preferences.json", "config-name"} {
			if strings.Contains(string(b), secret) {
				t.Errorf("response contains %q", secret)
			}
		}
	}
}

func TestPrefsPutRoundTripAndEffect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	f, prefs := prefsServer(t, path)
	if name := rootName(t, f); name != "config-name" {
		t.Fatalf("root name before = %q", name)
	}
	q := url.Values{"FriendlyName": {"Den Server"}, "ScheduledLibraryUpdateInterval": {"900"},
		"GdmEnabled": {"0"}, "logDebug": {"true"}}
	if code, body := f.putPrefs(t, testToken, q); code != http.StatusOK || body != "" {
		t.Fatalf("PUT = %d %q", code, body)
	}
	if name := rootName(t, f); name != "Den Server" {
		t.Errorf("root name after = %q", name)
	}
	if prefs.ScanInterval() != 15*time.Minute || prefs.Discovery() || !prefs.Debug() {
		t.Errorf("consumers see old values")
	}

	_, js := prefsJSON(t, f, "/:/prefs")
	xs := prefsXML(t, f)
	want := map[string][2]any{ // JSON value, XML value
		"FriendlyName":                   {"Den Server", "Den Server"},
		"ScheduledLibraryUpdatesEnabled": {true, "1"},
		"ScheduledLibraryUpdateInterval": {float64(900), "900"},
		"FSEventLibraryUpdatesEnabled":   {true, "1"},
		"GdmEnabled":                     {false, "0"},
		"logDebug":                       {true, "1"},
	}
	for i, s := range js {
		if s.Value != want[s.ID][0] || xs[i].ID != s.ID || xs[i].Value != want[s.ID][1] || xs[i].Type != s.Type {
			t.Errorf("%s: JSON %#v XML %+v", s.ID, s.Value, xs[i])
		}
	}
	if xs[4].Default != "1" || xs[4].Hidden != "0" || xs[5].Advanced != "1" || xs[2].EnumValues == "" {
		t.Errorf("XML attributes: %+v", xs)
	}

	// Writing back every XML value unchanged is accepted and changes nothing.
	back := url.Values{}
	for _, s := range xs {
		back.Set(s.ID, s.Value)
	}
	if code, body := f.putPrefs(t, testToken, back); code != http.StatusOK {
		t.Fatalf("round trip PUT = %d %q", code, body)
	}
	if _, again := prefsJSON(t, f, "/:/prefs"); !reflect.DeepEqual(again, js) {
		t.Errorf("round trip changed values:\n%v\n%v", again, js)
	}

	// Restart: a new server on the same file keeps the values.
	f2, _ := prefsServer(t, path)
	if name := rootName(t, f2); name != "Den Server" {
		t.Errorf("root name after restart = %q", name)
	}
	if _, after := prefsJSON(t, f2, "/:/prefs"); !reflect.DeepEqual(after, js) {
		t.Errorf("restart changed values:\n%v\n%v", after, js)
	}

	// A blank name falls back to the configured one.
	if code, _ := f2.putPrefs(t, testToken, url.Values{"FriendlyName": {""}}); code != http.StatusOK {
		t.Fatal(code)
	}
	if name := rootName(t, f2); name != "config-name" {
		t.Errorf("blank name gives %q", name)
	}
}

func TestPrefsGetOne(t *testing.T) {
	f, _ := prefsServer(t, "")
	code, s := prefsJSON(t, f, "/:/prefs/get?id=ScheduledLibraryUpdateInterval")
	if code != http.StatusOK || len(s) != 1 || s[0].ID != "ScheduledLibraryUpdateInterval" || s[0].Value != float64(3600) {
		t.Fatalf("get = %d %+v", code, s)
	}
	for _, q := range []string{"?id=foo", "?id=friendlyname", "?id=sendCrashReports", ""} {
		if resp := f.get(t, "/:/prefs/get"+q, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("get%s = %s", q, resp.Status)
		}
	}
}

func TestPrefsPutRejects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	f, _ := prefsServer(t, path)
	if code, _ := f.putPrefs(t, testToken, url.Values{"FriendlyName": {"Den"}}); code != http.StatusOK {
		t.Fatal(code)
	}
	before, _ := os.ReadFile(path)
	cases := []struct {
		q    string
		body string
	}{
		{"FriendlyName=New&foo=1", "cannot set preference value for unknown preference foo\n"},
		{"FriendlyName=New&sendCrashReports=1", "cannot set preference value for unknown preference sendCrashReports\n"},
		{"FriendlyName=New&GdmEnabled=2", ""},
		{"FriendlyName=New&ScheduledLibraryUpdateInterval=hourly", ""},
		{"FriendlyName=New&ScheduledLibraryUpdateInterval=60", ""},
		{"FriendlyName=New&FriendlyName=Other", ""},
		{"FriendlyName=New&foo=%ZZ", ""},
		{"FriendlyName=New&foo=1;GdmEnabled=0", ""},
		{"FriendlyName=bad%0Aname", ""},
		{"", ""},
	}
	for _, c := range cases {
		req, _ := http.NewRequest(http.MethodPut, f.srv.URL+"/:/prefs?"+c.q, nil)
		req.Header.Set(tokenHeader, testToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || (c.body != "" && string(b) != c.body) {
			t.Errorf("PUT ?%s = %d %q", c.q, resp.StatusCode, b)
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || rootName(t, f) != "Den" {
		t.Errorf("rejected PUT changed state")
	}

	// The token may travel as a query parameter; it is not a preference.
	q := url.Values{"X-Plex-Token": {testToken}, "X-Plex-Client-Identifier": {"c1"}, "FriendlyName": {"Q"}}
	if code, body := f.putPrefs(t, "", q); code != http.StatusOK {
		t.Errorf("query-token PUT = %d %q", code, body)
	}
	if rootName(t, f) != "Q" {
		t.Errorf("query-token PUT not applied")
	}
}

func TestPrefsAdminOnly(t *testing.T) {
	f, _ := prefsServer(t, "")
	f.createUser(t, "alice", "wonderland", false)
	f.createUser(t, "root", "password1", true)
	user := f.login(t, "alice", "wonderland")
	admin := f.login(t, "root", "password1")

	for _, path := range []string{"/:/prefs", "/:/prefs/get?id=FriendlyName"} {
		if code := status(f.api(t, "GET", path, user, nil, nil)); code != http.StatusForbidden {
			t.Errorf("user GET %s = %d", path, code)
		}
		if code := status(f.api(t, "GET", path, "", nil, nil)); code != http.StatusUnauthorized {
			t.Errorf("anonymous GET %s = %d", path, code)
		}
		if code := status(f.api(t, "GET", path, admin, nil, nil)); code != http.StatusOK {
			t.Errorf("admin GET %s = %d", path, code)
		}
	}
	if code, _ := f.putPrefs(t, user, url.Values{"FriendlyName": {"Mine"}}); code != http.StatusForbidden {
		t.Errorf("user PUT = %d", code)
	}
	if code, _ := f.putPrefs(t, "", url.Values{"FriendlyName": {"Mine"}}); code != http.StatusUnauthorized {
		t.Errorf("anonymous PUT = %d", code)
	}
	if rootName(t, f) != "config-name" {
		t.Fatal("refused PUT changed the name")
	}
	if code, _ := f.putPrefs(t, admin, url.Values{"FriendlyName": {"Admin's"}}); code != http.StatusOK {
		t.Errorf("admin PUT = %d", code)
	}
	if rootName(t, f) != "Admin's" {
		t.Errorf("admin PUT not applied")
	}
}

func TestPrefsPutStorageFailure(t *testing.T) {
	dir := t.TempDir()
	f, _ := prefsServer(t, filepath.Join(dir, "sub", "preferences.json"))
	if err := os.WriteFile(filepath.Join(dir, "sub"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code, body := f.putPrefs(t, testToken, url.Values{"FriendlyName": {"Den"}}); code != http.StatusInternalServerError {
		t.Errorf("PUT = %d %q", code, body)
	}
	if rootName(t, f) != "config-name" {
		t.Errorf("failed PUT changed the name")
	}
}

func TestPrefsConcurrentRequests(t *testing.T) {
	f, _ := prefsServer(t, filepath.Join(t.TempDir(), "preferences.json"))
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := range 5 {
				if code, body := f.putPrefs(t, testToken, url.Values{"FriendlyName": {fmt.Sprintf("n%d-%d", i, j)}}); code != http.StatusOK {
					t.Errorf("PUT = %d %q", code, body)
				}
			}
		}()
		go func() {
			defer wg.Done()
			for range 5 {
				resp := f.get(t, "/", map[string]string{"Accept": "application/json"})
				io.Copy(io.Discard, resp.Body)
				prefsXML(t, f)
			}
		}()
	}
	wg.Wait()
	if name := rootName(t, f); !strings.HasPrefix(name, "n") {
		t.Errorf("final name %q", name)
	}
}
