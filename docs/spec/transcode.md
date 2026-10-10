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
  - the `TranscodeSession` element, including its `transcodeHwEncoding`,
    `transcodeHwFullPipeline`, and `throttled` attributes;
  - the `decision` endpoint path, its `directPlay` parameter, the
    `generalDecisionCode`/`Text`, `directPlayDecisionCode`/`Text`, and
    `transcodeDecisionCode`/`Text` attributes, and the `decision` attribute
    on `Part` (`directplay`, `copy`, `transcode`) and `Stream` (`copy`,
    `transcode`, `burn`).
- The VP9 and Opus encoder options (`libvpx-vp9 -deadline realtime
  -cpu-used -row-mt`, `libopus`) come from ffmpeg's public documentation.
- The libx264 preset names come from x264's and ffmpeg's public
  documentation.
- Several variants in one master playlist, each with `BANDWIDTH` and
  `RESOLUTION`, come from the HLS specification (RFC 8216). ffmpeg's
  `split` filter and the HLS muxer's `var_stream_map` option come from
  ffmpeg's public documentation.
- The ffmpeg hardware options (`-hwaccel`, `-vaapi_device`,
  `-init_hw_device`, `scale_vaapi`, `scale_cuda`, `scale_qsv`, `hwupload`,
  and the `h264_*` encoders and their rate-control options) come from
  ffmpeg's own public documentation.
- These are **(sloplex)**:
  - the progressive `start.mp4` and `start.webm` endpoints;
  - capability session URLs;
  - the decision rules, limits, idle timeout, and ffmpeg arguments;
  - the adaptive quality levels: the ladder, the CPU budget rule, the `abr`
    parameter, and the level names in URLs;
  - the client capability parameters of the `decision` endpoint, and its
    decision codes and texts;
  - the CPU benchmark, load model, preset choice, and throttle limits.

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
  acceleration**), and otherwise `libx264 -preset <preset> -crf 21`. The
  preset comes from `x264Preset`; see **CPU transcoding**.
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
subtitle burn-in or further scaling:

```
[zscale=w=<W>:h=<H>:filter=bicubic,]
zscale=tin=<smpte2084|arib-std-b67>:min=bt2020nc:pin=bt2020:rin=tv:t=linear:npl=100,
format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,
zscale=t=bt709:m=bt709:r=tv,format=yuv420p
```

- The first `zscale` runs only when the output is smaller than the source.
  It resizes to the planned output size `W`×`H`, so the expensive
  floating-point stages work on output pixels. This reduces the number of pixels processed by those stages;
  actual savings depend on the machine and input (see **Measurements**).
- When it runs, an image subtitle being burned in is scaled to `W`×`H` too
  (`[0:<index>]scale=W:H`) before the overlay, so it lands where it would
  have at the source size.

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
{"available": true, "hwaccel": "vaapi", "encoder": "h264_vaapi", "device": "/dev/dri/renderD128", "requested": "auto", "maxTranscodes": 2,
 "x264Preset": "auto", "throttle": true, "softwareLoad": 0.42,
 "benchmark": {"faster": 100.8, "veryfast": 144, "superfast": 201.6, "ultrafast": 316.8}}
```

- `hwaccel` is `"none"` when encoding in software.
- `available` is false when ffmpeg is missing.
- `x264Preset` is the configured value. `throttle` is true when throttling
  is on and the OS supports it.
- `softwareLoad` is the estimated CPU share for active software encodes
  and encode-only hardware decoding/filtering, including conservative VP9
  reservations. See **Load model**.
- `benchmark` gives frames per second at 720p for each preset that auto
  mode uses. It is absent until the benchmark finishes, and absent when
  `x264Preset` is not `auto`.

## CPU transcoding (sloplex)

When there is no working GPU, or the GPU pipelines fail, every encode runs
on the CPU. Sloplex uses three measures to keep CPU-only servers
playing:

- tone mapping at the output size (see **HDR tone mapping**);
- choosing the libx264 preset from measured speed and current load;
- pausing HLS transcodes that are far ahead of the player.

### Configuration

| Config key          | Values                                                               | Default |
|---------------------|----------------------------------------------------------------------|---------|
| `x264Preset`        | `auto`, `ultrafast`, `superfast`, `veryfast`, `faster`, `fast`, `medium` | `auto`  |
| `transcodeThrottle` | `true`, `false`                                                      | `true`  |

A named preset is used for every software H.264 encode. WebM retains its
fixed VP9 realtime settings and has no x264 preset. With `auto`, each
transcode gets its own preset, as described below.

### Benchmark

With `auto`, the server measures libx264 once at startup, in the
background:

```
ffmpeg -f lavfi -i testsrc2=size=1280x720:rate=24 -frames:v 240
       -c:v libx264 -preset veryfast -crf 21 -pix_fmt yuv420p -f null -
```

- The result is in frames per second, using every core.
- The other presets are derived from fixed speed ratios relative to
  `veryfast`: `faster` 0.7, `superfast` 1.4, `ultrafast` 2.2. These ratios
  vary little between CPUs. One longer run is steadier than several short
  runs; on a busy machine, short runs measured `faster` as faster than
  `veryfast`.
- Until the benchmark finishes, or if it fails, software encodes use
  `veryfast`.

### Load model

The **load** of a software encode is the share of the whole machine it
needs to keep up with real time:

```
load = fps × ( out / (speed[preset] × B)
             + (in × decode[codec] + out × extra) / (speed[veryfast] × B) )
```

| Symbol | Meaning |
|--------|---------|
| `fps`  | The source frame rate (probed `avg_frame_rate`), or 30 if unknown |
| `out`  | Output pixels per frame, `W`×`H` |
| `in`   | Source pixels per frame |
| `B`    | Benchmark pixels per frame, 1280×720 |
| `speed[p]` | Benchmark frames per second for preset `p` |
| `decode[codec]` | `h264` 0.45, `hevc` 0.7, `vp9` 0.6, `av1` 0.8, anything else 0.4 |
| `extra` | 2.0 when tone mapping, plus 0.1 when burning in subtitles |

The weights express the cost of decoding and filtering relative to
libx264 `veryfast` encoding the same number of pixels. They were measured
with ffmpeg 6.1 on x86-64. Copied video has a load of 0.

VP9 output is not calibrated by the x264 benchmark. Each active WebM
video encode conservatively reserves 0.75 of the planning budget,
independent of the configured x264 preset; this is a scheduling reservation,
not a measured CPU-utilization estimate. WebM video copies reserve zero.
When the asynchronous benchmark completes, existing H.264 encode estimates
are refreshed at their unchanged presets before scheduling later encodes.

### Choosing a preset (auto)

- The **running load** is the sum of the loads of transcodes that are
  running now on the software pipeline, plus CPU decoding/filtering for
  encode-only hardware pipelines. A full hardware pipeline adds no load.
- Each new transcode gets the first preset of `faster`, `veryfast`,
  `superfast`, `ultrafast` for which running load plus its own load is at
  most **0.75**. If none fits, it gets `ultrafast`.
- So an idle machine gives better quality per bit. A busy machine, or a
  heavy source such as 4K HDR, gets faster presets so that playback keeps
  up.
- The preset is fixed when the transcode starts.
- The chosen preset is logged with `transcode started`.

### Throttling

HLS transcodes run at full speed until they are well ahead of the player.

- The server tracks the highest segment number each session has been
  asked for.
- **Pause:** the transcode is paused when the playlist lists more than
  90 s beyond that segment, summing playlist `EXTINF` durations rather
  than assuming copied-video keyframes produce four-second segments. ffmpeg gets `SIGSTOP`.
- **Resume:** it continues (`SIGCONT`) when the lead falls to 45 s or
  less.
- The lead is checked every second, and at once on each segment request.
  A request beyond the end of the playlist (a seek forward) therefore
  resumes ffmpeg immediately.
- Paused transcodes still count towards `maxTranscodes`.
- A paused transcode is listed with `throttled="1"` in
  `/transcode/sessions`.
- Throttling needs a Unix-like OS. Elsewhere, and when `transcodeThrottle`
  is `false`, HLS transcodes run at full speed to the end.
- Progressive MP4 and WebM streams are not throttled here. They slow down by
  themselves when the client stops reading.

### Measurements

Historical measurements reported by the original CPU-acceleration change
were taken on a 4-core x86-64 VM with AVX-512, using
ffmpeg 6.1 and a 12 s HEVC 10-bit 4K PQ clip transcoded to 1080p over HLS.

| | First segment | All 12 s |
|---|---|---|
| Before (tone map at 4K, `veryfast`) | 17.5 s | 38.4 s |
| After (tone map at 1080p, auto chose `ultrafast`) | 6.3 s | 14.2 s |

The table changes both filter order and encoder preset, so it does not
isolate either optimization.

A reproducible synthetic filter-order comparison is available with
`python3 test/bench/tonemap.py`. It uses a generated 48-frame, 3840×2160
10-bit PQ-tagged test pattern, 1920×1080 output, the same `veryfast` preset,
four encoder threads, and one filter thread for both pipelines. Three
alternating-order runs on the integration environment (ffmpeg 7.1.5,
9 visible CPUs) gave median wall times of **5.586 s before** and
**1.673 s after**: **3.34× faster** for this synthetic workload. This is
not a real-media playback or HEVC-decoding benchmark and does not establish
visual equivalence or a universal speedup. The benchmark checks successful
encoding to a null sink; separate `TestToneMapping` regressions inspect
encoded PQ/HLG outputs for BT.709 tags, output width, and expected diffuse
white/highlight luminance, while PGS tests check subtitle size and timing.

Two alternatives were historically measured and **not** adopted:

- **Encoding time-chunks in parallel ffmpeg processes:** no faster on
  4 cores. One ffmpeg already keeps about 3 of 4 cores busy. Every chunk
  also decodes again from the previous key frame, which made long-GOP
  sources slower.
- **A precomputed 3D LUT (`lut3d`) instead of the tone-mapping chain:** no
  faster. 16-bit `lut3d` is not SIMD-optimised in ffmpeg 6.1.

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
- `abr` **(sloplex)** controls lower quality levels: `0` none, `1` all
  candidates. Without it, they depend on the CPU budget; see **Adaptive
  quality levels**.
- `session` is an optional label chosen by the client, used by `stop` and
  `ping`. Starting a new session with the same label for the same account
  stops the old session; this is how seeking is done.

### `GET /video/:/transcode/universal/session/{sid}/base/index.m3u8` and `…/base/{n}.ts`

(Lower quality levels use their own name instead of `base`; see
**Adaptive quality levels**.)


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

### `GET /video/:/transcode/universal/decision`

Tells a client, before it starts playback, whether it can play a part as it
is, or what a conversion would do. Nothing is started.

It takes the same parameters as `start.m3u8` (media, `offset`,
`videoResolution`, `maxVideoBitrate`, `directStream`, and the stream
parameters in [streams.md](streams.md)), plus:

| Parameter | Default | Meaning |
|---|---|---|
| `protocol` | `hls` | The conversion the client would use: `hls`, `http` (`start.mp4`), or `webm` (`start.webm`) |
| `directPlay` | `1` | `0` rules out direct play |
| `containers` | `mp4` | **(sloplex)** Containers the client plays directly, comma-separated, as in `Part.container` (`mkv`, `mp4`, `webm`, `mpegts`, `avi`, `mov`, …) |
| `videoCodecs` | `h264` | **(sloplex)** Video codecs the client decodes, as ffprobe names them (`h264`, `hevc`, `vp9`, `av1`, …) |
| `audioCodecs` | `aac,mp3` | **(sloplex)** Audio codecs the client decodes (`aac`, `ac3`, `eac3`, `opus`, `flac`, …) |
| `maxAudioChannels` | `2` | **(sloplex)** The most audio channels the client plays |
| `hdr` | `0` | **(sloplex)** `1` if the client displays HDR |

Lists are matched without regard to case.

**Direct play** is possible when all of these hold:

- `directPlay` is not `0`;
- the part's container is in `containers`;
- the video codec is in `videoCodecs`;
- the selected audio stream's codec is in `audioCodecs`, with at most
  `maxAudioChannels` channels (a part without audio passes);
- the video fits `videoResolution` and `maxVideoBitrate`, when given;
- the video is not HDR, or `hdr=1`;
- nothing must be burned in. An image subtitle that is selected, or any
  selected subtitle with `subtitles=burn`, rules out direct play. Text
  subtitles do not, because the client fetches them from
  `/library/streams/{id}`.

**Conversion** follows the decisions above for the chosen `protocol`. It is
possible when ffmpeg is available and the client decodes its output: the
output video codec (`h264`, or `vp9` for `webm`) is in `videoCodecs` and
the output audio codec (`aac`, or `opus` for `webm`) is in `audioCodecs`.
Copied streams count as their own codec.

The response is a `MediaContainer` with one item, rendered as in lists. Its
first `Media` has `selected="1"` and describes what the client would get:

- **Direct play:** the stored `Media` and `Part`. The `Part` has
  `decision="directplay"`.
- **Conversion:**
  - `Media` has `protocol` (`hls`, `http`, or `webm`), `container`
    (`mpegts`, `mp4`, or `webm`), the output `videoCodec` and `audioCodec`,
    `width`, `height`, and `bitrate` in kbps.
  - `Part` has `decision="copy"` when the video is copied, otherwise
    `decision="transcode"`.
  - The `Part` holds the video `Stream` and the selected audio `Stream`,
    each with `decision` `copy` or `transcode`. A subtitle that would be
    burned in is included too, with `decision="burn"`.

**(sloplex)** The container attributes:

| Attribute | Values |
|---|---|
| `generalDecisionCode`, `generalDecisionText` | `1000` `Direct play OK.`; `1001` `Direct play not available; Conversion OK.`; `2000` `Neither direct play nor conversion is available.` |
| `directPlayDecisionCode`, `directPlayDecisionText` | `1000` `Direct play OK.`; `3000` `Direct play not available: ` followed by every reason, separated by `; `, for example `the container mkv is not supported; the audio codec ac3 is not supported` |
| `transcodeDecisionCode`, `transcodeDecisionText` | Only when direct play is not possible. `1001` `Conversion OK.`; `4000` `Conversion not available: ` and a reason, such as `ffmpeg is not installed` or `the client does not decode h264` |

With code `2000`, the item has no `Media`.

Errors are as for `start.m3u8`: `400` for bad parameters, `404` for unknown
or hidden media. A bad `protocol`, `directPlay`, `maxAudioChannels`, or `hdr`
value is also `400`.

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
| `throttled`     | `1` while the transcode is paused for being far ahead of the player; see **Throttling** |
| `transcodeHwEncoding` | The accelerator in use (`vaapi`, `qsv`, `nvenc`, `videotoolbox`), or empty for software or copy |
| `transcodeHwFullPipeline` | `1` when decoding, scaling, and encoding all run on the GPU |

## Adaptive quality levels (sloplex)

An HLS session can offer lower quality levels next to its main one, so
players on a slow connection switch down instead of stalling.

### When levels are added

Lower levels are only considered when all of these hold:

- the request is `start.m3u8`;
- the video is encoded, not copied;
- the encode runs on the software pipeline, that is no GPU accelerator is
  active (see **Hardware acceleration**);
- `abr` is not `0`.

The candidate levels come from this ladder:

| Level   | Box       | Video bitrate cap |
|---------|-----------|-------------------|
| `1080p` | 1920×1080 | 8000 kbps         |
| `720p`  | 1280×720  | 3000 kbps         |
| `480p`  | 854×480   | 1200 kbps         |

- A level's size is the source fitted into its box, as for
  `videoResolution`, so a 16:9 source gives 852×480 for `480p`.
- A level is a candidate only if it is shorter than the main output and its
  `BANDWIDTH` (below) is lower than the main output's.

Which candidates are used depends on `abr`:

| `abr` | Levels |
|---|---|
| absent | Candidates are added largest first while the CPU budget allows (below) |
| `1` | Every candidate, regardless of the budget |
| `0` | None |
| anything else | `400` |

**CPU budget.** The main output's preset is chosen first, as in **CPU
transcoding**. Then each candidate is added if the running load, plus the
main output's load, plus the loads of the levels added so far and this
one, stays within **0.75**. A level's load counts only its encoding,
because decoding, tone mapping, and burn-in are shared:

```
level load = fps × level pixels / (speed[preset] × B)
```

It uses the main output's preset. When the capacity is unknown (`x264Preset`
is not `auto`, or the benchmark has not finished or failed), no levels are
added unless `abr=1`. The session's load, used for later transcodes and
reported by `/sloplex/transcoder`, includes its levels.

### Output

- **One ffmpeg process** produces every level. The source is decoded, tone
  mapped, and has subtitles burned in once, at the main output size. Then
  the frames are split and scaled down for each level.
- Every level gets the same key frames every 4 s, so segment `n` covers the
  same time with identical timestamps in every level. Players can switch at
  any segment.
- Each level has its own audio stream, encoded or copied as for the main
  output.
- Rate control is as for the main output, plus each level's bitrate cap as
  `-maxrate`, with `-bufsize` twice that. A `maxVideoBitrate` cap applies
  to the main output only.

### Playlists

The master playlist lists the main output first, then the levels, largest
first:

```
#EXTM3U
#EXT-X-VERSION:3
#EXT-X-STREAM-INF:BANDWIDTH=5136640,RESOLUTION=1920x1080
session/<sid>/base/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=3160000,RESOLUTION=1280x720
session/<sid>/720p/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1360000,RESOLUTION=852x480
session/<sid>/480p/index.m3u8
```

- The main output is always named `base`.
- A level's `BANDWIDTH` is its cap plus 160 kbps for audio.
- With subtitle renditions (see [streams.md](streams.md)), every variant
  carries `SUBTITLES="subs"`.
- `…/session/{sid}/{level}/index.m3u8` and `…/{level}/{n}.ts` behave like
  the `base` URLs in **Endpoints**. An unknown level returns `404`.
- **Throttling** measures the main output's playlist against the highest
  segment requested in any level.
- `/transcode/sessions` and the `decision` endpoint describe the main
  output.

## Lifecycle (sloplex)

- An HLS session with no activity for 2 minutes is stopped and its directory
  deleted.
- On startup, the server deletes leftover contents inside `<dataDir>/transcode/`
  from any earlier run, preserving the directory itself so it can be a
  dedicated scratch mount. On shutdown it stops all transcodes and clears
  the contents again. Cleanup does not follow symlinks. This storage layout
  is **(sloplex)** design.
- ffmpeg runs at full speed, except that HLS transcodes far ahead of the
  player are paused; see **Throttling**.

## Not implemented yet

- Hardware decoding when video is copied, which is not needed.
- Tone mapping on the GPU (`tonemap_vaapi`, `tonemap_opencl`); HDR sources
  are tone mapped on the CPU.
- Dolby Vision profile 5, which has no HDR10 base layer, is not tone mapped
  correctly.
- Adaptive quality levels with hardware encoding; with an active GPU
  accelerator, sessions have one level.
- Starting a level's encoder only when a player first asks for it. Encoders
  that restart mid-stream need exactly matching timestamps, which stock
  ffmpeg does not give across restarts.
- Changing the preset of a running transcode as the load changes.
- Splitting decoding and encoding into separate processes. With ffmpeg 6.x,
  this gained 5–15% by overlapping work that the ffmpeg 6 CLI runs on one
  thread.

### Test-environment limitation

The synthetic HDR/PGS timing fixture disables HEVC B-frames: ffmpeg 7.1.5
can deliver subtitle heartbeat frames out of order for that very short
B-frame fixture, even with plain unscaled overlay. The test verifies
subtitle position, scaled size, and appearance/disappearance with ordered
input; this does not claim to fix ffmpeg's B-frame subtitle timing issue.
