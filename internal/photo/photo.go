// Package photo reads photos (position, capture time, size) and creates
// thumbnails.
package photo

import (
	"log/slog"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/NoelR0/gpx-cartographer/internal/exif"
	"github.com/NoelR0/gpx-cartographer/internal/files"
)

// Extensions are the supported file extensions.
var Extensions = map[string]bool{".jpg": true, ".jpeg": true, ".png": true}

// Photo describes a photo. Its fields are immutable after loading; the caller
// applies a later GPX-based location to a copy.
type Photo struct {
	ID     string // relative path
	Name   string
	HasPos bool
	Lat    float64
	Lon    float64
	Taken  time.Time // zero if unknown
	Width  int       // already rotated according to orientation
	Height int
	// "exif" or "track"
	LocatedBy string
}

// Library reads a photo directory and keeps the metadata in memory.
type Library struct {
	Dir      string
	CameraTZ *time.Location
	cache    *files.Cache[*Photo]
}

func NewLibrary(dir string, tz *time.Location) *Library {
	return &Library{Dir: dir, CameraTZ: tz, cache: files.NewCache[*Photo]()}
}

// Load walks the directory and returns all readable photos, sorted by capture
// time. Only new or changed files are opened.
func (l *Library) Load() ([]Photo, error) {
	entries, err := files.Walk(l.Dir, Extensions)
	if err != nil {
		return nil, err
	}
	l.cache.Prune(entries)

	result := make([]*Photo, len(entries))
	var todo []int
	for i, e := range entries {
		if p, ok := l.cache.Get(e); ok {
			result[i] = p
		} else {
			todo = append(todo, i)
		}
	}

	// Read new files in parallel – only the file header is read.
	files.Parallel(len(todo), max(4, runtime.NumCPU()), func(k int) {
		e := entries[todo[k]]
		p, err := l.read(e)
		if err != nil {
			slog.Warn("Cannot read photo", "file", e.Rel, "error", err)
		}
		l.cache.Put(e, p) // remember nil too, so broken files are not read again and again
		result[todo[k]] = p
	})

	photos := make([]Photo, 0, len(result))
	for _, p := range result {
		if p != nil {
			photos = append(photos, *p)
		}
	}
	sort.SliceStable(photos, func(i, j int) bool {
		a, b := photos[i].Taken, photos[j].Taken
		if a.IsZero() != b.IsZero() {
			return b.IsZero()
		}
		return a.Before(b)
	})
	return photos, nil
}

func (l *Library) read(e files.Entry) (*Photo, error) {
	f, err := os.Open(e.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m, err := exif.Read(f)
	if err != nil {
		return nil, err
	}

	p := &Photo{ID: e.Rel, Name: e.Rel[strings.LastIndexByte(e.Rel, '/')+1:], Width: m.Width, Height: m.Height}
	if m.Orientation >= 5 {
		p.Width, p.Height = p.Height, p.Width
	}
	if m.HasGPS {
		p.HasPos, p.Lat, p.Lon, p.LocatedBy = true, m.Lat, m.Lon, "exif"
	}
	p.Taken = parseTaken(m.DateTime, m.Offset, l.CameraTZ)
	return p, nil
}

func parseTaken(dt, offset string, tz *time.Location) time.Time {
	if dt == "" {
		return time.Time{}
	}
	loc := tz
	if off, ok := parseOffset(offset); ok {
		loc = off
	}
	t, err := time.ParseInLocation("2006:01:02 15:04:05", dt, loc)
	if err != nil {
		return time.Time{}
	}
	return t
}

// parseOffset parses an EXIF offset such as "+02:00".
func parseOffset(s string) (*time.Location, bool) {
	if len(s) != 6 || (s[0] != '+' && s[0] != '-') || s[3] != ':' {
		return nil, false
	}
	t, err := time.Parse("-07:00", s)
	if err != nil {
		return nil, false
	}
	_, secs := t.Zone()
	return time.FixedZone(s, secs), true
}
