// Package gdm answers GDM ("G'Day Mate") LAN discovery searches.
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
)

// Port is the UDP port servers listen on for searches.
const Port = 32414

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
func Response(a Announcement) []byte {
	var b strings.Builder
	b.WriteString("HTTP/1.0 200 OK\r\n")
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
