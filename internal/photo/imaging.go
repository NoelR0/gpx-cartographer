package photo

import (
	"image"
	"image/color"
	"math"
)

// downscale verkleinert src so, dass es in limit×limit passt
// (Flächenmittelung). Kleinere Bilder werden nicht vergrössert.
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

	// Pro Zielpixel Summe von R, G, B und Anzahl
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
			// Langsamer, allgemeiner Weg (z. B. CMYK-JPEG, Paletten-PNG)
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

// addRGBA summiert eine Zeile RGBA/NRGBA-Pixel (Alpha wird ignoriert).
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

// orient dreht bzw. spiegelt img entsprechend der EXIF-Ausrichtung (1–8),
// sodass es aufrecht angezeigt wird.
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
			// Quellpixel zum Zielpixel (dx, dy)
			var sx, sy int
			switch o {
			case 2: // horizontal gespiegelt
				sx, sy = w-1-dx, dy
			case 3: // 180° gedreht
				sx, sy = w-1-dx, h-1-dy
			case 4: // vertikal gespiegelt
				sx, sy = dx, h-1-dy
			case 5: // an der Hauptdiagonale gespiegelt
				sx, sy = dy, dx
			case 6: // muss 90° im Uhrzeigersinn gedreht werden
				sx, sy = dy, h-1-dx
			case 7: // an der Nebendiagonale gespiegelt
				sx, sy = w-1-dy, h-1-dx
			case 8: // muss 90° gegen den Uhrzeigersinn gedreht werden
				sx, sy = w-1-dy, dx
			}
			copy(dst.Pix[dst.PixOffset(dx, dy):][:4], img.Pix[img.PixOffset(sx, sy):][:4])
		}
	}
	return dst
}

// cropToAspect schneidet img mittig auf das Seitenverhältnis w:h zu. Manche
// Kameras betten das Vorschaubild immer als 4:3 ein und füllen den Rest mit
// schwarzen Balken, wenn das Foto z. B. 16:9 ist.
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
	if have > want { // zu breit -> links/rechts abschneiden
		nw := int(float64(th)*want + 0.5)
		r.Min.X += (tw - nw) / 2
		r.Max.X = r.Min.X + nw
	} else { // zu hoch -> oben/unten abschneiden
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
