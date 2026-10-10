// Package gdm answers GDM ("G'Day Mate") LAN discovery searches and
// announces the server.
// See docs/spec/discovery.md.
package gdm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"
)

// Port is the UDP port servers listen on for searches.
const Port = 32414

// AnnouncePort is the UDP port announcements are sent to.
const AnnouncePort = 32413

// AnnounceTargets are the default announcement destinations.
var AnnounceTargets = []*net.UDPAddr{
	{IP: net.IPv4(239, 0, 0, 250), Port: AnnouncePort},
	{IP: net.IPv4bcast, Port: AnnouncePort},
}

// Group is the GDM multicast group.
var Group = net.IPv4(239, 0, 0, 250)

// Announcement is what the server reports about itself.
type Announcement struct {
	MachineID string
	Name      string
	Version   string
	Port      int
	UpdatedAt int64
}

var searchPrefix = []byte("M-SEARCH * HTTP/1.")

// IsSearch reports whether a datagram is a GDM search request.
func IsSearch(b []byte) bool {
	return bytes.HasPrefix(b, searchPrefix)
}

// Response renders the reply to a search request.
func Response(a Announcement) []byte { return message("HTTP/1.0 200 OK", a) }

// Hello renders the announcement sent when the server starts.
func Hello(a Announcement) []byte { return message("HELLO * HTTP/1.0", a) }

// Bye renders the announcement sent when the server stops.
func Bye(a Announcement) []byte {
	return []byte("BYE * HTTP/1.0\r\nContent-Type: plex/media-server\r\n" +
		"Resource-Identifier: " + clean(a.MachineID) + "\r\n\r\n")
}

func message(first string, a Announcement) []byte {
	var b strings.Builder
	b.WriteString(first + "\r\n")
	header := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, clean(v)) }
	header("Content-Type", "plex/media-server")
	header("Resource-Identifier", a.MachineID)
	header("Name", a.Name)
	header("Port", fmt.Sprint(a.Port))
	header("Version", a.Version)
	header("Updated-At", fmt.Sprint(a.UpdatedAt))
	b.WriteString("\r\n")
	return []byte(b.String())
}

func clean(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

// Listen opens the discovery socket. It joins the multicast group if it can
// and otherwise falls back to plain UDP on the same port.
func Listen(log *slog.Logger) (net.PacketConn, error) {
	conn, err := net.ListenMulticastUDP("udp4", nil, &net.UDPAddr{IP: Group, Port: Port})
	if err == nil {
		return conn, nil
	}
	log.Warn("gdm: multicast unavailable, listening on unicast/broadcast only", "err", err)
	return net.ListenUDP("udp4", &net.UDPAddr{Port: Port})
}

// Serve answers search requests on conn until ctx is cancelled. It closes
// conn before returning. info is called for every reply so the announcement
// stays current.
func Serve(ctx context.Context, conn net.PacketConn, info func() Announcement, log *slog.Logger) error {
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	defer conn.Close()

	buf := make([]byte, 2048)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		if !IsSearch(buf[:n]) {
			continue
		}
		if _, err := conn.WriteTo(Response(info()), addr); err != nil {
			log.Warn("gdm: reply failed", "to", addr, "err", err)
		}
	}
}

// helloAt are the offsets from start at which HELLO is sent.
var helloAt = []time.Duration{0, 2 * time.Second, 10 * time.Second}

// Announce sends HELLO to targets at start and twice more, and BYE once ctx
// is cancelled. It returns after sending BYE (or giving up after 1 s).
func Announce(ctx context.Context, info func() Announcement, targets []*net.UDPAddr, log *slog.Logger) {
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		log.Warn("gdm: announcements disabled", "err", err)
		return
	}
	defer conn.Close()
	send := func(msg []byte) {
		for _, t := range targets {
			if _, err := conn.WriteToUDP(msg, t); err != nil {
				log.Debug("gdm: announcement failed", "to", t, "err", err)
			}
		}
	}
	start := time.Now()
	for _, at := range helloAt {
		select {
		case <-ctx.Done():
		case <-time.After(time.Until(start.Add(at))):
			send(Hello(info()))
			continue
		}
		break
	}
	<-ctx.Done()
	_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
	send(Bye(info()))
}
