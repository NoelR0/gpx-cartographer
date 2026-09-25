package regions

import (
	"math"
	"runtime"
	"testing"
	"time"
)

func TestLookup(t *testing.T) {
	d, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name               string
		lat, lon           float64
		state, country, co string
	}{
		{"Pforzheim", 48.89, 8.70, "Baden-Württemberg", "Germany", "Europe"},
		{"Zurich", 47.37, 8.54, "Zürich", "Switzerland", "Europe"},
		{"Denver", 39.74, -104.99, "Colorado", "United States of America", "North America"},
		{"Sydney", -33.87, 151.21, "New South Wales", "Australia", "Oceania"},
		{"Tokyo", 35.68, 139.69, "Tokyo", "Japan", "Asia"},
		{"Lake Constance island Mainau", 47.705, 9.195, "Baden-Württemberg", "Germany", "Europe"},
	}
	for _, c := range cases {
		i := d.Lookup(c.lat, c.lon)
		if i < 0 {
			t.Errorf("%s: not found", c.name)
			continue
		}
		s := d.States[i]
		country := d.Countries[s.Country]
		if s.Name != c.state || country.Name != c.country || d.Continents[country.Continent].Name != c.co {
			t.Errorf("%s: got %s / %s / %s", c.name, s.Name, country.Name, d.Continents[country.Continent].Name)
		}
	}
	if i := d.Lookup(40, -30); i >= 0 {
		t.Errorf("Atlantic: got %s", d.States[i].Name)
	}
}

func TestAreas(t *testing.T) {
	d, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	// reference values; Natural Earth outlines are generalized, so allow some slack
	want := map[string]float64{"Germany": 357_600, "Switzerland": 41_285, "France": 643_800}
	for _, c := range d.Countries {
		if w, ok := want[c.Name]; ok {
			if math.Abs(c.AreaKm2-w)/w > 0.03 {
				t.Errorf("%s: area %.0f km², want ≈ %.0f", c.Name, c.AreaKm2, w)
			}
			delete(want, c.Name)
		}
	}
	for name := range want {
		t.Errorf("%s: missing", name)
	}
	// land area of the earth incl. Antarctica ≈ 149 million km²
	if w := d.World.AreaKm2; w < 140e6 || w > 155e6 {
		t.Errorf("world: %.0f km²", w)
	}
}

func TestMemoryAndSpeed(t *testing.T) {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	d, err := decode(data)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("decoded heap: %.1f MB", float64(after.HeapAlloc-before.HeapAlloc)/(1<<20))

	start := time.Now()
	n := 0
	for lat := 45.0; lat < 55; lat += 0.02 {
		for lon := 5.0; lon < 15; lon += 0.02 {
			d.Lookup(lat, lon)
			n++
		}
	}
	t.Logf("%d lookups in %v", n, time.Since(start))
	runtime.KeepAlive(d)
}
