// Package regions finds the state/province, country and continent at a
// position, based on embedded Natural Earth data (public domain).
package regions

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

//go:generate go run ./gen -tol 300 -out regions.bin.gz

//go:embed regions.bin.gz
var data []byte

// DB holds all states with their outlines. The outlines take a few MB, so
// the data is only decoded on first use (see Load).
type DB struct {
	Continents []Area
	Countries  []Country
	States     []State
	World      Area

	index grid
}

// Area is a named region with its size in km².
type Area struct {
	Name    string
	AreaKm2 float64
}

type Country struct {
	Area
	Continent int
}

type State struct {
	Area
	Country int
	bbox    box
	rings   []ring
}

// ring is a closed outline in 1e-5 degrees, alternating lon, lat.
type ring struct {
	bbox box
	pts  []int32
}

type box struct{ minX, minY, maxX, maxY int32 }

func (b box) contains(x, y int32) bool {
	return x >= b.minX && x <= b.maxX && y >= b.minY && y <= b.maxY
}

var (
	loadOnce sync.Once
	db       *DB
	loadErr  error
)

// Load decodes the embedded data once and returns it.
func Load() (*DB, error) {
	loadOnce.Do(func() { db, loadErr = decode(data) })
	return db, loadErr
}

func decode(raw []byte) (*DB, error) {
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	r := &reader{r: bufio.NewReader(gz)}
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r.r, magic); err != nil || string(magic) != "GCR1" {
		return nil, errors.New("regions: invalid data")
	}
	d := &DB{World: Area{Name: "World"}}
	d.Continents = make([]Area, r.uint())
	for i := range d.Continents {
		d.Continents[i].Name = r.str()
	}
	d.Countries = make([]Country, r.uint())
	for i := range d.Countries {
		d.Countries[i].Name = r.str()
		d.Countries[i].Continent = int(r.uint())
	}
	d.States = make([]State, r.uint())
	for i := range d.States {
		s := &d.States[i]
		s.Name = r.str()
		s.Country = int(r.uint())
		s.AreaKm2 = float64(r.uint()) / 100
		s.bbox = emptyBox
		s.rings = make([]ring, r.uint())
		for j := range s.rings {
			pts := make([]int32, 2*r.uint())
			var x, y int32
			rb := emptyBox
			for k := 0; k < len(pts); k += 2 {
				x += int32(r.int())
				y += int32(r.int())
				pts[k], pts[k+1] = x, y
				rb = rb.extend(x, y)
			}
			s.rings[j] = ring{bbox: rb, pts: pts}
			s.bbox = s.bbox.union(rb)
		}
		if r.err != nil {
			return nil, fmt.Errorf("regions: %w", r.err)
		}
		if s.Country >= len(d.Countries) || d.Countries[s.Country].Continent >= len(d.Continents) {
			return nil, errors.New("regions: invalid reference")
		}
		c := &d.Countries[s.Country]
		c.AreaKm2 += s.AreaKm2
		d.Continents[c.Continent].AreaKm2 += s.AreaKm2
		d.World.AreaKm2 += s.AreaKm2
	}
	d.index = buildGrid(d.States)
	return d, nil
}

var emptyBox = box{1 << 30, 1 << 30, -1 << 30, -1 << 30}

func (b box) extend(x, y int32) box {
	return box{min(b.minX, x), min(b.minY, y), max(b.maxX, x), max(b.maxY, y)}
}

func (b box) union(o box) box {
	return box{min(b.minX, o.minX), min(b.minY, o.minY), max(b.maxX, o.maxX), max(b.maxY, o.maxY)}
}

type reader struct {
	r   *bufio.Reader
	err error
}

func (r *reader) uint() uint64 {
	v, err := binary.ReadUvarint(r.r)
	if err != nil && r.err == nil {
		r.err = err
	}
	return v
}

func (r *reader) int() int64 {
	v, err := binary.ReadVarint(r.r)
	if err != nil && r.err == nil {
		r.err = err
	}
	return v
}

func (r *reader) str() string {
	n := r.uint()
	if n > 1024 {
		r.err = errors.New("string too long")
		return ""
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r.r, b); err != nil && r.err == nil {
		r.err = err
	}
	return string(b)
}

// ---------------------------------------------------------------- Lookup

// grid lists, for every 1°×1° cell, the states whose bounding box touches it.
type grid struct {
	start []int32 // cell -> offset into items; len = cells+1
	items []uint16
}

const cellsX, cellsY = 360, 180

func cellOf(x, y int32) int {
	cx := min(max(int((x+180e5)/1e5), 0), cellsX-1)
	cy := min(max(int((y+90e5)/1e5), 0), cellsY-1)
	return cy*cellsX + cx
}

func buildGrid(states []State) grid {
	counts := make([]int32, cellsX*cellsY+1)
	visit := func(fn func(cell, state int)) {
		for i, s := range states {
			if len(s.rings) == 0 {
				continue
			}
			a, b := cellOf(s.bbox.minX, s.bbox.minY), cellOf(s.bbox.maxX, s.bbox.maxY)
			for cy := a / cellsX; cy <= b/cellsX; cy++ {
				for cx := a % cellsX; cx <= b%cellsX; cx++ {
					fn(cy*cellsX+cx, i)
				}
			}
		}
	}
	visit(func(cell, _ int) { counts[cell+1]++ })
	for i := 1; i < len(counts); i++ {
		counts[i] += counts[i-1]
	}
	g := grid{start: counts, items: make([]uint16, counts[len(counts)-1])}
	fill := make([]int32, cellsX*cellsY)
	copy(fill, counts)
	visit(func(cell, state int) {
		g.items[fill[cell]] = uint16(state)
		fill[cell]++
	})
	return g
}

// Lookup returns the index of the state at lat/lon, or -1 (e.g. at sea).
func (d *DB) Lookup(lat, lon float64) int {
	x, y := int32(lon*1e5), int32(lat*1e5)
	c := cellOf(x, y)
	for _, i := range d.index.items[d.index.start[c]:d.index.start[c+1]] {
		if s := &d.States[i]; s.bbox.contains(x, y) && s.contains(x, y) {
			return int(i)
		}
	}
	return -1
}

// contains uses the even-odd rule over all rings, so holes are handled.
// A ring whose bounding box does not contain the point cannot change the
// result and is skipped.
func (s *State) contains(x, y int32) bool {
	in := false
	px, py := float64(x), float64(y)
	for _, r := range s.rings {
		if !r.bbox.contains(x, y) {
			continue
		}
		p := r.pts
		for i, j := 0, len(p)-2; i < len(p); j, i = i, i+2 {
			xi, yi, xj, yj := float64(p[i]), float64(p[i+1]), float64(p[j]), float64(p[j+1])
			if (yi > py) != (yj > py) && px < (xj-xi)*(py-yi)/(yj-yi)+xi {
				in = !in
			}
		}
	}
	return in
}
