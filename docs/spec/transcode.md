# Transcoding

## Provenance

- These come from publicly available community documentation of the
  media-server HTTP API and from open-source client libraries' public docs:
  - the endpoint paths `/video/:/transcode/universal/start.m3u8`,
    `/video/:/transcode/universal/stop`, `/video/:/transcode/universal/ping`,
    and `/transcode/sessions`;
  - the session path shape `.../universal/session/{id}/base/index.m3u8`;
  - the parameters `path`, `mediaIndex`, `partIndex`, `offset`,
    `videoResolution`, `maxVideoBitrate`, `directStream`, and `session`;
  - the `TranscodeSession` element.
- These are **(sloplex)**:
  - the progressive `start.mp4` endpoint;
  - capability session URLs;
  - the decision rules, limits, idle timeout, and ffmpeg arguments.

## Model

A **transcode** converts one media part into something every client can
play: H.264 video and AAC stereo audio. It is delivered one of two ways:

| Protocol | Endpoint     | Output                                                        |
|----------|--------------|---------------------------------------------------------------|
| HLS      | `start.m3u8` | MPEG-TS segments of about 4 s plus playlists, written to `<dataDir>/transcode/<session>/` |
| HTTP     | `start.mp4`  | Fragmented MP4 streamed in the response body; nothing is written to disk |

- Transcoding needs `ffmpeg`, configured as in [artwork.md](artwork.md).
  Without it, the start endpoints return `503`.
- At most `maxTranscodes` transcodes run at once (config; default 2). HLS
  sessions and HTTP streams both count. Beyond that, the start endpoints
  return `503` with `Retry-After: 10`.

### Decisions (sloplex)

**Target box.**

- `videoResolution=WxH` caps the output size. It defaults to the source
  size, so there is no scaling.
- `maxVideoBitrate`, in kbps, caps the video bitrate. It defaults to no cap.

**Video is copied** when all of these hold:

- the source codec is `h264`;
- the source fits the target box;
- the source bitrate is within `maxVideoBitrate`;
- `directStream` is not `0`.

Otherwise video is encoded with `libx264 -preset veryfast -crf 21`:

- It is scaled down to fit the box, keeping the aspect ratio, with even
  dimensions.
- With a bitrate cap it adds `-maxrate` and `-bufsize` (2× the cap).
- For HLS it forces key frames every 4 s.

**Audio is copied** when the source is AAC with at most 2 channels.
Otherwise it is encoded to AAC stereo at 160 kbps. A source without audio
produces video-only output.

**Other streams.** Only the first video stream and the first audio stream
are used. Subtitles are not carried.

**Start position.** `offset`, in seconds, starts the output at that position
of the source. Output timestamps start at 0, so clients add `offset` back
when reporting progress.

## Endpoints

### Identifying the media

Both start endpoints take the media from these parameters:

| Name         | Meaning                                                     |
|--------------|-------------------------------------------------------------|
| `path`       | Required. `/library/metadata/{id}` of a movie or episode    |
| `mediaIndex` | Which Media of the item. Default 0                          |
| `partIndex`  | Which Part of that Media. Default 0                         |

| Problem                                                       | Response |
|---------------------------------------------------------------|----------|
| An unknown item, an item hidden by library access, or an index out of range | `404` |
| A show or season                                              | `400`    |

### `GET /video/:/transcode/universal/start.m3u8`

Starts an HLS session and returns a master playlist,
`application/vnd.apple.mpegurl`:

```
#EXTM3U
#EXT-X-VERSION:3
#EXT-X-STREAM-INF:BANDWIDTH=<bits/s>,RESOLUTION=<w>x<h>
session/<sid>/base/index.m3u8
```

- `<sid>` is a new random 128-bit hex ID created by the server.
- The variant URL is relative, so it resolves to
  `/video/:/transcode/universal/session/<sid>/base/index.m3u8`.
- `session` is an optional label chosen by the client, used by `stop` and
  `ping`. Starting a new session with the same label for the same account
  stops the old session; this is how seeking is done.

### `GET /video/:/transcode/universal/session/{sid}/base/index.m3u8` and `…/base/{n}.ts`

These serve the media playlist and its segments.

- **(sloplex)** These URLs need **no token**. Knowing the unguessable `sid`
  is the authorisation, so players that drop query strings can still play.
- An unknown `sid` returns `404`.
- Segment names are five digits (`00000.ts`). Any other name returns `404`.

How requests behave while the session is running:

- The playlist is returned once it lists at least one segment. It is an
  `EVENT` playlist that grows as ffmpeg works, and it ends with
  `#EXT-X-ENDLIST` when the transcode finishes.
- A segment is returned once the playlist lists it.
- Either kind of request waits up to 30 s, then returns `404` if the content
  still does not exist.
- If ffmpeg fails before producing what was asked for, the response is
  `500`.

Every request to a session counts as activity.

### `GET /video/:/transcode/universal/start.mp4` (sloplex)

Streams fragmented MP4 (`video/mp4`) for as long as the client reads. It
accepts the same parameters as `start.m3u8` except `session`, and the
response cannot be seeked. Closing the connection stops the transcode.

### `GET|PUT /video/:/transcode/universal/stop`

Stops the caller's HLS sessions that carry `session=<label>`, deletes their
files, and returns `200`.

### `GET|PUT /video/:/transcode/universal/ping`

Marks the caller's sessions labelled `session=<label>` as active and returns
`200`.

### `GET /transcode/sessions` (admin)

Returns a `MediaContainer` of `TranscodeSession` elements, one per running
transcode:

| Attribute       | Meaning                                    |
|-----------------|--------------------------------------------|
| `key`           | The session ID                             |
| `ratingKey`     | The item                                   |
| `protocol`      | `hls` or `http`                            |
| `videoDecision` | `copy` or `transcode`                      |
| `audioDecision` | `copy`, `transcode`, or empty when there is no audio |
| `width`, `height` | Output size                              |
| `complete`      | `1` once ffmpeg has finished               |

## Lifecycle (sloplex)

- An HLS session with no activity for 2 minutes is stopped and its directory
  deleted.
- On startup, the server deletes the leftover `<dataDir>/transcode/` from any
  earlier run. On shutdown it stops all transcodes.
- ffmpeg runs at full speed, without throttling.

## Not implemented yet

- Hardware encoders (VAAPI, NVENC, QSV).
- Subtitle burn-in.
- Multiple audio tracks.
- Adaptive bitrate ladders.
- The `decision` endpoint.
