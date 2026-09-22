package photo

import (
	"image"
	"image/color"
	"testing"
)

// quadrants creates a w×h image with four colored quarters:
// red top left, green top right, blue bottom left, white bottom right.
func quadrants(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{255, 255, 255, 255}
			switch {
			case x < w/2 && y < h/2:
				c = color.RGBA{255, 0, 0, 255}
			case y < h/2:
				c = color.RGBA{0, 255, 0, 255}
			case x < w/2:
				c = color.RGBA{0, 0, 255, 255}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

func corner(img *image.RGBA, right, bottom bool) string {
	b := img.Bounds()
	x, y := 0, 0
	if right {
		x = b.Dx() - 1
	}
	if bottom {
		y = b.Dy() - 1
	}
	c := img.RGBAAt(x, y)
	switch {
	case c.R > 200 && c.G > 200 && c.B > 200:
		return "W"
	case c.R > 200:
		return "R"
	case c.G > 200:
		return "G"
	case c.B > 200:
		return "B"
	}
	return "?"
}

func TestOrient(t *testing.T) {
	// Expected corners (top left, top right, bottom left, bottom right) after
	// correction when the stored image is RG/BW. Values match Pillow's
	// ImageOps.exif_transpose.
	want := map[int]string{
		1: "RGBW",
		2: "GRWB", // flip horizontally
		3: "WBGR", // 180°
		4: "BWRG", // flip vertically
		5: "RBGW", // transpose
		6: "BRWG", // 90° clockwise
		7: "WGBR", // transverse
		8: "GWRB", // 90° counterclockwise
	}
	src := quadrants(8, 4)
	for o, exp := range want {
		out := orient(src, o)
		got := corner(out, false, false) + corner(out, true, false) + corner(out, false, true) + corner(out, true, true)
		if got != exp {
			t.Errorf("orientation %d: %s, want %s", o, got, exp)
		}
		b := out.Bounds()
		if o >= 5 && (b.Dx() != 4 || b.Dy() != 8) || o < 5 && (b.Dx() != 8 || b.Dy() != 4) {
			t.Errorf("orientation %d: size %v", o, b)
		}
	}
}

func TestDownscale(t *testing.T) {
	out := downscale(quadrants(400, 300), 160)
	if b := out.Bounds(); b.Dx() != 160 || b.Dy() != 120 {
		t.Fatalf("size %v", b)
	}
	if got := corner(out, false, false) + corner(out, true, true); got != "RW" {
		t.Errorf("colors %s", got)
	}
	// YCbCr (as decoded from a JPEG)
	ycc := image.NewYCbCr(image.Rect(0, 0, 300, 400), image.YCbCrSubsampleRatio420)
	for i := range ycc.Y {
		ycc.Y[i] = 200
	}
	for i := range ycc.Cb {
		ycc.Cb[i], ycc.Cr[i] = 128, 128
	}
	out = downscale(ycc, 160)
	if b := out.Bounds(); b.Dx() != 120 || b.Dy() != 160 {
		t.Fatalf("size %v", b)
	}
	if c := out.RGBAAt(60, 80); c.R != 200 || c.G != 200 || c.B != 200 {
		t.Errorf("color %v", c)
	}
	// small images keep their size
	if b := downscale(quadrants(50, 20), 160).Bounds(); b.Dx() != 50 || b.Dy() != 20 {
		t.Errorf("enlarged: %v", b)
	}
}

func TestCropToAspect(t *testing.T) {
	// 4:3 thumbnail for a 16:9 photo -> remove bars at top/bottom
	thumb := image.NewRGBA(image.Rect(0, 0, 160, 120))
	b := cropToAspect(thumb, 1920, 1080).Bounds()
	if b.Dx() != 160 || b.Dy() != 90 || b.Min.Y != 15 {
		t.Errorf("16:9: %v", b)
	}
	// portrait photo in landscape thumbnail -> remove left/right
	b = cropToAspect(thumb, 1080, 1440).Bounds()
	if b.Dx() != 90 || b.Dy() != 120 {
		t.Errorf("3:4: %v", b)
	}
	// matching aspect ratio is kept
	if b = cropToAspect(thumb, 4000, 3000).Bounds(); b.Dx() != 160 || b.Dy() != 120 {
		t.Errorf("4:3: %v", b)
	}
}
