// Package tiles computes the slippy-map tiles (Web Mercator) that tracks pass
// through and the tiles discovered by them, including small areas enclosed
// by discovered tiles. It mirrors the computation in
// web/tiles.js.
package tiles

import "math"

const earthRadius = 6371.0088 // km, mean radius

// Tile is a tile at a fixed zoom level.
type Tile struct{ X, Y uint32 }

// Visited returns all tiles at zoom z that the segments pass through. Each
// segment alternates latitude, longitude in 1e-7 degrees. Between two points
// every tile the straight line crosses is included, so long simplified
// segments do not skip tiles.
func Visited(segments [][]int32, z int) map[Tile]struct{} {
	n := float64(uint32(1) << z)
	out := map[Tile]struct{}{}
	add := func(x, y int) {
		if x >= 0 && y >= 0 && float64(x) < n && float64(y) < n {
			out[Tile{uint32(x), uint32(y)}] = struct{}{}
		}
	}
	for _, seg := range segments {
		var px, py float64
		for i := 0; i+1 < len(seg); i += 2 {
			x, y := tileX(float64(seg[i+1])/1e7, n), tileY(float64(seg[i])/1e7, n)
			if i == 0 {
				add(int(math.Floor(x)), int(math.Floor(y)))
			} else {
				line(px, py, x, y, add)
			}
			px, py = x, y
		}
	}
	return out
}

// Discovered returns the visited tiles plus their eight neighbours at zoom z.
// Neighbours wrap around the antimeridian; rows beyond the poles are dropped.
func Discovered(visited map[Tile]struct{}, z int) map[Tile]struct{} {
	n := int(uint32(1) << z)
	out := make(map[Tile]struct{}, len(visited)*3)
	for t := range visited {
		for dy := -1; dy <= 1; dy++ {
			y := int(t.Y) + dy
			if y < 0 || y >= n {
				continue
			}
			for dx := -1; dx <= 1; dx++ {
				x := (int(t.X) + dx + n) % n
				out[Tile{uint32(x), uint32(y)}] = struct{}{}
			}
		}
	}
	return out
}

// FillEnclosed adds every undiscovered area of at most max tiles that is
// completely surrounded by discovered tiles (4-connected, so a gap at a
// corner does not let an area escape) to discovered and returns the number
// of tiles added. Areas reaching a pole are not enclosed.
func FillEnclosed(discovered map[Tile]struct{}, z, max int) int {
	n := int(uint32(1) << z)
	seen := map[Tile]int{} // tile -> the fill that reached it
	var area []Tile
	// collects the area around start into area; false once it grows beyond
	// max, reaches a pole or runs into an area that did so before
	fill := func(start Tile, id int) bool {
		area = append(area[:0], start)
		seen[start] = id
		for i := 0; i < len(area); i++ {
			x, y := int(area[i].X), int(area[i].Y)
			for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				ny := y + d[1]
				if ny < 0 || ny >= n {
					return false
				}
				t := Tile{uint32((x + d[0] + n) % n), uint32(ny)}
				if _, ok := discovered[t]; ok {
					continue
				}
				if prev, ok := seen[t]; ok {
					if prev != id {
						return false
					}
					continue
				}
				if len(area) >= max {
					return false
				}
				seen[t] = id
				area = append(area, t)
			}
		}
		return true
	}
	var starts []Tile
	for t := range discovered {
		for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			ny := int(t.Y) + d[1]
			if ny < 0 || ny >= n {
				continue
			}
			nt := Tile{uint32((int(t.X) + d[0] + n) % n), uint32(ny)}
			if _, ok := discovered[nt]; !ok {
				starts = append(starts, nt)
			}
		}
	}
	added := 0
	for i, s := range starts {
		if _, ok := seen[s]; ok {
			continue
		}
		if fill(s, i) {
			for _, t := range area {
				discovered[t] = struct{}{}
			}
			added += len(area)
		}
	}
	return added
}

// line walks the tiles crossed by the line a–b (Amanatides & Woo).
func line(ax, ay, bx, by float64, add func(x, y int)) {
	x, y := int(math.Floor(ax)), int(math.Floor(ay))
	ex, ey := int(math.Floor(bx)), int(math.Floor(by))
	add(x, y)
	dx, dy := bx-ax, by-ay
	sx, sy := sign(dx), sign(dy)
	tdx, tdy := math.Inf(1), math.Inf(1)
	tx, ty := math.Inf(1), math.Inf(1)
	if dx != 0 {
		tdx = math.Abs(1 / dx)
		if sx > 0 {
			tx = (float64(x) + 1 - ax) * tdx
		} else {
			tx = (ax - float64(x)) * tdx
		}
	}
	if dy != 0 {
		tdy = math.Abs(1 / dy)
		if sy > 0 {
			ty = (float64(y) + 1 - ay) * tdy
		} else {
			ty = (ay - float64(y)) * tdy
		}
	}
	for steps := abs(ex-x) + abs(ey-y); steps > 0; steps-- {
		if tx < ty {
			x += sx
			tx += tdx
		} else {
			y += sy
			ty += tdy
		}
		add(x, y)
	}
}

func sign(v float64) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func tileX(lon, n float64) float64 { return (lon + 180) / 360 * n }

func tileY(lat, n float64) float64 {
	r := math.Max(-85.0511, math.Min(85.0511, lat)) * math.Pi / 180
	return (1 - math.Log(math.Tan(r)+1/math.Cos(r))/math.Pi) / 2 * n
}

func tileLat(y, n float64) float64 {
	return math.Atan(math.Sinh(math.Pi*(1-2*y/n))) * 180 / math.Pi
}

// Center returns the latitude and longitude of the tile's center.
func (t Tile) Center(z int) (lat, lon float64) {
	n := float64(uint32(1) << z)
	return tileLat(float64(t.Y)+0.5, n), (float64(t.X)+0.5)/n*360 - 180
}

// AreaKm2 is the area of the tile on the earth's surface.
func (t Tile) AreaKm2(z int) float64 {
	n := float64(uint32(1) << z)
	top := tileLat(float64(t.Y), n) * math.Pi / 180
	bottom := tileLat(float64(t.Y)+1, n) * math.Pi / 180
	return earthRadius * earthRadius * 2 * math.Pi / n * (math.Sin(top) - math.Sin(bottom))
}
