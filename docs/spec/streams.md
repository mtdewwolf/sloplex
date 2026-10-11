# Streams: audio tracks and subtitles

## Provenance

- These come from publicly available community documentation of the
  media-server HTTP API and from open-source client libraries' public docs:
  - the `Stream` element and its attributes (`streamType` 1/2/3, `codec`,
    `index`, `language`, `languageCode`, `title`, `displayTitle`,
    `extendedDisplayTitle`, `selected`, `default`, `forced`,
    `hearingImpaired`, `channels`, `audioChannelLayout`, `key`);
  - the endpoints `PUT /library/parts/{id}` (with `audioStreamID` and
    `subtitleStreamID`) and `GET /library/streams/{id}`;
  - the transcode parameters `audioStreamID`, `subtitleStreamID`, and
    `subtitles`.
- Sidecar subtitle names (`Movie (2010).en.srt`, `.forced.`, `.sdh.`) follow
  publicly documented "local media assets" naming conventions.
- These are **(sloplex)**:
  - stream ID allocation;
  - the default-selection rules;
  - the `format` and `offset` parameters on `/library/streams/{id}`;
  - the meaning of `subtitles=auto`;
  - how burning in is done.

## Streams

### Embedded streams

Every part lists the streams found in its file by `ffprobe`: video, audio,
and subtitle streams, in file order. Each records:

- the codec;
- the ffprobe index;
- the `language` and `title` tags;
- the `default`, `forced`, and `hearing_impaired` flags;
- for audio, the channels and channel layout;
- for video, the width and height.

### Sidecar subtitles

Files next to the video named `<stem>.<tags>.<ext>` are subtitle streams of
its part.

- `<ext>` is `srt`, `vtt`, `ass`, or `ssa`.
- `<tags>` is zero or more dot-separated tokens, matched without regard to
  case:
  - a language: a 2- or 3-letter ISO 639 code, or an English language name
    such as `english`;
  - `forced`;
  - `sdh`, `cc`, or `hi`, meaning hearing impaired;
  - `default`.

  Unknown tokens are ignored.
- Example: `Heat (1995).en.forced.srt` is an English forced subtitle.

Sidecars are found again on every scan.

### IDs

- **(sloplex)** Streams get IDs that are unique across the server and stable
  across rescans.
- An embedded stream is matched by part and ffprobe index. A sidecar is
  matched by part and file path.
- If a file changes and its streams are re-probed, an embedded stream keeps
  its ID only while its index and codec stay the same.

### Languages

- `languageCode` is the ISO 639-2/B three-letter code, such as `eng`, `fre`,
  or `ger`.
- Two-letter codes and 639-2/T variants (`fra`, `deu`) are mapped to it.
- `language` is the English name. It is omitted when the language is unknown.

### Text and image subtitles

- **Text** codecs: `subrip`, `ass`, `ssa`, `webvtt`, `mov_text`, `text`.
  These, and every sidecar, can be fetched with `/library/streams/{id}`.
- **Image** codecs: `hdmv_pgs_subtitle`, `dvd_subtitle`, `dvb_subtitle`, and
  any other codec. These can only be burned in.

## The `Stream` element

Streams appear as `Stream` children of `Part`. They are only included in
`GET /library/metadata/{id}`, not in lists.

| Attribute | Notes |
|---|---|
| `id` | Stream ID |
| `streamType` | `1` video, `2` audio, `3` subtitle |
| `codec` | ffprobe codec name, or the sidecar format (`srt`, `ass`, `vtt`) |
| `index` | ffprobe index; omitted for sidecars |
| `language`, `languageCode` | See above |
| `title` | The stream's title tag, if any |
| `displayTitle` | Language (or "Unknown"), then for subtitles a `Forced`/`SDH` marker, then the format in parentheses. Audio adds the layout: `English (AAC Stereo)`, `French (AC3 5.1)`, `English Forced (SRT)`. Video is resolution, then `HDR10` or `HLG` for HDR, then the codec: `4K HDR10 (HEVC)` |
| `extendedDisplayTitle` | `displayTitle`, plus ` – <title>` when a title is set |
| `channels`, `audioChannelLayout` | Audio |
| `width`, `height` | Video |
| `default`, `forced`, `hearingImpaired` | Boolean flags, emitted only when true |
| `selected` | `1` on the audio stream and the subtitle stream that are selected for the requesting account |
| `key` | `/library/streams/{id}`, on text subtitles only |

### Default selection (sloplex)

**Audio:** the account's saved choice, else the stream flagged `default`,
else the first audio stream.

**Subtitles:** the account's saved choice, which may be "none", else the
first stream flagged `forced`, else none.

## Endpoints

### `PUT /library/parts/{id}`

Saves the requesting account's stream choice for one part. Parameters:

- `audioStreamID`: an audio stream of this part.
- `subtitleStreamID`: a subtitle stream of this part, or `0` for none.

Either may be left out. The response is `200`.

| Problem                                                | Response |
|--------------------------------------------------------|----------|
| The stream belongs to another part, or has the wrong type | `400` |
| The part is unknown or hidden by library access        | `404`    |

Choices are stored with play state. They are separate for each account and
are deleted with the account.

### `GET /library/streams/{id}`

Returns a text subtitle. The same rules as `/library/parts` apply for library
access.

| Parameter | Meaning |
|---|---|
| `format` | `srt`, `vtt`, or `ass`. Without it, the subtitle is returned in its own format; embedded streams are extracted first |
| `offset` | **(sloplex)** Seconds, only with `format=vtt`. Shifts every cue earlier by that much and drops cues that end before 0. Clients use it to match a transcode started at the same `offset` |

| Format | Content type               |
|--------|----------------------------|
| srt    | `application/x-subrip`     |
| vtt    | `text/vtt; charset=utf-8`  |
| ass    | `text/x-ssa`               |

- Text that is not valid UTF-8 is read as Windows-1252.
- An image subtitle returns `400`, and so does an unknown `format`.
- `GET /library/streams/{id}.{ext}`, the form in the official API
  reference, serves the same subtitles. Its extension and status rules are
  in [wire-conformance.md](wire-conformance.md).
- **(sloplex)** Extracted and converted subtitles are cached under
  `<dataDir>/cache/subs/`. The cache key includes the part's modification
  time.

## Transcoding

These parameters add to [transcode.md](transcode.md):

| Parameter | Meaning |
|---|---|
| `audioStreamID` | The audio stream to use. It defaults to the selection rules above |
| `subtitleStreamID` | The subtitle stream; `0` for none. It defaults to the selection rules above |
| `subtitles` | `auto` (default): burn in image subtitles only; text subtitles are left for the client to fetch. `burn`: burn in any selected subtitle. `none`: never burn |

- **Audio.** The chosen audio stream replaces "first audio stream". The copy
  rule (AAC, at most 2 channels) is applied to that stream.
- **Burning in.** Burning in forces video encoding.
  - It skips the full-hardware pipeline, because frames must be in system
    memory. The hardware-encode-only and software pipelines still apply.
  - **(sloplex)** Text subtitles are rendered with libass. The subtitle (a
    sidecar, or an embedded stream extracted to the cache) is copied into
    the transcode's working directory under a fixed name, so no file path
    needs escaping. When `offset` is set, `setpts` shifts frames to source
    time for rendering and back again.
  - **(sloplex)** Image subtitles are overlaid from their stream with
    `overlay=eof_action=pass` before scaling.
- An unknown stream ID, or one from another part, returns `400`.

### Subtitles inside HLS (sloplex)

Unless a subtitle is burned in or `subtitles=none` is given, the master
playlist from `start.m3u8` lists every text subtitle of the part (embedded
or sidecar) as a WebVTT rendition. HLS players can then show and switch
subtitles without fetching anything separately.

```
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English (SRT)",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,FORCED=NO,URI="session/<sid>/subs/<streamID>/index.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=…,SUBTITLES="subs"
```

Rendition attributes:

| Attribute  | Value |
|------------|-------|
| `NAME`     | `displayTitle`. Double quotes are replaced by `'`, and repeated names get ` (2)`, ` (3)`, and so on |
| `LANGUAGE` | The two-letter code when there is one, else the three-letter code. Omitted when unknown |
| `DEFAULT`  | `YES` for the subtitle selected by the rules above, otherwise `NO` |
| `FORCED`   | `YES` for forced streams |

The rendition URLs below sit under the session, so like the session's other
URLs they need no token.

`…/session/{sid}/subs/{streamID}/index.m3u8` returns a `VOD` playlist with a
single segment, `subs.vtt`. The segment lasts the item's duration minus the
transcode's `offset`, rounded up to whole seconds and at least 1 s.

`…/session/{sid}/subs/{streamID}/subs.vtt` returns the subtitle as WebVTT:

- Cues are shifted earlier by the transcode's `offset`.
- Its header has
  `X-TIMESTAMP-MAP=MPEGTS:<pts>,LOCAL:00:00:00.000`, where `<pts>` is the
  presentation timestamp of the first video packet in the session's first
  segment, which shows source time `offset`. If the segment has no video,
  the smallest timestamp is used. ffmpeg's MPEG-TS output does not start at
  0 (it typically starts near 1.4 s), so this value is read from the segment
  rather than assumed.
- The response waits for the first segment, under the same rules as segment
  requests.

A stream that is not a text subtitle of the session's part returns `404`.
