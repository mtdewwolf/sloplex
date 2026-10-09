# GDM LAN discovery

## Provenance

- The port numbers, multicast group, `M-SEARCH` request line, and response
  header names come from publicly available community documentation of the
  "G'Day Mate" (GDM) protocol and from open-source client libraries' public
  docs.
- The choice of headers to send and the fallback behaviour are **(sloplex)**.

## Transport

- UDP, IPv4.
- The server listens on port **32414**. It joins multicast group
  **239.0.0.250** and also accepts unicast and broadcast datagrams on that
  port.

## Search request (client → server)

A datagram whose payload starts with:

```
M-SEARCH * HTTP/1.
```

The rest of the payload is ignored. Datagrams that do not match are ignored.

## Search response (server → client)

The server sends this reply as a unicast datagram to the source address and
port of the request:

```
HTTP/1.0 200 OK\r\n
Content-Type: plex/media-server\r\n
Resource-Identifier: <machineIdentifier>\r\n
Name: <friendlyName>\r\n
Port: <HTTP port>\r\n
Version: <server version>\r\n
Updated-At: <unix seconds>\r\n
\r\n
```

- `Resource-Identifier` MUST equal `machineIdentifier` from `GET /identity`.
- `Port` is the TCP port of the HTTP API.
- Header values MUST NOT contain CR or LF. The server strips them.

## Not implemented yet

- Unsolicited `HELLO` / `BYE` announcements to clients on port 32413.
- Client/player discovery on ports 32410–32413.

## Fallback

- **(sloplex)** If joining the multicast group fails, for example in a
  container with no multicast route, the server listens on plain UDP
  `0.0.0.0:32414` and logs a warning.
- Discovery can be turned off with `"discovery": false` in the config.
