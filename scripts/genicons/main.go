// Command genicons draws the partyline mark as the two PNG icons the web app manifest lists
// (PUSH-20). It is run by hand when the mark changes: the PNGs are committed, the build never runs
// this program and nothing imports it. Only the standard library is used, so the build machine
// needs no rasterizer.
//
// Run it from the repository root: `go run ./scripts/genicons`.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

// The mark is the favicon in its 32x32 viewBox: an indigo rounded square with three light rules
// (web/public/favicon.svg, DESIGN.md rule 6).
const (
	viewBox   = 32.0
	corner    = 7.0
	ruleWidth = 3.0
)

// samples is the supersampling factor per axis: the mark has hard edges, so this is the whole
// anti-aliasing.
const samples = 4

var (
	ink  = color.NRGBA{R: 0x46, G: 0x50, B: 0x97, A: 0xff} // #465097, the accent fill
	rule = color.NRGBA{R: 0xfa, G: 0xfa, B: 0xfa, A: 0xff} // #fafafa, the light rules
)

// line is one of the mark's rules: horizontal, centered on y, from x=from to x=to.
type line struct {
	y, from, to float64
}

var lines = []line{
	{y: 11, from: 7, to: 25},
	{y: 16, from: 7, to: 19},
	{y: 21, from: 7, to: 22},
}

func main() {
	dir := flag.String("dir", "web/public/icons", "directory the icons are written to")
	flag.Parse()
	if err := run(*dir); err != nil {
		fmt.Fprintln(os.Stderr, "genicons:", err)
		os.Exit(1)
	}
}

func run(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- the icons are public assets
		return err
	}
	for _, size := range []int{192, 512} {
		name := filepath.Join(dir, fmt.Sprintf("icon-%d.png", size))
		if err := write(name, size); err != nil {
			return err
		}
		width, height, err := dimensions(name)
		if err != nil {
			return err
		}
		if width != size || height != size {
			return fmt.Errorf("%s is %dx%d, want %dx%d", name, width, height, size, size)
		}
		fmt.Printf("%s: %dx%d\n", name, width, height)
	}
	return nil
}

func write(name string, size int) error {
	f, err := os.Create(name) // #nosec G304 -- the icons are public assets under a fixed directory
	if err != nil {
		return err
	}
	if err := png.Encode(f, draw(size)); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// dimensions reads back what was written, so a wrong size fails the generator instead of the
// install.
func dimensions(name string) (int, int, error) {
	data, err := os.ReadFile(name) // #nosec G304 -- the icons are public assets under a fixed directory
	if err != nil {
		return 0, 0, err
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

// draw renders the mark at size pixels square.
func draw(size int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	scale := viewBox / float64(size)
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var inkSamples, ruleSamples int
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/samples) * scale
					y := (float64(py) + (float64(sy)+0.5)/samples) * scale
					switch {
					case !inside(x, y):
					case onRule(x, y):
						ruleSamples++
					default:
						inkSamples++
					}
				}
			}
			if inkSamples == 0 && ruleSamples == 0 {
				continue
			}
			img.SetNRGBA(px, py, blend(inkSamples, ruleSamples))
		}
	}
	return img
}

// inside reports whether (x, y) is in the rounded square: the inner rectangle [corner, size-corner]
// grown by a disk of radius corner.
func inside(x, y float64) bool {
	cx := math.Max(corner, math.Min(viewBox-corner, x))
	cy := math.Max(corner, math.Min(viewBox-corner, y))
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= corner*corner
}

// onRule reports whether (x, y) is within half the stroke width of one of the rules.
func onRule(x, y float64) bool {
	for _, l := range lines {
		cx := math.Max(l.from, math.Min(l.to, x))
		dx, dy := x-cx, y-l.y
		if dx*dx+dy*dy <= (ruleWidth/2)*(ruleWidth/2) {
			return true
		}
	}
	return false
}

// blend turns the sample counts of one pixel into its color: the covered fraction is the alpha,
// and the rules are opaque paint over the square.
func blend(inkSamples, ruleSamples int) color.NRGBA {
	const total = float64(samples * samples)
	inkPart := float64(inkSamples) / total
	rulePart := float64(ruleSamples) / total
	covered := inkPart + rulePart
	mix := func(a, b uint8) uint8 {
		return uint8(math.Round((float64(a)*inkPart + float64(b)*rulePart) / covered))
	}
	return color.NRGBA{
		R: mix(ink.R, rule.R),
		G: mix(ink.G, rule.G),
		B: mix(ink.B, rule.B),
		A: uint8(math.Round(covered * 255)),
	}
}
