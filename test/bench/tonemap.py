#!/usr/bin/env python3
"""Synthetic filter-order comparison; no downloads or real media required.

Runs both pipelines with the same libx264 preset and thread limits. This
isolates output-size tone mapping; it does not benchmark HEVC decoding,
startup latency, adaptive preset quality, or real-world playback.
"""
import json
import statistics
import subprocess
import time

TONE = ("zscale=tin=smpte2084:min=bt2020nc:pin=bt2020:rin=tv:t=linear:npl=100,"
        "format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,"
        "zscale=t=bt709:m=bt709:r=tv,format=yuv420p")
SCALE = "scale=1920:1080"
FILTERS = {
    "before": TONE + "," + SCALE,
    "after": "zscale=w=1920:h=1080:filter=bicubic," + TONE + "," + SCALE,
}
results = {key: [] for key in FILTERS}
for iteration in range(3):
    # Alternate order to reduce warm-up/order bias.
    for key in list(FILTERS)[::1 if iteration % 2 == 0 else -1]:
        command = ["ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error",
                   "-filter_threads", "1", "-f", "lavfi", "-i",
                   "testsrc2=size=3840x2160:rate=24,format=yuv420p10le,setparams=range=limited:color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc",
                   "-frames:v", "48", "-vf", FILTERS[key], "-c:v", "libx264",
                   "-preset", "veryfast", "-threads", "4", "-crf", "21",
                   "-pix_fmt", "yuv420p", "-f", "null", "-"]
        start = time.monotonic()
        subprocess.run(command, check=True, timeout=120)
        results[key].append(time.monotonic() - start)
medians = {key: statistics.median(value) for key, value in results.items()}
print(json.dumps({"seconds": results, "median_seconds": medians,
                  "speedup": medians["before"] / medians["after"]}, indent=2))
