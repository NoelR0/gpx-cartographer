// Package tiles computes the slippy-map tiles (Web Mercator) that tracks pass
// through and the tiles discovered by them. It mirrors the computation in
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
