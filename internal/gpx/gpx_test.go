package gpx

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// straightTrack erzeugt n Punkte auf einer Linie nach Norden, einer pro Sekunde.
func straightTrack(n int, start time.Time) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1">`)
	b.WriteString(`<trk><name> Testlauf </name><trkseg>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<trkpt lat="%.7f" lon="8.5"><ele>%d</ele><time>%s</time><name>pkt</name></trkpt>`,
			47.0+float64(i)*0.0001, 400+i, start.Add(time.Duration(i)*time.Second).Format(time.RFC3339))
	}
	b.WriteString(`</trkseg></trk></gpx>`)
	return b.String()
}

func parse(t *testing.T, s string, opt Options) []*Track {
	t.Helper()
	ts, err := Parse(strings.NewReader(s), "ordner/test.gpx", opt)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestParseTrack(t *testing.T) {
	start := time.Date(2026, 8, 15, 8, 0, 0, 0, time.UTC)
	ts := parse(t, straightTrack(101, start), Options{SimplifyM: 3})
	if len(ts) != 1 {
		t.Fatalf("%d Tracks", len(ts))
	}
	tr := ts[0]
	if tr.Name != "Testlauf" || tr.ID != "ordner/test.gpx#0" || tr.File != "ordner/test.gpx" {
		t.Errorf("Name/ID: %q %q %q (Punkt-<name> darf den Tracknamen nicht überschreiben)", tr.Name, tr.ID, tr.File)
	}
	// 100 × 0.0001° Breite ≈ 1112 m
	if math.Abs(tr.DistanceM-1112) > 5 {
		t.Errorf("Distanz = %.0f", tr.DistanceM)
	}
	if math.Abs(tr.AscentM-100) > 5 || tr.DescentM != 0 {
		t.Errorf("Höhenmeter = %.0f / %.0f", tr.AscentM, tr.DescentM)
	}
	if !tr.Start.Equal(start) || !tr.End.Equal(start.Add(100*time.Second)) {
		t.Errorf("Zeit = %v – %v", tr.Start, tr.End)
	}
	// Eine gerade Linie wird auf Anfangs- und Endpunkt reduziert …
	if d, timed := tr.PointCount(); d != 2 || timed != 101 {
		t.Errorf("Punkte: Karte %d (erwartet 2), Zeitindex %d (erwartet 101)", d, timed)
	}
}

func TestSimplifyKeepsCorners(t *testing.T) {
	// L-Form: 50 Punkte nach Norden, dann 50 nach Osten
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
		t.Errorf("L-Form: %d Punkte, erwartet 3", d)
	}
	tr = parse(t, b.String(), Options{SimplifyM: 0})[0]
	if d, _ := tr.PointCount(); d != 100 {
		t.Errorf("ohne Vereinfachung: %d Punkte, erwartet 100", d)
	}
}

func TestRoutesSegmentsAndNames(t *testing.T) {
	src := `<gpx>
	  <rte><rtept lat="46.5" lon="7.9"/><rtept lat="46.6" lon="7.9"/></rte>
	  <trk><name>Zwei Teile</name>
	    <trkseg><trkpt lat="1" lon="1"/><trkpt lat="1.001" lon="1"/></trkseg>
	    <trkseg><trkpt lat="2" lon="2"/></trkseg>
	    <trkseg></trkseg>
	  </trk>
	  <trk><trkseg></trkseg></trk>
	</gpx>`
	ts := parse(t, src, Options{})
	if len(ts) != 2 {
		t.Fatalf("%d Tracks, erwartet 2 (leerer Track wird verworfen)", len(ts))
	}
	if ts[0].Name != "test" || !ts[0].Start.IsZero() {
		t.Errorf("Route: Name %q (Dateiname erwartet), Start %v", ts[0].Name, ts[0].Start)
	}
	if ts[1].Name != "Zwei Teile" || len(ts[1].Segments) != 2 || ts[1].ID != "ordner/test.gpx#1" {
		t.Errorf("Track: %q, %d Segmente, ID %q", ts[1].Name, len(ts[1].Segments), ts[1].ID)
	}
}

func TestInvalid(t *testing.T) {
	for _, s := range []string{"<gpx><trk>", "kein xml", "<kml></kml>"} {
		if _, err := Parse(strings.NewReader(s), "x.gpx", Options{}); err == nil {
			t.Errorf("%q: Fehler erwartet", s)
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

	// Genau in der Mitte der ersten 100 s -> interpoliert
	lat, lon, ok := Locate(ts, start.Add(50*time.Second), gap)
	if !ok || math.Abs(lat-47.05) > 1e-6 || math.Abs(lon-8.1) > 1e-6 {
		t.Errorf("Mitte: %v %v %v", lat, lon, ok)
	}
	// Andere Zeitzone, gleicher Zeitpunkt
	zurich := time.FixedZone("CEST", 2*3600)
	if lat2, _, _ := Locate(ts, start.Add(50*time.Second).In(zurich), gap); lat2 != lat {
		t.Error("Zeitzone darf keine Rolle spielen")
	}
	// Grosse Lücke (28 min): 2 min nach dem zweiten Punkt -> nächster Punkt
	lat, lon, ok = Locate(ts, start.Add(220*time.Second), gap)
	if !ok || lat != 47.1 || lon != 8.2 {
		t.Errorf("Lücke: %v %v %v", lat, lon, ok)
	}
	// Mitten in der Lücke -> nichts
	if _, _, ok := Locate(ts, start.Add(15*time.Minute), gap); ok {
		t.Error("in der Lücke darf nichts gefunden werden")
	}
	// Kurz vor dem Start / lange danach
	if _, _, ok := Locate(ts, start.Add(-time.Minute), gap); !ok {
		t.Error("1 min vor Start sollte gefunden werden")
	}
	if _, _, ok := Locate(ts, start.Add(2*time.Hour), gap); ok {
		t.Error("2 h nach Ende darf nichts gefunden werden")
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
		t.Errorf("Start/Ende: %v %v", tr.Start, tr.End)
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
