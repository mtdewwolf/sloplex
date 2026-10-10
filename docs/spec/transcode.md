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
  - the `TranscodeSession` element, including its `transcodeHwEncoding` and
    `transcodeHwFullPipeline` attributes.
- The VP9 and Opus encoder options (`libvpx-vp9 -deadline realtime
  -cpu-used -row-mt`, `libopus`) come from ffmpeg's public documentation.
- The ffmpeg hardware options (`-hwaccel`, `-vaapi_device`,
  `-init_hw_device`, `scale_vaapi`, `scale_cuda`, `scale_qsv`, `hwupload`,
  and the `h264_*` encoders and their rate-control options) come from
  ffmpeg's own public documentation.
- These are **(sloplex)**:
  - the progressive `start.mp4` and `start.webm` endpoints;
  - capability session URLs;
  - the decision rules, limits, idle timeout, and ffmpeg arguments.

## Model

A **transcode** converts one media part into something every client can
play: H.264 video and AAC stereo audio. It is delivered one of three ways:

| Protocol | Endpoint     | Output                                                        |
|----------|--------------|---------------------------------------------------------------|
| HLS      | `start.m3u8` | MPEG-TS segments of about 4 s plus playlists, written to `<dataDir>/transcode/<session>/` |
| HTTP     | `start.mp4`  | Fragmented MP4 streamed in the response body; nothing is written to disk |
| HTTP     | `start.webm` | **(sloplex)** VP9 video and Opus audio in WebM, streamed like `start.mp4`; see **WebM** |

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

Otherwise video is encoded:

- It is scaled down to fit the box, keeping the aspect ratio, with even
  dimensions.
- It uses the hardware encoder if one is active (see **Hardware
  acceleration**), and otherwise `libx264 -preset veryfast -crf 21`.
- With a bitrate cap it adds `-maxrate` and `-bufsize` (2× the cap).
- For HLS it forces key frames every 4 s.

**HDR sources** are never copied. They are tone mapped to SDR; see **HDR
tone mapping**.

**Audio is copied** when the source is AAC with at most 2 channels.
Otherwise it is encoded to AAC stereo at 160 kbps. A source without audio
produces video-only output.

**Other streams.** Only the first video stream and one audio stream are
used. The audio stream is the first one unless another is chosen. Subtitles
are never muxed into the output, but they can be burned in. See
[streams.md](streams.md) for `audioStreamID`, `subtitleStreamID`, and
`subtitles`.

**Start position.** `offset`, in seconds, starts the output at that position
of the source. Output timestamps start at 0, so clients add `offset` back
when reporting progress.

## WebM (sloplex)

`start.webm` is for browsers that cannot decode H.264, such as Chromium
builds without proprietary codecs. It follows the decisions above, with
these changes:

- **Video is copied** when the source codec is `vp9` (instead of `h264`) and
  the other copy conditions hold. Otherwise it is encoded with
  `libvpx-vp9 -deadline realtime -cpu-used 8 -row-mt 1 -crf 32
  -pix_fmt yuv420p`, with `-b:v 0`, or with `-b:v`, `-maxrate`, and
  `-bufsize` (2×) set from `maxVideoBitrate`.
- VP9 is always encoded in software, so only the software pipeline is used.
  Software VP9 is much slower than H.264, so clients should set
  `videoResolution`; the web app asks for `1280x720`.
- **Audio is copied** when the source is Opus with at most 2 channels.
  Otherwise it is encoded to Opus stereo at 128 kbps.
- The source's per-stream `DURATION` tags are not copied. Matroska sources
  carry them for the whole file, and browsers would take them as the
  length of a stream that starts at `offset`.
- The output has no duration and no cues, and the response cannot be
  seeked.

## HDR tone mapping (sloplex)

A video stream is **HDR** when ffprobe reports its transfer as one of:

- `smpte2084` (PQ, used by HDR10 and Dolby Vision base layers);
- `arib-std-b67` (HLG).

The item's Media reports it as `hdr` (`pq` or `hlg`) in the stored probe
data.

Encoding an HDR source runs this filter chain in software before any
subtitle burn-in or scaling:

```
zscale=tin=<smpte2084|arib-std-b67>:min=bt2020nc:pin=bt2020:rin=tv:t=linear:npl=100,
format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,
zscale=t=bt709:m=bt709:r=tv,format=yuv420p
```

- The output is tagged `-colorspace bt709 -color_primaries bt709
  -color_trc bt709`.
- Like burn-in, tone mapping needs frames in system memory. It therefore
  skips the full-hardware pipeline; the hardware-encode-only and software
  pipelines still apply.
- **Effect:** diffuse white (about 100 nits) lands in the upper mid-tones,
  and highlights of 1000 nits and more reach full white. Naive conversion
  instead leaves the picture grey and dull.

## Hardware acceleration (sloplex)

### Configuration

| Config key      | Values                                                   | Default |
|-----------------|----------------------------------------------------------|---------|
| `hwaccel`       | `none`, `auto`, `vaapi`, `qsv`, `nvenc`, `videotoolbox`  | `auto`  |
| `hwaccelDevice` | DRM render node for `vaapi` and `qsv`                    | `/dev/dri/renderD128` |

### Detection at startup

The server checks which accelerator works by encoding a short test pattern.

- `auto` tries these in order and uses the first that works:
  - Linux: `nvenc`, `qsv`, `vaapi`;
  - macOS: `videotoolbox`.
- An explicitly named accelerator is checked the same way.
- If nothing works, the server logs a warning and uses software encoding.
  It never refuses to start over this.
- The result is shown by `GET /sloplex/transcoder`.

### Pipelines

Each video encode tries a chain of pipelines, fastest first. It moves to the
next one when ffmpeg fails before producing any output:

1. **Full hardware:** hardware decode, scaling, and encoding. Frames stay on
   the GPU.
2. **Hardware encode only:** software decode and scaling, then hardware
   encode. This covers source codecs the GPU cannot decode.
3. **Software:** libx264.

`videotoolbox` has no separate step 2, because its decoder falls back to
software by itself.

A failure after output has started is not retried. Once the client has
data, restarting would break the stream.

### Arguments

| Accel        | Step 1 (full hardware)                                                         | Step 2 (encode only)                                  | Encoder and rate control |
|--------------|--------------------------------------------------------------------------------|-------------------------------------------------------|--------------------------|
| vaapi        | `-hwaccel vaapi -hwaccel_device D -hwaccel_output_format vaapi`, `scale_vaapi=w:h:format=nv12` | `-vaapi_device D`, `format=nv12,hwupload` (then `scale_vaapi` when scaling) | `h264_vaapi`. No cap: `-qp 23`. With a cap: `-b:v` 0.8×cap, `-maxrate`, `-bufsize` |
| qsv          | `-hwaccel qsv -qsv_device D -hwaccel_output_format qsv` plus the matching QSV decoder (`-c:v hevc_qsv` and so on), `scale_qsv=w:h:format=nv12` | software `scale`, `format=nv12`                      | `h264_qsv -preset veryfast -forced_idr 1`. No cap: `-global_quality 23`. With a cap: as vaapi |
| nvenc        | `-hwaccel cuda -hwaccel_output_format cuda`, `scale_cuda=w:h:format=nv12`      | software `scale`, `format=yuv420p`                    | `h264_nvenc -preset p4 -rc vbr -cq 23 -b:v 0 -forced-idr 1`. A cap adds `-maxrate` and `-bufsize` |
| videotoolbox | `-hwaccel videotoolbox`, software `scale`, `format=nv12`                       | –                                                     | `h264_videotoolbox -realtime 1`. Bitrate is the cap, or by output height: ≤480p 1500k, ≤720p 3000k, ≤1080p 6000k, otherwise 15000k |

- `w` and `h` are the planned output size. A scaler only runs when the size
  changes, or when converting 10-bit sources to 8-bit `nv12`.
- Every pipeline forces key frames for HLS, as in the software pipeline.

### `GET /sloplex/transcoder` (admin)

Returns JSON:

```json
{"available": true, "hwaccel": "vaapi", "encoder": "h264_vaapi", "device": "/dev/dri/renderD128", "requested": "auto", "maxTranscodes": 2}
```

- `hwaccel` is `"none"` when encoding in software.
- `available` is false when ffmpeg is missing.

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

### `GET /video/:/transcode/universal/start.webm` (sloplex)

The same as `start.mp4`, but it streams WebM (`video/webm`) as described in
**WebM**.

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
| `transcodeHwEncoding` | The accelerator in use (`vaapi`, `qsv`, `nvenc`, `videotoolbox`), or empty for software or copy |
| `transcodeHwFullPipeline` | `1` when decoding, scaling, and encoding all run on the GPU |

## Lifecycle (sloplex)

- An HLS session with no activity for 2 minutes is stopped and its directory
  deleted.
- On startup, the server deletes the leftover `<dataDir>/transcode/` from any
  earlier run. On shutdown it stops all transcodes.
- ffmpeg runs at full speed, without throttling.

## Not implemented yet

- Hardware decoding when video is copied, which is not needed.
- Tone mapping on the GPU (`tonemap_vaapi`, `tonemap_opencl`); HDR sources
  are tone mapped on the CPU.
- Dolby Vision profile 5, which has no HDR10 base layer, is not tone mapped
  correctly.
- Adaptive bitrate ladders.
- The `decision` endpoint.
