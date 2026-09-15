// Package db provides all database access. A single *DB wraps a WAL-mode
// SQLite connection with a single writer and unlimited concurrent readers.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// ─── Models ──────────────────────────────────────────────────────────────────

type FileType string

const (
	FileTypeImage FileType = "image"
	FileTypeVideo FileType = "video"
)

// File is the central entity — one row per indexed file.
type File struct {
	ID        int64    `json:"id"`
	Path      string   `json:"path"`
	Filename  string   `json:"filename"`
	Directory string   `json:"directory"`
	FileType  FileType `json:"file_type"`
	Extension string   `json:"extension"`
	Size      int64    `json:"size"`
	Width     int      `json:"width"`
	Height    int      `json:"height"`

	CreatedAt  int64  `json:"created_at"`
	ModifiedAt int64  `json:"modified_at"`
	IndexedAt  int64  `json:"indexed_at"`
	SHA256     string `json:"sha256,omitempty"`

	// Perceptual hashes (uint64 stored as int64 in SQLite)
	PHash uint64 `json:"phash,omitempty"`
	AHash uint64 `json:"ahash,omitempty"`
	DHash uint64 `json:"dhash,omitempty"`

	// Color (raw bytes not sent to frontend)
	ColorHistogram []byte   `json:"-"`
	DominantColors []string `json:"dominant_colors,omitempty"`

	// Video-only
	DurationMs  int64    `json:"duration_ms,omitempty"`
	FPS         float64  `json:"fps,omitempty"`
	Codec       string   `json:"codec,omitempty"`
	AudioCodec  string   `json:"audio_codec,omitempty"`
	Bitrate     int64    `json:"bitrate,omitempty"`
	FrameCount  int      `json:"frame_count,omitempty"`
	VideoPHashes []uint64 `json:"-"` // large; not needed client-side

	// EXIF
	ExifMake     string  `json:"exif_make,omitempty"`
	ExifModel    string  `json:"exif_model,omitempty"`
	ExifDatetime string  `json:"exif_datetime,omitempty"`
	ExifGPSLat   float64 `json:"exif_gps_lat,omitempty"`
	ExifGPSLng   float64 `json:"exif_gps_lng,omitempty"`

	IndexError string `json:"index_error,omitempty"`
}

// WatchDir is a directory the indexer should monitor.
type WatchDir struct {
	ID            int64    `json:"id"`
	Path          string   `json:"path"`
	Recursive     bool     `json:"recursive"`
	Extensions    []string `json:"extensions"`
	Enabled       bool     `json:"enabled"`
	CreatedAt     int64    `json:"created_at"`
	LastScanAt    int64    `json:"last_scan_at"`
	LastScanCount int      `json:"last_scan_count"`
}

// ScanJob tracks progress of a directory scan.
type ScanJob struct {
	ID          int64  `json:"id"`
	DirID       int64  `json:"dir_id"`
	Status      string `json:"status"`
	Total       int    `json:"total"`
	Processed   int    `json:"processed"`
	Errors      int    `json:"errors"`
	StartedAt   int64  `json:"started_at"`
	CompletedAt int64  `json:"completed_at"`
}

// Stats is returned by /api/stats.
type Stats struct {
	TotalFiles  int64 `json:"total_files"`
	TotalImages int64 `json:"total_images"`
	TotalVideos int64 `json:"total_videos"`
	TotalSize   int64 `json:"total_size"`
	IndexedDirs int64 `json:"indexed_dirs"`
	ActiveScans int64 `json:"active_scans"`
}

// ─── DB ──────────────────────────────────────────────────────────────────────

type DB struct {
	sql  *sql.DB
	lock sync.Mutex // serializes all writes (SQLite single-writer)
}

func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys=ON"+
			"&_cache_size=-32000&_busy_timeout=10000&_temp_store=MEMORY",
		path,
	)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetConnMaxLifetime(0) // don't recycle the single connection

	db := &DB{sql: sqlDB}
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migration: %w", err)
	}
	// ponytail: MaxOpenConns(1) prevents SQLITE_BUSY, but setting
	// busy_timeout explicitly works around DSN parsing quirks in modernc.org/sqlite.
	if _, err := sqlDB.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("busy_timeout: %w", err)
	}
	return db, nil
}

func (db *DB) Close() error { return db.sql.Close() }

// ─── Schema ──────────────────────────────────────────────────────────────────

const schema = `
CREATE TABLE IF NOT EXISTS files (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    path             TEXT    NOT NULL UNIQUE,
    filename         TEXT    NOT NULL,
    directory        TEXT    NOT NULL,
    file_type        TEXT    NOT NULL,
    extension        TEXT    NOT NULL,
    size             INTEGER NOT NULL DEFAULT 0,
    width            INTEGER NOT NULL DEFAULT 0,
    height           INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL DEFAULT 0,
    modified_at      INTEGER NOT NULL DEFAULT 0,
    indexed_at       INTEGER NOT NULL DEFAULT 0,
    sha256           TEXT    NOT NULL DEFAULT '',

    phash            INTEGER NOT NULL DEFAULT 0,
    ahash            INTEGER NOT NULL DEFAULT 0,
    dhash            INTEGER NOT NULL DEFAULT 0,

    color_histogram  BLOB,
    dominant_colors  TEXT    NOT NULL DEFAULT '[]',

    duration_ms      INTEGER NOT NULL DEFAULT 0,
    fps              REAL    NOT NULL DEFAULT 0,
    codec            TEXT    NOT NULL DEFAULT '',
    audio_codec      TEXT    NOT NULL DEFAULT '',
    bitrate          INTEGER NOT NULL DEFAULT 0,
    frame_count      INTEGER NOT NULL DEFAULT 0,
    video_phashes    TEXT    NOT NULL DEFAULT '[]',

    exif_make        TEXT    NOT NULL DEFAULT '',
    exif_model       TEXT    NOT NULL DEFAULT '',
    exif_datetime    TEXT    NOT NULL DEFAULT '',
    exif_gps_lat     REAL    NOT NULL DEFAULT 0,
    exif_gps_lng     REAL    NOT NULL DEFAULT 0,

    index_error      TEXT    NOT NULL DEFAULT '',

    last_verified_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS repair_state (
    id                     INTEGER PRIMARY KEY CHECK (id = 1),
    last_cycle_completed_at INTEGER NOT NULL DEFAULT 0,
    last_cycle_checked     INTEGER NOT NULL DEFAULT 0,
    last_cycle_repaired    INTEGER NOT NULL DEFAULT 0,
    last_cycle_errors      INTEGER NOT NULL DEFAULT 0,
    last_cycle_sha_bytes   INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_files_phash     ON files(phash);
CREATE INDEX IF NOT EXISTS idx_files_ahash     ON files(ahash);
CREATE INDEX IF NOT EXISTS idx_files_dhash     ON files(dhash);
CREATE INDEX IF NOT EXISTS idx_files_type      ON files(file_type);
CREATE INDEX IF NOT EXISTS idx_files_modified  ON files(modified_at);
CREATE INDEX IF NOT EXISTS idx_files_directory ON files(directory);
CREATE INDEX IF NOT EXISTS idx_files_extension ON files(extension);
CREATE INDEX IF NOT EXISTS idx_files_size      ON files(size);

CREATE TABLE IF NOT EXISTS watch_dirs (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    path             TEXT    NOT NULL UNIQUE,
    recursive        INTEGER NOT NULL DEFAULT 1,
    extensions       TEXT    NOT NULL DEFAULT '[]',
    enabled          INTEGER NOT NULL DEFAULT 1,
    created_at       INTEGER NOT NULL,
    last_scan_at     INTEGER NOT NULL DEFAULT 0,
    last_scan_count  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS scan_jobs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    dir_id       INTEGER NOT NULL REFERENCES watch_dirs(id) ON DELETE CASCADE,
    status       TEXT    NOT NULL DEFAULT 'running',
    total        INTEGER NOT NULL DEFAULT 0,
    processed    INTEGER NOT NULL DEFAULT 0,
    errors       INTEGER NOT NULL DEFAULT 0,
    started_at   INTEGER NOT NULL,
    completed_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_jobs_dir ON scan_jobs(dir_id);
`

func (db *DB) migrate() error {
	ctx := context.Background()
	if _, err := db.sql.ExecContext(ctx, schema); err != nil {
		return err
	}
	// Additive migration for databases created before last_verified_at
	// existed — CREATE TABLE IF NOT EXISTS won't add the column.
	// ponytail: no IF NOT EXISTS for ADD COLUMN; match the error instead.
	if _, err := db.sql.ExecContext(ctx,
		`ALTER TABLE files ADD COLUMN last_verified_at INTEGER NOT NULL DEFAULT 0`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return err
	}
	// Index lives here (not in schema) so legacy DBs get it after the column.
	_, err := db.sql.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_files_verified ON files(last_verified_at)`)
	return err
}

// ─── File queries ─────────────────────────────────────────────────────────────

const insertFileSQL = `
INSERT INTO files (
    path, filename, directory, file_type, extension, size, width, height,
    created_at, modified_at, indexed_at, sha256,
    phash, ahash, dhash, color_histogram, dominant_colors,
    duration_ms, fps, codec, audio_codec, bitrate, frame_count, video_phashes,
    exif_make, exif_model, exif_datetime, exif_gps_lat, exif_gps_lng, index_error,
    last_verified_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,strftime('%s','now'))
ON CONFLICT(path) DO UPDATE SET
    filename=excluded.filename, directory=excluded.directory,
    file_type=excluded.file_type, extension=excluded.extension,
    size=excluded.size, width=excluded.width, height=excluded.height,
    created_at=excluded.created_at, modified_at=excluded.modified_at,
    indexed_at=excluded.indexed_at, sha256=excluded.sha256,
    phash=excluded.phash, ahash=excluded.ahash, dhash=excluded.dhash,
    color_histogram=excluded.color_histogram, dominant_colors=excluded.dominant_colors,
    duration_ms=excluded.duration_ms, fps=excluded.fps,
    codec=excluded.codec, audio_codec=excluded.audio_codec,
    bitrate=excluded.bitrate, frame_count=excluded.frame_count,
    video_phashes=excluded.video_phashes,
    exif_make=excluded.exif_make, exif_model=excluded.exif_model,
    exif_datetime=excluded.exif_datetime, exif_gps_lat=excluded.exif_gps_lat,
    exif_gps_lng=excluded.exif_gps_lng, index_error=excluded.index_error,
    last_verified_at=strftime('%s','now')`

func (db *DB) UpsertFile(ctx context.Context, f *File) error {
	db.lock.Lock()
	defer db.lock.Unlock()
	// ponytail: SQLite stores NaN as NULL, violating NOT NULL
	if f.ExifGPSLat != f.ExifGPSLat {
		f.ExifGPSLat = 0
	}
	if f.ExifGPSLng != f.ExifGPSLng {
		f.ExifGPSLng = 0
	}
	domColors, _ := json.Marshal(f.DominantColors)

	// Encode video hashes as signed int64 slice for JSON
	rawHashes := make([]int64, len(f.VideoPHashes))
	for i, h := range f.VideoPHashes {
		rawHashes[i] = int64(h)
	}
	videoPHashes, _ := json.Marshal(rawHashes)

	_, err := db.sql.ExecContext(ctx, insertFileSQL,
		f.Path, f.Filename, f.Directory, string(f.FileType), f.Extension,
		f.Size, f.Width, f.Height,
		f.CreatedAt, f.ModifiedAt, f.IndexedAt, f.SHA256,
		int64(f.PHash), int64(f.AHash), int64(f.DHash),
		f.ColorHistogram, string(domColors),
		f.DurationMs, f.FPS, f.Codec, f.AudioCodec, f.Bitrate, f.FrameCount,
		string(videoPHashes),
		f.ExifMake, f.ExifModel, f.ExifDatetime, f.ExifGPSLat, f.ExifGPSLng,
		f.IndexError,
	)
	return err
}

func (db *DB) DeleteFileByPath(ctx context.Context, path string) error {
	db.lock.Lock()
	defer db.lock.Unlock()
	_, err := db.sql.ExecContext(ctx, `DELETE FROM files WHERE path=?`, path)
	return err
}

func (db *DB) DeleteFileByID(ctx context.Context, id int64) error {
	db.lock.Lock()
	defer db.lock.Unlock()
	_, err := db.sql.ExecContext(ctx, `DELETE FROM files WHERE id=?`, id)
	return err
}

func (db *DB) GetFileByPath(ctx context.Context, path string) (*File, error) {
	row := db.sql.QueryRowContext(ctx, `SELECT `+fileColumns+` FROM files WHERE path=?`, path)
	return scanOne(row)
}

func (db *DB) GetFileByID(ctx context.Context, id int64) (*File, error) {
	row := db.sql.QueryRowContext(ctx, `SELECT `+fileColumns+` FROM files WHERE id=?`, id)
	return scanOne(row)
}

// FilePathsUnder returns all file paths under a given directory prefix.
func (db *DB) FilePathsUnder(ctx context.Context, dir string) ([]string, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT path FROM files WHERE path = ? OR path LIKE ?`, dir, dir+`/%`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// FileFilter controls which files AllFiles returns.
type FileFilter struct {
	FileTypes  []FileType
	Extensions []string
	MinWidth   int
	MaxWidth   int
	MinHeight  int
	MaxHeight  int
	MinSize    int64
	MaxSize    int64
	MinModAt   int64
	MaxModAt   int64
	PathLike   string
	Codecs     []string
	MinFPS     float64
	MaxFPS     float64
	MinBitrate int64
	MaxBitrate int64
	MinDurMs   int64
	MaxDurMs   int64
}

func (db *DB) AllFiles(ctx context.Context, f FileFilter) ([]*File, error) {
	where, args := buildWhere(f)
	rows, err := db.sql.QueryContext(ctx, `SELECT `+fileColumns+` FROM files`+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []*File
	for rows.Next() {
		file, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, rows.Err()
}

func buildWhere(f FileFilter) (string, []any) {
	var clauses []string
	var args []any

	add := func(clause string, arg any) {
		clauses = append(clauses, clause)
		args = append(args, arg)
	}
	addIn := func(col string, vals []string) {
		ph := make([]string, len(vals))
		for i, v := range vals {
			ph[i] = "?"
			args = append(args, v)
		}
		clauses = append(clauses, col+" IN ("+strings.Join(ph, ",")+")")
	}

	if len(f.FileTypes) > 0 {
		strs := make([]string, len(f.FileTypes))
		for i, t := range f.FileTypes { strs[i] = string(t) }
		addIn("file_type", strs)
	}
	if len(f.Extensions) > 0 { addIn("extension", f.Extensions) }
	if len(f.Codecs) > 0     { addIn("codec", f.Codecs) }

	if f.MinWidth > 0   { add("width >= ?",      f.MinWidth) }
	if f.MaxWidth > 0   { add("width <= ?",      f.MaxWidth) }
	if f.MinHeight > 0  { add("height >= ?",     f.MinHeight) }
	if f.MaxHeight > 0  { add("height <= ?",     f.MaxHeight) }
	if f.MinSize > 0    { add("size >= ?",        f.MinSize) }
	if f.MaxSize > 0    { add("size <= ?",        f.MaxSize) }
	if f.MinModAt > 0   { add("modified_at >= ?", f.MinModAt) }
	if f.MaxModAt > 0   { add("modified_at <= ?", f.MaxModAt) }
	if f.MinFPS > 0     { add("fps >= ?",         f.MinFPS) }
	if f.MaxFPS > 0     { add("fps <= ?",         f.MaxFPS) }
	if f.MinBitrate > 0 { add("bitrate >= ?",     f.MinBitrate) }
	if f.MaxBitrate > 0 { add("bitrate <= ?",     f.MaxBitrate) }
	if f.MinDurMs > 0   { add("duration_ms >= ?", f.MinDurMs) }
	if f.MaxDurMs > 0   { add("duration_ms <= ?", f.MaxDurMs) }
	if f.PathLike != "" { add("path LIKE ?",       "%"+f.PathLike+"%") }

	// Always skip errored / un-hashed rows
	clauses = append(clauses, "index_error = ''", "phash != 0")

	return " WHERE " + strings.Join(clauses, " AND "), args
}

const fileColumns = `
    id, path, filename, directory, file_type, extension, size, width, height,
    created_at, modified_at, indexed_at, sha256,
    phash, ahash, dhash, color_histogram, dominant_colors,
    duration_ms, fps, codec, audio_codec, bitrate, frame_count, video_phashes,
    exif_make, exif_model, exif_datetime, exif_gps_lat, exif_gps_lng, index_error`

type scanner interface{ Scan(dest ...any) error }

func scanOne(s scanner) (*File, error) {
	f, err := scanRow(s)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return f, err
}

func scanRow(s scanner) (*File, error) {
	var f File
	var domJSON, vidJSON string
	var ph, ah, dh int64
	err := s.Scan(
		&f.ID, &f.Path, &f.Filename, &f.Directory, &f.FileType, &f.Extension,
		&f.Size, &f.Width, &f.Height,
		&f.CreatedAt, &f.ModifiedAt, &f.IndexedAt, &f.SHA256,
		&ph, &ah, &dh,
		&f.ColorHistogram, &domJSON,
		&f.DurationMs, &f.FPS, &f.Codec, &f.AudioCodec, &f.Bitrate,
		&f.FrameCount, &vidJSON,
		&f.ExifMake, &f.ExifModel, &f.ExifDatetime, &f.ExifGPSLat, &f.ExifGPSLng,
		&f.IndexError,
	)
	if err != nil {
		return nil, err
	}
	f.PHash = uint64(ph)
	f.AHash = uint64(ah)
	f.DHash = uint64(dh)
	json.Unmarshal([]byte(domJSON), &f.DominantColors)

	var rawH []int64
	if json.Unmarshal([]byte(vidJSON), &rawH) == nil {
		f.VideoPHashes = make([]uint64, len(rawH))
		for i, h := range rawH {
			f.VideoPHashes[i] = uint64(h)
		}
	}
	return &f, nil
}

// ─── WatchDir queries ─────────────────────────────────────────────────────────

func (db *DB) InsertWatchDir(ctx context.Context, d *WatchDir) (int64, error) {
	db.lock.Lock()
	defer db.lock.Unlock()
	extJSON, _ := json.Marshal(d.Extensions)
	res, err := db.sql.ExecContext(ctx,
		`INSERT INTO watch_dirs (path, recursive, extensions, enabled, created_at)
         VALUES (?,?,?,?,?)`,
		d.Path, boolInt(d.Recursive), string(extJSON), boolInt(d.Enabled),
		time.Now().Unix(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) WatchDirs(ctx context.Context) ([]*WatchDir, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT id,path,recursive,extensions,enabled,created_at,last_scan_at,last_scan_count
         FROM watch_dirs ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var dirs []*WatchDir
	for rows.Next() {
		var d WatchDir
		var rec, ena int
		var extJSON string
		if err := rows.Scan(&d.ID, &d.Path, &rec, &extJSON, &ena,
			&d.CreatedAt, &d.LastScanAt, &d.LastScanCount); err != nil {
			return nil, err
		}
		d.Recursive = rec != 0
		d.Enabled = ena != 0
		json.Unmarshal([]byte(extJSON), &d.Extensions)
		dirs = append(dirs, &d)
	}
	return dirs, rows.Err()
}

func (db *DB) DeleteWatchDir(ctx context.Context, id int64) error {
	db.lock.Lock()
	defer db.lock.Unlock()
	_, err := db.sql.ExecContext(ctx, `DELETE FROM watch_dirs WHERE id=?`, id)
	return err
}

func (db *DB) UpdateWatchDirScan(ctx context.Context, id int64, count int) error {
	db.lock.Lock()
	defer db.lock.Unlock()
	_, err := db.sql.ExecContext(ctx,
		`UPDATE watch_dirs SET last_scan_at=?, last_scan_count=? WHERE id=?`,
		time.Now().Unix(), count, id)
	return err
}

func (db *DB) ToggleWatchDir(ctx context.Context, id int64, enabled bool) error {
	db.lock.Lock()
	defer db.lock.Unlock()
	_, err := db.sql.ExecContext(ctx,
		`UPDATE watch_dirs SET enabled=? WHERE id=?`, boolInt(enabled), id)
	return err
}

// ─── ScanJob queries ──────────────────────────────────────────────────────────

func (db *DB) CreateScanJob(ctx context.Context, dirID int64) (int64, error) {
	db.lock.Lock()
	defer db.lock.Unlock()
	res, err := db.sql.ExecContext(ctx,
		`INSERT INTO scan_jobs (dir_id, started_at) VALUES (?,?)`,
		dirID, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) UpdateScanJob(ctx context.Context, id int64, total, processed, errors int, status string) error {
	db.lock.Lock()
	defer db.lock.Unlock()
	var completedAt int64
	if status != "running" {
		completedAt = time.Now().Unix()
	}
	_, err := db.sql.ExecContext(ctx,
		`UPDATE scan_jobs SET total=?, processed=?, errors=?, status=?, completed_at=? WHERE id=?`,
		total, processed, errors, status, completedAt, id)
	return err
}

func (db *DB) RecentScanJobs(ctx context.Context) ([]*ScanJob, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT id,dir_id,status,total,processed,errors,started_at,completed_at
         FROM scan_jobs ORDER BY id DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []*ScanJob
	for rows.Next() {
		var j ScanJob
		if err := rows.Scan(&j.ID, &j.DirID, &j.Status, &j.Total, &j.Processed,
			&j.Errors, &j.StartedAt, &j.CompletedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, &j)
	}
	return jobs, rows.Err()
}

// ─── Health / repair ──────────────────────────────────────────────────────────

// FilesMissingFingerprints returns up to limit video paths stored without
// usable fingerprints (past failures recorded as clean PHash=0 rows) that
// haven't been verified since `since`. Repaired and retried rows carry fresh
// stamps and drop out, so cycles always terminate.
func (db *DB) FilesMissingFingerprints(ctx context.Context, since int64, limit int) ([]string, error) {
	return db.pathList(ctx,
		`SELECT path FROM files WHERE file_type='video'
		 AND (video_phashes='[]' OR video_phashes='' OR phash=0)
		 AND last_verified_at < ?
		 ORDER BY id LIMIT ?`, since, limit)
}

// CountMissingFingerprints counts what FilesMissingFingerprints would return.
func (db *DB) CountMissingFingerprints(ctx context.Context, since int64) (int64, error) {
	var n int64
	err := db.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM files WHERE file_type='video'
		 AND (video_phashes='[]' OR video_phashes='' OR phash=0)
		 AND last_verified_at < ?`, since).Scan(&n)
	return n, err
}

// FilesWithOldErrors returns up to limit paths whose last index attempt
// failed before olderThan (unix), oldest first — at most one retry per
// health cycle per file.
func (db *DB) FilesWithOldErrors(ctx context.Context, olderThan int64, limit int) ([]string, error) {
	return db.pathList(ctx,
		`SELECT path FROM files WHERE index_error != ''
		 AND last_verified_at < ? ORDER BY last_verified_at ASC, id ASC LIMIT ?`,
		olderThan, limit)
}

// VerifyCandidate is one row for the health loop's oldest-first pass.
type VerifyCandidate struct {
	ID         int64
	Path       string
	Size       int64
	ModifiedAt int64
	SHA256     string
}

// NextVerifyBatch returns up to limit files not verified since cycleStart
// and not freshly indexed, oldest verification first. Crash resume is
// implicit: verified files carry fresh timestamps and drop out.
func (db *DB) NextVerifyBatch(ctx context.Context, cycleStart int64, limit int) ([]VerifyCandidate, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT id, path, size, modified_at, sha256 FROM files
		 WHERE last_verified_at < ? AND indexed_at < ?
		 ORDER BY last_verified_at ASC, id ASC LIMIT ?`,
		cycleStart, cycleStart, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VerifyCandidate
	for rows.Next() {
		var c VerifyCandidate
		if err := rows.Scan(&c.ID, &c.Path, &c.Size, &c.ModifiedAt, &c.SHA256); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// TouchVerified stamps a file as checked without touching its fingerprints.
func (db *DB) TouchVerified(ctx context.Context, id int64, now int64) error {
	db.lock.Lock()
	defer db.lock.Unlock()
	_, err := db.sql.ExecContext(ctx,
		`UPDATE files SET last_verified_at=? WHERE id=?`, now, id)
	return err
}

// CountUnverifiedSince returns how many files still predate cycleStart —
// the cycle is complete when this reaches zero.
func (db *DB) CountUnverifiedSince(ctx context.Context, cycleStart int64) (int64, error) {
	var n int64
	err := db.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM files WHERE last_verified_at < ? AND indexed_at < ?`,
		cycleStart, cycleStart).Scan(&n)
	return n, err
}

// RepairState records the last completed health cycle for the UI.
type RepairState struct {
	LastCycleCompletedAt int64 `json:"last_cycle_completed_at"`
	LastCycleChecked     int64 `json:"last_cycle_checked"`
	LastCycleRepaired    int64 `json:"last_cycle_repaired"`
	LastCycleErrors      int64 `json:"last_cycle_errors"`
	LastCycleSHABytes    int64 `json:"last_cycle_sha_bytes"`
}

func (db *DB) GetRepairState(ctx context.Context) (*RepairState, error) {
	s := &RepairState{}
	err := db.sql.QueryRowContext(ctx,
		`SELECT last_cycle_completed_at, last_cycle_checked, last_cycle_repaired,
		        last_cycle_errors, last_cycle_sha_bytes FROM repair_state WHERE id=1`,
	).Scan(&s.LastCycleCompletedAt, &s.LastCycleChecked, &s.LastCycleRepaired,
		&s.LastCycleErrors, &s.LastCycleSHABytes)
	if err == sql.ErrNoRows {
		return &RepairState{}, nil
	}
	return s, err
}

func (db *DB) SetRepairState(ctx context.Context, s *RepairState) error {
	db.lock.Lock()
	defer db.lock.Unlock()
	_, err := db.sql.ExecContext(ctx,
		`INSERT INTO repair_state (id, last_cycle_completed_at, last_cycle_checked,
		  last_cycle_repaired, last_cycle_errors, last_cycle_sha_bytes)
		 VALUES (1,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET
		  last_cycle_completed_at=excluded.last_cycle_completed_at,
		  last_cycle_checked=excluded.last_cycle_checked,
		  last_cycle_repaired=excluded.last_cycle_repaired,
		  last_cycle_errors=excluded.last_cycle_errors,
		  last_cycle_sha_bytes=excluded.last_cycle_sha_bytes`,
		s.LastCycleCompletedAt, s.LastCycleChecked, s.LastCycleRepaired,
		s.LastCycleErrors, s.LastCycleSHABytes)
	return err
}

func (db *DB) pathList(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ─── Stats ────────────────────────────────────────────────────────────────────

func (db *DB) Stats(ctx context.Context) (*Stats, error) {
	var s Stats
	db.sql.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(size),0) FROM files WHERE index_error=''`).
		Scan(&s.TotalFiles, &s.TotalSize)
	db.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM files WHERE file_type='image' AND index_error=''`).
		Scan(&s.TotalImages)
	db.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM files WHERE file_type='video' AND index_error=''`).
		Scan(&s.TotalVideos)
	db.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM watch_dirs WHERE enabled=1`).Scan(&s.IndexedDirs)
	db.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM scan_jobs WHERE status='running'`).Scan(&s.ActiveScans)
	return &s, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
