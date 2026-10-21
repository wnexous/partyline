// Package avatars turns an uploaded image into a safe 256x256 PNG for an
// intelligence profile. It is a leaf package: no other partyline package is
// imported.
package avatars

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	_ "image/jpeg" // register the JPEG decoder
	"image/png"
	"io"
	"net/http"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register the WebP decoder
)

// ErrInvalid is returned for any input that is not an acceptable avatar image.
var ErrInvalid = errors.New("invalid avatar image")

const (
	// maxBytes is the upload limit from PROF-06 (2 MiB).
	maxBytes = 2 << 20
	// maxDimension bounds decoded dimensions before decoding (decompression bombs).
	maxDimension = 4096
	// size is the side of the stored avatar (PROF-07).
	size = 256
)

// Process reads at most 2 MiB of PNG, JPEG or WebP image data and returns a
// 256x256 PNG, center-cropped to a square and scaled with CatmullRom. Any other
// input returns ErrInvalid.
func Process(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, ErrInvalid
	}
	if len(data) > maxBytes {
		return nil, ErrInvalid
	}
	switch http.DetectContentType(data) {
	case "image/png", "image/jpeg", "image/webp":
	default:
		return nil, ErrInvalid
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalid
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxDimension || cfg.Height > maxDimension {
		return nil, ErrInvalid
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalid
	}
	b := img.Bounds()
	side := b.Dx()
	if b.Dy() < side {
		side = b.Dy()
	}
	crop := image.Rect(
		b.Min.X+(b.Dx()-side)/2,
		b.Min.Y+(b.Dy()-side)/2,
		b.Min.X+(b.Dx()-side)/2+side,
		b.Min.Y+(b.Dy()-side)/2+side,
	)
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, crop, draw.Src, nil)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
