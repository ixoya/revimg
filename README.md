# revimg

Local reverse image & video search engine. Drop a photo or clip; revimg finds visually similar files in your indexed directories — all offline, nothing leaves your machine.

```
make build   # builds frontend + single Go binary
./revimg     # opens on http://127.0.0.1:7777
```

---

## Features

**Image algorithms**
| Algorithm | Method | Bits | Strength |
|-----------|--------|------|----------|
| pHash | DCT-II on 32×32 Lanczos3 downsample | 64 | Rotation/scale/JPEG robust |
| dHash | Horizontal pixel-pair gradient on 9×8 | 64 | Crop detection |
| aHash | Mean threshold on 8×8 | 64 | Brightness invariant |
| Color histogram | 3D HSV histogram (H×16, S×4, V×4 = 256 bins) | — | Colour composition |

**Video**
- Frame sampling at configurable FPS (default 1 fps, up to 32 frames)
- Per-frame pHash → temporal sequence stored per video
- Sliding-window Hamming comparison for offset-tolerant matching
- Bitrate, codec, FPS, duration range filters

**Search controls**
- Combined weighted score with per-algorithm tunable weights
- Similarity threshold slider (0–100 %)
- File type, extension, dimensions, file size, date range filters
- Filename glob and path substring pattern matching
- Video-specific: codec, FPS range, bitrate range, duration range
- Max results cap

---

## Requirements

| Dependency | Required | Notes |
|------------|----------|-------|
| Go 1.22+ | ✅ | Build only |
| Bun 1.0+ | ✅ | Build only (frontend) |
| ffmpeg + ffprobe | Optional | Video indexing & thumbnails |

---

## Quick start

```bash
# 1. Clone
git clone https://github.com/ieync/revimg && cd revimg

# 2. Build (compiles frontend into the binary)
make build

# 3. Run
./revimg
# → http://127.0.0.1:7777

# 4. Add a directory via the Libraries page or API
curl -s -X POST http://localhost:7777/api/dirs \
  -H 'Content-Type: application/json' \
  -d '{"path":"/home/user/Photos","recursive":true}'
```

---

## Configuration

Config is stored at `~/.local/share/revimg/config.json` (Linux),
`~/Library/Application Support/revimg/config.json` (macOS),
or `%APPDATA%\revimg\config.json` (Windows).

Override the data directory with `REVIMG_DATA_DIR=/path/to/dir ./revimg`.

Key settings (editable in the Settings page or config.json):

```json
{
  "listen_addr":        "127.0.0.1:7777",
  "num_workers":        8,
  "video_enabled":      true,
  "ffmpeg_path":        "ffmpeg",
  "ffprobe_path":       "ffprobe",
  "video_fps":          1.0,
  "max_frames":         32,
  "hash_size":          8,
  "default_threshold":  0.85,
  "max_results":        200,
  "weight_phash":       0.45,
  "weight_dhash":       0.25,
  "weight_ahash":       0.10,
  "weight_color":       0.20
}
```

---

## REST API

```
POST /api/search                    Search by uploaded file
  multipart fields:
    file   — query image or video
    params — JSON SearchParams (optional)

GET  /api/dirs                      List watch directories
POST /api/dirs                      Add directory  { path, recursive, extensions }
DELETE /api/dirs/:id                Remove directory
POST /api/dirs/:id/scan             Trigger manual rescan
POST /api/dirs/:id/toggle           Enable/disable  { enabled: bool }

GET  /api/files                     List indexed files (with filters)
DELETE /api/files/:id               Remove file from index

GET  /api/thumb/:id                 JPEG thumbnail (generated on demand)
GET  /api/open/:id                  Serve raw file

GET  /api/stats                     Index statistics
GET  /api/status                    Server status + active scan progress
GET  /api/jobs                      Recent scan jobs

GET  /api/config                    Current configuration
PUT  /api/config                    Update configuration (partial merge)
```

---

## Architecture

```
revimg (single binary)
├── main.go                 Entry point, embedded FS, graceful shutdown
├── internal/
│   ├── config/             Configuration load/save
│   ├── db/                 SQLite (WAL mode, modernc.org/sqlite, no CGO)
│   │   └── db.go           Schema, models, all queries
│   ├── hasher/
│   │   ├── dct.go          Separable 2D DCT-II implementation
│   │   ├── image.go        pHash / aHash / dHash / colour histogram / EXIF
│   │   └── video.go        ffprobe metadata + ffmpeg frame extraction
│   ├── indexer/
│   │   ├── indexer.go      Worker pool, SHA-256 change detection
│   │   └── watcher.go      fsnotify + 300ms debounce
│   ├── search/
│   │   └── engine.go       Linear scan, weighted scoring, all filters
│   └── api/
│       └── server.go       HTTP handlers (Go 1.22 pattern routing)
└── frontend/               SvelteKit + static adapter (embedded in binary)
    └── src/
        ├── routes/         +page.svelte, directories, settings
        └── lib/            api.js, stores, components
```

**Database** — single WAL-mode SQLite file at `$DATA_DIR/revimg.db`. Indexes on phash, ahash, dhash, file_type, modified_at, directory, extension, size. Thumbnails are JPEG files in `$DATA_DIR/thumbs/{id}.jpg`.

**Search** — O(n) linear scan over all indexed files. At 100 k files each comparison takes ~2 µs → ~200 ms worst case. Adequate for typical local collections; a BK-tree pre-filter can be layered on for >500 k files.

---

## Development

```bash
# Terminal 1: Go backend (auto-reload with air or just re-run)
go run .

# Terminal 2: Vite dev server (proxies /api → :7777)
cd frontend && bun run dev
# → http://localhost:5173
```

---

## License

MIT
