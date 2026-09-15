package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

// Config holds all application configuration.
type Config struct {
	DBPath     string `json:"db_path"`
	ListenAddr string `json:"listen_addr"`
	DataDir    string `json:"data_dir"`

	// Indexing
	NumWorkers  int   `json:"num_workers"`
	MaxFileSize int64 `json:"max_file_size"` // bytes

	// Video
	VideoEnabled bool    `json:"video_enabled"`
	FFmpegPath   string  `json:"ffmpeg_path"`
	FFprobePath  string  `json:"ffprobe_path"`
	VideoFPS     float64 `json:"video_fps"`
	MaxFrames    int     `json:"max_frames"`

	// Hashing
	HashSize  int `json:"hash_size"`   // 8, 16, 32 — controls DCT hash resolution
	ColorBins int `json:"color_bins"` // per HSV channel, total bins = ColorBins^3

	// Search defaults
	DefaultThreshold float64 `json:"default_threshold"`
	MaxResults       int     `json:"max_results"`
	ThumbSize        int     `json:"thumb_size"`

	// Weights for combined similarity score
	WeightPHash float64 `json:"weight_phash"`
	WeightDHash float64 `json:"weight_dhash"`
	WeightAHash float64 `json:"weight_ahash"`
	WeightColor float64 `json:"weight_color"`

	// Logging
	LogPath string `json:"log_path"` // empty = stdout only

	// Directory names to skip during scanning (matched against the last component)
	IgnoredDirs []string `json:"ignored_dirs"`

	// When true, the indexer skips DB deletions if the file's parent directory
	// no longer exists — prevents wiping the index when a drive is unmounted.
	PreserveOnUnmount bool `json:"preserve_on_unmount"`

	// Background health loop (repair missing fingerprints, verify stale files)
	RepairEnabled        bool  `json:"repair_enabled"`
	RepairIntervalH      int   `json:"repair_interval_h"`       // hours between cycles; 0 uses 24
	RepairThreads        int   `json:"repair_threads"`          // ffmpeg threads per file; 1 = quietest
	RepairDelayMs        int   `json:"repair_delay_ms"`         // sleep between files
	RepairSHABudgetBytes int64 `json:"repair_sha_budget_bytes"` // bitrot re-read budget per cycle
}

var defaults = Config{
	ListenAddr:       "127.0.0.1:7777",
	NumWorkers:       runtime.NumCPU(),
	MaxFileSize:      1 << 30, // 1 GiB
	VideoEnabled:     true,
	FFmpegPath:       "ffmpeg",
	FFprobePath:      "ffprobe",
	VideoFPS:         1.0,
	MaxFrames:        32,
	HashSize:         8,
	ColorBins:        8,
	DefaultThreshold: 0.85,
	MaxResults:       200,
	ThumbSize:        360,
	WeightPHash:      0.45,
	WeightDHash:      0.25,
	WeightAHash:      0.10,
	WeightColor:      0.20,
	IgnoredDirs:       []string{"node_modules", ".git", ".svn", "__pycache__", ".venv", "venv", ".cache", "vendor", ".next", ".nuxt", "dist", "build", ".svelte-kit"},
	PreserveOnUnmount: true,

	RepairEnabled:        true,
	RepairIntervalH:      24,
	RepairThreads:        1,
	RepairDelayMs:        100,
	RepairSHABudgetBytes: 5 << 30, // 5 GiB of bitrot re-reads per cycle
}

// Load reads config from disk, falling back to defaults.
func Load() *Config {
	dataDir := userDataDir()
	cfg := defaults
	cfg.DataDir = dataDir
	cfg.DBPath = filepath.Join(dataDir, "revimg.db")

	cfgPath := filepath.Join(dataDir, "config.json")
	if data, err := os.ReadFile(cfgPath); err == nil {
		// Merge file values over defaults
		if err := json.Unmarshal(data, &cfg); err != nil {
			// Bad config file — keep defaults, don't crash
		}
	}

	if err := os.MkdirAll(cfg.ThumbDir(), 0o755); err != nil {
		panic("cannot create data dir: " + err.Error())
	}

	return &cfg
}

// Save persists the current configuration to disk.
func (c *Config) Save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(c.DataDir, "config.json"), data, 0o644)
}

// ThumbDir returns the directory where generated thumbnails are stored.
func (c *Config) ThumbDir() string {
	return filepath.Join(c.DataDir, "thumbs")
}

func userDataDir() string {
	if dir := os.Getenv("REVIMG_DATA_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "revimg")
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "revimg")
	default:
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "revimg")
		}
		return filepath.Join(home, ".local", "share", "revimg")
	}
}
