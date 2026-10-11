# GDM LAN discovery

## Provenance

- The port numbers, multicast group, `M-SEARCH` request line, the `HELLO`
  and `BYE` announcement lines, and the response header names come from
  publicly available community documentation of the "G'Day Mate" (GDM)
  protocol and from open-source client libraries' public docs.
- The choice of headers to send, the announcement timing and destinations,
  and the fallback behaviour are **(sloplex)**.

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

## Announcements (server → clients)

The server announces itself without being asked, so clients that are
already running see it appear and disappear at once.

- `HELLO` when discovery starts:

  ```
  HELLO * HTTP/1.0\r\n
  Content-Type: plex/media-server\r\n
  Resource-Identifier: <machineIdentifier>\r\n
  Name: <friendlyName>\r\n
  Port: <HTTP port>\r\n
  Version: <server version>\r\n
  Updated-At: <unix seconds>\r\n
  \r\n
  ```

- `BYE` when the server shuts down:

  ```
  BYE * HTTP/1.0\r\n
  Content-Type: plex/media-server\r\n
  Resource-Identifier: <machineIdentifier>\r\n
  \r\n
  ```

- **(sloplex)** Each announcement goes to UDP port **32413** at both the
  multicast group `239.0.0.250` and the broadcast address
  `255.255.255.255`, from an ephemeral port. A destination that fails is
  logged at debug level and skipped.
- **(sloplex)** UDP can lose datagrams, so `HELLO` is sent three times: at
  start, after 2 s, and after 10 s. `BYE` is sent once, and shutdown waits
  for it, up to 1 s.
- Header values follow the same rules as the search response.

## Not implemented yet

- Client/player discovery on ports 32410–32413.

## Fallback

- **(sloplex)** If joining the multicast group fails, for example in a
  container with no multicast route, the server listens on plain UDP
  `0.0.0.0:32414` and logs a warning.
- Discovery can be turned off with `"discovery": false` in the config. That
  also turns off announcements. A stored `GdmEnabled` preference overrides
  the config at the next start; see [preferences.md](preferences.md).
- `Name` is the server name in effect when each reply or announcement is
  sent, so a `FriendlyName` change applies without a restart.
