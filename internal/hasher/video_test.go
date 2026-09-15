package hasher

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestExtractVideoFeatures exercises the ffmpeg frame-extraction path
// (including the -fps_mode/-vsync fallback). Needs a real video:
//
//	REVIMG_TEST_VIDEO=/path/to/file.mp4 go test ./internal/hasher/ -run TestExtractVideoFeatures -v
func TestExtractVideoFeatures(t *testing.T) {
	path := os.Getenv("REVIMG_TEST_VIDEO")
	if path == "" {
		t.Skip("REVIMG_TEST_VIDEO not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	feats, err := ExtractVideoFeatures(ctx, path, ProbeConfig{
		FFmpegPath: "ffmpeg", FFprobePath: "ffprobe", SampleFPS: 1, MaxFrames: 32,
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(feats.FrameHashes) == 0 || feats.PHash == 0 {
		t.Fatalf("empty fingerprints: %+v", feats)
	}
	t.Logf("frames=%d phash=%d duration_ms=%d", len(feats.FrameHashes), feats.PHash, feats.DurationMs)
}
