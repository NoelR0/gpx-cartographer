package main

import (
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strconv"
	"sync"

	"github.com/NoelR0/gpx-cartographer/internal/gpx"
	"github.com/NoelR0/gpx-cartographer/internal/regions"
	"github.com/NoelR0/gpx-cartographer/internal/tiles"
)

// explorerZoom is the zoom level of the explorer tiles (as in web/tiles.js).
const explorerZoom = 14

// coverageCache keeps how much area of every state has been discovered. It is
// recomputed only when the set of tracks changes.
type coverageCache struct {
	mu    sync.Mutex
	entry *coverage
}

type coverage struct {
	tracks     []*gpx.Track // the input, to detect changes
	states     []float64    // discovered km² per state
	countries  []float64
	continents []float64
	world      float64
}

func (c *coverageCache) get(db *regions.DB, tracks []*gpx.Track) *coverage {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entry == nil || !slices.Equal(c.entry.tracks, tracks) {
		c.entry = computeCoverage(db, tracks, explorerZoom)
	}
	return c.entry
}

// computeCoverage assigns every discovered tile (visited tiles and their
// neighbours) to the state its center lies in and adds up the tile areas.
// Tiles at sea are not counted.
func computeCoverage(db *regions.DB, tracks []*gpx.Track, z int) *coverage {
	var segs [][]int32
	for _, t := range tracks {
		segs = append(segs, t.Segments...)
	}
	e := &coverage{
		tracks:     tracks,
		states:     make([]float64, len(db.States)),
		countries:  make([]float64, len(db.Countries)),
		continents: make([]float64, len(db.Continents)),
	}
	for t := range tiles.Discovered(tiles.Visited(segs, z), z) {
		lat, lon := t.Center(z)
		if i := db.Lookup(lat, lon); i >= 0 {
			a := t.AreaKm2(z)
			s := db.States[i]
			e.states[i] += a
			e.countries[s.Country] += a
			e.continents[db.Countries[s.Country].Continent] += a
			e.world += a
		}
	}
	return e
}

type areaJSON struct {
	Name       string  `json:"name"`
	VisitedKm2 float64 `json:"visited_km2"`
	AreaKm2    float64 `json:"area_km2"`
}

func newAreaJSON(a regions.Area, visited float64) *areaJSON {
	round := func(v float64) float64 { return math.Round(v*100) / 100 }
	return &areaJSON{Name: a.Name, VisitedKm2: round(visited), AreaKm2: round(a.AreaKm2)}
}

// handleCoverage reports the discovered share of the world and of the
// continent, country and state at lat/lon.
func (s *server) handleCoverage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	db, err := regions.Load()
	if err != nil {
		slog.Error("Region data not readable", "error", err)
		http.Error(w, "region data not available", http.StatusInternalServerError)
		return
	}
	var timed []*gpx.Track
	for _, t := range s.loadTracks() {
		if !t.Start.IsZero() {
			timed = append(timed, t)
		}
	}
	c := s.coverage.get(db, timed)

	out := struct {
		World     *areaJSON `json:"world"`
		Continent *areaJSON `json:"continent"`
		Country   *areaJSON `json:"country"`
		State     *areaJSON `json:"state"`
	}{World: newAreaJSON(db.World, c.world)}
	lat, errLat := strconv.ParseFloat(q.Get("lat"), 64)
	lon, errLon := strconv.ParseFloat(q.Get("lon"), 64)
	if errLat == nil && errLon == nil {
		if i := db.Lookup(lat, lon); i >= 0 {
			st := db.States[i]
			co := db.Countries[st.Country]
			out.State = newAreaJSON(st.Area, c.states[i])
			out.Country = newAreaJSON(co.Area, c.countries[st.Country])
			out.Continent = newAreaJSON(db.Continents[co.Continent], c.continents[co.Continent])
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, r, out)
}
