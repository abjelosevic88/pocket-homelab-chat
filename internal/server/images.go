package server

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// maxSide is the longest side a photo keeps in a chat: sharp on any
// screen, a fraction of what a phone takes.
const maxSide = 2400

var errNotPicture = errors.New("that is not a picture (JPEG, PNG, GIF or WebP)")

// preparePicture checks an upload and makes it fit for the web: photos are
// turned upright, made at most maxSide wide or high and saved again — which
// also drops what the camera wrote into them (where it was taken, the
// phone). PNG, GIF and WebP stay as they are unless they are huge.
func preparePicture(data []byte) (out []byte, mime, ext string, w, h int, err error) {
	mime = http.DetectContentType(data)
	switch mime {
	case "image/jpeg":
		ext = ".jpg"
	case "image/png":
		ext = ".png"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	default:
		return nil, "", "", 0, 0, errNotPicture
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", "", 0, 0, errNotPicture
	}
	if cfg.Width*cfg.Height > 80_000_000 {
		return nil, "", "", 0, 0, errors.New("that picture is too large (more than 80 megapixels)")
	}
	w, h = cfg.Width, cfg.Height
	if mime == "image/gif" || (mime != "image/jpeg" && w <= maxSide && h <= maxSide) {
		return data, mime, ext, w, h, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", "", 0, 0, errNotPicture
	}
	if mime == "image/jpeg" {
		img = orient(img, jpegOrientation(data))
	}
	img = fit(img, maxSide)
	b := img.Bounds()
	w, h = b.Dx(), b.Dy()
	var buf bytes.Buffer
	switch mime {
	case "image/png":
		err = png.Encode(&buf, img)
	case "image/webp":
		// no WebP encoder in Go: a big WebP becomes a JPEG
		mime, ext = "image/jpeg", ".jpg"
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85})
	default:
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85})
	}
	if err != nil {
		return nil, "", "", 0, 0, err
	}
	return buf.Bytes(), mime, ext, w, h, nil
}

var _ = gif.Decode

// fit scales an image down so that neither side is longer than max.
func fit(img image.Image, max int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= max && h <= max {
		return img
	}
	if w >= h {
		h = h * max / w
		w = max
	} else {
		w = w * max / h
		h = max
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	return dst
}

// jpegOrientation reads the EXIF orientation (1–8) of a JPEG; 1 when none.
func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return 1
		}
		marker := data[i+1]
		size := int(binary.BigEndian.Uint16(data[i+2:]))
		if marker == 0xDA || size < 2 || i+2+size > len(data) {
			return 1
		}
		seg := data[i+4 : i+2+size]
		if marker == 0xE1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	off := int(bo.Uint32(t[4:]))
	if off+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[off:]))
	for k := 0; k < n; k++ {
		e := off + 2 + k*12
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			v := int(bo.Uint16(t[e+8:]))
			if v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// orient turns a photo the way the camera said it was held.
func orient(img image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch o {
			case 2:
				nx, ny = w-1-x, y
			case 3:
				nx, ny = w-1-x, h-1-y
			case 4:
				nx, ny = x, h-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = h-1-y, x
			case 7:
				nx, ny = h-1-y, w-1-x
			case 8:
				nx, ny = y, w-1-x
			}
			dst.Set(nx, ny, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
