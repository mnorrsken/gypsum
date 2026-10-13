package wiki

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/image/draw"
)

// Scan images are sent to the model at up to 2576 px on the long edge and
// 3.75 megapixels, the high-resolution vision limit of current Claude models.
// Within that limit the model's coordinates map 1:1 to the image pixels, so
// figures are cropped from the same image that was sent.
const (
	scanMaxPx     = 2576
	scanMaxPixels = 3_750_000
)

// Figure styles the model can pick for a cropped figure.
const (
	figureBW      = "bw"      // black ink line art → 2 colors, PNG
	figureGray    = "gray"    // drawings with a few shades → 4 gray levels, PNG
	figurePalette = "palette" // flat-color drawings → posterized to the named colors, PNG
	figurePhoto   = "photo"   // photographs, gradients → full-color JPEG

	grayLevels = 4 // shades kept by the gray style
)

// scanPhoto is a decoded photo and the upright, scaled copy sent to the
// model. Figures are cut from the full-resolution original.
type scanPhoto struct {
	raw         image.Image // full resolution, as stored (not yet turned upright)
	orientation int         // EXIF orientation of raw (1 = upright)
	sent        *image.RGBA // upright copy within the model's limits
	jpeg        []byte      // sent, encoded for the request
}

// prepareScanImage decodes a photo and makes the upright copy for the model,
// scaled to its image limits.
func prepareScanImage(data []byte) (*scanPhoto, error) {
	img, err := decodeImage(data)
	if err != nil {
		return nil, err
	}
	p := &scanPhoto{raw: img, orientation: 1}
	if bytes.HasPrefix(data, []byte{0xFF, 0xD8}) {
		p.orientation = jpegOrientation(data)
	}
	b := img.Bounds()
	maxPx := scanMaxPx
	if w, h := b.Dx(), b.Dy(); w*h > scanMaxPixels {
		// Shrink the long edge further so the area fits too.
		scale := float64(scanMaxPixels) / float64(w*h)
		long := float64(max(w, h)) * math.Sqrt(scale)
		maxPx = min(maxPx, int(long))
	}
	p.sent = orientImage(scaleImage(img, maxPx), p.orientation)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, p.sent, &jpeg.Options{Quality: 90}); err != nil {
		return nil, fmt.Errorf("encoding scan image: %w", err)
	}
	p.jpeg = buf.Bytes()
	return p, nil
}

// cropFigure cuts box, given in the pixels of the sent image, out of the
// full-resolution photo and returns it upright. It returns nil when the box
// is empty or too small to be a figure.
func (p *scanPhoto) cropFigure(box image.Rectangle) *image.RGBA {
	box = box.Canon().Intersect(p.sent.Bounds())
	if box.Dx() < 16 || box.Dy() < 16 {
		return nil
	}
	rb := p.raw.Bounds()
	rawW, rawH := rb.Dx(), rb.Dy()
	upW, upH := rawW, rawH // size of the full photo once upright
	if p.orientation >= 5 {
		upW, upH = rawH, rawW
	}
	sx := float64(upW) / float64(p.sent.Bounds().Dx())
	sy := float64(upH) / float64(p.sent.Bounds().Dy())
	full := image.Rect(
		int(math.Floor(float64(box.Min.X)*sx)), int(math.Floor(float64(box.Min.Y)*sy)),
		int(math.Ceil(float64(box.Max.X)*sx)), int(math.Ceil(float64(box.Max.Y)*sy)),
	).Intersect(image.Rect(0, 0, upW, upH))

	r := uprightToRaw(full, p.orientation, rawW, rawH).Add(rb.Min)
	crop := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(crop, crop.Bounds(), p.raw, r.Min, draw.Src)
	return orientImage(crop, p.orientation)
}

// uprightToRaw maps a rectangle in the upright photo back to the stored
// (raw) photo of size w×h with the given EXIF orientation.
func uprightToRaw(r image.Rectangle, orientation, w, h int) image.Rectangle {
	pt := func(x, y int) image.Point {
		switch orientation {
		case 2:
			return image.Pt(w-x, y)
		case 3:
			return image.Pt(w-x, h-y)
		case 4:
			return image.Pt(x, h-y)
		case 5:
			return image.Pt(y, x)
		case 6:
			return image.Pt(y, h-x)
		case 7:
			return image.Pt(w-y, h-x)
		case 8:
			return image.Pt(w-y, x)
		}
		return image.Pt(x, y)
	}
	a, b := pt(r.Min.X, r.Min.Y), pt(r.Max.X, r.Max.Y)
	return image.Rect(a.X, a.Y, b.X, b.Y) // Rect sorts the corners
}

// renderFigure converts a cropped figure to the requested style and encodes
// it. It returns the encoded bytes and the file extension.
func renderFigure(img *image.RGBA, style string, colors []string) ([]byte, string, error) {
	var buf bytes.Buffer
	switch style {
	case figureBW:
		gray := stretchGray(toGray(img))
		t := otsuThreshold(gray)
		pal := image.NewPaletted(gray.Bounds(), color.Palette{color.Black, color.White})
		for i, v := range gray.Pix {
			if v > t {
				pal.Pix[i] = 1
			}
		}
		if err := png.Encode(&buf, pal); err != nil {
			return nil, "", fmt.Errorf("encoding figure: %w", err)
		}
		return buf.Bytes(), ".png", nil
	case figurePalette:
		palette := parsePalette(colors)
		if len(palette) < 2 {
			return renderFigure(img, figureGray, nil)
		}
		pal := image.NewPaletted(img.Bounds(), palette)
		draw.Draw(pal, pal.Bounds(), img, image.Point{}, draw.Src) // nearest palette color, no dithering
		if err := png.Encode(&buf, pal); err != nil {
			return nil, "", fmt.Errorf("encoding figure: %w", err)
		}
		return buf.Bytes(), ".png", nil
	case figureGray, "grayscale": // "grayscale" is the style's earlier name
		gray := stretchGray(toGray(img))
		levels := grayClusters(gray, grayLevels)
		pal := make(color.Palette, len(levels))
		for i, l := range levels {
			pal[i] = color.Gray{Y: l}
		}
		out := image.NewPaletted(gray.Bounds(), pal)
		for i, v := range gray.Pix {
			out.Pix[i] = uint8(nearestLevel(levels, v))
		}
		if err := png.Encode(&buf, out); err != nil {
			return nil, "", fmt.Errorf("encoding figure: %w", err)
		}
		return buf.Bytes(), ".png", nil
	default:
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return nil, "", fmt.Errorf("encoding figure: %w", err)
		}
		return buf.Bytes(), ".jpg", nil
	}
}

func toGray(img *image.RGBA) *image.Gray {
	gray := image.NewGray(img.Bounds())
	draw.Draw(gray, gray.Bounds(), img, img.Bounds().Min, draw.Src)
	return gray
}

// stretchGray spreads the 2nd–98th percentile of brightness over the full
// range, which evens out dim phone photos of paper.
func stretchGray(g *image.Gray) *image.Gray {
	if len(g.Pix) == 0 {
		return g
	}
	sorted := append([]uint8(nil), g.Pix...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	lo := int(sorted[len(sorted)*2/100])
	hi := int(sorted[len(sorted)*98/100])
	if hi-lo < 16 {
		return g
	}
	out := image.NewGray(g.Bounds())
	for i, v := range g.Pix {
		n := (int(v) - lo) * 255 / (hi - lo)
		out.Pix[i] = uint8(min(255, max(0, n)))
	}
	return out
}

// otsuThreshold picks the brightness that best splits ink from paper.
func otsuThreshold(g *image.Gray) uint8 {
	var hist [256]int
	for _, v := range g.Pix {
		hist[v]++
	}
	total := len(g.Pix)
	sum := 0
	for i, c := range hist {
		sum += i * c
	}
	var sumB, wB int
	best, threshold := 0.0, 127
	for t := 0; t < 256; t++ {
		wB += hist[t]
		if wB == 0 {
			continue
		}
		wF := total - wB
		if wF == 0 {
			break
		}
		sumB += t * hist[t]
		mB := float64(sumB) / float64(wB)
		mF := float64(sum-sumB) / float64(wF)
		between := float64(wB) * float64(wF) * (mB - mF) * (mB - mF)
		if between > best {
			best, threshold = between, t
		}
	}
	return uint8(threshold)
}

// parsePalette reads up to 8 "#rrggbb" colors, skipping invalid ones.
func parsePalette(colors []string) color.Palette {
	var pal color.Palette
	for _, c := range colors {
		c = strings.TrimPrefix(strings.TrimSpace(c), "#")
		if len(c) != 6 {
			continue
		}
		v, err := strconv.ParseUint(c, 16, 32)
		if err != nil {
			continue
		}
		pal = append(pal, color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255})
		if len(pal) == 8 {
			break
		}
	}
	return pal
}

// grayClusters picks k gray levels that best represent the image's tones
// (k-means on the brightness histogram, starting from evenly spread levels).
func grayClusters(g *image.Gray, k int) []uint8 {
	var hist [256]float64
	for _, v := range g.Pix {
		hist[v]++
	}
	centers := make([]float64, k)
	for i := range centers {
		centers[i] = float64(i) * 255 / float64(k-1)
	}
	for iter := 0; iter < 20; iter++ {
		sum := make([]float64, k)
		cnt := make([]float64, k)
		for v, n := range hist {
			if n == 0 {
				continue
			}
			best := 0
			for c := range centers {
				if math.Abs(float64(v)-centers[c]) < math.Abs(float64(v)-centers[best]) {
					best = c
				}
			}
			sum[best] += float64(v) * n
			cnt[best] += n
		}
		for c := range centers {
			if cnt[c] > 0 {
				centers[c] = sum[c] / cnt[c]
			}
		}
	}
	out := make([]uint8, k)
	for i, c := range centers {
		out[i] = uint8(math.Round(c))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// nearestLevel returns the index of the level closest to v.
func nearestLevel(levels []uint8, v uint8) int {
	best := 0
	for i, l := range levels {
		if absDiff(l, v) < absDiff(levels[best], v) {
			best = i
		}
	}
	return best
}

func absDiff(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}
