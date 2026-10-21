package avatars_test

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"testing"

	"github.com/wnexous/partyline/internal/avatars"
)

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// bands returns a w x h image with three distinct vertical bands: the outer
// quarters red (left) and blue (right), the middle half green.
func bands(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	red := color.RGBA{R: 220, G: 20, B: 20, A: 255}
	green := color.RGBA{R: 20, G: 200, B: 20, A: 255}
	blue := color.RGBA{R: 20, G: 20, B: 220, A: 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			switch {
			case x < w/4:
				img.Set(x, y, red)
			case x < 3*w/4:
				img.Set(x, y, green)
			default:
				img.Set(x, y, blue)
			}
		}
	}
	return img
}

func decodeOutput(t *testing.T, out []byte) image.Image {
	t.Helper()
	if len(out) == 0 {
		t.Fatal("Process returned empty output")
	}
	if got := pngSignature(out); !got {
		t.Fatal("output is not a PNG")
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if got := img.Bounds().Dx(); got != 256 {
		t.Fatalf("width = %d, want 256", got)
	}
	if got := img.Bounds().Dy(); got != 256 {
		t.Fatalf("height = %d, want 256", got)
	}
	return img
}

func pngSignature(b []byte) bool {
	sig := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	return len(b) >= len(sig) && bytes.Equal(b[:len(sig)], sig)
}

func assertNear(t *testing.T, img image.Image, x, y int, want color.RGBA) {
	t.Helper()
	r, g, b, _ := img.At(x, y).RGBA()
	got := color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 255}
	const tol = 3
	if abs(int(got.R), int(want.R)) > tol || abs(int(got.G), int(want.G)) > tol || abs(int(got.B), int(want.B)) > tol {
		t.Fatalf("pixel (%d,%d) = %v, want ~%v", x, y, got, want)
	}
}

func abs(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

func TestProcessPNGCenterCropsToMiddleSquare(t *testing.T) {
	// 400x200: red 0-99, green 100-299, blue 300-399. The center square is
	// exactly the green band, so any output pixel that is not green means the
	// whole image was scaled instead of the middle square kept (PROF-07).
	green := color.RGBA{R: 20, G: 200, B: 20, A: 255}
	out, err := avatars.Process(bytes.NewReader(encodePNG(t, bands(400, 200))))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	img := decodeOutput(t, out)
	for _, p := range [][2]int{{0, 0}, {255, 0}, {0, 255}, {255, 255}, {128, 128}, {10, 128}, {245, 128}} {
		assertNear(t, img, p[0], p[1], green)
	}
}

func TestProcessJPEGProducesAvatar(t *testing.T) {
	// Same 400x200 bands as the PNG case: the center square is the green band.
	out, err := avatars.Process(bytes.NewReader(encodeJPEG(t, bands(400, 200))))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	img := decodeOutput(t, out)
	green := color.RGBA{R: 20, G: 200, B: 20, A: 255}
	// Sampled away from the crop edges, where JPEG chroma subsampling bleeds.
	for _, p := range [][2]int{{128, 128}, {64, 128}, {192, 128}, {128, 64}, {128, 192}} {
		assertNear(t, img, p[0], p[1], green)
	}
}

func TestProcessWebPProducesAvatar(t *testing.T) {
	data, err := os.ReadFile("testdata/tiny.webp")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	out, err := avatars.Process(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	img := decodeOutput(t, out)
	// The fixture is 32x16 with green in the middle 16 columns; the center
	// square is entirely green.
	assertNear(t, img, 128, 128, color.RGBA{R: 20, G: 200, B: 20, A: 255})
	assertNear(t, img, 5, 128, color.RGBA{R: 20, G: 200, B: 20, A: 255})
}

func TestProcessRejectsInvalidInputs(t *testing.T) {
	valid := encodePNG(t, bands(400, 200))
	truncated := valid[:len(valid)/2]
	oversized := make([]byte, 2<<20+1)
	copy(oversized, valid)
	tooWide := encodePNG(t, image.NewRGBA(image.Rect(0, 0, 5000, 10)))

	var gifBuf bytes.Buffer
	if err := gif.Encode(&gifBuf, image.NewRGBA(image.Rect(0, 0, 10, 10)), nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}

	cases := []struct {
		name string
		data []byte
	}{
		{"gif", gifBuf.Bytes()},
		{"text", []byte("this is not an image")},
		{"truncated png", truncated},
		{"two mebibytes plus one", oversized},
		{"too wide", tooWide},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := avatars.Process(bytes.NewReader(tc.data))
			if !errors.Is(err, avatars.ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestProcessAcceptsExactlyTwoMiB(t *testing.T) {
	// The limit is inclusive: a valid image padded to exactly 2 MiB is still
	// accepted (PROF-06, "up to 2 MB").
	valid := encodePNG(t, bands(400, 200))
	atLimit := make([]byte, 2<<20)
	copy(atLimit, valid)
	out, err := avatars.Process(bytes.NewReader(atLimit))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	decodeOutput(t, out)
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestProcessRejectsUnreadableInput(t *testing.T) {
	_, err := avatars.Process(failingReader{})
	if !errors.Is(err, avatars.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestProcessAcceptsMaximumDimension(t *testing.T) {
	// 4096 is the inclusive bound from the decompression-bomb guard.
	out, err := avatars.Process(bytes.NewReader(encodePNG(t, image.NewRGBA(image.Rect(0, 0, 4096, 1)))))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	decodeOutput(t, out)
}
