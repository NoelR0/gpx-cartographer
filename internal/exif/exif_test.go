package exif

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"math"
	"testing"
)

// ---------------------------------------------------------------- building test data

type tEntry struct {
	tag   uint16
	typ   uint16
	count uint32
	data  []byte // raw data; for pointers to other IFDs, ptr is used
	ptr   int    // index of an IFD whose offset is written (-1 = none)
	thumb bool   // write the thumbnail offset
}

type tIFD []tEntry

func ascii(bo binary.ByteOrder, tag uint16, s string) tEntry {
	return tEntry{tag: tag, typ: 2, count: uint32(len(s) + 1), data: append([]byte(s), 0), ptr: -1}
}

func short(bo binary.ByteOrder, tag uint16, v uint16) tEntry {
	b := make([]byte, 2)
	bo.PutUint16(b, v)
	return tEntry{tag: tag, typ: 3, count: 1, data: b, ptr: -1}
}

func long(bo binary.ByteOrder, tag uint16, v uint32) tEntry {
	b := make([]byte, 4)
	bo.PutUint32(b, v)
	return tEntry{tag: tag, typ: 4, count: 1, data: b, ptr: -1}
}

func rationals(bo binary.ByteOrder, tag uint16, vals ...[2]uint32) tEntry {
	b := make([]byte, 8*len(vals))
	for i, v := range vals {
		bo.PutUint32(b[i*8:], v[0])
		bo.PutUint32(b[i*8+4:], v[1])
	}
	return tEntry{tag: tag, typ: 5, count: uint32(len(vals)), data: b, ptr: -1}
}

func pointer(tag uint16, ifd int) tEntry {
	return tEntry{tag: tag, typ: 4, count: 1, ptr: ifd}
}

// buildTIFF lays out the IFDs one after another. ifds[0] is IFD0, next1 is the
// index of IFD1 (or -1). The thumbnail offset is written for entries with
// thumb=true.
func buildTIFF(bo binary.ByteOrder, ifds []tIFD, next1 int, thumb []byte) []byte {
	sizeOf := func(ifd tIFD) int {
		n := 2 + 12*len(ifd) + 4
		for _, e := range ifd {
			if len(e.data) > 4 {
				n += len(e.data) + len(e.data)%2
			}
		}
		return n
	}
	offsets := make([]int, len(ifds))
	off := 8
	for i, ifd := range ifds {
		offsets[i] = off
		off += sizeOf(ifd)
	}
	thumbOff := off

	out := make([]byte, off, off+len(thumb))
	if bo == binary.LittleEndian {
		copy(out, "II")
	} else {
		copy(out, "MM")
	}
	bo.PutUint16(out[2:], 42)
	bo.PutUint32(out[4:], uint32(offsets[0]))

	for i, ifd := range ifds {
		p := offsets[i]
		bo.PutUint16(out[p:], uint16(len(ifd)))
		dataOff := p + 2 + 12*len(ifd) + 4
		for j, e := range ifd {
			q := p + 2 + 12*j
			bo.PutUint16(out[q:], e.tag)
			bo.PutUint16(out[q+2:], e.typ)
			bo.PutUint32(out[q+4:], e.count)
			switch {
			case e.ptr >= 0:
				bo.PutUint32(out[q+8:], uint32(offsets[e.ptr]))
			case e.thumb:
				bo.PutUint32(out[q+8:], uint32(thumbOff))
			case len(e.data) <= 4:
				copy(out[q+8:q+12], e.data)
			default:
				bo.PutUint32(out[q+8:], uint32(dataOff))
				copy(out[dataOff:], e.data)
				dataOff += len(e.data) + len(e.data)%2
			}
		}
		next := uint32(0)
		if i == 0 && next1 >= 0 {
			next = uint32(offsets[next1])
		}
		bo.PutUint32(out[p+2+12*len(ifd):], next)
	}
	return append(out, thumb...)
}

// wrapJPEG creates a small JPEG and inserts the TIFF data as APP1.
func wrapJPEG(t *testing.T, w, h int, tiff []byte) []byte {
	t.Helper()
	var img bytes.Buffer
	if err := jpeg.Encode(&img, image.NewGray(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	var out bytes.Buffer
	out.Write(img.Bytes()[:2]) // SOI
	out.Write(seg)
	out.Write(payload)
	out.Write(img.Bytes()[2:])
	return out.Bytes()
}

func smallJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewGray(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func fullExample(t *testing.T, bo binary.ByteOrder) []byte {
	thumb := smallJPEG(t, 32, 24)
	ifd0 := tIFD{
		short(bo, tagOrientation, 6),
		ascii(bo, tagDateTime, "2020:01:01 00:00:00"),
		pointer(tagExifIFD, 1),
		pointer(tagGPSIFD, 2),
	}
	exifIFD := tIFD{
		ascii(bo, tagDateTimeOrig, "2026:08:15 10:30:15"),
		ascii(bo, tagOffsetTimeOrig, "+02:00"),
	}
	gps := tIFD{
		ascii(bo, tagGPSLatitudeRef, "N"),
		rationals(bo, tagGPSLatitude, [2]uint32{47, 1}, [2]uint32{22, 1}, [2]uint32{1234, 100}),
		ascii(bo, tagGPSLongitudeRef, "W"),
		rationals(bo, tagGPSLongitude, [2]uint32{8, 1}, [2]uint32{32, 1}, [2]uint32{0, 1}),
	}
	ifd1 := tIFD{
		short(bo, 0x0103, 6),
		{tag: tagThumbOffset, typ: 4, count: 1, ptr: -1, thumb: true},
		long(bo, tagThumbLength, uint32(len(thumb))),
	}
	return wrapJPEG(t, 64, 48, buildTIFF(bo, []tIFD{ifd0, exifIFD, gps, ifd1}, 3, thumb))
}

// ---------------------------------------------------------------- Tests

func TestReadJPEG(t *testing.T) {
	for _, bo := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		t.Run(bo.String(), func(t *testing.T) {
			m, err := Read(bytes.NewReader(fullExample(t, bo)))
			if err != nil {
				t.Fatal(err)
			}
			if m.Width != 64 || m.Height != 48 {
				t.Errorf("size = %dx%d, want 64x48", m.Width, m.Height)
			}
			if m.Orientation != 6 {
				t.Errorf("orientation = %d", m.Orientation)
			}
			if !m.HasGPS {
				t.Fatal("no GPS data")
			}
			wantLat := 47 + 22.0/60 + 12.34/3600
			if math.Abs(m.Lat-wantLat) > 1e-9 || math.Abs(m.Lon-(-(8+32.0/60))) > 1e-9 {
				t.Errorf("position = %v, %v", m.Lat, m.Lon)
			}
			if m.DateTime != "2026:08:15 10:30:15" || m.Offset != "+02:00" {
				t.Errorf("time = %q %q (DateTimeOriginal must override DateTime)", m.DateTime, m.Offset)
			}
			thumb, err := jpeg.DecodeConfig(bytes.NewReader(m.Thumbnail))
			if err != nil || thumb.Width != 32 || thumb.Height != 24 {
				t.Errorf("thumbnail: %v %+v", err, thumb)
			}
		})
	}
}

func TestReadJPEGWithoutExif(t *testing.T) {
	m, err := Read(bytes.NewReader(smallJPEG(t, 10, 7)))
	if err != nil {
		t.Fatal(err)
	}
	if m.Width != 10 || m.Height != 7 || m.HasGPS || m.Orientation != 1 || m.Thumbnail != nil {
		t.Errorf("unexpected: %+v", m)
	}
}

func TestNullIslandIgnored(t *testing.T) {
	bo := binary.LittleEndian
	gps := tIFD{
		ascii(bo, tagGPSLatitudeRef, "N"),
		rationals(bo, tagGPSLatitude, [2]uint32{0, 1}, [2]uint32{0, 1}, [2]uint32{0, 1}),
		ascii(bo, tagGPSLongitudeRef, "E"),
		rationals(bo, tagGPSLongitude, [2]uint32{0, 1}, [2]uint32{0, 1}, [2]uint32{0, 1}),
	}
	data := wrapJPEG(t, 8, 8, buildTIFF(bo, []tIFD{{pointer(tagGPSIFD, 1)}, gps}, -1, nil))
	m, err := Read(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if m.HasGPS {
		t.Error("0/0 must not count as a position")
	}
}

func TestCorruptExifDoesNotFail(t *testing.T) {
	// Broken EXIF data: the photo must still be read with its size
	data := wrapJPEG(t, 12, 9, []byte("II*\x00\xff\xff\xff\x7f garbage"))
	m, err := Read(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if m.Width != 12 || m.Height != 9 {
		t.Errorf("size = %dx%d", m.Width, m.Height)
	}
}

func TestTruncatedAndGarbage(t *testing.T) {
	full := fullExample(t, binary.LittleEndian)
	// Every truncated version may at most return an error, never panic.
	for n := 0; n < len(full); n += 7 {
		_, _ = Read(bytes.NewReader(full[:n]))
	}
	if _, err := Read(bytes.NewReader([]byte("definitely not an image"))); err == nil {
		t.Error("expected an error")
	}
}

func TestReadPNG(t *testing.T) {
	bo := binary.BigEndian
	tiff := buildTIFF(bo, []tIFD{{short(bo, tagOrientation, 3)}}, -1, nil)

	chunk := func(typ string, data []byte) []byte {
		b := make([]byte, 8, 12+len(data))
		binary.BigEndian.PutUint32(b, uint32(len(data)))
		copy(b[4:], typ)
		b = append(b, data...)
		return append(b, 0, 0, 0, 0) // CRC is not checked
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 300)
	binary.BigEndian.PutUint32(ihdr[4:], 200)

	var png bytes.Buffer
	png.Write(pngSignature)
	png.Write(chunk("IHDR", ihdr))
	png.Write(chunk("tEXt", []byte("Comment\x00hello")))
	png.Write(chunk("eXIf", tiff))
	png.Write(chunk("IDAT", []byte{1, 2, 3}))

	m, err := Read(&png)
	if err != nil {
		t.Fatal(err)
	}
	if m.Width != 300 || m.Height != 200 || m.Orientation != 3 {
		t.Errorf("unexpected: %+v", m)
	}
}
