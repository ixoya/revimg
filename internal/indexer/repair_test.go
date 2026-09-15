package indexer

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/user/revimg/internal/config"
	"github.com/user/revimg/internal/db"
	"github.com/user/revimg/internal/hasher"
)

// End-to-end repair cycle on a scratch archive: one poisoned video row
// must gain real fingerprints, one healthy image must verify in place.
func TestRepairCycleHealsScratchArchive(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not available")
	}
	tmp := t.TempDir()
	ctx := context.Background()

	vid := filepath.Join(tmp, "clip.mp4")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=128x128:rate=5",
		"-pix_fmt", "yuv420p", "-y", vid)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gen video: %v %s", err, out)
	}
	imgPath := filepath.Join(tmp, "still.png")
	func() {
		f, err := os.Create(imgPath)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		img := image.NewRGBA(image.Rect(0, 0, 16, 16))
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				img.Set(x, y, color.RGBA{uint8(x * 16), uint8(y * 16), 128, 255})
			}
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}()

	cfg := &config.Config{
		DataDir: tmp, DBPath: filepath.Join(tmp, "t.db"),
		NumWorkers: 1, MaxFileSize: 1 << 30,
		VideoEnabled: true, FFmpegPath: "ffmpeg", FFprobePath: "ffprobe",
		VideoFPS: 1, MaxFrames: 8, HashSize: 8, ColorBins: 8,
		RepairEnabled: true, RepairThreads: 1, RepairDelayMs: 1,
		RepairSHABudgetBytes: 1 << 30, ThumbSize: 64, PreserveOnUnmount: true,
	}
	if err := os.MkdirAll(cfg.ThumbDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	idx := New(d, cfg)
	defer idx.Shutdown(context.Background())

	vinfo, _ := os.Stat(vid)
	vsha, _ := hasher.FileSHA256(vid)
	if err := d.UpsertFile(ctx, &db.File{ // poisoned: no fingerprints
		Path: vid, Filename: "clip.mp4", Directory: tmp,
		FileType: db.FileTypeVideo, Extension: ".mp4",
		Size: vinfo.Size(), ModifiedAt: vinfo.ModTime().Unix(),
		IndexedAt: vinfo.ModTime().Unix(), SHA256: vsha,
	}); err != nil {
		t.Fatal(err)
	}
	ifeats, err := hasher.ExtractImageFeatures(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	iinfo, _ := os.Stat(imgPath)
	isha, _ := hasher.FileSHA256(imgPath)
	if err := d.UpsertFile(ctx, &db.File{ // healthy image
		Path: imgPath, Filename: "still.png", Directory: tmp,
		FileType: db.FileTypeImage, Extension: ".png",
		Size: iinfo.Size(), ModifiedAt: iinfo.ModTime().Unix(),
		IndexedAt: iinfo.ModTime().Unix(), SHA256: isha,
		PHash: ifeats.PHash, AHash: ifeats.AHash, DHash: ifeats.DHash,
		ColorHistogram: hasher.SerializeHistogram(ifeats.ColorHistogram),
		DominantColors: ifeats.DominantColors,
		Width:          ifeats.Width, Height: ifeats.Height,
	}); err != nil {
		t.Fatal(err)
	}

	// Backdate seeds: stamps are whole seconds, so rows written in the
	// same second as the cycle would be indistinguishable from
	// repaired-this-cycle rows and skipped.
	for _, p := range []string{vid, imgPath} {
		f, err := d.GetFileByPath(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		f.IndexedAt = 100
		if err := d.UpsertFile(ctx, f); err != nil {
			t.Fatal(err)
		}
		if err := d.TouchVerified(ctx, f.ID, 100); err != nil {
			t.Fatal(err)
		}
	}

	cycleStart := time.Now().Unix()
	if !idx.StartRepair() {
		t.Fatal("StartRepair refused")
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		if st := idx.RepairStatus(); st != nil && !st.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("repair cycle did not finish in 60s")
		}
		time.Sleep(100 * time.Millisecond)
	}

	v, err := d.GetFileByPath(ctx, vid)
	if err != nil || v == nil || v.PHash == 0 || len(v.VideoPHashes) == 0 {
		t.Fatalf("video not healed: %+v, err=%v", v, err)
	}
	if n, _ := d.CountUnverifiedSince(ctx, cycleStart); n != 0 {
		t.Fatalf("unverified remaining: %d", n)
	}
	st, _ := d.GetRepairState(ctx)
	if st.LastCycleChecked != 1 || st.LastCycleRepaired != 1 || st.LastCycleCompletedAt == 0 {
		t.Fatalf("repair state not recorded: %+v", st)
	}
}
