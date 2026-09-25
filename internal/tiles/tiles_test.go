package tiles

import (
	"math"
	"testing"
)

func pt(x, y float64, z int) (lat, lon int32) {
	n := float64(uint32(1) << z)
	return int32(math.Round(tileLat(y, n) * 1e7)), int32(math.Round((x/n*360 - 180) * 1e7))
}

func seg(z int, xy ...float64) []int32 {
	var s []int32
	for i := 0; i+1 < len(xy); i += 2 {
		lat, lon := pt(xy[i], xy[i+1], z)
		s = append(s, lat, lon)
	}
	return s
}

func TestVisited(t *testing.T) {
	// one long straight line per row over a 6×6 block, plus a diagonal
	var block []float64
	for r := range 6 {
		y := 5800 + float64(r) + 0.5
		if r%2 == 0 {
			block = append(block, 8500.3, y, 8505.7, y)
		} else {
			block = append(block, 8505.7, y, 8500.3, y)
		}
	}
	got := Visited([][]int32{seg(14, block...), seg(14, 200.1, 100.2, 203.7, 103.9)}, 14)
	if len(got) != 43 {
		t.Fatalf("got %d tiles, want 43", len(got))
	}
	for x := uint32(8500); x <= 8505; x++ {
		for y := uint32(5800); y <= 5805; y++ {
			if _, ok := got[Tile{x, y}]; !ok {
				t.Errorf("missing tile %d/%d", x, y)
			}
		}
	}
}

func TestDiscovered(t *testing.T) {
	visited := map[Tile]struct{}{
		{10, 10}: {}, {11, 10}: {}, // neighbours overlap
		{0, 0}: {}, // wraps in x, clipped in y
	}
	got := Discovered(visited, 4)
	if len(got) != 12+6 {
		t.Fatalf("got %d tiles, want 18", len(got))
	}
	for _, want := range []Tile{{9, 9}, {12, 11}, {15, 0}, {15, 1}, {1, 1}} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing tile %v", want)
		}
	}
}

func TestArea(t *testing.T) {
	// all tiles of a zoom level cover the earth between ±85.05°
	sum := 0.0
	for y := uint32(0); y < 1<<6; y++ {
		sum += Tile{0, y}.AreaKm2(6) * (1 << 6)
	}
	want := 4 * math.Pi * earthRadius * earthRadius * math.Sin(85.0511*math.Pi/180)
	if math.Abs(sum-want)/want > 1e-4 {
		t.Errorf("sum %.0f, want %.0f", sum, want)
	}
}
