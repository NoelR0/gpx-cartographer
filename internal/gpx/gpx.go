// Package gpx reads GPX files (tracks and routes), computes statistics and
// stores the geometry in a memory-efficient way:
//
//   - For display on the map, every segment is simplified with Douglas-Peucker.
//   - For locating photos, a compact time index with all points is kept
//     (12 bytes per point).
package gpx

import (
	"encoding/xml"
	"errors"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Elevation changes below this value are treated as GPS noise.
const elevationThreshold = 5.0

// Moving time: movement is judged over windows of at least movingWindowS
// seconds (straight-line distance, which cancels out GPS jitter while
// standing still). A window counts as moving if its speed reaches
// minMovingSpeed. Gaps between two points longer than pauseGapS (e.g. a
// paused recording) never count as moving.
const (
	movingWindowS  = 30
	minMovingSpeed = 0.3 // m/s, slow enough for steep uphill hiking
	pauseGapS      = 300
)

// Track is a <trk> or an <rte> from a GPX file.
type Track struct {
	ID   string // "<file>#<index>"
	Name string
	File string // relative path of the GPX file

	DistanceM float64
	AscentM   float64
	DescentM  float64
	MovingS   int64     // seconds in motion, see movingTime
	Start     time.Time // zero if the track has no timestamps
	End       time.Time

	// Simplified geometry for the map: per segment, alternating
	// latitude, longitude in 1e-7 degrees.
	Segments [][]int32

	// Time index of all points with a timestamp, sorted by time.
	// times are seconds since Start.
	lats, lons, times []int32
}

// Options controls parsing.
type Options struct {
	// Maximum deviation of the simplified line in meters (0 = off).
	SimplifyM float64
}

type point struct {
	lat, lon float64
	ele      float64
	hasEle   bool
	t        int64 // Unix seconds
	hasTime  bool
}

type rawPoint struct {
	Lat  string `xml:"lat,attr"`
	Lon  string `xml:"lon,attr"`
	Ele  string `xml:"ele"`
	Time string `xml:"time"`
}

// Parse reads all tracks and routes from r. rel is the relative file path.
func Parse(r io.Reader, rel string, opt Options) ([]*Track, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.CharsetReader = charsetReader

	var (
		tracks []*Track
		cur    *builder
		stack  []string
		sawGPX bool
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch el := tok.(type) {
		case xml.StartElement:
			name := el.Name.Local
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			switch {
			case name == "gpx":
				sawGPX = true
			case (name == "trk" || name == "rte") && parent == "gpx":
				cur = newBuilder(rel, len(tracks), opt)
			case name == "trkseg" && parent == "trk" && cur != nil:
				cur.endSegment()
			case (name == "trkpt" && parent == "trkseg" || name == "rtept" && parent == "rte") && cur != nil:
				var rp rawPoint
				if err := dec.DecodeElement(&rp, &el); err != nil {
					return nil, err
				}
				if p, ok := rp.parse(); ok {
					cur.add(p)
				}
				continue // DecodeElement has already consumed the end element
			case name == "name" && (parent == "trk" || parent == "rte") && cur != nil:
				var s string
				if err := dec.DecodeElement(&s, &el); err != nil {
					return nil, err
				}
				cur.track.Name = strings.TrimSpace(s)
				continue
			}
			stack = append(stack, name)
		case xml.EndElement:
			if len(stack) == 0 {
				continue
			}
			name := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if (name == "trk" || name == "rte") && cur != nil && len(stack) == 1 {
				if t := cur.finish(); t != nil {
					tracks = append(tracks, t)
				}
				cur = nil
			}
		}
	}
	if !sawGPX {
		return nil, errors.New("gpx: no <gpx> element found")
	}
	return tracks, nil
}

// charsetReader accepts Latin-1 in addition to UTF-8, which some older
// devices write into the XML declaration.
func charsetReader(label string, in io.Reader) (io.Reader, error) {
	switch strings.ToLower(label) {
	case "iso-8859-1", "iso8859-1", "latin1", "latin-1", "windows-1252", "cp1252":
		return &latin1Reader{r: in}, nil
	}
	return in, nil // read us-ascii, utf-8 and unknown charsets unchanged
}

type latin1Reader struct {
	r       io.Reader
	buf     []byte
	pending []byte // already converted to UTF-8, not yet returned
	err     error
}

func (l *latin1Reader) Read(p []byte) (int, error) {
	for len(l.pending) == 0 {
		if l.err != nil {
			return 0, l.err
		}
		if l.buf == nil {
			l.buf = make([]byte, 4096)
		}
		var n int
		n, l.err = l.r.Read(l.buf)
		for _, b := range l.buf[:n] {
			l.pending = utf8.AppendRune(l.pending, rune(b))
		}
	}
	n := copy(p, l.pending)
	l.pending = l.pending[n:]
	if len(l.pending) == 0 {
		l.pending = l.pending[:0:0]
	}
	return n, nil
}

func (rp rawPoint) parse() (point, bool) {
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(rp.Lat), 64)
	lon, err2 := strconv.ParseFloat(strings.TrimSpace(rp.Lon), 64)
	if err1 != nil || err2 != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return point{}, false
	}
	p := point{lat: lat, lon: lon}
	if v, err := strconv.ParseFloat(strings.TrimSpace(rp.Ele), 64); err == nil {
		p.ele, p.hasEle = v, true
	}
	if t, ok := parseTime(rp.Time); ok {
		p.t, p.hasTime = t.Unix(), true
	}
	return p, true
}

func parseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	// Without a time zone: per the spec, GPX times are UTC
	if t, err := time.Parse("2006-01-02T15:04:05.999999999", s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// ---------------------------------------------------------------- Building

type timedPoint struct {
	t        int64
	lat, lon int32
}

type builder struct {
	track  *Track
	opt    Options
	seg    []point
	timed  []timedPoint
	sorted bool
}

func newBuilder(rel string, idx int, opt Options) *builder {
	name := rel
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSuffix(name, ".gpx")
	name = strings.TrimSuffix(name, ".GPX")
	return &builder{
		track:  &Track{ID: rel + "#" + strconv.Itoa(idx), Name: name, File: rel},
		opt:    opt,
		sorted: true,
	}
}

func (b *builder) add(p point) {
	b.seg = append(b.seg, p)
	if p.hasTime {
		tp := timedPoint{t: p.t, lat: e7(p.lat), lon: e7(p.lon)}
		if n := len(b.timed); n > 0 && tp.t < b.timed[n-1].t {
			b.sorted = false
		}
		b.timed = append(b.timed, tp)
	}
}

// endSegment accounts for the current segment and releases its points.
func (b *builder) endSegment() {
	seg := b.seg
	b.seg = b.seg[:0]
	if len(seg) == 0 {
		return
	}
	t := b.track

	ref, hasRef := 0.0, false
	for i, p := range seg {
		if i > 0 {
			t.DistanceM += haversine(seg[i-1], p)
		}
		if !p.hasEle {
			continue
		}
		// hysteresis against GPS elevation noise
		switch {
		case !hasRef:
			ref, hasRef = p.ele, true
		case p.ele-ref >= elevationThreshold:
			t.AscentM += p.ele - ref
			ref = p.ele
		case ref-p.ele >= elevationThreshold:
			t.DescentM += ref - p.ele
			ref = p.ele
		}
	}

	t.MovingS += movingTime(seg)

	keep := simplify(seg, b.opt.SimplifyM)
	out := make([]int32, 0, 2*len(keep))
	for _, i := range keep {
		out = append(out, e7(seg[i].lat), e7(seg[i].lon))
	}
	t.Segments = append(t.Segments, out)
}

func (b *builder) finish() *Track {
	b.endSegment()
	t := b.track
	if len(t.Segments) == 0 {
		return nil
	}
	if len(b.timed) > 0 {
		if !b.sorted {
			sort.SliceStable(b.timed, func(i, j int) bool { return b.timed[i].t < b.timed[j].t })
		}
		start := b.timed[0].t
		t.Start = time.Unix(start, 0).UTC()
		t.End = time.Unix(b.timed[len(b.timed)-1].t, 0).UTC()
		n := len(b.timed)
		t.lats, t.lons, t.times = make([]int32, n), make([]int32, n), make([]int32, n)
		for i, p := range b.timed {
			t.lats[i], t.lons[i], t.times[i] = p.lat, p.lon, int32(p.t-start)
		}
	}
	b.seg, b.timed = nil, nil
	return t
}

// movingTime returns the seconds of seg during which the recorder moved.
func movingTime(seg []point) int64 {
	var moving int64
	anchor, prev := -1, -1
	flush := func(end int) {
		if anchor < 0 || end <= anchor {
			return
		}
		dt := seg[end].t - seg[anchor].t
		if dt > 0 && haversine(seg[anchor], seg[end]) >= minMovingSpeed*float64(dt) {
			moving += dt
		}
	}
	for i, p := range seg {
		if !p.hasTime {
			continue
		}
		switch {
		case prev < 0:
			anchor = i
		case p.t < seg[prev].t || p.t-seg[prev].t > pauseGapS:
			flush(prev)
			anchor = i
		case p.t-seg[anchor].t >= movingWindowS:
			flush(i)
			anchor = i
		}
		prev = i
	}
	flush(prev)
	return moving
}

// ---------------------------------------------------------------- Geometry

func e7(v float64) int32 { return int32(math.Round(v * 1e7)) }

// FromE7 converts a stored coordinate value back to degrees.
func FromE7(v int32) float64 { return float64(v) / 1e7 }

const earthRadius = 6371000.0

func haversine(a, b point) float64 {
	p1, p2 := a.lat*math.Pi/180, b.lat*math.Pi/180
	dp := p2 - p1
	dl := (b.lon - a.lon) * math.Pi / 180
	h := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * earthRadius * math.Asin(math.Min(1, math.Sqrt(h)))
}

// simplify returns the indices of the points kept by Douglas-Peucker with
// tolerance tol (meters). Iterative, so that very long tracks do not cause
// deep recursion.
func simplify(seg []point, tol float64) []int {
	n := len(seg)
	if n <= 2 || tol <= 0 {
		idx := make([]int, n)
		for i := range idx {
			idx[i] = i
		}
		return idx
	}

	// Local flat projection in meters – accurate enough for short distances.
	lat0 := seg[0].lat * math.Pi / 180
	kx := math.Cos(lat0) * earthRadius * math.Pi / 180
	ky := earthRadius * math.Pi / 180
	xs, ys := make([]float64, n), make([]float64, n)
	for i, p := range seg {
		xs[i], ys[i] = p.lon*kx, p.lat*ky
	}

	keep := make([]bool, n)
	keep[0], keep[n-1] = true, true
	tol2 := tol * tol
	stack := [][2]int{{0, n - 1}}
	for len(stack) > 0 {
		r := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		first, last := r[0], r[1]
		maxD, idx := -1.0, -1
		for i := first + 1; i < last; i++ {
			if d := segDist2(xs[i], ys[i], xs[first], ys[first], xs[last], ys[last]); d > maxD {
				maxD, idx = d, i
			}
		}
		if idx >= 0 && maxD > tol2 {
			keep[idx] = true
			stack = append(stack, [2]int{first, idx}, [2]int{idx, last})
		}
	}

	out := make([]int, 0, n/4)
	for i, k := range keep {
		if k {
			out = append(out, i)
		}
	}
	return out
}

// segDist2 is the squared distance from (px,py) to the segment a–b.
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

// ---------------------------------------------------------------- Locating

// Locate finds the position at time when on one of the tracks. If when lies
// between two points, it interpolates linearly. maxGap is the largest allowed
// time distance to a track point.
func Locate(tracks []*Track, when time.Time, maxGap time.Duration) (lat, lon float64, ok bool) {
	gap := int64(maxGap / time.Second)
	best := int64(math.MaxInt64)
	ts := when.Unix()
	for _, t := range tracks {
		if len(t.times) == 0 || ts < t.Start.Unix()-gap || ts > t.End.Unix()+gap {
			continue
		}
		off := ts - t.Start.Unix()
		i := sort.Search(len(t.times), func(i int) bool { return int64(t.times[i]) >= off })

		hasBefore, hasAfter := i > 0, i < len(t.times)
		if hasBefore && hasAfter {
			gb, ga := off-int64(t.times[i-1]), int64(t.times[i])-off
			if gb <= gap && ga <= gap {
				d := min(gb, ga)
				if d < best {
					best = d
					f := 0.0
					if gb+ga > 0 {
						f = float64(gb) / float64(gb+ga)
					}
					lat = FromE7(t.lats[i-1]) + (FromE7(t.lats[i])-FromE7(t.lats[i-1]))*f
					lon = FromE7(t.lons[i-1]) + (FromE7(t.lons[i])-FromE7(t.lons[i-1]))*f
					ok = true
				}
				continue
			}
		}
		// only one neighbor within range -> use that point
		for _, j := range []int{i - 1, i} {
			if j < 0 || j >= len(t.times) {
				continue
			}
			d := off - int64(t.times[j])
			if d < 0 {
				d = -d
			}
			if d <= gap && d < best {
				best, lat, lon, ok = d, FromE7(t.lats[j]), FromE7(t.lons[j]), true
			}
		}
	}
	return lat, lon, ok
}

// PointCount returns (stored map points, points in the time index).
func (t *Track) PointCount() (display, timed int) {
	for _, s := range t.Segments {
		display += len(s) / 2
	}
	return display, len(t.times)
}
