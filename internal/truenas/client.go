package truenas

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// Timeout bounds a whole exchange with TrueNAS.
const Timeout = 30 * time.Second

// jobCheckDelay is how long Update waits before reading the job once.
var jobCheckDelay = 2 * time.Second

// UntrustedError reports a certificate that was not trusted, with its
// fingerprint so the owner can compare and pin it.
type UntrustedError struct {
	Fingerprint string
	Err         error
}

func (e *UntrustedError) Error() string { return "certificate not trusted" }
func (e *UntrustedError) Unwrap() error { return e.Err }

func fingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	return hex.EncodeToString(sum[:])
}

// Test logs in and looks the app up, returning its state.
func Test(ctx context.Context, s Settings) (state string, err error) {
	err = session(ctx, s, func(c *rpcConn) error {
		var apps []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		}
		if err := c.call("app.query", []any{[][]any{{"name", "=", s.App}}}, &apps); err != nil {
			return err
		}
		if len(apps) == 0 {
			return fmt.Errorf("TrueNAS has no app named %q", s.App)
		}
		state = apps[0].State
		return nil
	})
	return state, err
}

// Update asks TrueNAS to pull the app's images and redeploy it, returning
// the job ID once the job has not failed straight away.
func Update(ctx context.Context, s Settings) (job int, err error) {
	err = session(ctx, s, func(c *rpcConn) error {
		if err := c.call("app.pull_images", []any{s.App, map[string]bool{"redeploy": true}}, &job); err != nil {
			return err
		}
		select {
		case <-time.After(jobCheckDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
		var jobs []struct {
			State string `json:"state"`
			Error string `json:"error"`
		}
		if err := c.call("core.get_jobs", []any{[][]any{{"id", "=", job}}}, &jobs); err != nil {
			return err
		}
		if len(jobs) > 0 && jobs[0].State == "FAILED" {
			return fmt.Errorf("TrueNAS could not update the app: %s", jobs[0].Error)
		}
		return nil
	})
	return job, err
}

// session connects, logs in with the API key, and runs f.
func session(ctx context.Context, s Settings, f func(*rpcConn) error) error {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	ws, err := dial(ctx, s)
	if err != nil {
		return err
	}
	defer ws.Close()
	stop := context.AfterFunc(ctx, func() { ws.conn.SetDeadline(time.Now()) })
	defer stop()
	c := &rpcConn{ws: ws}
	var ok bool
	if err := c.call("auth.login_with_api_key", []any{s.APIKey}, &ok); err != nil {
		return err
	}
	if !ok {
		return errors.New("TrueNAS refused the API key")
	}
	return f(c)
}

// ---- WebSocket (RFC 6455), client side ----

type wsConn struct {
	conn net.Conn
	br   *bufio.Reader
}

func (w *wsConn) Close() error { return w.conn.Close() }

func dial(ctx context.Context, s Settings) (*wsConn, error) {
	u, err := url.Parse(s.URL)
	if err != nil || u.Scheme != "https" {
		return nil, fmt.Errorf("bad TrueNAS address %q", s.URL)
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "443")
	}
	cfg := &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}
	if s.CertSHA256 != "" {
		// The pin replaces the usual checks, so a self-signed certificate
		// for an IP address works.
		cfg.InsecureSkipVerify = true
		cfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("no certificate")
			}
			if fp := fingerprint(cs.PeerCertificates[0]); fp != s.CertSHA256 {
				return &UntrustedError{Fingerprint: fp, Err: errors.New("the certificate does not match the saved fingerprint")}
			}
			return nil
		}
	}
	d := tls.Dialer{Config: cfg}
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		var untrusted *UntrustedError
		var verify *tls.CertificateVerificationError
		switch {
		case errors.As(err, &untrusted):
			return nil, untrusted
		case errors.As(err, &verify) && len(verify.UnverifiedCertificates) > 0:
			return nil, &UntrustedError{Fingerprint: fingerprint(verify.UnverifiedCertificates[0]), Err: verify.Err}
		}
		return nil, fmt.Errorf("could not connect to TrueNAS: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	ws := &wsConn{conn: conn, br: bufio.NewReader(conn)}
	if err := ws.handshake(u.Host); err != nil {
		conn.Close()
		return nil, err
	}
	return ws, nil
}

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func acceptKey(key string) string {
	sum := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func (w *wsConn) handshake(host string) error {
	nonce := make([]byte, 16)
	rand.Read(nonce)
	key := base64.StdEncoding.EncodeToString(nonce)
	req := &http.Request{Method: http.MethodGet, URL: &url.URL{Path: "/api/current"}, Host: host,
		Header: http.Header{
			"Upgrade": {"websocket"}, "Connection": {"Upgrade"},
			"Sec-Websocket-Key": {key}, "Sec-Websocket-Version": {"13"},
		}}
	if err := req.Write(w.conn); err != nil {
		return fmt.Errorf("could not talk to TrueNAS: %w", err)
	}
	resp, err := http.ReadResponse(w.br, req)
	if err != nil {
		return fmt.Errorf("could not talk to TrueNAS: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Sec-Websocket-Accept") != acceptKey(key) {
		return fmt.Errorf("%s does not offer the TrueNAS API (it answered %s)", host, resp.Status)
	}
	return nil
}

const (
	opContinue = 0x0
	opText     = 0x1
	opBinary   = 0x2
	opClose    = 0x8
	opPing     = 0x9
	opPong     = 0xA
	maxMessage = 16 << 20
)

// writeFrame sends one final, masked frame, as clients must.
func (w *wsConn) writeFrame(op byte, payload []byte) error {
	hdr := []byte{0x80 | op}
	switch n := len(payload); {
	case n < 126:
		hdr = append(hdr, 0x80|byte(n))
	case n <= 0xFFFF:
		hdr = append(hdr, 0x80|126, byte(n>>8), byte(n))
	default:
		hdr = binary.BigEndian.AppendUint64(append(hdr, 0x80|127), uint64(n))
	}
	mask := make([]byte, 4)
	rand.Read(mask)
	frame := append(append(hdr, mask...), payload...)
	for i := range payload {
		frame[len(hdr)+4+i] ^= mask[i%4]
	}
	_, err := w.conn.Write(frame)
	return err
}

// readMessage returns the next text or binary message, answering pings.
func (w *wsConn) readMessage() ([]byte, error) {
	var msg []byte
	for {
		var h [2]byte
		if _, err := io.ReadFull(w.br, h[:]); err != nil {
			return nil, err
		}
		fin, op, masked := h[0]&0x80 != 0, h[0]&0x0F, h[1]&0x80 != 0
		n := uint64(h[1] & 0x7F)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(w.br, b[:]); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(w.br, b[:]); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		if n > maxMessage || uint64(len(msg))+n > maxMessage {
			return nil, errors.New("TrueNAS sent a message that is too large")
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(w.br, mask[:]); err != nil {
				return nil, err
			}
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(w.br, payload); err != nil {
			return nil, err
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		switch op {
		case opPing:
			if err := w.writeFrame(opPong, payload); err != nil {
				return nil, err
			}
		case opPong:
		case opClose:
			return nil, errors.New("TrueNAS closed the connection")
		case opText, opBinary, opContinue:
			msg = append(msg, payload...)
			if fin {
				return msg, nil
			}
		default:
			return nil, fmt.Errorf("TrueNAS sent an unknown frame type %d", op)
		}
	}
}

// ---- JSON-RPC 2.0 ----

type rpcConn struct {
	ws   *wsConn
	next int
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Reason string `json:"reason"`
	} `json:"data"`
}

func (c *rpcConn) call(method string, params, result any) error {
	c.next++
	id := c.next
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	if err := c.ws.writeFrame(opText, req); err != nil {
		return fmt.Errorf("could not talk to TrueNAS: %w", err)
	}
	for {
		b, err := c.ws.readMessage()
		if err != nil {
			return fmt.Errorf("%s: %w", method, err)
		}
		var resp struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if err := json.Unmarshal(b, &resp); err != nil {
			return fmt.Errorf("%s: TrueNAS sent something that is not JSON-RPC", method)
		}
		if resp.ID == nil || *resp.ID != id {
			continue // an event, or an answer to something else
		}
		if resp.Error != nil {
			msg := resp.Error.Message
			if resp.Error.Data.Reason != "" {
				msg = resp.Error.Data.Reason
			}
			return fmt.Errorf("TrueNAS refused %s: %s", method, msg)
		}
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("%s: unexpected answer from TrueNAS", method)
		}
		return nil
	}
}
