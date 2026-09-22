package photo

import (
	"image"
	"image/color"
	"math"
)

// downscale shrinks src to fit into limit×limit (area averaging). Smaller
// images are not enlarged.
func downscale(src image.Image, limit int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dw, dh := sw, sh
	if sw > limit || sh > limit {
		if sw >= sh {
			dw, dh = limit, max(1, sh*limit/sw)
		} else {
			dw, dh = max(1, sw*limit/sh), limit
		}
	}

	// per target pixel: sum of R, G, B and count
	acc := make([]uint32, dw*dh*4)
	xmap := make([]int, sw)
	for x := range xmap {
		xmap[x] = x * dw / sw
	}

	for y := 0; y < sh; y++ {
		row := (y * dh / sh) * dw
		switch img := src.(type) {
		case *image.YCbCr:
			for x := 0; x < sw; x++ {
				yi := img.YOffset(b.Min.X+x, b.Min.Y+y)
				ci := img.COffset(b.Min.X+x, b.Min.Y+y)
				r, g, bb := color.YCbCrToRGB(img.Y[yi], img.Cb[ci], img.Cr[ci])
				a := acc[(row+xmap[x])*4:]
				a[0] += uint32(r)
				a[1] += uint32(g)
				a[2] += uint32(bb)
				a[3]++
			}
		case *image.Gray:
			for x := 0; x < sw; x++ {
				v := uint32(img.Pix[img.PixOffset(b.Min.X+x, b.Min.Y+y)])
				a := acc[(row+xmap[x])*4:]
				a[0] += v
				a[1] += v
				a[2] += v
				a[3]++
			}
		case *image.RGBA:
			addRGBA(acc, img.Pix[img.PixOffset(b.Min.X, b.Min.Y+y):], sw, row, xmap)
		case *image.NRGBA:
			addRGBA(acc, img.Pix[img.PixOffset(b.Min.X, b.Min.Y+y):], sw, row, xmap)
		default:
			// slow, generic path (e.g. CMYK JPEG, paletted PNG)
			for x := 0; x < sw; x++ {
				r, g, bb, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
				a := acc[(row+xmap[x])*4:]
				a[0] += r >> 8
				a[1] += g >> 8
				a[2] += bb >> 8
				a[3]++
			}
		}
	}

	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for i := 0; i < dw*dh; i++ {
		a := acc[i*4:]
		n := a[3]
		if n == 0 {
			n = 1
		}
		dst.Pix[i*4+0] = uint8(a[0] / n)
		dst.Pix[i*4+1] = uint8(a[1] / n)
		dst.Pix[i*4+2] = uint8(a[2] / n)
		dst.Pix[i*4+3] = 0xFF
	}
	return dst
}

// addRGBA accumulates a row of RGBA/NRGBA pixels (alpha is ignored).
func addRGBA(acc []uint32, pix []uint8, sw, row int, xmap []int) {
	for x := 0; x < sw; x++ {
		p := pix[x*4:]
		a := acc[(row+xmap[x])*4:]
		a[0] += uint32(p[0])
		a[1] += uint32(p[1])
		a[2] += uint32(p[2])
		a[3]++
	}
}

// orient rotates or flips img according to the EXIF orientation (1–8) so
// that it is displayed upright.
func orient(img *image.RGBA, o int) *image.RGBA {
	if o <= 1 || o > 8 {
		return img
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for dy := 0; dy < dh; dy++ {
		for dx := 0; dx < dw; dx++ {
			// source pixel for target pixel (dx, dy)
			var sx, sy int
			switch o {
			case 2: // flipped horizontally
				sx, sy = w-1-dx, dy
			case 3: // rotated 180°
				sx, sy = w-1-dx, h-1-dy
			case 4: // flipped vertically
				sx, sy = dx, h-1-dy
			case 5: // transposed (flipped along the main diagonal)
				sx, sy = dy, dx
			case 6: // needs a 90° clockwise rotation
				sx, sy = dy, h-1-dx
			case 7: // transversed (flipped along the anti-diagonal)
				sx, sy = w-1-dy, h-1-dx
			case 8: // needs a 90° counterclockwise rotation
				sx, sy = w-1-dy, dx
			}
			copy(dst.Pix[dst.PixOffset(dx, dy):][:4], img.Pix[img.PixOffset(sx, sy):][:4])
		}
	}
	return dst
}

// cropToAspect center-crops img to the aspect ratio w:h. Some cameras always
// embed the thumbnail as 4:3 and fill the rest with black bars when the photo
// is e.g. 16:9.
func cropToAspect(img image.Image, w, h int) image.Image {
	b := img.Bounds()
	tw, th := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || tw <= 0 || th <= 0 {
		return img
	}
	want := float64(w) / float64(h)
	have := float64(tw) / float64(th)
	if math.Abs(want-have)/want < 0.03 {
		return img
	}
	r := b
	if have > want { // too wide -> crop left/right
		nw := int(float64(th)*want + 0.5)
		r.Min.X += (tw - nw) / 2
		r.Max.X = r.Min.X + nw
	} else { // too tall -> crop top/bottom
		nh := int(float64(tw)/want + 0.5)
		r.Min.Y += (th - nh) / 2
		r.Max.Y = r.Min.Y + nh
	}
	if s, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return s.SubImage(r)
	}
	return img
}
