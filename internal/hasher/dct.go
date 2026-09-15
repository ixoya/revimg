// Package hasher implements perceptual hashing algorithms for images and video.
package hasher

import "math"

// dct1D computes an unnormalized DCT-II on the given signal.
// Using the unnormalized form is fine for hashing — we only compare
// relative magnitudes, so the scale factor cancels out.
func dct1D(in []float64) []float64 {
	n := len(in)
	out := make([]float64, n)
	piOverN := math.Pi / float64(n)
	for k := 0; k < n; k++ {
		var s float64
		scale := piOverN * float64(k)
		for i := 0; i < n; i++ {
			s += in[i] * math.Cos(scale*(float64(i)+0.5))
		}
		out[k] = s
	}
	return out
}

// DCT2D applies a separable 2D DCT-II to a square NxN matrix
// (row-major, i.e. matrix[row][col]).
// Complexity O(N³) — fine for N=32 (the pHash input size).
func DCT2D(in [][]float64) [][]float64 {
	n := len(in)

	// DCT along rows
	rowDCT := make([][]float64, n)
	for r := range in {
		rowDCT[r] = dct1D(in[r])
	}

	// DCT along columns
	out := make([][]float64, n)
	for r := range out {
		out[r] = make([]float64, n)
	}
	col := make([]float64, n)
	for c := 0; c < n; c++ {
		for r := 0; r < n; r++ {
			col[r] = rowDCT[r][c]
		}
		dcol := dct1D(col)
		for r := 0; r < n; r++ {
			out[r][c] = dcol[r]
		}
	}
	return out
}
