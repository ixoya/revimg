package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func openTest(t *testing.T) (*DB, context.Context) {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d, context.Background()
}

func seedFile(t *testing.T, d *DB, ctx context.Context, path string, ft FileType, phash uint64, hashes []uint64, indexErr string) int64 {
	t.Helper()
	f := &File{
		Path: path, Filename: filepath.Base(path), Directory: filepath.Dir(path),
		FileType: ft, Extension: ".mp4", Size: 100, ModifiedAt: 100, IndexedAt: 100,
		SHA256: "abc", PHash: phash, VideoPHashes: hashes, IndexError: indexErr,
	}
	if err := d.UpsertFile(ctx, f); err != nil {
		t.Fatalf("upsert %s: %v", path, err)
	}
	got, err := d.GetFileByPath(ctx, path)
	if err != nil || got == nil {
		t.Fatalf("refetch %s: %v", path, err)
	}
	return got.ID
}

// Legacy databases lack last_verified_at; Open must add it.
func TestMigrateAddsLastVerified(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`CREATE TABLE files (
		id INTEGER PRIMARY KEY AUTOINCREMENT, path TEXT NOT NULL UNIQUE,
		filename TEXT NOT NULL DEFAULT '', directory TEXT NOT NULL DEFAULT '',
		file_type TEXT NOT NULL DEFAULT '', extension TEXT NOT NULL DEFAULT '',
		size INTEGER NOT NULL DEFAULT 0, width INTEGER NOT NULL DEFAULT 0,
		height INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL DEFAULT 0,
		modified_at INTEGER NOT NULL DEFAULT 0, indexed_at INTEGER NOT NULL DEFAULT 0,
		sha256 TEXT NOT NULL DEFAULT '', phash INTEGER NOT NULL DEFAULT 0,
		ahash INTEGER NOT NULL DEFAULT 0, dhash INTEGER NOT NULL DEFAULT 0,
		color_histogram BLOB, dominant_colors TEXT NOT NULL DEFAULT '[]',
		duration_ms INTEGER NOT NULL DEFAULT 0, fps REAL NOT NULL DEFAULT 0,
		codec TEXT NOT NULL DEFAULT '', audio_codec TEXT NOT NULL DEFAULT '',
		bitrate INTEGER NOT NULL DEFAULT 0, frame_count INTEGER NOT NULL DEFAULT 0,
		video_phashes TEXT NOT NULL DEFAULT '[]', exif_make TEXT NOT NULL DEFAULT '',
		exif_model TEXT NOT NULL DEFAULT '', exif_datetime TEXT NOT NULL DEFAULT '',
		exif_gps_lat REAL NOT NULL DEFAULT 0, exif_gps_lng REAL NOT NULL DEFAULT 0,
		index_error TEXT NOT NULL DEFAULT '')`)
	if err != nil {
		t.Fatal(err)
	}
	raw.Close()

	d, err := Open(path) // must not fail on duplicate column on 2nd run either
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	defer d.Close()
	if _, err := Open(path); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	ctx := context.Background()
	id := seedFile(t, d, ctx, "/v/a.mp4", FileTypeVideo, 7, []uint64{7}, "")
	if err := d.TouchVerified(ctx, id, 1234); err != nil {
		t.Fatalf("touch after migrate: %v", err)
	}
}

func TestMissingFingerprints(t *testing.T) {
	d, ctx := openTest(t)
	seedFile(t, d, ctx, "/v/good.mp4", FileTypeVideo, 9, []uint64{9}, "")
	seedFile(t, d, ctx, "/v/empty.mp4", FileTypeVideo, 0, nil, "")
	seedFile(t, d, ctx, "/v/img.jpg", FileTypeImage, 0, nil, "") // images excluded
	now := int64(9999999999)

	got, err := d.FilesMissingFingerprints(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "/v/empty.mp4" {
		t.Fatalf("missing = %v", got)
	}
	if n, _ := d.CountMissingFingerprints(ctx, now); n != 1 {
		t.Fatalf("count = %d", n)
	}
	// Verifying it this cycle drops it out (cycle termination).
	f, _ := d.GetFileByPath(ctx, "/v/empty.mp4")
	_ = d.TouchVerified(ctx, f.ID, now)
	if got, _ := d.FilesMissingFingerprints(ctx, now, 10); len(got) != 0 {
		t.Fatalf("after touch, missing = %v", got)
	}
}

func TestVerifyBatchOldestFirstAndResume(t *testing.T) {
	d, ctx := openTest(t)
	ids := map[string]int64{}
	for _, p := range []string{"/a.mp4", "/b.mp4", "/c.mp4"} {
		ids[p] = seedFile(t, d, ctx, p, FileTypeVideo, 5, []uint64{5}, "")
	}
	// Upsert stamps now; backdate to fixed times.
	_ = d.TouchVerified(ctx, ids["/a.mp4"], 100)
	_ = d.TouchVerified(ctx, ids["/b.mp4"], 200)
	_ = d.TouchVerified(ctx, ids["/c.mp4"], 300)
	cycle := int64(1000)

	b, err := d.NextVerifyBatch(ctx, cycle, 2)
	if err != nil || len(b) != 2 || b[0].Path != "/a.mp4" || b[1].Path != "/b.mp4" {
		t.Fatalf("batch = %+v, err = %v", b, err)
	}
	// "Crash" after verifying one: resume returns the rest, oldest first.
	_ = d.TouchVerified(ctx, ids["/a.mp4"], cycle+1)
	b, _ = d.NextVerifyBatch(ctx, cycle, 10)
	if len(b) != 2 || b[0].Path != "/b.mp4" || b[1].Path != "/c.mp4" {
		t.Fatalf("resume batch = %+v", b)
	}
	if n, _ := d.CountUnverifiedSince(ctx, cycle); n != 2 {
		t.Fatalf("unverified = %d", n)
	}
	// Freshly indexed files (indexed_at >= cycle) are skipped even if stale.
	_ = d.TouchVerified(ctx, ids["/b.mp4"], 1) // backdate verified...
	f, _ := d.GetFileByPath(ctx, "/b.mp4")
	_, _ = d.sql.Exec(`UPDATE files SET indexed_at=? WHERE id=?`, cycle+5, f.ID)
	b, _ = d.NextVerifyBatch(ctx, cycle, 10)
	for _, c := range b {
		if c.Path == "/b.mp4" {
			t.Fatalf("freshly indexed file returned: %+v", b)
		}
	}
}

func TestOldErrorsCooldown(t *testing.T) {
	d, ctx := openTest(t)
	seedFile(t, d, ctx, "/v/broken.mp4", FileTypeVideo, 0, nil, "ffprobe: exit status 1")
	seedFile(t, d, ctx, "/v/ok.mp4", FileTypeVideo, 3, []uint64{3}, "")
	cycle := int64(5000)
	// Upsert stamps real now; backdate so the rows predate the cycle.
	for _, p := range []string{"/v/broken.mp4", "/v/ok.mp4"} {
		f, _ := d.GetFileByPath(ctx, p)
		_ = d.TouchVerified(ctx, f.ID, 100)
	}

	got, err := d.FilesWithOldErrors(ctx, cycle, 10)
	if err != nil || len(got) != 1 || got[0] != "/v/broken.mp4" {
		t.Fatalf("errors = %v, err = %v", got, err)
	}
	// A retry this cycle stamps it; no second retry within the cycle.
	f, _ := d.GetFileByPath(ctx, "/v/broken.mp4")
	_ = d.TouchVerified(ctx, f.ID, cycle+1)
	if got, _ := d.FilesWithOldErrors(ctx, cycle, 10); len(got) != 0 {
		t.Fatalf("after retry, errors = %v", got)
	}
}

func TestRepairStateRoundtrip(t *testing.T) {
	d, ctx := openTest(t)
	s, err := d.GetRepairState(ctx)
	if err != nil || s.LastCycleChecked != 0 {
		t.Fatalf("fresh state = %+v, err = %v", s, err)
	}
	want := &RepairState{LastCycleCompletedAt: 42, LastCycleChecked: 7,
		LastCycleRepaired: 3, LastCycleErrors: 1, LastCycleSHABytes: 99}
	if err := d.SetRepairState(ctx, want); err != nil {
		t.Fatal(err)
	}
	if s, _ := d.GetRepairState(ctx); *s != *want {
		t.Fatalf("roundtrip = %+v", s)
	}
}
