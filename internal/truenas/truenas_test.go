package truenas

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeNAS is a TrueNAS API stand-in: a WebSocket server speaking JSON-RPC.
type fakeNAS struct {
	srv      *httptest.Server
	mu       sync.Mutex
	calls    []string
	pulled   []any
	jobState string
	pongs    int
}

func newFakeNAS(t *testing.T) *fakeNAS {
	t.Helper()
	f := &fakeNAS{jobState: "RUNNING"}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeNAS) fingerprint() string { return fingerprint(f.srv.Certificate()) }

func (f *fakeNAS) settings() Settings {
	return Settings{URL: f.srv.URL, App: "sloplex", APIKey: "good-key", CertSHA256: f.fingerprint()}
}

func (f *fakeNAS) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/current" || r.Header.Get("Upgrade") != "websocket" || r.Header.Get("Sec-Websocket-Version") != "13" {
		http.Error(w, "not the API", http.StatusNotFound)
		return
	}
	conn, brw, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + acceptKey(r.Header.Get("Sec-Websocket-Key")) + "\r\n\r\n")
	brw.Flush()
	for {
		op, payload, err := readClientFrame(brw.Reader)
		if err != nil {
			return
		}
		if op == opPong {
			f.mu.Lock()
			f.pongs++
			f.mu.Unlock()
			continue
		}
		var req struct {
			ID     int               `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		json.Unmarshal(payload, &req)
		f.mu.Lock()
		f.calls = append(f.calls, req.Method)
		f.mu.Unlock()
		reply := func(result any) {
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
			writeServerFrames(conn, b)
		}
		switch req.Method {
		case "auth.login_with_api_key":
			var key string
			json.Unmarshal(req.Params[0], &key)
			reply(key == "good-key")
		case "app.query":
			var filters [][]string
			json.Unmarshal(req.Params[0], &filters)
			// A large answer exercises both extended length forms.
			apps := []map[string]string{}
			if len(filters) == 1 && filters[0][2] == "sloplex" {
				apps = append(apps, map[string]string{"name": "sloplex", "state": "RUNNING", "notes": strings.Repeat("x", 70000)})
			}
			reply(apps)
		case "app.pull_images":
			var args []any
			for _, p := range req.Params {
				var a any
				json.Unmarshal(p, &a)
				args = append(args, a)
			}
			f.mu.Lock()
			f.pulled = args
			f.mu.Unlock()
			// An event and a ping arrive before the answer.
			writeServerFrames(conn, []byte(`{"jsonrpc":"2.0","method":"collection_update","params":{"msg":"added"}}`))
			conn.Write([]byte{0x80 | opPing, 2, 'h', 'i'})
			reply(42)
		case "core.get_jobs":
			f.mu.Lock()
			state := f.jobState
			f.mu.Unlock()
			reply([]map[string]any{{"id": 42, "state": state, "error": "image not found"}})
		default:
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID,
				"error": map[string]any{"code": -32601, "message": "Method not found", "data": map[string]string{"reason": "no " + req.Method}}})
			writeServerFrames(conn, b)
		}
	}
}

// readClientFrame reads one frame, which must be masked.
func readClientFrame(r *bufio.Reader) (byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	if h[1]&0x80 == 0 {
		return 0, nil, errors.New("client frame not masked")
	}
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		io.ReadFull(r, b[:])
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		io.ReadFull(r, b[:])
		n = binary.BigEndian.Uint64(b[:])
	}
	var mask [4]byte
	io.ReadFull(r, mask[:])
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return 0, nil, err
	}
	for i := range p {
		p[i] ^= mask[i%4]
	}
	return h[0] & 0x0F, p, nil
}

// writeServerFrames sends b unmasked, split into two frames to exercise
// continuation.
func writeServerFrames(w io.Writer, b []byte) {
	half := len(b) / 2
	frame := func(first byte, p []byte) []byte {
		out := []byte{first}
		switch n := len(p); {
		case n < 126:
			out = append(out, byte(n))
		case n <= 0xFFFF:
			out = append(out, 126, byte(n>>8), byte(n))
		default:
			out = binary.BigEndian.AppendUint64(append(out, 127), uint64(n))
		}
		return append(out, p...)
	}
	w.Write(append(frame(opText, b[:half]), frame(0x80|opContinue, b[half:])...))
}

func TestTestAndUpdate(t *testing.T) {
	f := newFakeNAS(t)
	s := f.settings()
	state, err := Test(context.Background(), s)
	if err != nil || state != "RUNNING" {
		t.Fatalf("Test = %q, %v", state, err)
	}

	jobCheckDelay = 10 * time.Millisecond
	job, err := Update(context.Background(), s)
	if err != nil || job != 42 {
		t.Fatalf("Update = %d, %v", job, err)
	}
	f.mu.Lock()
	pulled, _ := json.Marshal(f.pulled)
	pongs := f.pongs
	calls := strings.Join(f.calls, " ")
	f.mu.Unlock()
	if string(pulled) != `["sloplex",{"redeploy":true}]` {
		t.Errorf("app.pull_images params = %s", pulled)
	}
	if pongs != 1 {
		t.Errorf("pongs = %d, want 1", pongs)
	}
	if want := "auth.login_with_api_key app.query auth.login_with_api_key app.pull_images core.get_jobs"; calls != want {
		t.Errorf("calls = %q, want %q", calls, want)
	}

	f.mu.Lock()
	f.jobState = "FAILED"
	f.mu.Unlock()
	if _, err := Update(context.Background(), s); err == nil || !strings.Contains(err.Error(), "image not found") {
		t.Errorf("failed job: %v", err)
	}
}

func TestTestErrors(t *testing.T) {
	f := newFakeNAS(t)
	s := f.settings()

	bad := s
	bad.APIKey = "wrong"
	if _, err := Test(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "refused the API key") {
		t.Errorf("wrong key: %v", err)
	}
	other := s
	other.App = "jellyfin"
	if _, err := Test(context.Background(), other); err == nil || !strings.Contains(err.Error(), `no app named "jellyfin"`) {
		t.Errorf("missing app: %v", err)
	}

	// Without a pin, the self-signed certificate is reported with its
	// fingerprint and nothing is sent.
	unpinned := s
	unpinned.CertSHA256 = ""
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
	_, err := Test(context.Background(), unpinned)
	var untrusted *UntrustedError
	if !errors.As(err, &untrusted) || untrusted.Fingerprint != f.fingerprint() {
		t.Errorf("unpinned: %v", err)
	}
	wrong := s
	wrong.CertSHA256 = strings.Repeat("ab", 32)
	if _, err := Test(context.Background(), wrong); !errors.As(err, &untrusted) || untrusted.Fingerprint != f.fingerprint() {
		t.Errorf("wrong pin: %v", err)
	}
	f.mu.Lock()
	if len(f.calls) != 0 {
		t.Errorf("calls reached an untrusted server: %v", f.calls)
	}
	f.mu.Unlock()

	// A server that is not the TrueNAS API.
	notAPI := httptest.NewTLSServer(http.NotFoundHandler())
	defer notAPI.Close()
	s2 := Settings{URL: notAPI.URL, App: "sloplex", APIKey: "k", CertSHA256: fingerprint(notAPI.Certificate())}
	if _, err := Test(context.Background(), s2); err == nil || !strings.Contains(err.Error(), "does not offer the TrueNAS API") {
		t.Errorf("not the API: %v", err)
	}
	// Nothing listening.
	notAPI.Close()
	if _, err := Test(context.Background(), s2); err == nil || !strings.Contains(err.Error(), "could not connect") {
		t.Errorf("closed: %v", err)
	}
}

// TestRPCError checks that a JSON-RPC error carries TrueNAS's reason.
func TestRPCError(t *testing.T) {
	f := newFakeNAS(t)
	err := session(context.Background(), f.settings(), func(c *rpcConn) error {
		var x any
		return c.call("app.nonsense", []any{}, &x)
	})
	if err == nil || err.Error() != "TrueNAS refused app.nonsense: no app.nonsense" {
		t.Errorf("rpc error: %v", err)
	}
}

func TestTimeout(t *testing.T) {
	// A server that accepts the WebSocket and then says nothing.
	block := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, brw, _ := w.(http.Hijacker).Hijack()
		defer conn.Close()
		brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + acceptKey(r.Header.Get("Sec-Websocket-Key")) + "\r\n\r\n")
		brw.Flush()
		<-block
	}))
	defer srv.Close()
	defer close(block)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Test(ctx, Settings{URL: srv.URL, App: "sloplex", APIKey: "k", CertSHA256: fingerprint(srv.Certificate())})
	if err == nil || time.Since(start) > 5*time.Second {
		t.Errorf("silent server: %v after %v", err, time.Since(start))
	}
}

func TestApply(t *testing.T) {
	fp := strings.Repeat("ab", 32)
	old := Settings{URL: "https://nas.lan", App: "sloplex", APIKey: "secret", CertSHA256: fp}
	for _, c := range []struct {
		name                string
		old                 Settings
		url, app, key, cert string
		want                Settings
		err                 string
	}{
		{"new", Settings{}, "https://10.0.0.38/", "sloplex", " k1 ", "", Settings{URL: "https://10.0.0.38", App: "sloplex", APIKey: "k1"}, ""},
		{"port and pin", Settings{}, "https://nas.lan:8443", "sloplex", "k", strings.ToUpper(strings.Repeat("AB:", 31)) + "AB",
			Settings{URL: "https://nas.lan:8443", App: "sloplex", APIKey: "k", CertSHA256: fp}, ""},
		{"keep key", old, "https://nas.lan", "media", "", fp, Settings{URL: "https://nas.lan", App: "media", APIKey: "secret", CertSHA256: fp}, ""},
		{"new key", old, "https://nas.lan", "sloplex", "k2", fp, Settings{URL: "https://nas.lan", App: "sloplex", APIKey: "k2", CertSHA256: fp}, ""},
		{"address changed", old, "https://evil.example", "sloplex", "", fp, Settings{}, "enter the API key again"},
		{"pin changed", old, "https://nas.lan", "sloplex", "", strings.Repeat("cd", 32), Settings{}, "enter the API key again"},
		{"pin removed", old, "https://nas.lan", "sloplex", "", "", Settings{}, "enter the API key again"},
		{"no key at all", Settings{}, "https://nas.lan", "sloplex", "", "", Settings{}, "enter an API key"},
		{"http", Settings{}, "http://nas.lan", "sloplex", "k", "", Settings{}, "must start with https://"},
		{"path", Settings{}, "https://nas.lan/ui", "sloplex", "k", "", Settings{}, "no path"},
		{"no host", Settings{}, "https://", "sloplex", "k", "", Settings{}, "must look like"},
		{"bare host", Settings{}, "nas.lan", "sloplex", "k", "", Settings{}, "must"},
		{"bad app", Settings{}, "https://nas.lan", "Sloplex App", "k", "", Settings{}, "app name"},
		{"bad pin", Settings{}, "https://nas.lan", "sloplex", "k", "abc", Settings{}, "64 hex digits"},
	} {
		got, err := Apply(c.old, c.url, c.app, c.key, c.cert)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: %+v, %v; want error %q", c.name, got, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: %+v, %v; want %+v", c.name, got, err, c.want)
		}
	}
}

func TestStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "truenas.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Get(); ok {
		t.Error("settings before saving")
	}
	s := Settings{URL: "https://nas.lan", App: "sloplex", APIKey: "secret"}
	if err := st.Set(s); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v, %v", fi.Mode(), err)
	}
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := again.Get(); !ok || got != s {
		t.Errorf("reopened: %+v %v", got, ok)
	}
	if err := again.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Get(); ok {
		t.Error("settings after deleting")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file after deleting: %v", err)
	}
	if err := again.Delete(); err != nil {
		t.Errorf("deleting twice: %v", err)
	}
	os.WriteFile(path, []byte("{"), 0o600)
	if _, err := Open(path); err == nil {
		t.Error("broken file opened")
	}
}
