// Package photo liest Fotos ein (Position, Aufnahmezeit, Grösse) und erzeugt
// Vorschaubilder.
package photo

import (
	"log/slog"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"cartographer/internal/exif"
	"cartographer/internal/files"
)

// Extensions sind die unterstützten Dateiendungen.
var Extensions = map[string]bool{".jpg": true, ".jpeg": true, ".png": true}

// Photo beschreibt ein Foto. Die Felder sind nach dem Einlesen unveränderlich;
// eine nachträgliche Verortung über GPX setzt der Aufrufer auf einer Kopie.
type Photo struct {
	ID     string // relativer Pfad
	Name   string
	HasPos bool
	Lat    float64
	Lon    float64
	Taken  time.Time // Zero, wenn unbekannt
	Width  int       // bereits gemäss Ausrichtung gedreht
	Height int
	// "exif" oder "track"
	LocatedBy string
}

// Library liest einen Foto-Ordner und merkt sich die Metadaten im Speicher.
type Library struct {
	Dir      string
	CameraTZ *time.Location
	cache    *files.Cache[*Photo]
}

func NewLibrary(dir string, tz *time.Location) *Library {
	return &Library{Dir: dir, CameraTZ: tz, cache: files.NewCache[*Photo]()}
}

// Load durchsucht den Ordner und liefert alle lesbaren Fotos, sortiert nach
// Aufnahmezeit. Nur neue oder geänderte Dateien werden geöffnet.
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

	// Neue Dateien parallel einlesen – es wird nur der Dateikopf gelesen.
	files.Parallel(len(todo), max(4, runtime.NumCPU()), func(k int) {
		e := entries[todo[k]]
		p, err := l.read(e)
		if err != nil {
			slog.Warn("Foto kann nicht gelesen werden", "datei", e.Rel, "fehler", err)
		}
		l.cache.Put(e, p) // auch nil merken, damit defekte Dateien nicht ständig neu gelesen werden
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

// parseOffset liest einen EXIF-Offset wie "+02:00".
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
