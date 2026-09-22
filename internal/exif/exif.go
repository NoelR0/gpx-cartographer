// Package exif liest die für GPX Cartographer relevanten Metadaten aus JPEG- und
// PNG-Dateien: Bildgrösse, Ausrichtung, GPS-Position, Aufnahmezeit und das
// eingebettete Vorschaubild. Es wird nur der Dateikopf gelesen, nie die
// eigentlichen Bilddaten.
//
// Implementiert nach der EXIF-2.32- bzw. TIFF-6.0-Spezifikation.
package exif

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Meta enthält alles, was aus dem Dateikopf gelesen wurde.
type Meta struct {
	// Grösse der gespeicherten Pixel, ohne Berücksichtigung der Ausrichtung.
	Width, Height int
	// EXIF-Ausrichtung 1–8 (1 = normal).
	Orientation int

	HasGPS   bool
	Lat, Lon float64

	// Aufnahmezeit im EXIF-Format "2006:01:02 15:04:05" (Ortszeit der Kamera).
	DateTime string
	// Zeitzonen-Offset wie "+02:00", falls von der Kamera gespeichert.
	Offset string

	// Eingebettetes JPEG-Vorschaubild (IFD1), nil wenn nicht vorhanden.
	Thumbnail []byte
}

var ErrNotSupported = errors.New("exif: dateiformat nicht unterstützt")

// Read erkennt das Format anhand der ersten Bytes und liest die Metadaten.
func Read(r io.Reader) (*Meta, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	head, err := br.Peek(8)
	if err != nil {
		return nil, err
	}
	switch {
	case head[0] == 0xFF && head[1] == 0xD8:
		return readJPEG(br)
	case bytes.Equal(head, pngSignature):
		return readPNG(br)
	}
	return nil, ErrNotSupported
}

// ---------------------------------------------------------------- JPEG

func readJPEG(r *bufio.Reader) (*Meta, error) {
	m := &Meta{Orientation: 1}
	if _, err := r.Discard(2); err != nil { // SOI
		return nil, err
	}
	var exifSeen, sizeSeen bool
	for {
		// Marker suchen; beliebig viele 0xFF-Füllbytes sind erlaubt.
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if b != 0xFF {
			continue
		}
		marker, err := r.ReadByte()
		for err == nil && marker == 0xFF {
			marker, err = r.ReadByte()
		}
		if err != nil {
			return nil, err
		}
		switch {
		case marker == 0xD8, marker == 0x01, marker >= 0xD0 && marker <= 0xD7:
			continue // Marker ohne Nutzdaten
		case marker == 0xD9 || marker == 0xDA:
			// EOI oder Start of Scan: ab hier folgen nur noch Bilddaten
			if !sizeSeen {
				return nil, errors.New("exif: keine Bildgrösse im JPEG-Kopf")
			}
			return m, nil
		}

		var lenBuf [2]byte
		if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
			return nil, err
		}
		n := int(binary.BigEndian.Uint16(lenBuf[:])) - 2
		if n < 0 {
			return nil, errors.New("exif: ungültige Segmentlänge")
		}

		switch {
		case marker == 0xE1 && !exifSeen:
			seg := make([]byte, n)
			if _, err := io.ReadFull(r, seg); err != nil {
				return nil, err
			}
			if bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
				exifSeen = true
				// Fehlerhafte EXIF-Daten sind kein Grund, das Foto zu verwerfen.
				_ = parseTIFF(seg[6:], m)
			}
		case isSOF(marker):
			seg := make([]byte, n)
			if _, err := io.ReadFull(r, seg); err != nil {
				return nil, err
			}
			if len(seg) >= 5 {
				m.Height = int(binary.BigEndian.Uint16(seg[1:3]))
				m.Width = int(binary.BigEndian.Uint16(seg[3:5]))
				sizeSeen = true
			}
		default:
			if _, err := r.Discard(n); err != nil {
				return nil, err
			}
		}
	}
}

// SOF0–SOF15 ausser DHT (C4), JPG (C8) und DAC (CC).
func isSOF(m byte) bool {
	return m >= 0xC0 && m <= 0xCF && m != 0xC4 && m != 0xC8 && m != 0xCC
}

// ---------------------------------------------------------------- PNG

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}

func readPNG(r *bufio.Reader) (*Meta, error) {
	m := &Meta{Orientation: 1}
	if _, err := r.Discard(8); err != nil {
		return nil, err
	}
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil, err
		}
		n := int(binary.BigEndian.Uint32(hdr[:4]))
		typ := string(hdr[4:8])
		switch typ {
		case "IHDR", "eXIf":
			if n > 16<<20 {
				return nil, errors.New("exif: PNG-Chunk zu gross")
			}
			data := make([]byte, n)
			if _, err := io.ReadFull(r, data); err != nil {
				return nil, err
			}
			if typ == "IHDR" && n >= 8 {
				m.Width = int(binary.BigEndian.Uint32(data[0:4]))
				m.Height = int(binary.BigEndian.Uint32(data[4:8]))
			} else if typ == "eXIf" {
				_ = parseTIFF(data, m)
			}
			if _, err := r.Discard(4); err != nil { // CRC
				return nil, err
			}
		case "IDAT", "IEND":
			// eXIf muss laut Spezifikation vor IDAT stehen
			return m, nil
		default:
			if _, err := r.Discard(n + 4); err != nil {
				return nil, err
			}
		}
	}
}

// ---------------------------------------------------------------- TIFF/EXIF

const (
	tagOrientation     = 0x0112
	tagDateTime        = 0x0132
	tagExifIFD         = 0x8769
	tagGPSIFD          = 0x8825
	tagThumbOffset     = 0x0201
	tagThumbLength     = 0x0202
	tagDateTimeOrig    = 0x9003
	tagOffsetTime      = 0x9010
	tagOffsetTimeOrig  = 0x9011
	tagGPSLatitudeRef  = 0x0001
	tagGPSLatitude     = 0x0002
	tagGPSLongitudeRef = 0x0003
	tagGPSLongitude    = 0x0004
)

// Byte-Grösse der TIFF-Datentypen (Index = Typ-Nummer).
var typeSize = [...]int{0, 1, 1, 2, 4, 8, 1, 1, 2, 4, 8, 4, 8}

type entry struct {
	typ   uint16
	count uint32
	raw   [4]byte // Wert oder Offset
}

type tiff struct {
	data []byte
	bo   binary.ByteOrder
}

func parseTIFF(data []byte, m *Meta) error {
	if len(data) < 8 {
		return errors.New("exif: TIFF-Kopf zu kurz")
	}
	t := &tiff{data: data}
	switch string(data[:2]) {
	case "II":
		t.bo = binary.LittleEndian
	case "MM":
		t.bo = binary.BigEndian
	default:
		return errors.New("exif: unbekannte Byte-Reihenfolge")
	}
	if t.bo.Uint16(data[2:4]) != 42 {
		return errors.New("exif: keine TIFF-Kennung")
	}

	ifd0, next, err := t.readIFD(t.bo.Uint32(data[4:8]))
	if err != nil {
		return err
	}
	if e, ok := ifd0[tagOrientation]; ok {
		if o := int(t.uint(e)); o >= 1 && o <= 8 {
			m.Orientation = o
		}
	}
	if e, ok := ifd0[tagDateTime]; ok {
		m.DateTime = t.ascii(e)
	}

	if e, ok := ifd0[tagExifIFD]; ok {
		if sub, _, err := t.readIFD(t.uint(e)); err == nil {
			if e, ok := sub[tagDateTimeOrig]; ok {
				if s := t.ascii(e); s != "" {
					m.DateTime = s
				}
			}
			if e, ok := sub[tagOffsetTimeOrig]; ok {
				m.Offset = t.ascii(e)
			} else if e, ok := sub[tagOffsetTime]; ok {
				m.Offset = t.ascii(e)
			}
		}
	}

	if e, ok := ifd0[tagGPSIFD]; ok {
		if gps, _, err := t.readIFD(t.uint(e)); err == nil {
			t.readGPS(gps, m)
		}
	}

	// IFD1 enthält das Vorschaubild
	if next != 0 {
		if ifd1, _, err := t.readIFD(next); err == nil {
			off, ok1 := ifd1[tagThumbOffset]
			n, ok2 := ifd1[tagThumbLength]
			if ok1 && ok2 {
				start, length := int(t.uint(off)), int(t.uint(n))
				if start > 0 && length > 2 && start+length <= len(data) &&
					data[start] == 0xFF && data[start+1] == 0xD8 {
					m.Thumbnail = data[start : start+length]
				}
			}
		}
	}
	return nil
}

func (t *tiff) readIFD(off uint32) (map[uint16]entry, uint32, error) {
	o := int(off)
	if o < 8 || o+2 > len(t.data) {
		return nil, 0, fmt.Errorf("exif: IFD-Offset %d ausserhalb der Daten", off)
	}
	n := int(t.bo.Uint16(t.data[o:]))
	o += 2
	if n > 1000 || o+n*12+4 > len(t.data) {
		return nil, 0, errors.New("exif: IFD abgeschnitten")
	}
	entries := make(map[uint16]entry, n)
	for i := 0; i < n; i++ {
		p := t.data[o+i*12:]
		var e entry
		e.typ = t.bo.Uint16(p[2:4])
		e.count = t.bo.Uint32(p[4:8])
		copy(e.raw[:], p[8:12])
		entries[t.bo.Uint16(p[0:2])] = e
	}
	next := t.bo.Uint32(t.data[o+n*12:])
	return entries, next, nil
}

// bytes liefert die Rohdaten eines Eintrags (inline oder über den Offset).
func (t *tiff) bytes(e entry) []byte {
	if int(e.typ) >= len(typeSize) || e.typ == 0 {
		return nil
	}
	size := uint64(typeSize[e.typ]) * uint64(e.count)
	if size <= 4 {
		return e.raw[:size]
	}
	off := uint64(t.bo.Uint32(e.raw[:]))
	if off+size > uint64(len(t.data)) {
		return nil
	}
	return t.data[off : off+size]
}

func (t *tiff) uint(e entry) uint32 {
	b := t.bytes(e)
	switch {
	case (e.typ == 3 || e.typ == 8) && len(b) >= 2: // SHORT, SSHORT
		return uint32(t.bo.Uint16(b))
	case (e.typ == 4 || e.typ == 9 || e.typ == 13) && len(b) >= 4: // LONG, SLONG, IFD
		return t.bo.Uint32(b)
	case (e.typ == 1 || e.typ == 7) && len(b) >= 1:
		return uint32(b[0])
	}
	return 0
}

func (t *tiff) ascii(e entry) string {
	b := t.bytes(e)
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return strings.TrimSpace(string(b))
}

// rationals liest vorzeichenlose Brüche (Typ 5) als float64.
func (t *tiff) rationals(e entry) []float64 {
	if e.typ != 5 && e.typ != 10 {
		return nil
	}
	b := t.bytes(e)
	out := make([]float64, 0, len(b)/8)
	for i := 0; i+8 <= len(b); i += 8 {
		num, den := t.bo.Uint32(b[i:]), t.bo.Uint32(b[i+4:])
		if den == 0 {
			return nil
		}
		if e.typ == 10 {
			out = append(out, float64(int32(num))/float64(int32(den)))
		} else {
			out = append(out, float64(num)/float64(den))
		}
	}
	return out
}

func (t *tiff) readGPS(gps map[uint16]entry, m *Meta) {
	latE, ok1 := gps[tagGPSLatitude]
	lonE, ok2 := gps[tagGPSLongitude]
	if !ok1 || !ok2 {
		return
	}
	lat, okLat := dmsToDeg(t.rationals(latE))
	lon, okLon := dmsToDeg(t.rationals(lonE))
	if !okLat || !okLon {
		return
	}
	if e, ok := gps[tagGPSLatitudeRef]; ok && strings.EqualFold(t.ascii(e), "S") {
		lat = -lat
	}
	if e, ok := gps[tagGPSLongitudeRef]; ok && strings.EqualFold(t.ascii(e), "W") {
		lon = -lon
	}
	// 0/0 ("Null Island") schreiben manche Apps, wenn sie keinen Fix hatten
	if lat == 0 && lon == 0 || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return
	}
	m.HasGPS, m.Lat, m.Lon = true, lat, lon
}

func dmsToDeg(v []float64) (float64, bool) {
	switch len(v) {
	case 3:
		return v[0] + v[1]/60 + v[2]/3600, true
	case 2:
		return v[0] + v[1]/60, true
	case 1:
		return v[0], true
	}
	return 0, false
}
