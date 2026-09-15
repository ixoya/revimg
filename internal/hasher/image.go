package hasher

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"math"
	"math/bits"
	"os"
	"sort"

	// Supported image decoders
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/disintegration/imaging"
	"github.com/rwcarlsen/goexif/exif"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// pHash DCT input resolution. Higher = more discriminative but slower.
const dctSize = 32

// Color histogram parameters.
const (
	hBins    = 16 // Hue (0-360)
	sBins    = 4  // Saturation (0-1)
	vBins    = 4  // Value (0-1)
	histSize = hBins * sBins * vBins // 256 bins total
)

// ImageFeatures holds every computed feature for one image file.
type ImageFeatures struct {
	Width          int
	Height         int
	PHash          uint64
	AHash          uint64
	DHash          uint64
	ColorHistogram []float32 // histSize floats, normalized (sum ≈ 1)
	DominantColors []string  // top-5 hex colors

	// EXIF metadata (best-effort, zero-value if absent)
	ExifMake     string
	ExifModel    string
	ExifDatetime string
	ExifGPSLat   float64
	ExifGPSLng   float64
}

// ExtractImageFeatures opens the file at path and computes all features.
// Returns a non-nil error if the file cannot be decoded as an image.
func ExtractImageFeatures(path string) (*ImageFeatures, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	bounds := img.Bounds()
	feats := &ImageFeatures{
		Width:  bounds.Dx(),
		Height: bounds.Dy(),
	}

	feats.PHash, err = computePHash(img)
	if err != nil {
		return nil, err
	}
	feats.AHash, err = computeAHash(img)
	if err != nil {
		return nil, err
	}
	feats.DHash, err = computeDHash(img)
	if err != nil {
		return nil, err
	}
	feats.ColorHistogram = computeColorHistogram(img)
	feats.DominantColors = dominantColors(img, 5)

	// Re-open for EXIF (the image decoder may have consumed the reader)
	f.Seek(0, io.SeekStart)
	fillEXIF(f, feats)

	return feats, nil
}

// ExtractImageFeaturesFromBytes decodes an in-memory image buffer.
// Used during search when the user uploads a query file.
func ExtractImageFeaturesFromBytes(data []byte, _ string) (*ImageFeatures, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	bounds := img.Bounds()
	feats := &ImageFeatures{
		Width:  bounds.Dx(),
		Height: bounds.Dy(),
	}
	var e error
	feats.PHash, e = computePHash(img)
	if e != nil {
		return nil, e
	}
	feats.AHash, _ = computeAHash(img)
	feats.DHash, _ = computeDHash(img)
	feats.ColorHistogram = computeColorHistogram(img)
	feats.DominantColors = dominantColors(img, 5)
	return feats, nil
}

// ─── pHash ───────────────────────────────────────────────────────────────────

// computePHash computes the DCT-based perceptual hash (64-bit).
//
// Algorithm:
//  1. Resize to dctSize×dctSize with Lanczos3 (high-quality antialiasing)
//  2. Convert to grayscale
//  3. Apply 2D DCT-II
//  4. Extract the top-left 8×8 low-frequency coefficients (64 values)
//  5. Compute the mean, excluding the DC term at [0][0]
//  6. Each bit = 1 if coefficient > mean
func computePHash(img image.Image) (uint64, error) {
	small := imaging.Resize(img, dctSize, dctSize, imaging.Lanczos)
	gray := imaging.Grayscale(small)

	matrix := make([][]float64, dctSize)
	for y := 0; y < dctSize; y++ {
		matrix[y] = make([]float64, dctSize)
		for x := 0; x < dctSize; x++ {
			r, _, _, _ := gray.At(x, y).RGBA()
			matrix[y][x] = float64(r >> 8) // convert uint16 → uint8 range
		}
	}

	dct := DCT2D(matrix)

	// Collect top-left 8×8 coefficients
	const block = 8
	coeffs := make([]float64, block*block)
	for y := 0; y < block; y++ {
		for x := 0; x < block; x++ {
			coeffs[y*block+x] = dct[y][x]
		}
	}

	// Mean of all coefficients except DC (index 0)
	var sum float64
	for i := 1; i < len(coeffs); i++ {
		sum += coeffs[i]
	}
	mean := sum / float64(len(coeffs)-1)

	var hash uint64
	for i, v := range coeffs {
		if v > mean {
			hash |= 1 << uint(i)
		}
	}
	return hash, nil
}

// ─── aHash ───────────────────────────────────────────────────────────────────

// computeAHash computes the average hash (8×8 = 64 bits).
//
// Very fast, tolerates mild scaling and brightness changes.
// Less resistant to rotation/cropping than pHash.
func computeAHash(img image.Image) (uint64, error) {
	small := imaging.Resize(img, 8, 8, imaging.Lanczos)
	gray := imaging.Grayscale(small)

	pixels := make([]float64, 64)
	var sum float64
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			r, _, _, _ := gray.At(x, y).RGBA()
			v := float64(r >> 8)
			pixels[y*8+x] = v
			sum += v
		}
	}
	mean := sum / 64.0

	var hash uint64
	for i, v := range pixels {
		if v >= mean {
			hash |= 1 << uint(i)
		}
	}
	return hash, nil
}

// ─── dHash ───────────────────────────────────────────────────────────────────

// computeDHash computes the difference hash (9×8 → 64-bit).
//
// Compares adjacent horizontal pixel pairs. Fast and effective for detecting
// structural differences that don't show up in frequency domain.
func computeDHash(img image.Image) (uint64, error) {
	// 9 wide so each row has 8 adjacent pairs
	small := imaging.Resize(img, 9, 8, imaging.Lanczos)
	gray := imaging.Grayscale(small)

	var hash uint64
	var bit uint
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			l, _, _, _ := gray.At(x, y).RGBA()
			r, _, _, _ := gray.At(x+1, y).RGBA()
			if l > r {
				hash |= 1 << bit
			}
			bit++
		}
	}
	return hash, nil
}

// ─── Hamming ─────────────────────────────────────────────────────────────────

// HammingDistance returns the number of differing bits between a and b.
func HammingDistance(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}

// HammingSimilarity maps Hamming distance onto [0, 1].
func HammingSimilarity(a, b uint64) float64 {
	return 1.0 - float64(HammingDistance(a, b))/64.0
}

// ─── Color histogram ─────────────────────────────────────────────────────────

// computeColorHistogram builds a normalized 3D HSV histogram (histSize bins).
//
// Sampling at most 100×100 points for speed; the relative bin counts are
// stable even at low resolution.
func computeColorHistogram(img image.Image) []float32 {
	hist := make([]float32, histSize)
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	// Adaptive step: sample at most 10 000 pixels
	stepX := imax(1, w/100)
	stepY := imax(1, h/100)

	var total float32
	for y := bounds.Min.Y; y < bounds.Max.Y; y += stepY {
		for x := bounds.Min.X; x < bounds.Max.X; x += stepX {
			r32, g32, b32, _ := img.At(x, y).RGBA()
			hv, sv, vv := rgbToHSV(uint8(r32>>8), uint8(g32>>8), uint8(b32>>8))

			hi := imin(int(hv/360.0*float64(hBins)), hBins-1)
			si := imin(int(sv*float64(sBins)), sBins-1)
			vi := imin(int(vv*float64(vBins)), vBins-1)

			hist[hi*sBins*vBins+si*vBins+vi]++
			total++
		}
	}
	if total > 0 {
		for i := range hist {
			hist[i] /= total
		}
	}
	return hist
}

// HistogramIntersection returns a [0, 1] similarity between two normalized
// HSV histograms. Both histograms must have the same length.
func HistogramIntersection(h1, h2 []float32) float64 {
	if len(h1) != len(h2) {
		return 0
	}
	var s float64
	for i := range h1 {
		if h1[i] < h2[i] {
			s += float64(h1[i])
		} else {
			s += float64(h2[i])
		}
	}
	return s
}

// SerializeHistogram packs a []float32 slice into little-endian binary.
func SerializeHistogram(h []float32) []byte {
	buf := make([]byte, len(h)*4)
	for i, v := range h {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// DeserializeHistogram unpacks a little-endian binary blob into []float32.
func DeserializeHistogram(b []byte) []float32 {
	if len(b)%4 != 0 || len(b) == 0 {
		return nil
	}
	h := make([]float32, len(b)/4)
	for i := range h {
		h[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return h
}

// ─── Dominant colours ────────────────────────────────────────────────────────

// dominantColors extracts the top-n most frequent quantized colors.
// Quantises to 4 bits per channel (4096-color palette) for stability.
func dominantColors(img image.Image, n int) []string {
	counts := make(map[uint32]int)
	bounds := img.Bounds()
	stepX := imax(1, bounds.Dx()/50)
	stepY := imax(1, bounds.Dy()/50)

	for y := bounds.Min.Y; y < bounds.Max.Y; y += stepY {
		for x := bounds.Min.X; x < bounds.Max.X; x += stepX {
			r32, g32, b32, _ := img.At(x, y).RGBA()
			r := uint32(r32>>8) >> 4
			g := uint32(g32>>8) >> 4
			b := uint32(b32>>8) >> 4
			counts[(r<<8)|(g<<4)|b]++
		}
	}

	type kv struct {
		k uint32
		v int
	}
	pairs := make([]kv, 0, len(counts))
	for k, v := range counts {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].v > pairs[j].v })

	colors := make([]string, 0, n)
	for i := 0; i < n && i < len(pairs); i++ {
		k := pairs[i].k
		r := uint8((k>>8)&0xF) * 17
		g := uint8((k>>4)&0xF) * 17
		b := uint8(k&0xF) * 17
		colors = append(colors, fmt.Sprintf("#%02x%02x%02x", r, g, b))
	}
	return colors
}

// ─── SHA-256 ─────────────────────────────────────────────────────────────────

// FileSHA256 computes the SHA-256 of the file at path (hex string).
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// ─── EXIF ────────────────────────────────────────────────────────────────────

func fillEXIF(r io.Reader, f *ImageFeatures) {
	x, err := exif.Decode(r)
	if err != nil {
		return
	}
	if t, err := x.Get(exif.Make); err == nil {
		f.ExifMake, _ = t.StringVal()
	}
	if t, err := x.Get(exif.Model); err == nil {
		f.ExifModel, _ = t.StringVal()
	}
	if t, err := x.DateTime(); err == nil {
		f.ExifDatetime = t.Format("2006-01-02T15:04:05")
	}
	if lat, lng, err := x.LatLong(); err == nil {
		f.ExifGPSLat = lat
		f.ExifGPSLng = lng
	}
}

// ─── Thumbnail ───────────────────────────────────────────────────────────────

// GenerateThumbnail creates a square thumbnail at maxSide×maxSide, saved as JPEG.
func GenerateThumbnail(srcPath, dstPath string, maxSide int) error {
	img, err := imaging.Open(srcPath, imaging.AutoOrientation(true))
	if err != nil {
		return err
	}
	thumb := imaging.Fit(img, maxSide, maxSide, imaging.Lanczos)
	return imaging.Save(thumb, dstPath)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func rgbToHSV(r, g, b uint8) (h, s, v float64) {
	rf := float64(r) / 255.0
	gf := float64(g) / 255.0
	bf := float64(b) / 255.0

	cmax := math.Max(math.Max(rf, gf), bf)
	cmin := math.Min(math.Min(rf, gf), bf)
	delta := cmax - cmin

	v = cmax
	if cmax == 0 {
		return
	}
	s = delta / cmax
	if delta == 0 {
		return
	}
	switch cmax {
	case rf:
		h = 60 * math.Mod((gf-bf)/delta, 6)
	case gf:
		h = 60 * ((bf-rf)/delta + 2)
	default:
		h = 60 * ((rf-gf)/delta + 4)
	}
	if h < 0 {
		h += 360
	}
	return
}

func imax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func imin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
