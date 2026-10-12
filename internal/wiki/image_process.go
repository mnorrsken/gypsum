package wiki

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	_ "golang.org/x/image/webp"
	_ "image/gif" // register decoders for previews

	"golang.org/x/image/draw"
)

// maxImagePixels guards against decompression bombs: a tiny file that
// decodes to a huge bitmap.
const maxImagePixels = 60_000_000

// jpegQuality is used whenever a JPEG is re-encoded.
const jpegQuality = 85

// processImage prepares an uploaded image for storage. JPEGs are always
// re-encoded, which applies the EXIF orientation and drops the EXIF block
// (camera details and GPS location); both JPEGs and PNGs are scaled down so
// the longest side is at most maxPx. Other formats are stored unchanged, and
// maxPx 0 stores everything unchanged.
func processImage(data []byte, ext string, maxPx int) ([]byte, error) {
	if maxPx <= 0 {
		return data, nil
	}
	switch ext {
	case ".jpg", ".jpeg":
		img, err := decodeImage(data)
		if err != nil {
			return nil, err
		}
		rgba := orientImage(scaleImage(img, maxPx), jpegOrientation(data))
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, rgba, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return nil, fmt.Errorf("encoding jpeg: %w", err)
		}
		return buf.Bytes(), nil
	case ".png":
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return nil, uploadErrorf("not a valid PNG image")
		}
		if max(cfg.Width, cfg.Height) <= maxPx {
			return data, nil
		}
		img, err := decodeImage(data)
		if err != nil {
			return nil, err
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, scaleImage(img, maxPx)); err != nil {
			return nil, fmt.Errorf("encoding png: %w", err)
		}
		return buf.Bytes(), nil
	}
	return data, nil
}

// decodeImage decodes any registered raster format after checking that the
// bitmap size is sane.
func decodeImage(data []byte) (image.Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, uploadErrorf("could not read image: %v", err)
	}
	if cfg.Width*cfg.Height > maxImagePixels {
		return nil, uploadErrorf("image is too large (%dx%d)", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, uploadErrorf("could not read image: %v", err)
	}
	return img, nil
}

// scaleImage returns img as RGBA with its longest side at most maxPx.
func scaleImage(img image.Image, maxPx int) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if longest := max(w, h); maxPx > 0 && longest > maxPx {
		w = max(1, w*maxPx/longest)
		h = max(1, h*maxPx/longest)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if w == b.Dx() && h == b.Dy() {
		draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	} else {
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	}
	return dst
}

// orientImage rotates or flips src so it displays upright for the given
// EXIF orientation (1–8).
func orientImage(src *image.RGBA, orientation int) *image.RGBA {
	if orientation < 2 || orientation > 8 {
		return src
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := w, h
	if orientation >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch orientation {
			case 2: // mirrored
				dx, dy = w-1-x, y
			case 3: // rotated 180°
				dx, dy = w-1-x, h-1-y
			case 4: // mirrored vertically
				dx, dy = x, h-1-y
			case 5: // transposed
				dx, dy = y, x
			case 6: // needs 90° clockwise
				dx, dy = h-1-y, x
			case 7: // transversed
				dx, dy = h-1-y, w-1-x
			case 8: // needs 90° counter-clockwise
				dx, dy = y, w-1-x
			}
			si := src.PixOffset(x, y)
			di := dst.PixOffset(dx, dy)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}

// jpegOrientation returns the EXIF orientation tag of a JPEG, or 1 when it
// has none.
func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	pos := 2
	for pos+4 <= len(data) {
		if data[pos] != 0xFF {
			return 1
		}
		marker := data[pos+1]
		if marker == 0xDA || marker == 0xD9 { // start of scan / end of image
			return 1
		}
		segLen := int(binary.BigEndian.Uint16(data[pos+2:]))
		if segLen < 2 || pos+2+segLen > len(data) {
			return 1
		}
		seg := data[pos+4 : pos+2+segLen]
		if marker == 0xE1 && len(seg) > 6 && string(seg[:6]) == "Exif\x00\x00" {
			return exifOrientation(seg[6:])
		}
		pos += 2 + segLen
	}
	return 1
}

// exifOrientation reads tag 0x0112 from IFD0 of a TIFF-structured EXIF block.
func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	ifd := int(order.Uint32(tiff[4:]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 1
	}
	count := int(order.Uint16(tiff[ifd:]))
	for i := 0; i < count; i++ {
		entry := ifd + 2 + i*12
		if entry+12 > len(tiff) {
			return 1
		}
		if order.Uint16(tiff[entry:]) == 0x0112 {
			if v := int(order.Uint16(tiff[entry+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// imagePreview returns an image no larger than maxPx on its longest side in a
// format MCP clients accept, re-encoding only when needed.
func imagePreview(data []byte, ext string, maxPx int, maxBytes int) ([]byte, string, error) {
	mimeByExt := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}
	mime, ok := mimeByExt[ext]
	if !ok {
		return nil, "", fmt.Errorf("no preview for %s", ext)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("reading image: %w", err)
	}
	if max(cfg.Width, cfg.Height) <= maxPx && len(data) <= maxBytes {
		return data, mime, nil
	}
	img, err := decodeImage(data)
	if err != nil {
		return nil, "", err
	}
	scaled := scaleImage(img, maxPx)
	var buf bytes.Buffer
	if ext == ".png" || ext == ".gif" {
		err, mime = png.Encode(&buf, scaled), "image/png"
	} else {
		err, mime = jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: jpegQuality}), "image/jpeg"
	}
	if err != nil {
		return nil, "", fmt.Errorf("encoding preview: %w", err)
	}
	return buf.Bytes(), mime, nil
}
