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
		flat, onPaper := flattenLighting(img)
		gray := stretchGray(toGray(flat))
		t := otsuThreshold(gray)
		pal := image.NewPaletted(gray.Bounds(), color.Palette{color.Black, color.White})
		w := gray.Rect.Dx()
		for i, v := range gray.Pix {
			// On paper, colored marker counts as ink however light it is.
			if v > t && !(onPaper && isInk(flat.RGBAAt(i%w, i/w))) {
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
		pal := posterize(img, palette)
		if err := png.Encode(&buf, pal); err != nil {
			return nil, "", fmt.Errorf("encoding figure: %w", err)
		}
		return buf.Bytes(), ".png", nil
	case figureGray, "grayscale": // "grayscale" is the style's earlier name
		flat, _ := flattenLighting(img)
		gray := stretchGray(toGray(flat))
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

// flattenLighting evens out the light on a drawing on paper or a whiteboard.
// It estimates the paper color in a grid of blocks (the brightest tenth of
// each block) and divides it out, so the paper comes out white everywhere,
// also in shadows and under a color cast, and the ink keeps its color.
// Blocks covered by ink take the paper color of their neighbours. An image
// that is not mostly light, gray paper (pixel art on a dark screen, colored
// paper) is returned unchanged, with false.
func flattenLighting(img *image.RGBA) (*image.RGBA, bool) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return img, false
	}
	bs := max(16, max(w, h)/24)
	gw, gh := (w+bs-1)/bs, (h+bs-1)/bs
	paper := make([][3]float64, gw*gh)
	lum := make([]float64, gw*gh)
	for gy := 0; gy < gh; gy++ {
		for gx := 0; gx < gw; gx++ {
			x0, y0 := b.Min.X+gx*bs, b.Min.Y+gy*bs
			x1, y1 := min(x0+bs, b.Max.X), min(y0+bs, b.Max.Y)
			var hist [256]int
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					c := img.RGBAAt(x, y)
					hist[luma(c)]++
				}
			}
			// The brightness above which the brightest tenth lies.
			cut, n := 255, 0
			for need := (x1 - x0) * (y1 - y0) / 10; cut > 0 && n+hist[cut] <= need; cut-- {
				n += hist[cut]
			}
			var sum [3]float64
			cnt := 0.0
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					if c := img.RGBAAt(x, y); int(luma(c)) >= cut {
						sum[0] += float64(c.R)
						sum[1] += float64(c.G)
						sum[2] += float64(c.B)
						cnt++
					}
				}
			}
			i := gy*gw + gx
			paper[i] = [3]float64{sum[0] / cnt, sum[1] / cnt, sum[2] / cnt}
			lum[i] = 0.299*paper[i][0] + 0.587*paper[i][1] + 0.114*paper[i][2]
		}
	}
	sorted := append([]float64(nil), lum...)
	sort.Float64s(sorted)
	median := sorted[len(sorted)/2]
	// A block shows paper when it is light, close to the typical paper
	// brightness and nearly gray.
	valid := make([]bool, len(paper))
	nValid := 0
	for i, p := range paper {
		chroma := max(p[0], p[1], p[2]) - min(p[0], p[1], p[2])
		if lum[i] >= 100 && lum[i] >= 0.75*median && chroma < 60 {
			valid[i] = true
			nValid++
		}
	}
	if nValid*2 < len(paper) {
		return img, false
	}
	// Fill ink-covered blocks from their neighbours, ring by ring.
	for nValid < len(paper) {
		next := append([]bool(nil), valid...)
		for gy := 0; gy < gh; gy++ {
			for gx := 0; gx < gw; gx++ {
				i := gy*gw + gx
				if valid[i] {
					continue
				}
				var sum [3]float64
				cnt := 0.0
				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						nx, ny := gx+dx, gy+dy
						if nx >= 0 && ny >= 0 && nx < gw && ny < gh && valid[ny*gw+nx] {
							j := ny*gw + nx
							sum[0] += paper[j][0]
							sum[1] += paper[j][1]
							sum[2] += paper[j][2]
							cnt++
						}
					}
				}
				if cnt > 0 {
					paper[i] = [3]float64{sum[0] / cnt, sum[1] / cnt, sum[2] / cnt}
					next[i] = true
					nValid++
				}
			}
		}
		valid = next
	}
	// Divide each pixel by the paper color, blended between block centres.
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		fy := min(max((float64(y)+0.5)/float64(bs)-0.5, 0), float64(gh-1))
		y0 := int(fy)
		y1, ty := min(y0+1, gh-1), fy-float64(y0)
		for x := 0; x < w; x++ {
			fx := min(max((float64(x)+0.5)/float64(bs)-0.5, 0), float64(gw-1))
			x0 := int(fx)
			x1, tx := min(x0+1, gw-1), fx-float64(x0)
			c := img.RGBAAt(b.Min.X+x, b.Min.Y+y)
			in := [3]float64{float64(c.R), float64(c.G), float64(c.B)}
			var px [3]uint8
			for k := range 3 {
				top := paper[y0*gw+x0][k]*(1-tx) + paper[y0*gw+x1][k]*tx
				bot := paper[y1*gw+x0][k]*(1-tx) + paper[y1*gw+x1][k]*tx
				bg := max(top*(1-ty)+bot*ty, 1)
				px[k] = uint8(min(255, math.Round(in[k]*255/bg)))
			}
			out.SetRGBA(x, y, color.RGBA{px[0], px[1], px[2], 255})
		}
	}
	return out, true
}

// isInk tells ink from paper on a page whose light was flattened (paper is
// near white): dark pixels and clearly colored ones are ink.
func isInk(c color.RGBA) bool {
	chroma := max(c.R, c.G, c.B) - min(c.R, c.G, c.B)
	return luma(c) < 190 || chroma >= 24
}

// posterize maps each pixel to the nearest palette color, compared in Lab
// so hue counts more than in RGB, without dithering. On paper, the paper
// becomes the palette's lightest color, and ink the color whose mix with
// white comes closest: pale or thin marker strokes are ink thinned by the
// white paper, so they keep their hue instead of turning black or white.
func posterize(img *image.RGBA, palette color.Palette) *image.Paletted {
	flat, onPaper := flattenLighting(img)
	labs := make([][3]float64, len(palette))
	paper := 0
	for i, c := range palette {
		r, g, b, _ := c.RGBA()
		labs[i] = toLab(uint8(r>>8), uint8(g>>8), uint8(b>>8))
		if labs[i][0] > labs[paper][0] {
			paper = i
		}
	}
	nearest := func(lab [3]float64) int16 {
		best, bestD := 0, math.Inf(1)
		for i, p := range labs {
			if d := (lab[0]-p[0])*(lab[0]-p[0]) + (lab[1]-p[1])*(lab[1]-p[1]) + (lab[2]-p[2])*(lab[2]-p[2]); d < bestD {
				best, bestD = i, d
			}
		}
		return int16(best)
	}
	// nearestInk measures, in RGB, how far c is from the line between white
	// and each ink color.
	nearestInk := func(c color.RGBA) int16 {
		best, bestD := 0, math.Inf(1)
		off := [3]float64{float64(c.R) - 255, float64(c.G) - 255, float64(c.B) - 255}
		for i, pc := range palette {
			if i == paper && len(palette) > 2 {
				continue
			}
			r, g, b, _ := pc.RGBA()
			dir := [3]float64{float64(r>>8) - 255, float64(g>>8) - 255, float64(b>>8) - 255}
			t := 0.0
			if dd := dir[0]*dir[0] + dir[1]*dir[1] + dir[2]*dir[2]; dd > 0 {
				t = min(1, max(0, (off[0]*dir[0]+off[1]*dir[1]+off[2]*dir[2])/dd))
			}
			d := 0.0
			for k := range 3 {
				d += (off[k] - t*dir[k]) * (off[k] - t*dir[k])
			}
			if d < bestD {
				best, bestD = i, d
			}
		}
		return int16(best)
	}
	// Colors are looked up at 5 bits per channel; plenty for a few colors.
	var cache [1 << 15]int16
	for i := range cache {
		cache[i] = -1
	}
	b := flat.Bounds()
	out := image.NewPaletted(image.Rect(0, 0, b.Dx(), b.Dy()), palette)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := flat.RGBAAt(x, y)
			key := int(c.R>>3)<<10 | int(c.G>>3)<<5 | int(c.B>>3)
			if cache[key] < 0 {
				switch {
				case !onPaper:
					cache[key] = nearest(toLab(c.R, c.G, c.B))
				case !isInk(c):
					cache[key] = int16(paper)
				default:
					cache[key] = nearestInk(c)
				}
			}
			out.Pix[(y-b.Min.Y)*out.Stride+(x-b.Min.X)] = uint8(cache[key])
		}
	}
	despeckle(out)
	return out
}

// despeckle gives each pixel that matches none of its 8 neighbours the color
// most of them have, which removes the stray colored dots that color noise
// and JPEG fringes leave along strokes.
func despeckle(img *image.Paletted) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	src := append([]uint8(nil), img.Pix...)
	var count [256]int
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			c := src[y*img.Stride+x]
			alone := true
			for dy := -1; dy <= 1 && alone; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if (dx != 0 || dy != 0) && src[(y+dy)*img.Stride+x+dx] == c {
						alone = false
						break
					}
				}
			}
			if !alone {
				continue
			}
			best := src[y*img.Stride+x-1]
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if dx == 0 && dy == 0 {
						continue
					}
					n := src[(y+dy)*img.Stride+x+dx]
					if count[n]++; count[n] > count[best] {
						best = n
					}
				}
			}
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					count[src[(y+dy)*img.Stride+x+dx]] = 0
				}
			}
			img.Pix[y*img.Stride+x] = best
		}
	}
}

// toLab converts an sRGB color to CIE L*a*b* (D65 white).
func toLab(r, g, b uint8) [3]float64 {
	lin := func(v uint8) float64 {
		c := float64(v) / 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	rl, gl, bl := lin(r), lin(g), lin(b)
	x := (0.4124*rl + 0.3576*gl + 0.1805*bl) / 0.95047
	y := 0.2126*rl + 0.7152*gl + 0.0722*bl
	z := (0.0193*rl + 0.1192*gl + 0.9505*bl) / 1.08883
	f := func(t float64) float64 {
		if t > 216.0/24389 {
			return math.Cbrt(t)
		}
		return (24389.0/27*t + 16) / 116
	}
	fx, fy, fz := f(x), f(y), f(z)
	return [3]float64{116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)}
}


func luma(c color.RGBA) uint8 {
	return uint8((299*int(c.R) + 587*int(c.G) + 114*int(c.B)) / 1000)
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
