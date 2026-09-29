# Playback verification — 2026-09-29

Read-only benchmark of the end-to-end playback path, run while the post-incident Plex
rescan was still working through series sections (deliberately worst-case). Raw logs:
`/home/vps/playback-tests/` on the host.

## Path under test

```
Plex client -> Plex HTTP (127.0.0.1:32400) -> library symlink (/mnt/media-manager or /media)
           -> /mnt/altmount FUSE (AltMount v0.3.2, max_read=1 MiB)
           -> Usenet providers (5, NNTPS)
```

## Results

### Raw FUSE reads (1 MiB chunks, cold regions)

| File | Type | Size | First pass | TTFB | Second pass |
|---|---|---:|---:|---:|---:|
| The Lord of the Rings: The Two Towers | 4K HEVC remux | 116.5 GB | 37.1 MB/s | 305 ms | 4.4 GB/s (page cache) |
| Game of Thrones S03E10 | 4K HEVC | 35.7 GB | 30.3 MB/s | 851 ms | — |
| Submerged (2005) | 1080p H.264 | 11.7 GB | 30.9 MB/s | 971 ms | 4.6 GB/s (page cache) |

### Random seeks (LoTR, 10 × 8 MiB across the file)

TTFB 6 ms – 2.16 s, throughput 2.7 – 421 MB/s. Cold seeks have the usual latency
because each one triggers a fresh Usenet fetch; sequential playback after the seek is
the number that matters and stays at 30+ MB/s.

### Concurrency (3 streams × 256 MiB, six fresh offset regions)

| Stream | Rate |
|---|---:|
| LoTR 4K | 17.6 MB/s |
| GoT 4K | 18.3 MB/s |
| Submerged 1080p | 24.6 MB/s |
| **Aggregate** | **≈60 MB/s** (768 MiB in 14.6 s wall) |

### Plex HTTP direct streaming (Range request through the full Plex path)

| Title | First request (cold) | Cached range |
|---|---|---|
| Submerged 1080p (128 MiB range) | 12.1 MB/s | 1.2 GB/s |
| LoTR 4K (128 MiB range) | 24.2 MB/s, TTFB 37 ms | — |

Context: LoTR 4K averages ≈8.3 MB/s over its 3 h 56 min runtime; the measured
24–37 MB/s cold path leaves ~3× direct-play headroom.

### CPU software decode (temporary ffmpeg container, no GPU used)

| Content | Decode speed |
|---|---|
| 1080p H.264 | 41.2× real-time |
| 4K HEVC (DV/HDR10 remux) | 3.5× real-time |

Both measured while the Plex scan was competing for CPU, so these are conservative.
Software transcoding is viable on this host if a client cannot direct-play.

### Library health (AltMount `file_health` database)

| Status | Files |
|---|---:|
| healthy | 16,803 |
| pending | 218 |
| checking | 50 |
| degraded (zero-filled gaps, still playable) | 6 |
| corrupted (unplayable) | 1 |

Degraded/corrupted files found (legacy content with dead Usenet articles):

- corrupted: `Little.Princess.S02E30...`
- degraded: `Peter.Pedal.S11E13`, `Peter.Pedal.S11E18`, `Ricky.Zoom.S01E19`,
  `Disenchantment.S03E07`, `Disenchantment.S03E09`, `Braceface.S01E19`
- previously logged as unopenable: Asterix et Cleopatre (1968), Englemageren (2023),
  The Hills Run Red (2009), Dybvaaaad S05E05, Fanget På Politiets Kamera S08E10,
  Først Til Verdens Ende S04E01/S04E04, Rick and Morty S07E07

## Findings

1. **Direct play is comfortably fast.** Cold sequential reads through the full
   Plex → symlink → FUSE → Usenet path run at 24–37 MB/s with sub-second startup,
   and any re-read is effectively instant.
2. **Concurrent capacity.** With the rescan competing, three simultaneous streams
   shared ≈60 MB/s. Expect roughly two to three concurrent 4K remux streams, or
   four-plus 1080p streams, in steady state.
3. **No playback-path bugs were found.** Every error seen during testing came from
   legacy releases whose Usenet articles no longer exist (`430 no such article`),
   not from the pipeline itself.
4. **The rescan is slow but not hung.** Plex keeps retrying reads on dead-article
   legacy files (e.g. Kung Fu Panda episodes), which stretches each section's scan;
   it continues moving through the library.
5. **AltMount repair cannot notify an *arr** (`failed to find ARR instance for file
   path`) because Radarr/Sonarr were removed. Harmless; optional to silence.
6. **Benign log line** after the v0.3.2 upgrade: `Failed to create rclone config
   directory: permission denied` — rclone mount is disabled, so this is noise.

## Recommendations

- Keep clients on **direct play**; prefer text (SRT) subtitles so Plex never needs a
  burn-in transcode.
- Treat 4K remux transcoding as a fallback, not the norm — it works (3.5× headroom)
  but burns CPU that direct play leaves free.
- Decide what to do with the 6 degraded / 1 corrupted / 8 unopenable legacy files:
  re-request healthy releases in Media Manager, or accept them as-is.
- Optional cleanups: silence the rclone config-dir warning and the repair
  notification path (no *arrs exist).

## Artifacts

| File | Content |
|---|---|
| `reads-20260929-144808.log` | Raw FUSE TTFB / sequential / concurrency results |
| `plex-playback-*.log` | Plex HTTP streaming and transcode-decision attempts |
| `reader.py`, `run-reads.sh`, `plex-playback-test.py` | Re-runnable harnesses |
