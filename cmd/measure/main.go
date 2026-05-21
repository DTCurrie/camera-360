// Measures the fisheye image circle by scanning radially from an assumed
// center and finding where the image transitions from scene to dark vignette.
// More robust than centroid-of-bright-pixels because it doesn't depend on
// where scene content happens to land.
//
//	go run ./cmd/measure -in out/front.jpg
package main

import (
	"flag"
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"os"
)

func main() {
	in := flag.String("in", "out/front.jpg", "fisheye half-frame JPEG (960x960)")
	thresh := flag.Int("thresh", 25, "luma threshold for 'scene vs dark vignette'")
	flag.Parse()

	f, err := os.Open(*in)
	must(err)
	defer f.Close()
	img, err := jpeg.Decode(f)
	must(err)
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	fmt.Printf("frame: %dx%d\n", w, h)

	// Scan along many radial directions from the geometric center. For each
	// direction, walk outward and report the first radius where 5 consecutive
	// samples are below threshold (i.e. we've crossed into the dark vignette).
	cx, cy := float64(w)/2, float64(h)/2

	const nDirs = 360
	radii := make([]float64, 0, nDirs)
	for i := 0; i < nDirs; i++ {
		angle := float64(i) * math.Pi * 2 / float64(nDirs)
		dx, dy := math.Cos(angle), math.Sin(angle)
		var r float64
		darkStreak := 0
		for rr := 1.0; rr < math.Min(cx, cy)*math.Sqrt(2); rr += 0.5 {
			x := int(cx + rr*dx)
			y := int(cy + rr*dy)
			if x < 0 || x >= w || y < 0 || y >= h {
				r = rr - 1
				break
			}
			c := img.At(x, y)
			rr2, gg2, bb2, _ := c.RGBA()
			luma := int((rr2 + 2*gg2 + bb2) >> 10)
			if luma < *thresh {
				darkStreak++
				if darkStreak >= 5 {
					r = rr - 5
					break
				}
			} else {
				darkStreak = 0
			}
		}
		radii = append(radii, r)
	}

	// Stats on radii: ignore the ~5% of directions with smallest r (might be
	// dark scene content), then take median of the rest.
	sortedR := append([]float64(nil), radii...)
	for i := 1; i < len(sortedR); i++ {
		for j := i; j > 0 && sortedR[j-1] > sortedR[j]; j-- {
			sortedR[j-1], sortedR[j] = sortedR[j], sortedR[j-1]
		}
	}
	med := sortedR[len(sortedR)/2]
	p05 := sortedR[len(sortedR)*5/100]
	p95 := sortedR[len(sortedR)*95/100]

	fmt.Printf("center used (geometric): (%.0f, %.0f)\n", cx, cy)
	fmt.Printf("image circle radius:\n")
	fmt.Printf("  p05 = %.1f  (likely dark scene content, ignore)\n", p05)
	fmt.Printf("  p50 = %.1f  <- recommended R\n", med)
	fmt.Printf("  p95 = %.1f\n", p95)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

var _ = image.Rectangle{}
