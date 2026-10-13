package wiki

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"testing"
)

// quadrantDoc is a w×h "document" with four solid colored quadrants.
func quadrantDoc(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{220, 30, 30, 255} // top-left red
			switch {
			case x >= w/2 && y < h/2:
				c = color.RGBA{30, 160, 30, 255} // top-right green
			case x >= w/2 && y >= h/2:
				c = color.RGBA{30, 30, 220, 255} // bottom-right blue
			case x < w/2 && y >= h/2:
				c = color.RGBA{230, 200, 30, 255} // bottom-left yellow
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// photoOfDoc draws doc into the quadrilateral quad (TL, TR, BR, BL) of a gray
// w×h "table", the way a camera at an angle would see it.
func photoOfDoc(t *testing.T, doc *image.RGBA, quad [4]Point, w, h int) *image.RGBA {
	t.Helper()
	db := doc.Bounds()
	rect := [4]Point{{0, 0}, {float64(db.Dx()), 0}, {float64(db.Dx()), float64(db.Dy())}, {0, float64(db.Dy())}}
	toDoc, err := homography(quad, rect)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			d := applyHomography(toDoc, Point{float64(x) + 0.5, float64(y) + 0.5})
			if d.X >= 0 && d.Y >= 0 && d.X < float64(db.Dx()) && d.Y < float64(db.Dy()) {
				img.SetRGBA(x, y, doc.RGBAAt(int(d.X), int(d.Y)))
			} else {
				img.SetRGBA(x, y, color.RGBA{120, 100, 80, 255})
			}
		}
	}
	return img
}

func TestOrderCorners(t *testing.T) {
	want := [4]Point{{10, 20}, {300, 5}, {320, 400}, {0, 380}}
	shuffled := [4]Point{want[2], want[0], want[3], want[1]}
	if got := orderCorners(shuffled); got != want {
		t.Errorf("orderCorners = %v, want %v", got, want)
	}
}

func TestReadingOrder(t *testing.T) {
	// A page turned about 120°: its own top-left is at the bottom right.
	turned := [4]Point{{700, 900}, {150, 950}, {100, 100}, {650, 60}}
	if got := readingOrder(turned); got != turned {
		t.Errorf("clockwise corners in reading order were reordered: %v", got)
	}
	// Counter-clockwise input falls back to the geometric order.
	ccw := [4]Point{turned[0], turned[3], turned[2], turned[1]}
	if got := readingOrder(ccw); got != orderCorners(ccw) {
		t.Errorf("counter-clockwise corners kept: %v", got)
	}
}

func TestUsableQuad(t *testing.T) {
	tests := []struct {
		name string
		q    [4]Point
		want bool
	}{
		{"tilted page", [4]Point{{100, 80}, {700, 120}, {650, 900}, {60, 850}}, true},
		{"whole photo", [4]Point{{2, 1}, {798, 3}, {799, 998}, {1, 999}}, false},
		{"tiny", [4]Point{{10, 10}, {60, 10}, {60, 60}, {10, 60}}, false},
		{"small receipt", [4]Point{{300, 300}, {420, 310}, {410, 640}, {290, 630}}, true},
		{"self-crossing", [4]Point{{100, 80}, {650, 900}, {700, 120}, {60, 850}}, false},
	}
	for _, tt := range tests {
		if got := usableQuad(tt.q, 800, 1000); got != tt.want {
			t.Errorf("%s: usableQuad = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// cameraQuad projects a w×h rectangle lying on a table through a pinhole
// camera (focal length f, optical centre in the middle of a pw×ph photo)
// that is tilted by tiltDeg and turned by turnDeg, and returns the corners
// as they appear in the photo (TL, TR, BR, BL).
func cameraQuad(w, h, f float64, pw, ph int, tiltDeg, turnDeg float64) [4]Point {
	tilt, turn := tiltDeg*math.Pi/180, turnDeg*math.Pi/180
	var out [4]Point
	for i, c := range [4][2]float64{{-w / 2, -h / 2}, {w / 2, -h / 2}, {w / 2, h / 2}, {-w / 2, h / 2}} {
		// Turn the page on the table, tilt it away from the camera, then
		// push it in front of the camera.
		x := c[0]*math.Cos(turn) - c[1]*math.Sin(turn)
		y := c[0]*math.Sin(turn) + c[1]*math.Cos(turn)
		y, z := y*math.Cos(tilt), y*math.Sin(tilt)+2.2*math.Max(w, h)
		out[i] = Point{f*x/z + float64(pw)/2, f*y/z + float64(ph)/2}
	}
	return out
}

func TestRectangleAspect(t *testing.T) {
	for _, tc := range []struct{ tilt, turn float64 }{{0, 0}, {25, 8}, {40, -15}, {50, 30}} {
		q := cameraQuad(2100, 2970, 1400, 1200, 1600, tc.tilt, tc.turn)
		aspect, ok := rectangleAspect(q, 600, 800)
		if !ok {
			// Straight on, nothing to measure: the edge lengths are right then.
			if tc.tilt == 0 {
				continue
			}
			t.Fatalf("tilt %v turn %v: no estimate", tc.tilt, tc.turn)
		}
		if want := 2100.0 / 2970; math.Abs(aspect-want)/want > 0.03 {
			t.Errorf("tilt %v turn %v: aspect %.3f, want %.3f", tc.tilt, tc.turn, aspect, want)
		}
	}
}

func TestStraightenRecoversTheDocument(t *testing.T) {
	doc := quadrantDoc(420, 594) // A4 proportions
	quad := cameraQuad(420, 594, 1000, 900, 1100, 35, 12)
	photo := photoOfDoc(t, doc, quad, 900, 1100)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, photo, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	p, err := prepareScanImage(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	flat, err := p.straighten(orderCorners(quad))
	if err != nil {
		t.Fatal(err)
	}
	b := flat.sent.Bounds()
	if ratio := float64(b.Dy()) / float64(b.Dx()); math.Abs(ratio-594.0/420) > 0.05 {
		t.Errorf("straightened page is %dx%d (ratio %.2f), want about A4 (1.41)", b.Dx(), b.Dy(), ratio)
	}
	// Each quadrant's centre has its color back, so nothing is mirrored,
	// rotated or shifted.
	checks := []struct {
		fx, fy float64
		want   color.RGBA
	}{
		{0.25, 0.25, color.RGBA{220, 30, 30, 255}},
		{0.75, 0.25, color.RGBA{30, 160, 30, 255}},
		{0.75, 0.75, color.RGBA{30, 30, 220, 255}},
		{0.25, 0.75, color.RGBA{230, 200, 30, 255}},
	}
	for _, c := range checks {
		got := flat.sent.RGBAAt(int(c.fx*float64(b.Dx())), int(c.fy*float64(b.Dy())))
		if d := colorDist(got, c.want); d > 40 {
			t.Errorf("at (%.2f, %.2f) color %v, want %v", c.fx, c.fy, got, c.want)
		}
	}
	// The table around the page is gone: the corners are page colors.
	if d := colorDist(flat.sent.RGBAAt(3, 3), color.RGBA{220, 30, 30, 255}); d > 60 {
		t.Errorf("top-left corner is %v, not the page", flat.sent.RGBAAt(3, 3))
	}
}

func TestStraightenHonoursEXIFOrientation(t *testing.T) {
	// The same tilted page, stored sideways with orientation 6.
	doc := quadrantDoc(300, 400)
	quad := [4]Point{{60, 50}, {420, 90}, {400, 560}, {40, 520}}
	upright := photoOfDoc(t, doc, quad, 480, 640)
	// Store it rotated 90° counter-clockwise; orientation 6 turns it back.
	raw := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for y := 0; y < 640; y++ {
		for x := 0; x < 480; x++ {
			raw.SetRGBA(y, 479-x, upright.RGBAAt(x, y))
		}
	}
	if got := orientImage(raw, 6); colorDist(got.RGBAAt(240, 320), upright.RGBAAt(240, 320)) > 0 {
		t.Fatal("test setup: orientation 6 does not undo the rotation")
	}
	p := &scanPhoto{raw: raw, orientation: 6}
	p.sent = orientImage(scaleImage(raw, scanMaxPx), 6)
	flat, err := p.straighten(orderCorners(quad))
	if err != nil {
		t.Fatal(err)
	}
	b := flat.sent.Bounds()
	if d := colorDist(flat.sent.RGBAAt(b.Dx()/4, b.Dy()/4), color.RGBA{220, 30, 30, 255}); d > 40 {
		t.Errorf("top-left quadrant is %v, want red", flat.sent.RGBAAt(b.Dx()/4, b.Dy()/4))
	}
	if d := colorDist(flat.sent.RGBAAt(3*b.Dx()/4, b.Dy()/4), color.RGBA{30, 160, 30, 255}); d > 40 {
		t.Errorf("top-right quadrant is %v, want green", flat.sent.RGBAAt(3*b.Dx()/4, b.Dy()/4))
	}
}

func colorDist(a, b color.RGBA) float64 {
	dr, dg, db := float64(a.R)-float64(b.R), float64(a.G)-float64(b.G), float64(a.B)-float64(b.B)
	return math.Sqrt(dr*dr + dg*dg + db*db)
}
