package wiki

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"sort"
)

// cornerMaxPx is the long edge of the image sent to find the document's
// corners. Every current Claude model takes this size without resizing, so the
// returned coordinates map back exactly; a page outline needs no more detail.
const cornerMaxPx = 1568

// straightenedMaxPixels caps the size of a straightened photo.
const straightenedMaxPixels = 16_000_000

// Point is a position in image pixels.
type Point struct {
	X, Y float64
}

// orderCorners returns four points as top-left, top-right, bottom-right,
// bottom-left, whatever order they came in.
func orderCorners(pts [4]Point) [4]Point {
	var cx, cy float64
	for _, p := range pts {
		cx += p.X / 4
		cy += p.Y / 4
	}
	sorted := pts
	sort.Slice(sorted[:], func(i, j int) bool {
		return math.Atan2(sorted[i].Y-cy, sorted[i].X-cx) < math.Atan2(sorted[j].Y-cy, sorted[j].X-cx)
	})
	// Angles run clockwise from the left (y points down); start at the
	// corner with the smallest x+y, which is the top-left.
	start := 0
	for i, p := range sorted {
		if p.X+p.Y < sorted[start].X+sorted[start].Y {
			start = i
		}
	}
	var out [4]Point
	for i := range out {
		out[i] = sorted[(start+i)%4]
	}
	return out
}

// readingOrder keeps corners given in reading order (the page's own top-left
// first, then clockwise, so a turned page comes out upright) when they form a
// clockwise outline, and otherwise falls back to the corner nearest the
// photo's top-left.
func readingOrder(pts [4]Point) [4]Point {
	area := 0.0
	for i := 0; i < 4; i++ {
		a, b := pts[i], pts[(i+1)%4]
		area += a.X*b.Y - b.X*a.Y
	}
	ordered := orderCorners(pts)
	if area <= 0 { // counter-clockwise or crossed (y points down)
		return ordered
	}
	for _, p := range pts {
		found := false
		for _, o := range ordered {
			if p == o {
				found = true
			}
		}
		if !found {
			return ordered
		}
	}
	// Same four points, clockwise: keep the given start corner.
	return pts
}

// quadFromOutline rebuilds the page's four corners from the outline of its
// visible part. Models place a corner they cannot see (common when a page is
// photographed at a low angle) on the photo's border, partway along an edge,
// which bends the page into a triangle; but they trace the visible outline,
// including where the page's edges cross the border, well. The outline's
// edges that do not run along the border are the page's sides; extended,
// they meet at the true corners, also outside the photo. It reports false
// unless exactly four sides are found.
func quadFromOutline(outline []Point, w, h float64) ([4]Point, bool) {
	var quad [4]Point
	tol := 0.01 * math.Max(w, h)
	// Drop points right next to the previous one: models sometimes mark a
	// corner twice, and the tiny edge between would count as a side.
	var pts []Point
	for _, p := range outline {
		if len(pts) == 0 || math.Hypot(p.X-pts[len(pts)-1].X, p.Y-pts[len(pts)-1].Y) > 2*tol {
			pts = append(pts, p)
		}
	}
	for len(pts) > 1 && math.Hypot(pts[0].X-pts[len(pts)-1].X, pts[0].Y-pts[len(pts)-1].Y) <= 2*tol {
		pts = pts[:len(pts)-1]
	}
	outline = pts
	n := len(outline)
	if n < 4 {
		return quad, false
	}
	side := func(p Point) int { // which border p lies on, as a bit set
		s := 0
		if p.X <= tol {
			s |= 1
		}
		if p.X >= w-tol {
			s |= 2
		}
		if p.Y <= tol {
			s |= 4
		}
		if p.Y >= h-tol {
			s |= 8
		}
		return s
	}
	// The sides, merging neighbouring edges that point the same way (a
	// model may add points along a straight edge).
	var sides [][2]Point
	for i := range outline {
		a, b := outline[i], outline[(i+1)%n]
		if side(a)&side(b) != 0 || a == b {
			continue // along the border
		}
		if k := len(sides); k > 0 && sides[k-1][1] == a && angleBetween(sides[k-1], [2]Point{a, b}) < 10 {
			sides[k-1][1] = b
			continue
		}
		sides = append(sides, [2]Point{a, b})
	}
	if k := len(sides); k > 4 && sides[k-1][1] == sides[0][0] && angleBetween(sides[k-1], sides[0]) < 10 {
		sides[0][0] = sides[k-1][0]
		sides = sides[:k-1]
	}
	if len(sides) != 4 {
		return quad, false
	}
	limit := 3 * math.Hypot(w, h)
	for i := range quad {
		p, ok := lineIntersection(sides[(i+3)%4], sides[i])
		if !ok || math.Hypot(p.X-w/2, p.Y-h/2) > limit {
			return quad, false
		}
		quad[i] = p
	}
	return quad, true
}

// angleBetween returns the angle in degrees between the directions of two
// segments.
func angleBetween(a, b [2]Point) float64 {
	d := math.Abs(math.Atan2(a[1].Y-a[0].Y, a[1].X-a[0].X)-math.Atan2(b[1].Y-b[0].Y, b[1].X-b[0].X)) * 180 / math.Pi
	if d > 180 {
		d = 360 - d
	}
	return d
}

// lineIntersection returns where the lines through a and b cross.
func lineIntersection(a, b [2]Point) (Point, bool) {
	d1 := Point{a[1].X - a[0].X, a[1].Y - a[0].Y}
	d2 := Point{b[1].X - b[0].X, b[1].Y - b[0].Y}
	den := d1.X*d2.Y - d1.Y*d2.X
	// Lines within about 3° of parallel give no usable corner.
	if math.Abs(den) < 0.05*math.Hypot(d1.X, d1.Y)*math.Hypot(d2.X, d2.Y) {
		return Point{}, false
	}
	t := ((b[0].X-a[0].X)*d2.Y - (b[0].Y-a[0].Y)*d2.X) / den
	return Point{a[0].X + t*d1.X, a[0].Y + t*d1.Y}, true
}

// alignCorners turns quad, in outline order, into the reading order of
// guess (the model's corners): it picks the rotation and direction that put
// each corner nearest its guess.
func alignCorners(quad, guess [4]Point) [4]Point {
	best, bestD := quad, math.Inf(1)
	for _, rev := range []bool{false, true} {
		for start := 0; start < 4; start++ {
			var q [4]Point
			d := 0.0
			for i := range q {
				j := (start + i) % 4
				if rev {
					j = (start - i + 4) % 4
				}
				q[i] = quad[j]
				d += math.Hypot(q[i].X-guess[i].X, q[i].Y-guess[i].Y)
			}
			if d < bestD {
				best, bestD = q, d
			}
		}
	}
	return best
}

// usableQuad reports whether q (ordered TL, TR, BR, BL) is a convex outline
// worth straightening in a w×h image: at least 2% of the photo (a receipt on
// a table can be small), with no nearly flat corner, and not just the
// photo's own frame (an already flat, cropped page needs no correction).
func usableQuad(q [4]Point, w, h float64) bool {
	// Convex: every turn along the outline goes the same way. And no
	// corner is nearly flat: that is a triangle with a corner placed along an
	// edge, not a page.
	sign := 0.0
	for i := 0; i < 4; i++ {
		a, b, c := q[i], q[(i+1)%4], q[(i+2)%4]
		cross := (b.X-a.X)*(c.Y-b.Y) - (b.Y-a.Y)*(c.X-b.X)
		if cross == 0 || (sign != 0 && math.Signbit(cross) != math.Signbit(sign)) {
			return false
		}
		sign = cross
		if angleBetween([2]Point{a, b}, [2]Point{b, c}) < 12 {
			return false
		}
	}
	area := 0.0
	for i := 0; i < 4; i++ {
		a, b := q[i], q[(i+1)%4]
		area += a.X*b.Y - b.X*a.Y
	}
	if math.Abs(area)/2 < 0.02*w*h {
		return false
	}
	frame := [4]Point{{0, 0}, {w, 0}, {w, h}, {0, h}}
	near := 0.02 * math.Hypot(w, h)
	for i := range q {
		if math.Hypot(q[i].X-frame[i].X, q[i].Y-frame[i].Y) > near {
			return true
		}
	}
	return false
}

// homography returns the 3×3 matrix (row-major, h[8] = 1) that maps the four
// src points onto the four dst points.
func homography(src, dst [4]Point) ([9]float64, error) {
	var a [8][9]float64
	for i := 0; i < 4; i++ {
		x, y, u, v := src[i].X, src[i].Y, dst[i].X, dst[i].Y
		a[2*i] = [9]float64{x, y, 1, 0, 0, 0, -u * x, -u * y, u}
		a[2*i+1] = [9]float64{0, 0, 0, x, y, 1, -v * x, -v * y, v}
	}
	// Gaussian elimination with partial pivoting.
	for col := 0; col < 8; col++ {
		pivot := col
		for r := col + 1; r < 8; r++ {
			if math.Abs(a[r][col]) > math.Abs(a[pivot][col]) {
				pivot = r
			}
		}
		if math.Abs(a[pivot][col]) < 1e-12 {
			return [9]float64{}, fmt.Errorf("corners are degenerate")
		}
		a[col], a[pivot] = a[pivot], a[col]
		for r := 0; r < 8; r++ {
			if r == col {
				continue
			}
			f := a[r][col] / a[col][col]
			for c := col; c < 9; c++ {
				a[r][c] -= f * a[col][c]
			}
		}
	}
	var h [9]float64
	for i := 0; i < 8; i++ {
		h[i] = a[i][8] / a[i][i]
	}
	h[8] = 1
	return h, nil
}

func applyHomography(h [9]float64, p Point) Point {
	w := h[6]*p.X + h[7]*p.Y + h[8]
	return Point{(h[0]*p.X + h[1]*p.Y + h[2]) / w, (h[3]*p.X + h[4]*p.Y + h[5]) / w}
}

// uprightToRawPoint maps a point in the upright photo back to the stored
// (raw) photo of size w×h with the given EXIF orientation.
func uprightToRawPoint(p Point, orientation int, w, h float64) Point {
	switch orientation {
	case 2:
		return Point{w - p.X, p.Y}
	case 3:
		return Point{w - p.X, h - p.Y}
	case 4:
		return Point{p.X, h - p.Y}
	case 5:
		return Point{p.Y, p.X}
	case 6:
		return Point{p.Y, h - p.X}
	case 7:
		return Point{w - p.Y, h - p.X}
	case 8:
		return Point{w - p.Y, p.X}
	}
	return p
}

// straighten cuts the document outlined by quad (TL, TR, BR, BL in pixels of
// the sent image) out of the full-resolution photo, corrects the perspective,
// and returns it as a new upright photo ready to send.
func (p *scanPhoto) straighten(quad [4]Point) (*scanPhoto, error) {
	rb := p.raw.Bounds()
	rawW, rawH := float64(rb.Dx()), float64(rb.Dy())
	upW, upH := rawW, rawH
	if p.orientation >= 5 {
		upW, upH = rawH, rawW
	}
	sx := upW / float64(p.sent.Bounds().Dx())
	sy := upH / float64(p.sent.Bounds().Dy())
	var full [4]Point
	for i, c := range quad {
		full[i] = Point{c.X * sx, c.Y * sy}
	}

	// Output size: the edge lengths give the scale; the page's real
	// proportions come from the perspective when it can be measured.
	edgeW := math.Max(math.Hypot(full[1].X-full[0].X, full[1].Y-full[0].Y), math.Hypot(full[2].X-full[3].X, full[2].Y-full[3].Y))
	edgeH := math.Max(math.Hypot(full[3].X-full[0].X, full[3].Y-full[0].Y), math.Hypot(full[2].X-full[1].X, full[2].Y-full[1].Y))
	outW, outH := edgeW, edgeH
	if aspect, ok := rectangleAspect(full, upW/2, upH/2); ok {
		outW = edgeH * aspect
		if outW < edgeW {
			outW, outH = edgeW, edgeW/aspect
		}
	}
	if outW*outH > straightenedMaxPixels {
		f := math.Sqrt(straightenedMaxPixels / (outW * outH))
		outW, outH = outW*f, outH*f
	}
	w, h := int(math.Round(outW)), int(math.Round(outH))
	if w < 16 || h < 16 {
		return nil, fmt.Errorf("document outline is too small")
	}

	// Map each output pixel back into the upright photo, then the raw one.
	rect := [4]Point{{0, 0}, {float64(w), 0}, {float64(w), float64(h)}, {0, float64(h)}}
	back, err := homography(rect, full)
	if err != nil {
		return nil, err
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			up := applyHomography(back, Point{float64(x) + 0.5, float64(y) + 0.5})
			if up.X < 0 || up.Y < 0 || up.X > upW || up.Y > upH {
				// Part of the page that runs off the photo.
				out.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
				continue
			}
			src := uprightToRawPoint(up, p.orientation, rawW, rawH)
			out.SetRGBA(x, y, bilinear(p.raw, src.X+float64(rb.Min.X)-0.5, src.Y+float64(rb.Min.Y)-0.5))
		}
	}

	flat := &scanPhoto{raw: out, orientation: 1}
	flat.sent = scaleImage(out, sentMaxPx(w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, flat.sent, &jpeg.Options{Quality: 90}); err != nil {
		return nil, fmt.Errorf("encoding straightened image: %w", err)
	}
	flat.jpeg = buf.Bytes()
	return flat, nil
}

// bilinear samples img at a fractional position, clamped to its edges.
func bilinear(img image.Image, x, y float64) color.RGBA {
	b := img.Bounds()
	x = math.Max(float64(b.Min.X), math.Min(x, float64(b.Max.X-1)))
	y = math.Max(float64(b.Min.Y), math.Min(y, float64(b.Max.Y-1)))
	x0, y0 := int(x), int(y)
	x1, y1 := min(x0+1, b.Max.X-1), min(y0+1, b.Max.Y-1)
	fx, fy := x-float64(x0), y-float64(y0)
	c00 := color.RGBAModel.Convert(img.At(x0, y0)).(color.RGBA)
	c10 := color.RGBAModel.Convert(img.At(x1, y0)).(color.RGBA)
	c01 := color.RGBAModel.Convert(img.At(x0, y1)).(color.RGBA)
	c11 := color.RGBAModel.Convert(img.At(x1, y1)).(color.RGBA)
	mix := func(a, b, c, d uint8) uint8 {
		top := float64(a)*(1-fx) + float64(b)*fx
		bot := float64(c)*(1-fx) + float64(d)*fx
		return uint8(math.Round(top*(1-fy) + bot*fy))
	}
	return color.RGBA{mix(c00.R, c10.R, c01.R, c11.R), mix(c00.G, c10.G, c01.G, c11.G), mix(c00.B, c10.B, c01.B, c11.B), 255}
}

// sentMaxPx is the long-edge limit that keeps a w×h image within the model's
// size limits (scanMaxPx on the long edge, scanMaxPixels in area).
func sentMaxPx(w, h int) int {
	maxPx := scanMaxPx
	if w*h > scanMaxPixels {
		long := float64(max(w, h)) * math.Sqrt(float64(scanMaxPixels)/float64(w*h))
		maxPx = min(maxPx, int(long))
	}
	return maxPx
}

// cornerImage returns the photo scaled for the corner request, as JPEG, and
// the factor that maps its pixels back to the sent image.
func (p *scanPhoto) cornerImage() ([]byte, float64, int, int, error) {
	small := scaleImage(p.sent, cornerMaxPx)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, small, &jpeg.Options{Quality: 85}); err != nil {
		return nil, 0, 0, 0, fmt.Errorf("encoding corner image: %w", err)
	}
	scale := float64(p.sent.Bounds().Dx()) / float64(small.Bounds().Dx())
	return buf.Bytes(), scale, small.Bounds().Dx(), small.Bounds().Dy(), nil
}

// rectangleAspect estimates the width/height ratio of the real rectangle
// whose photo has the corners q (TL, TR, BR, BL), for a camera whose optical
// centre is at (u0, v0). It first recovers the focal length from how the
// edges converge (Zhang & He, "Whiteboard scanning and image enhancement",
// 2007). It reports false when there is too little perspective to measure.
func rectangleAspect(q [4]Point, u0, v0 float64) (float64, bool) {
	// The paper's corner order: m1 TL, m2 TR, m3 BL, m4 BR (homogeneous).
	m1 := [3]float64{q[0].X, q[0].Y, 1}
	m2 := [3]float64{q[1].X, q[1].Y, 1}
	m3 := [3]float64{q[3].X, q[3].Y, 1}
	m4 := [3]float64{q[2].X, q[2].Y, 1}
	k2 := dot3(cross3(m1, m4), m3) / dot3(cross3(m2, m4), m3)
	k3 := dot3(cross3(m1, m4), m2) / dot3(cross3(m3, m4), m2)
	var n2, n3 [3]float64
	for i := range n2 {
		n2[i] = k2*m2[i] - m1[i]
		n3[i] = k3*m3[i] - m1[i]
	}
	if math.Abs(n2[2]) < 1e-9 || math.Abs(n3[2]) < 1e-9 {
		return 0, false // edges parallel in the photo: no perspective to use
	}
	f2 := -((n2[0]*n3[0] - (n2[0]*n3[2]+n2[2]*n3[0])*u0 + n2[2]*n3[2]*u0*u0) +
		(n2[1]*n3[1] - (n2[1]*n3[2]+n2[2]*n3[1])*v0 + n2[2]*n3[2]*v0*v0)) / (n2[2] * n3[2])
	if !(f2 > 0) {
		return 0, false
	}
	f := math.Sqrt(f2)
	// |K^-1 n| with K = [[f 0 u0] [0 f v0] [0 0 1]].
	norm := func(n [3]float64) float64 {
		x := (n[0] - u0*n[2]) / f
		y := (n[1] - v0*n[2]) / f
		return math.Sqrt(x*x + y*y + n[2]*n[2])
	}
	aspect := norm(n2) / norm(n3)
	if !(aspect > 0.1 && aspect < 10) {
		return 0, false
	}
	return aspect, true
}

func cross3(a, b [3]float64) [3]float64 {
	return [3]float64{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}

func dot3(a, b [3]float64) float64 {
	return a[0]*b[0] + a[1]*b[1] + a[2]*b[2]
}

// outlinePreview draws the found page outline on a small copy of the photo
// (the area outside the page darkened) and returns it as JPEG.
func outlinePreview(sent *image.RGBA, quad [4]Point) ([]byte, error) {
	const previewPx = 720
	small := scaleImage(sent, previewPx)
	f := float64(small.Bounds().Dx()) / float64(sent.Bounds().Dx())
	var q [4]Point
	for i, p := range quad {
		q[i] = Point{p.X * f, p.Y * f}
	}
	b := small.Bounds()
	accent := color.RGBA{79, 70, 229, 255}
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			p := Point{float64(x) + 0.5, float64(y) + 0.5}
			d := distToOutline(p, q)
			switch {
			case d < 2.5:
				small.SetRGBA(x, y, accent)
			case !insideQuad(p, q):
				c := small.RGBAAt(x, y)
				small.SetRGBA(x, y, color.RGBA{c.R / 3, c.G / 3, c.B / 3, 255})
			}
			for _, corner := range q {
				if math.Hypot(p.X-corner.X, p.Y-corner.Y) < 7 {
					small.SetRGBA(x, y, accent)
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, small, &jpeg.Options{Quality: 80}); err != nil {
		return nil, fmt.Errorf("encoding preview: %w", err)
	}
	return buf.Bytes(), nil
}

// insideQuad reports whether p lies inside the convex outline q.
func insideQuad(p Point, q [4]Point) bool {
	sign := 0.0
	for i := 0; i < 4; i++ {
		a, b := q[i], q[(i+1)%4]
		cross := (b.X-a.X)*(p.Y-a.Y) - (b.Y-a.Y)*(p.X-a.X)
		if sign != 0 && math.Signbit(cross) != math.Signbit(sign) {
			return false
		}
		sign = cross
	}
	return true
}

// distToOutline is the distance from p to the nearest edge of q.
func distToOutline(p Point, q [4]Point) float64 {
	best := math.Inf(1)
	for i := 0; i < 4; i++ {
		a, b := q[i], q[(i+1)%4]
		dx, dy := b.X-a.X, b.Y-a.Y
		t := ((p.X-a.X)*dx + (p.Y-a.Y)*dy) / (dx*dx + dy*dy)
		t = math.Max(0, math.Min(1, t))
		best = math.Min(best, math.Hypot(p.X-(a.X+t*dx), p.Y-(a.Y+t*dy)))
	}
	return best
}
