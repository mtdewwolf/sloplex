package gdm

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

func TestResponse(t *testing.T) {
	got := string(Response(Announcement{MachineID: "abc", Name: "Den\r\nEvil: 1", Version: "0.1", Port: 32400, UpdatedAt: 42}))
	want := "HTTP/1.0 200 OK\r\n" +
		"Content-Type: plex/media-server\r\n" +
		"Resource-Identifier: abc\r\n" +
		"Name: DenEvil: 1\r\n" +
		"Port: 32400\r\n" +
		"Version: 0.1\r\n" +
		"Updated-At: 42\r\n\r\n"
	if got != want {
		t.Errorf("Response =\n%q\nwant\n%q", got, want)
	}
}

func TestServe(t *testing.T) {
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, conn, func() Announcement {
			return Announcement{MachineID: "m1", Name: "srv", Port: 32400}
		}, slog.New(slog.DiscardHandler))
	}()

	client, err := net.DialUDP("udp4", nil, conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("hello")); err != nil { // ignored
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("M-SEARCH * HTTP/1.1\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1024)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(buf[:n]), "Resource-Identifier: m1\r\n") {
		t.Errorf("reply = %q", buf[:n])
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop on cancel")
	}
}

func TestHelloBye(t *testing.T) {
	a := Announcement{MachineID: "abc\r\n", Name: "Den", Version: "0.1", Port: 32400, UpdatedAt: 42}
	if got, want := string(Hello(a)), "HELLO * HTTP/1.0\r\nContent-Type: plex/media-server\r\nResource-Identifier: abc\r\n"+
		"Name: Den\r\nPort: 32400\r\nVersion: 0.1\r\nUpdated-At: 42\r\n\r\n"; got != want {
		t.Errorf("Hello =\n%q\nwant\n%q", got, want)
	}
	if got, want := string(Bye(a)), "BYE * HTTP/1.0\r\nContent-Type: plex/media-server\r\nResource-Identifier: abc\r\n\r\n"; got != want {
		t.Errorf("Bye =\n%q\nwant\n%q", got, want)
	}
}

func TestAnnounce(t *testing.T) {
	old := helloAt
	helloAt = []time.Duration{0, 50 * time.Millisecond, 100 * time.Millisecond}
	defer func() { helloAt = old }()

	l, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	// An unreachable target is skipped without stopping the others.
	targets := []*net.UDPAddr{{IP: net.IPv4(127, 0, 0, 1), Port: 0}, l.LocalAddr().(*net.UDPAddr)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Announce(ctx, func() Announcement { return Announcement{MachineID: "m1", Name: "srv", Port: 32400} }, targets, slog.New(slog.DiscardHandler))
	}()

	read := func() string {
		_ = l.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 2048)
		n, _, err := l.ReadFromUDP(buf)
		if err != nil {
			t.Fatal(err)
		}
		return strings.SplitN(string(buf[:n]), "\r\n", 2)[0]
	}
	for i := 0; i < 3; i++ {
		if got := read(); got != "HELLO * HTTP/1.0" {
			t.Fatalf("announcement %d = %q", i, got)
		}
	}
	cancel()
	if got := read(); got != "BYE * HTTP/1.0" {
		t.Fatalf("after cancel = %q", got)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Announce did not return after BYE")
	}

	// Cancelled before the HELLOs finish: BYE follows at once.
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	Announce(ctx, func() Announcement { return Announcement{MachineID: "m1"} }, targets[1:], slog.New(slog.DiscardHandler))
	if got := read(); got != "BYE * HTTP/1.0" || time.Since(start) > time.Second {
		t.Errorf("early cancel: %q after %v", got, time.Since(start))
	}
}
