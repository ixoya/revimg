// Package search implements the multi-algorithm similarity search engine.
package search

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/user/revimg/internal/config"
	"github.com/user/revimg/internal/db"
	"github.com/user/revimg/internal/hasher"
)

// ─── Request / Response ───────────────────────────────────────────────────────

// Weights controls how much each algorithm contributes to the final score.
type Weights struct {
	PHash float64 `json:"phash"`
	AHash float64 `json:"ahash"`
	DHash float64 `json:"dhash"`
	Color float64 `json:"color"`
}

// DefaultWeights returns the weights from config.
func DefaultWeights(cfg *config.Config) Weights {
	return Weights{
		PHash: cfg.WeightPHash,
		AHash: cfg.WeightAHash,
		DHash: cfg.WeightDHash,
		Color: cfg.WeightColor,
	}
}

// Params is the full set of search parameters submitted by the client.
type Params struct {
	MinSimilarity float64  `json:"min_similarity"`
	MaxResults    int      `json:"max_results"`
	Weights       Weights  `json:"weights"`
	HashSize      int      `json:"hash_size"`

	FileTypes  []string `json:"file_types"`
	Extensions []string `json:"extensions"`

	MinWidth  int `json:"min_width"`
	MaxWidth  int `json:"max_width"`
	MinHeight int `json:"min_height"`
	MaxHeight int `json:"max_height"`

	MinSize int64 `json:"min_size"`
	MaxSize int64 `json:"max_size"`

	MinDate   int64  `json:"min_date"`
	MaxDate   int64  `json:"max_date"`
	DateField string `json:"date_field"`

	PathPattern     string `json:"path_pattern"`
	FilenamePattern string `json:"filename_pattern"`

	Codecs     []string `json:"codecs"`
	MinFPS     float64  `json:"min_fps"`
	MaxFPS     float64  `json:"max_fps"`
	MinBitrate int64    `json:"min_bitrate"`
	MaxBitrate int64    `json:"max_bitrate"`
	MinDurMs   int64    `json:"min_duration_ms"`
	MaxDurMs   int64    `json:"max_duration_ms"`
}

// QueryFeatures holds the computed features of the uploaded query file.
type QueryFeatures struct {
	FileType       string    `json:"file_type"`
	Width          int       `json:"width"`
	Height         int       `json:"height"`
	PHash          uint64    `json:"phash,omitempty"`
	AHash          uint64    `json:"ahash,omitempty"`
	DHash          uint64    `json:"dhash,omitempty"`
	ColorHistogram []float32 `json:"-"`
	VideoPHashes   []uint64  `json:"-"`
}

// Result is one ranked match from a search.
type Result struct {
	File       *db.File `json:"file"`
	Score      float64  `json:"score"`
	ScorePHash float64  `json:"score_phash,omitempty"`
	ScoreAHash float64  `json:"score_ahash,omitempty"`
	ScoreDHash float64  `json:"score_dhash,omitempty"`
	ScoreColor float64  `json:"score_color,omitempty"`
	ScoreVideo float64  `json:"score_video,omitempty"`
}

// SearchResponse is the full API response for a search request.
type SearchResponse struct {
	Query      *QueryFeatures `json:"query"`
	Results    []*Result      `json:"results"`
	Total      int            `json:"total"`
	DurationMs int64          `json:"duration_ms"`
}

// ─── Engine ───────────────────────────────────────────────────────────────────

// Engine performs similarity searches against the indexed file database.
type Engine struct {
	db  *db.DB
	cfg *config.Config
}

// New creates a search Engine.
func New(database *db.DB, cfg *config.Config) *Engine {
	return &Engine{db: database, cfg: cfg}
}

// Search performs a linear scan over all matching indexed files and returns
// ranked results.
//
// Performance: at 100k files each scoring ~2µs → ~200ms worst case.
// A BK-tree pre-filter can be added for very large indexes (>500k).
func (e *Engine) Search(ctx context.Context, q *QueryFeatures, p Params) (*SearchResponse, error) {
	start := time.Now()

	// Sanitise
	if p.MinSimilarity <= 0 {
		p.MinSimilarity = e.cfg.DefaultThreshold
	}
	if p.MaxResults <= 0 {
		p.MaxResults = e.cfg.MaxResults
	}
	normaliseWeights(&p.Weights)

	f := buildFilter(p)
	files, err := e.db.AllFiles(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("db scan: %w", err)
	}

	results := make([]*Result, 0, 64)
	for _, file := range files {
		if ctx.Err() != nil {
			break
		}
		r := e.score(q, file, p.Weights)
		if r.Score >= p.MinSimilarity && textMatch(file, p) {
			results = append(results, r)
		}
	}

	// Sort descending by score, then path for stable ordering
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].File.Path < results[j].File.Path
	})

	total := len(results)
	if total > p.MaxResults {
		results = results[:p.MaxResults]
	}

	return &SearchResponse{
		Query:      q,
		Results:    results,
		Total:      total,
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

// score computes the weighted similarity between query q and indexed file f.
func (e *Engine) score(q *QueryFeatures, f *db.File, w Weights) *Result {
	r := &Result{File: f}

	switch {
	case q.FileType == "image" && f.FileType == db.FileTypeImage:
		r.ScorePHash = hasher.HammingSimilarity(q.PHash, f.PHash)
		r.ScoreAHash = hasher.HammingSimilarity(q.AHash, f.AHash)
		r.ScoreDHash = hasher.HammingSimilarity(q.DHash, f.DHash)
		if len(q.ColorHistogram) > 0 && len(f.ColorHistogram) > 0 {
			if fh := hasher.DeserializeHistogram(f.ColorHistogram); fh != nil {
				r.ScoreColor = hasher.HistogramIntersection(q.ColorHistogram, fh)
			}
		}
		r.Score = w.PHash*r.ScorePHash +
			w.AHash*r.ScoreAHash +
			w.DHash*r.ScoreDHash +
			w.Color*r.ScoreColor

	case q.FileType == "video" && f.FileType == db.FileTypeVideo:
		r.ScorePHash = hasher.HammingSimilarity(q.PHash, f.PHash)
		if len(q.VideoPHashes) > 0 && len(f.VideoPHashes) > 0 {
			r.ScoreVideo = hasher.VideoSimilarity(q.VideoPHashes, f.VideoPHashes)
		} else {
			r.ScoreVideo = r.ScorePHash
		}
		if len(q.ColorHistogram) > 0 && len(f.ColorHistogram) > 0 {
			if fh := hasher.DeserializeHistogram(f.ColorHistogram); fh != nil {
				r.ScoreColor = hasher.HistogramIntersection(q.ColorHistogram, fh)
			}
		}
		r.Score = 0.60*r.ScoreVideo + 0.20*r.ScorePHash + 0.20*r.ScoreColor

	default:
		// Cross-type: compare primary hash + colour only
		r.ScorePHash = hasher.HammingSimilarity(q.PHash, f.PHash)
		if len(q.ColorHistogram) > 0 && len(f.ColorHistogram) > 0 {
			if fh := hasher.DeserializeHistogram(f.ColorHistogram); fh != nil {
				r.ScoreColor = hasher.HistogramIntersection(q.ColorHistogram, fh)
			}
		}
		r.Score = 0.7*r.ScorePHash + 0.3*r.ScoreColor
	}

	return r
}

// ─── Filters ─────────────────────────────────────────────────────────────────

func buildFilter(p Params) db.FileFilter {
	f := db.FileFilter{
		MinWidth:   p.MinWidth,
		MaxWidth:   p.MaxWidth,
		MinHeight:  p.MinHeight,
		MaxHeight:  p.MaxHeight,
		MinSize:    p.MinSize,
		MaxSize:    p.MaxSize,
		Codecs:     p.Codecs,
		MinFPS:     p.MinFPS,
		MaxFPS:     p.MaxFPS,
		MinBitrate: p.MinBitrate,
		MaxBitrate: p.MaxBitrate,
		MinDurMs:   p.MinDurMs,
		MaxDurMs:   p.MaxDurMs,
	}
	for _, t := range p.FileTypes {
		f.FileTypes = append(f.FileTypes, db.FileType(t))
	}
	for _, ext := range p.Extensions {
		f.Extensions = append(f.Extensions, strings.ToLower(ext))
	}
	// Date field
	f.MinModAt = p.MinDate
	f.MaxModAt = p.MaxDate
	return f
}

// textMatch applies path/filename pattern filters that are faster in Go than SQL.
func textMatch(f *db.File, p Params) bool {
	if p.PathPattern != "" &&
		!strings.Contains(strings.ToLower(f.Path), strings.ToLower(p.PathPattern)) {
		return false
	}
	if p.FilenamePattern != "" {
		name := strings.ToLower(f.Filename)
		pat  := strings.ToLower(p.FilenamePattern)
		// Try glob first, fall back to contains
		if matched, err := filepath.Match(pat, name); err != nil || !matched {
			if !strings.Contains(name, pat) {
				return false
			}
		}
	}
	return true
}

func normaliseWeights(w *Weights) {
	sum := w.PHash + w.AHash + w.DHash + w.Color
	if sum == 0 {
		w.PHash = 0.45
		w.DHash = 0.25
		w.AHash = 0.10
		w.Color = 0.20
		return
	}
	w.PHash /= sum
	w.AHash /= sum
	w.DHash /= sum
	w.Color /= sum
}


