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
