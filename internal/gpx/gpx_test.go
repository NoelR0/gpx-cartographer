package gpx

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// straightTrack creates n points on a line heading north, one per second.
func straightTrack(n int, start time.Time) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1">`)
	b.WriteString(`<trk><name> Test run </name><trkseg>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<trkpt lat="%.7f" lon="8.5"><ele>%d</ele><time>%s</time><name>pt</name></trkpt>`,
			47.0+float64(i)*0.0001, 400+i, start.Add(time.Duration(i)*time.Second).Format(time.RFC3339))
	}
	b.WriteString(`</trkseg></trk></gpx>`)
	return b.String()
}

func parse(t *testing.T, s string, opt Options) []*Track {
	t.Helper()
	ts, err := Parse(strings.NewReader(s), "folder/test.gpx", opt)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestParseTrack(t *testing.T) {
	start := time.Date(2026, 8, 15, 8, 0, 0, 0, time.UTC)
	ts := parse(t, straightTrack(101, start), Options{SimplifyM: 3})
	if len(ts) != 1 {
		t.Fatalf("%d tracks", len(ts))
	}
	tr := ts[0]
	if tr.Name != "Test run" || tr.ID != "folder/test.gpx#0" || tr.File != "folder/test.gpx" {
		t.Errorf("name/ID: %q %q %q (a point <name> must not override the track name)", tr.Name, tr.ID, tr.File)
	}
	// 100 × 0.0001° latitude ≈ 1112 m
	if math.Abs(tr.DistanceM-1112) > 5 {
		t.Errorf("distance = %.0f", tr.DistanceM)
	}
	if math.Abs(tr.AscentM-100) > 5 || tr.DescentM != 0 {
		t.Errorf("ascent/descent = %.0f / %.0f", tr.AscentM, tr.DescentM)
	}
	if !tr.Start.Equal(start) || !tr.End.Equal(start.Add(100*time.Second)) {
		t.Errorf("time = %v – %v", tr.Start, tr.End)
	}
	// A straight line is reduced to its start and end points …
	if d, timed := tr.PointCount(); d != 2 || timed != 101 {
		t.Errorf("points: map %d (want 2), time index %d (want 101)", d, timed)
	}
}

func TestSimplifyKeepsCorners(t *testing.T) {
	// L shape: 50 points north, then 50 east
	var b strings.Builder
	b.WriteString(`<gpx><trk><trkseg>`)
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&b, `<trkpt lat="%.6f" lon="8.0"/>`, 47+float64(i)*0.0001)
	}
	for i := 1; i <= 50; i++ {
		fmt.Fprintf(&b, `<trkpt lat="%.6f" lon="%.6f"/>`, 47+49*0.0001, 8+float64(i)*0.0001)
	}
	b.WriteString(`</trkseg></trk></gpx>`)
	tr := parse(t, b.String(), Options{SimplifyM: 3})[0]
	if d, _ := tr.PointCount(); d != 3 {
		t.Errorf("L shape: %d points, want 3", d)
	}
	tr = parse(t, b.String(), Options{SimplifyM: 0})[0]
	if d, _ := tr.PointCount(); d != 100 {
		t.Errorf("without simplification: %d points, want 100", d)
	}
}

func TestRoutesSegmentsAndNames(t *testing.T) {
	src := `<gpx>
	  <rte><rtept lat="46.5" lon="7.9"/><rtept lat="46.6" lon="7.9"/></rte>
	  <trk><name>Two parts</name>
	    <trkseg><trkpt lat="1" lon="1"/><trkpt lat="1.001" lon="1"/></trkseg>
	    <trkseg><trkpt lat="2" lon="2"/></trkseg>
	    <trkseg></trkseg>
	  </trk>
	  <trk><trkseg></trkseg></trk>
	</gpx>`
	ts := parse(t, src, Options{})
	if len(ts) != 2 {
		t.Fatalf("%d tracks, want 2 (empty track is discarded)", len(ts))
	}
	if ts[0].Name != "test" || !ts[0].Start.IsZero() {
		t.Errorf("route: name %q (want file name), start %v", ts[0].Name, ts[0].Start)
	}
	if ts[1].Name != "Two parts" || len(ts[1].Segments) != 2 || ts[1].ID != "folder/test.gpx#1" {
		t.Errorf("track: %q, %d segments, ID %q", ts[1].Name, len(ts[1].Segments), ts[1].ID)
	}
}

func TestInvalid(t *testing.T) {
	for _, s := range []string{"<gpx><trk>", "not xml", "<kml></kml>"} {
		if _, err := Parse(strings.NewReader(s), "x.gpx", Options{}); err == nil {
			t.Errorf("%q: expected an error", s)
		}
	}
}

func TestLatin1(t *testing.T) {
	src := "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><gpx><trk><name>Z\xfcrich</name><trkseg><trkpt lat=\"47\" lon=\"8\"/></trkseg></trk></gpx>"
	ts := parse(t, src, Options{})
	if ts[0].Name != "Zürich" {
		t.Errorf("Name = %q", ts[0].Name)
	}
}

func TestLocate(t *testing.T) {
	start := time.Date(2026, 8, 15, 8, 0, 0, 0, time.UTC)
	src := `<gpx><trk><trkseg>
	  <trkpt lat="47.0" lon="8.0"><time>2026-08-15T08:00:00Z</time></trkpt>
	  <trkpt lat="47.1" lon="8.2"><time>2026-08-15T08:01:40Z</time></trkpt>
	  <trkpt lat="47.2" lon="8.2"><time>2026-08-15T08:30:00Z</time></trkpt>
	</trkseg></trk></gpx>`
	ts := parse(t, src, Options{SimplifyM: 3})
	gap := 5 * time.Minute

	// exactly in the middle of the first 100 s -> interpolated
	lat, lon, ok := Locate(ts, start.Add(50*time.Second), gap)
	if !ok || math.Abs(lat-47.05) > 1e-6 || math.Abs(lon-8.1) > 1e-6 {
		t.Errorf("middle: %v %v %v", lat, lon, ok)
	}
	// different time zone, same instant
	zurich := time.FixedZone("CEST", 2*3600)
	if lat2, _, _ := Locate(ts, start.Add(50*time.Second).In(zurich), gap); lat2 != lat {
		t.Error("the time zone must not matter")
	}
	// large gap (28 min): 2 min after the second point -> nearest point
	lat, lon, ok = Locate(ts, start.Add(220*time.Second), gap)
	if !ok || lat != 47.1 || lon != 8.2 {
		t.Errorf("gap: %v %v %v", lat, lon, ok)
	}
	// in the middle of the gap -> nothing
	if _, _, ok := Locate(ts, start.Add(15*time.Minute), gap); ok {
		t.Error("nothing must be found inside the gap")
	}
	// shortly before the start / long after the end
	if _, _, ok := Locate(ts, start.Add(-time.Minute), gap); !ok {
		t.Error("1 min before the start should be found")
	}
	if _, _, ok := Locate(ts, start.Add(2*time.Hour), gap); ok {
		t.Error("nothing must be found 2 h after the end")
	}
}

func TestUnsortedTimes(t *testing.T) {
	src := `<gpx><trk><trkseg>
	  <trkpt lat="47.2" lon="8"><time>2026-08-15T08:02:00Z</time></trkpt>
	  <trkpt lat="47.0" lon="8"><time>2026-08-15T08:00:00Z</time></trkpt>
	  <trkpt lat="47.1" lon="8"><time>2026-08-15T08:01:00</time></trkpt>
	</trkseg></trk></gpx>`
	tr := parse(t, src, Options{})[0]
	if tr.Start.Format("15:04") != "08:00" || tr.End.Format("15:04") != "08:02" {
		t.Errorf("start/end: %v %v", tr.Start, tr.End)
	}
	lat, _, ok := Locate([]*Track{tr}, time.Date(2026, 8, 15, 8, 0, 30, 0, time.UTC), time.Minute)
	if !ok || math.Abs(lat-47.05) > 1e-6 {
		t.Errorf("Locate: %v %v", lat, ok)
	}
}

func BenchmarkParse(b *testing.B) {
	src := straightTrack(10000, time.Now())
	b.SetBytes(int64(len(src)))
	for i := 0; i < b.N; i++ {
		_, _ = Parse(strings.NewReader(src), "x.gpx", Options{SimplifyM: 3})
	}
}

func TestMovingTime(t *testing.T) {
	var seg []point
	var ts int64
	lat := 47.0
	add := func(n int, step float64, jitter bool) {
		for i := 0; i < n; i++ {
			ts++
			lat += step
			p := point{lat: lat, lon: 8.5, t: ts, hasTime: true}
			if jitter && i%2 == 1 {
				p.lat += 0.00002 // ~2 m of GPS noise while standing still
			}
			seg = append(seg, p)
		}
	}
	add(120, 0.00001, false) // ~1.1 m/s for 2 minutes
	add(120, 0, true)        // standing still for 2 minutes
	ts += 600                // recording paused for 10 minutes
	add(60, 0.00001, false)  // moving again for 1 minute

	// The last moving second before the stop and the first one after the
	// pause fall into windows that are partly still, so allow some slack.
	if got := movingTime(seg); got < 170 || got > 190 {
		t.Errorf("moving time = %d s, want ~180", got)
	}
}
