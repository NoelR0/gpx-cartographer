// Command gen converts the Natural Earth admin-1 (states/provinces) and
// admin-0 (countries) data into the compact file embedded by package regions.
//
// Natural Earth data is in the public domain: https://www.naturalearthdata.com
//
// Usage (from the repository root):
//
//	go generate ./internal/regions
//
// The two zip files are downloaded unless their paths are given with -states
// and -countries.
package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
)

const (
	baseURL     = "https://naciscdn.org/naturalearth/10m/cultural/"
	statesZip   = "ne_10m_admin_1_states_provinces.zip"
	countryZip  = "ne_10m_admin_0_countries.zip"
	earthRadius = 6371.0088 // km, mean radius
)

func main() {
	states := flag.String("states", "", "path to "+statesZip+" (downloaded if empty)")
	countries := flag.String("countries", "", "path to "+countryZip+" (downloaded if empty)")
	out := flag.String("out", "regions.bin.gz", "output file")
	tol := flag.Float64("tol", 100, "simplification tolerance in meters")
	flag.Parse()

	sz, err := openZip(*states, statesZip)
	if err != nil {
		log.Fatal(err)
	}
	cz, err := openZip(*countries, countryZip)
	if err != nil {
		log.Fatal(err)
	}

	// countries: ADM0_A3 -> name, continent
	crecs, err := readDBF(zipFile(cz, ".dbf"))
	if err != nil {
		log.Fatal("countries: ", err)
	}
	type country struct{ name, continent string }
	byA3 := map[string]country{}
	for _, r := range crecs {
		byA3[r["ADM0_A3"]] = country{name: first(r["NAME_EN"], r["NAME"]), continent: r["CONTINENT"]}
	}

	srecs, err := readDBF(zipFile(sz, ".dbf"))
	if err != nil {
		log.Fatal("states: ", err)
	}
	shapes, err := readSHP(zipFile(sz, ".shp"))
	if err != nil {
		log.Fatal("states: ", err)
	}
	if len(shapes) != len(srecs) {
		log.Fatalf("states: %d shapes but %d records", len(shapes), len(srecs))
	}

	var (
		continents, countryNames []string
		countryCont              []int
		contIdx                  = map[string]int{}
		countryIdx               = map[string]int{}
	)
	type state struct {
		name    string
		country int
		areaKm2 float64
		rings   [][]float64 // lon, lat pairs
	}
	var list []state
	pointsIn, pointsOut := 0, 0
	for i, r := range srecs {
		a3 := r["adm0_a3"]
		c, ok := byA3[a3]
		if !ok {
			log.Printf("no country %q for state %q, using %q", a3, r["name"], r["admin"])
			c = country{name: r["admin"], continent: "Other"}
		}
		ci, ok := countryIdx[a3]
		if !ok {
			k, ok := contIdx[c.continent]
			if !ok {
				k = len(continents)
				contIdx[c.continent] = k
				continents = append(continents, c.continent)
			}
			ci = len(countryNames)
			countryIdx[a3] = ci
			countryNames = append(countryNames, c.name)
			countryCont = append(countryCont, k)
		}
		st := state{name: first(r["name_en"], r["name"]), country: ci}
		signed := 0.0
		for _, ring := range shapes[i] {
			signed += ringArea(ring)
			pointsIn += len(ring) / 2
			if s := simplifyRing(ring, *tol); len(s) >= 8 {
				st.rings = append(st.rings, s)
				pointsOut += len(s) / 2
			}
		}
		st.areaKm2 = math.Abs(signed)
		list = append(list, st)
	}
	for a3, c := range byA3 {
		if _, ok := countryIdx[a3]; !ok {
			log.Printf("country without states (not included): %s %s", a3, c.name)
		}
	}

	// write
	var buf bytes.Buffer
	buf.WriteString("GCR1")
	uv := func(v uint64) { buf.Write(binary.AppendUvarint(nil, v)) }
	sv := func(v int64) { buf.Write(binary.AppendVarint(nil, v)) }
	str := func(s string) { uv(uint64(len(s))); buf.WriteString(s) }
	uv(uint64(len(continents)))
	for _, s := range continents {
		str(s)
	}
	uv(uint64(len(countryNames)))
	for i, s := range countryNames {
		str(s)
		uv(uint64(countryCont[i]))
	}
	uv(uint64(len(list)))
	for _, st := range list {
		str(st.name)
		uv(uint64(st.country))
		uv(uint64(math.Round(st.areaKm2 * 100))) // hectares
		uv(uint64(len(st.rings)))
		for _, ring := range st.rings {
			uv(uint64(len(ring) / 2))
			var px, py int64
			for j := 0; j < len(ring); j += 2 {
				x, y := int64(math.Round(ring[j]*1e5)), int64(math.Round(ring[j+1]*1e5))
				sv(x - px)
				sv(y - py)
				px, py = x, y
			}
		}
	}

	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	gz, _ := gzip.NewWriterLevel(f, gzip.BestCompression)
	if _, err := gz.Write(buf.Bytes()); err != nil {
		log.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		log.Fatal(err)
	}
	info, _ := f.Stat()
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
	log.Printf("%d continents, %d countries, %d states; points %d -> %d; %s: %d KB",
		len(continents), len(countryNames), len(list), pointsIn, pointsOut, *out, info.Size()>>10)
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------- Input

func openZip(p, name string) (*zip.Reader, error) {
	var data []byte
	var err error
	if p != "" {
		data, err = os.ReadFile(p)
	} else {
		log.Printf("downloading %s", baseURL+name)
		var res *http.Response
		if res, err = http.Get(baseURL + name); err == nil {
			defer res.Body.Close()
			if res.StatusCode != http.StatusOK {
				return nil, fmt.Errorf("%s: HTTP %d", name, res.StatusCode)
			}
			data, err = io.ReadAll(res.Body)
		}
	}
	if err != nil {
		return nil, err
	}
	return zip.NewReader(bytes.NewReader(data), int64(len(data)))
}

func zipFile(z *zip.Reader, ext string) []byte {
	for _, f := range z.File {
		if path.Ext(f.Name) == ext {
			rc, err := f.Open()
			if err != nil {
				log.Fatal(err)
			}
			data, err := io.ReadAll(rc)
			if err != nil {
				log.Fatal(err)
			}
			return data
		}
	}
	log.Fatalf("no %s file in zip", ext)
	return nil
}

// readDBF reads all records of a dBase III file; values are trimmed strings.
func readDBF(d []byte) ([]map[string]string, error) {
	if len(d) < 32 {
		return nil, errors.New("dbf too short")
	}
	n := int(binary.LittleEndian.Uint32(d[4:]))
	headerLen := int(binary.LittleEndian.Uint16(d[8:]))
	recLen := int(binary.LittleEndian.Uint16(d[10:]))
	type field struct {
		name string
		off  int
		len  int
	}
	var fields []field
	off := 1 // deletion flag
	for i := 32; i+32 <= headerLen && d[i] != 0x0d; i += 32 {
		name := string(bytes.TrimRight(d[i:i+11], "\x00"))
		l := int(d[i+16])
		fields = append(fields, field{name, off, l})
		off += l
	}
	if headerLen+n*recLen > len(d) {
		return nil, errors.New("dbf truncated")
	}
	out := make([]map[string]string, n)
	for r := range n {
		rec := d[headerLen+r*recLen : headerLen+(r+1)*recLen]
		m := make(map[string]string, len(fields))
		for _, f := range fields {
			m[f.name] = strings.Trim(string(rec[f.off:f.off+f.len]), " \x00")
		}
		out[r] = m
	}
	return out, nil
}

// readSHP reads a polygon shapefile. Each shape is a list of rings; a ring is
// a flat list of lon, lat pairs.
func readSHP(d []byte) ([][][]float64, error) {
	if len(d) < 100 {
		return nil, errors.New("shp too short")
	}
	var shapes [][][]float64
	le := binary.LittleEndian
	for p := 100; p+8 <= len(d); {
		clen := int(binary.BigEndian.Uint32(d[p+4:])) * 2
		c := d[p+8 : p+8+clen]
		p += 8 + clen
		var rings [][]float64
		if typ := le.Uint32(c); typ == 5 {
			numParts, numPoints := int(le.Uint32(c[36:])), int(le.Uint32(c[40:]))
			parts := make([]int, numParts+1)
			for i := range numParts {
				parts[i] = int(le.Uint32(c[44+4*i:]))
			}
			parts[numParts] = numPoints
			pts := c[44+4*numParts:]
			for i := range numParts {
				ring := make([]float64, 0, 2*(parts[i+1]-parts[i]))
				for j := parts[i]; j < parts[i+1]; j++ {
					ring = append(ring, math.Float64frombits(le.Uint64(pts[16*j:])), math.Float64frombits(le.Uint64(pts[16*j+8:])))
				}
				rings = append(rings, ring)
			}
		} else if typ != 0 {
			return nil, fmt.Errorf("unsupported shape type %d", typ)
		}
		shapes = append(shapes, rings)
	}
	return shapes, nil
}

// ---------------------------------------------------------------- Geometry

// ringArea is the signed area of a ring on the sphere in km².
func ringArea(r []float64) float64 {
	const rad = math.Pi / 180
	sum := 0.0
	for i := 0; i+3 < len(r); i += 2 {
		sum += (r[i+2] - r[i]) * rad * (2 + math.Sin(r[i+1]*rad) + math.Sin(r[i+3]*rad))
	}
	return sum * earthRadius * earthRadius / 2
}

// simplifyRing applies Douglas-Peucker with tolerance tol (meters) to a
// closed ring of lon, lat pairs.
func simplifyRing(r []float64, tol float64) []float64 {
	n := len(r) / 2
	if n <= 4 {
		return r
	}
	kx := math.Cos(r[1]*math.Pi/180) * earthRadius * 1000 * math.Pi / 180
	ky := earthRadius * 1000 * math.Pi / 180
	x := func(i int) float64 { return r[2*i] * kx }
	y := func(i int) float64 { return r[2*i+1] * ky }
	keep := make([]bool, n)
	keep[0], keep[n-1] = true, true
	stack := [][2]int{{0, n - 1}}
	for len(stack) > 0 {
		a, b := stack[len(stack)-1][0], stack[len(stack)-1][1]
		stack = stack[:len(stack)-1]
		maxD, idx := -1.0, -1
		for i := a + 1; i < b; i++ {
			if d := segDist2(x(i), y(i), x(a), y(a), x(b), y(b)); d > maxD {
				maxD, idx = d, i
			}
		}
		if idx >= 0 && maxD > tol*tol {
			keep[idx] = true
			stack = append(stack, [2]int{a, idx}, [2]int{idx, b})
		}
	}
	out := make([]float64, 0, 64)
	for i, k := range keep {
		if k {
			out = append(out, r[2*i], r[2*i+1])
		}
	}
	return slices.Clip(out)
}

func segDist2(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	if dx != 0 || dy != 0 {
		t := ((px-ax)*dx + (py-ay)*dy) / (dx*dx + dy*dy)
		switch {
		case t > 1:
			ax, ay = bx, by
		case t > 0:
			ax, ay = ax+dx*t, ay+dy*t
		}
	}
	dx, dy = px-ax, py-ay
	return dx*dx + dy*dy
}
