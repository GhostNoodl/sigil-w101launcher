// icongen renders Sigil's app icon: a gradient magic circle with a
// lightning bolt, on the app's dark background. It draws at 4x and
// box-downscales for smooth edges.
//
// Usage: go run ./tools/icongen [out.png]   (default: docs/icon-256.png)
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const (
	ss   = 4        // supersample factor
	size = 256 * ss // working size
)

var (
	bg      = color.NRGBA{18, 18, 26, 255}   // --bg
	accent  = color.NRGBA{124, 92, 255, 255} // --accent
	accent2 = color.NRGBA{0, 212, 255, 255}  // --accent2
)

func lerp(a, b uint8, t float64) uint8 {
	return uint8(float64(a)*(1-t) + float64(b)*t)
}

func mix(c1, c2 color.NRGBA, t float64) color.NRGBA {
	return color.NRGBA{lerp(c1.R, c2.R, t), lerp(c1.G, c2.G, t), lerp(c1.B, c2.B, t), 255}
}

// bolt polygon, normalized to [0,1] window coords (a classic ⚡ zigzag).
var bolt = [][2]float64{
	{0.565, 0.170},
	{0.360, 0.545},
	{0.475, 0.545},
	{0.430, 0.830},
	{0.640, 0.455},
	{0.520, 0.455},
}

func inPoly(x, y float64, poly [][2]float64) bool {
	inside := false
	j := len(poly) - 1
	for i := range poly {
		xi, yi := poly[i][0], poly[i][1]
		xj, yj := poly[j][0], poly[j][1]
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			inside = !inside
		}
		j = i
	}
	return inside
}

func roundedRectAlpha(x, y, w, r float64) bool {
	// inside rounded rect [0,w)² with corner radius r
	if x < 0 || y < 0 || x >= w || y >= w {
		return false
	}
	cx, cy := x, y
	if cx < r {
		cx = r - cx
	} else if cx >= w-r {
		cx = cx - (w - r - 1)
	} else {
		cx = 0
	}
	if cy < r {
		cy = r - cy
	} else if cy >= w-r {
		cy = cy - (w - r - 1)
	} else {
		cy = 0
	}
	return cx*cx+cy*cy <= r*r+1
}

func pixel(px, py int) color.NRGBA {
	w := float64(size)
	x := float64(px) + 0.5
	y := float64(py) + 0.5

	if !roundedRectAlpha(x, y, w, w*0.225) {
		return color.NRGBA{0, 0, 0, 0}
	}

	c := w / 2
	dx, dy := x-c, y-c
	dist := math.Hypot(dx, dy)

	// center bolt
	if inPoly(x/w, y/w, bolt) {
		t := (y/w - 0.17) / 0.66 // vertical gradient over bolt height
		return mix(accent2, accent, t)
	}

	// rune dots on the ring (8 dots, alternating radius)
	for i := 0; i < 8; i++ {
		ang := float64(i) * math.Pi / 4
		dr := w * 0.345
		dxc := c + dr*math.Cos(ang) - x
		dyc := c + dr*math.Sin(ang) - y
		dotR := w * 0.020
		if i%2 == 0 {
			dotR = w * 0.027
		}
		if dxc*dxc+dyc*dyc <= dotR*dotR {
			return mix(accent, accent2, float64(i)/7)
		}
	}

	// gradient ring
	ringMid, ringHalf := w*0.345, w*0.030
	if dist >= ringMid-ringHalf && dist <= ringMid+ringHalf {
		ang := math.Atan2(dy, dx) + math.Pi // 0..2pi
		t := ang / (2 * math.Pi)
		return mix(accent, accent2, t)
	}

	return bg
}

func main() {
	out := "docs/icon-256.png"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}

	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetNRGBA(x, y, pixel(x, y))
		}
	}

	// box downscale to 256
	final := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			var r, g, b, a int
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					p := img.NRGBAAt(x*ss+sx, y*ss+sy)
					r += int(p.R) * int(p.A)
					g += int(p.G) * int(p.A)
					b += int(p.B) * int(p.A)
					a += int(p.A)
				}
			}
			n := ss * ss
			if a == 0 {
				final.SetNRGBA(x, y, color.NRGBA{0, 0, 0, 0})
				continue
			}
			final.SetNRGBA(x, y, color.NRGBA{
				uint8(r / a), uint8(g / a), uint8(b / a), uint8(a / n),
			})
		}
	}

	f, err := os.Create(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	if err := png.Encode(f, final); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote", out)
}
