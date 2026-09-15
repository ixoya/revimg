package hasher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// VideoFeatures holds all metadata and content hashes for one video.
type VideoFeatures struct {
	Width       int
	Height      int
	DurationMs  int64
	FPS         float64
	Codec       string // video codec (h264, hevc, vp9, av1 …)
	AudioCodec  string
	Bitrate     int64 // total bitrate bps
	FrameCount  int
	PHash       uint64   // hash of the middle frame (representative thumbnail)
	AHash       uint64
	DHash       uint64
	FrameHashes []uint64 // pHash per sampled frame
	ColorHistogram []float32
}

// ProbeConfig controls how video files are analysed.
type ProbeConfig struct {
	FFmpegPath  string
	FFprobePath string
	SampleFPS   float64 // frames per second to sample
	MaxFrames   int     // cap on total frames to hash
	Threads     int     // ffmpeg decode threads; 0 = default (all cores)
}

// ExtractVideoFeatures extracts metadata and content hashes for a video.
// It shells out to ffprobe (metadata) and ffmpeg (frame extraction).
// If either binary is unavailable the error is returned immediately.
func ExtractVideoFeatures(ctx context.Context, path string, cfg ProbeConfig) (*VideoFeatures, error) {
	meta, err := probeVideo(ctx, cfg.FFprobePath, path)
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}

	fps := cfg.SampleFPS
	if fps <= 0 {
		fps = 1.0
	}
	maxFrames := cfg.MaxFrames
	if maxFrames <= 0 {
		maxFrames = 32
	}

	frameHashes, colorHist, err := sampleFrames(ctx, cfg.FFmpegPath, path, fps, maxFrames, cfg.Threads)
	if err != nil {
		// Fatal: a metadata-only row (PHash=0, no frame hashes) can never
		// match anything, so store the failure visibly instead of a fake-clean row.
		return nil, fmt.Errorf("frame extract: %w", err)
	}
	meta.FrameHashes = frameHashes
	meta.ColorHistogram = colorHist
	if len(frameHashes) > 0 {
		// Use middle frame hash as the primary hash
		mid := frameHashes[len(frameHashes)/2]
		meta.PHash = mid
		meta.AHash = mid // video aHash == frame aHash for simplicity
		meta.DHash = mid
	}

	return meta, nil
}

// VideoSimilarity computes a [0,1] similarity between two sets of frame hashes.
// Uses a sliding-window minimum Hamming approach to handle offsets (e.g. one
// video starts a few seconds into the other).
func VideoSimilarity(a, b []uint64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	// Make a the shorter sequence
	if len(a) > len(b) {
		a, b = b, a
	}
	na, nb := len(a), len(b)

	best := 0.0
	windowSize := imin(na, nb)

	for offset := 0; offset <= nb-windowSize; offset++ {
		var scoreSum float64
		for i := 0; i < windowSize; i++ {
			scoreSum += HammingSimilarity(a[i%na], b[offset+i])
		}
		score := scoreSum / float64(windowSize)
		if score > best {
			best = score
			if best > 0.99 {
				break // good enough
			}
		}
	}
	return best
}

// ─── Internal ─────────────────────────────────────────────────────────────────

// ffprobeOutput mirrors the JSON structure returned by ffprobe -v quiet -print_format json.
type ffprobeOutput struct {
	Streams []struct {
		CodecType    string `json:"codec_type"`
		CodecName    string `json:"codec_name"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		RFrameRate   string `json:"r_frame_rate"`
		NbFrames     string `json:"nb_frames"`
		BitRate      string `json:"bit_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
		BitRate  string `json:"bit_rate"`
	} `json:"format"`
}

func probeVideo(ctx context.Context, ffprobePath, videoPath string) (*VideoFeatures, error) {
	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "quiet",
		"-print_format", "json",
		"-show_streams",
		"-show_format",
		videoPath,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, stderr.String())
	}

	var probe ffprobeOutput
	if err := json.Unmarshal(stdout.Bytes(), &probe); err != nil {
		return nil, err
	}

	f := &VideoFeatures{}

	// Parse format-level duration and bitrate
	if d, err := strconv.ParseFloat(probe.Format.Duration, 64); err == nil {
		f.DurationMs = int64(d * 1000)
	}
	if br, err := strconv.ParseInt(probe.Format.BitRate, 10, 64); err == nil {
		f.Bitrate = br
	}

	// Parse per-stream fields
	for _, s := range probe.Streams {
		switch s.CodecType {
		case "video":
			f.Width = s.Width
			f.Height = s.Height
			f.Codec = s.CodecName
			f.FPS = parseRationalFPS(s.RFrameRate)
			if nb, err := strconv.Atoi(s.NbFrames); err == nil {
				f.FrameCount = nb
			}
			if br, err := strconv.ParseInt(s.BitRate, 10, 64); err == nil && br > 0 {
				f.Bitrate = br
			}
		case "audio":
			f.AudioCodec = s.CodecName
		}
	}

	return f, nil
}

// parseRationalFPS parses ffprobe's r_frame_rate "num/den" string.
func parseRationalFPS(r string) float64 {
	parts := strings.SplitN(r, "/", 2)
	if len(parts) != 2 {
		return 0
	}
	num, err1 := strconv.ParseFloat(parts[0], 64)
	den, err2 := strconv.ParseFloat(parts[1], 64)
	if err1 != nil || err2 != nil || den == 0 {
		return 0
	}
	return num / den
}

// sampleFrames extracts up to maxFrames frames at sampleFPS using ffmpeg,
// decodes each as JPEG from stdout, and computes pHash + color histogram.
func sampleFrames(ctx context.Context, ffmpegPath, videoPath string, sampleFPS float64, maxFrames, threads int) ([]uint64, []float32, error) {
	// Use a temp directory for frame output
	tmp, err := os.MkdirTemp("", "revimg-frames-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmp)

	pattern := filepath.Join(tmp, "f%06d.jpg")

	// Build ffmpeg command:
	//   -i <input>         input file
	//   -vf fps=<rate>     sample at rate fps
	//   -frames:v <n>      cap frame count
	//   -q:v 3             JPEG quality
	//   -fps_mode vfr      variable frame sync to avoid duplicates
	//                      (-vsync on ffmpeg < 7; retried as fallback)
	extract := func(syncFlag string) error {
		var stderr bytes.Buffer
		args := []string{
			"-hide_banner", "-loglevel", "error",
			"-i", videoPath,
			"-vf", fmt.Sprintf("fps=%.4f", sampleFPS),
			"-frames:v", strconv.Itoa(maxFrames),
			"-q:v", "3",
			syncFlag, "vfr",
		}
		if threads > 0 {
			args = append(args, "-threads", strconv.Itoa(threads))
		}
		args = append(args, pattern)
		cmd := exec.CommandContext(ctx, ffmpegPath, args...)
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("ffmpeg frame extract (%s): %w: %s", syncFlag, err, stderr.String())
		}
		return nil
	}
	// ponytail: try modern flag first, legacy retry only if ffmpeg rejects it
	if err := extract("-fps_mode"); err != nil && strings.Contains(err.Error(), "Unrecognized option") {
		if err := extract("-vsync"); err != nil {
			return nil, nil, err
		}
	} else if err != nil {
		return nil, nil, err
	}

	entries, err := filepath.Glob(filepath.Join(tmp, "f*.jpg"))
	if err != nil || len(entries) == 0 {
		return nil, nil, fmt.Errorf("no frames extracted")
	}

	hashes := make([]uint64, 0, len(entries))
	var accumHist []float32

	for _, entry := range entries {
		img, err := openJPEG(entry)
		if err != nil {
			continue // skip corrupt frame
		}
		h, err := computePHash(img)
		if err != nil {
			continue
		}
		hashes = append(hashes, h)

		// Accumulate colour histogram across frames
		fh := computeColorHistogram(img)
		if accumHist == nil {
			accumHist = make([]float32, len(fh))
		}
		for i, v := range fh {
			accumHist[i] += v
		}
	}

	if len(hashes) == 0 {
		return nil, nil, fmt.Errorf("no valid frames")
	}

	// Normalise accumulated histogram
	total := float32(len(hashes))
	for i := range accumHist {
		accumHist[i] /= total
	}

	// Deduplicate consecutive identical hashes (scene cuts → unique hashes)
	hashes = deduplicateConsecutive(hashes)

	return hashes, accumHist, nil
}

func openJPEG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return jpeg.Decode(f)
}

// deduplicateConsecutive removes consecutive duplicate hashes (Hamming==0).
func deduplicateConsecutive(hashes []uint64) []uint64 {
	if len(hashes) == 0 {
		return nil
	}
	out := []uint64{hashes[0]}
	for i := 1; i < len(hashes); i++ {
		if hashes[i] != out[len(out)-1] {
			out = append(out, hashes[i])
		}
	}
	return out
}

// CheckFFmpeg returns nil if both ffmpeg and ffprobe are available.
func CheckFFmpeg(ffmpegPath, ffprobePath string) error {
	for _, bin := range []string{ffmpegPath, ffprobePath} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%s not found in PATH: %w", bin, err)
		}
	}
	return nil
}

// VideoThumbnail extracts a single frame at seek seconds and saves it as JPEG.
func VideoThumbnail(ctx context.Context, ffmpegPath, videoPath, dstPath string, durationMs int64, maxSide int) error {
	seek := float64(durationMs) / 2000.0 // middle of video
	if seek < 0 {
		seek = 0
	}
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-hide_banner", "-loglevel", "error",
		"-ss", strconv.FormatFloat(seek, 'f', 3, 64),
		"-i", videoPath,
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", maxSide, maxSide),
		"-q:v", "3",
		"-y", dstPath,
	)
	return cmd.Run()
}

// TemporalCoherence returns the fraction of consecutive frame pairs that have
// Hamming distance ≤ threshold. High value → smooth video (few cuts).
func TemporalCoherence(hashes []uint64, threshold int) float64 {
	if len(hashes) < 2 {
		return 1.0
	}
	var similar int
	for i := 1; i < len(hashes); i++ {
		if HammingDistance(hashes[i-1], hashes[i]) <= threshold {
			similar++
		}
	}
	return float64(similar) / float64(len(hashes)-1)
}

// EstimateVideoHashFromFrames derives a compact video signature — the
// median pHash across all sampled frames weighted by temporal position.
// This single uint64 can be compared for rough video similarity without
// the full sequence comparison.
func EstimateVideoHashFromFrames(hashes []uint64) uint64 {
	if len(hashes) == 0 {
		return 0
	}
	if len(hashes) == 1 {
		return hashes[0]
	}
	// Bit majority vote: each bit set in the hash if a majority of frames set it
	counts := [64]int{}
	for _, h := range hashes {
		for bit := 0; bit < 64; bit++ {
			if h>>uint(bit)&1 == 1 {
				counts[bit]++
			}
		}
	}
	thresh := len(hashes) / 2
	var result uint64
	for bit := 0; bit < 64; bit++ {
		if counts[bit] > thresh {
			result |= 1 << uint(bit)
		}
	}
	return result
}


