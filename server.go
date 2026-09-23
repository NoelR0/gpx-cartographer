package main

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NoelR0/gpx-cartographer/internal/files"
	"github.com/NoelR0/gpx-cartographer/internal/gpx"
	"github.com/NoelR0/gpx-cartographer/internal/photo"
	"github.com/NoelR0/gpx-cartographer/web"
)

var gpxExtensions = map[string]bool{".gpx": true}

type server struct {
	cfg    *Config
	photos *photo.Library
	thumbs *photo.Thumbnailer
	tracks *files.Cache[[]*gpx.Track]
}

func newServer(cfg *Config) *server {
	return &server{
		cfg:    cfg,
		photos: photo.NewLibrary(cfg.PhotoDir, cfg.CameraTZ),
		thumbs: photo.NewThumbnailer(cfg.ThumbSize, cfg.ThumbWorkers, cfg.ThumbCacheMB<<20),
		tracks: files.NewCache[[]*gpx.Track](),
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/data", s.handleData)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/photo/thumb", s.handleThumb)
	mux.HandleFunc("GET /api/photo/full", s.handlePhoto(false))
	mux.HandleFunc("GET /api/photo/original", s.handlePhoto(true))
	mux.HandleFunc("GET /api/track/download", s.handleTrackDownload)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(web.FS)))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, web.FS, "index.html")
	})
	return securityHeaders(mux)
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// tile servers such as tile.openstreetmap.org require a Referer
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------- Data

func (s *server) loadTracks() []*gpx.Track {
	entries, err := files.Walk(s.cfg.GPXDir, gpxExtensions)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("GPX directory not readable", "error", err)
		}
		return nil
	}
	s.tracks.Prune(entries)
	opt := gpx.Options{SimplifyM: s.cfg.SimplifyM}
	perFile := make([][]*gpx.Track, len(entries))
	var todo []int
	for i, e := range entries {
		if ts, ok := s.tracks.Get(e); ok {
			perFile[i] = ts
		} else {
			todo = append(todo, i)
		}
	}
	files.Parallel(len(todo), runtime.GOMAXPROCS(0), func(k int) {
		e := entries[todo[k]]
		ts := parseGPXFile(e, opt)
		s.tracks.Put(e, ts)
		perFile[todo[k]] = ts
	})
	var all []*gpx.Track
	for _, ts := range perFile {
		all = append(all, ts...)
	}
	return all
}

func parseGPXFile(e files.Entry, opt gpx.Options) []*gpx.Track {
	f, err := os.Open(e.Path)
	if err != nil {
		slog.Warn("GPX file not readable", "file", e.Rel, "error", err)
		return nil
	}
	defer f.Close()
	ts, err := gpx.Parse(f, e.Rel, opt)
	if err != nil {
		slog.Warn("Invalid GPX file", "file", e.Rel, "error", err)
		return nil
	}
	return ts
}

type photoJSON struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Lat       *float64 `json:"lat"`
	Lon       *float64 `json:"lon"`
	Taken     *string  `json:"taken"`
	W         int      `json:"w"`
	H         int      `json:"h"`
	LocatedBy *string  `json:"located_by"`
}

type trackJSON struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	File      string    `json:"file"`
	DistanceM int       `json:"distance_m"`
	AscentM   int       `json:"ascent_m"`
	DescentM  int       `json:"descent_m"`
	Start     *string   `json:"start"`
	End       *string   `json:"end"`
	DurationS *int      `json:"duration_s"`
	MovingS   *int      `json:"moving_s"`
	Segments  []segJSON `json:"segments"`
}

// segJSON outputs a segment as [[lat,lon],...] with 5 decimal places (~1 m).
type segJSON []int32

func (s segJSON) MarshalJSON() ([]byte, error) {
	buf := make([]byte, 0, len(s)*12+2)
	buf = append(buf, '[')
	for i := 0; i+1 < len(s); i += 2 {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, '[')
		buf = strconv.AppendFloat(buf, gpx.FromE7(s[i]), 'f', 5, 64)
		buf = append(buf, ',')
		buf = strconv.AppendFloat(buf, gpx.FromE7(s[i+1]), 'f', 5, 64)
		buf = append(buf, ']')
	}
	return append(buf, ']'), nil
}

func (s *server) handleData(w http.ResponseWriter, r *http.Request) {
	tracks := s.loadTracks()
	photos, err := s.photos.Load()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Warn("Photo directory not readable", "error", err)
	}

	out := struct {
		Photos []photoJSON     `json:"photos"`
		Tracks []trackJSON     `json:"tracks"`
		Dirs   map[string]bool `json:"dirs"`
	}{
		Photos: make([]photoJSON, 0, len(photos)),
		Tracks: make([]trackJSON, 0, len(tracks)),
		Dirs:   map[string]bool{"photos": isDir(s.cfg.PhotoDir), "gpx": isDir(s.cfg.GPXDir)},
	}

	for i := range photos {
		p := &photos[i]
		if !p.HasPos && s.cfg.MatchPhotos && !p.Taken.IsZero() {
			if lat, lon, ok := gpx.Locate(tracks, p.Taken, s.cfg.MatchMaxGap); ok {
				p.HasPos, p.Lat, p.Lon, p.LocatedBy = true, lat, lon, "track"
			}
		}
		j := photoJSON{ID: p.ID, Name: p.Name, W: p.Width, H: p.Height}
		if p.HasPos {
			lat, lon := math.Round(p.Lat*1e6)/1e6, math.Round(p.Lon*1e6)/1e6
			j.Lat, j.Lon, j.LocatedBy = &lat, &lon, &p.LocatedBy
		}
		if !p.Taken.IsZero() {
			ts := p.Taken.Format(time.RFC3339)
			j.Taken = &ts
		}
		out.Photos = append(out.Photos, j)
	}

	for _, t := range tracks {
		j := trackJSON{
			ID: t.ID, Name: t.Name, File: t.File,
			DistanceM: int(t.DistanceM + 0.5),
			AscentM:   int(t.AscentM + 0.5),
			DescentM:  int(t.DescentM + 0.5),
			Segments:  make([]segJSON, len(t.Segments)),
		}
		for i, seg := range t.Segments {
			j.Segments[i] = seg
		}
		if !t.Start.IsZero() {
			start, end := t.Start.Format(time.RFC3339), t.End.Format(time.RFC3339)
			dur := int(t.End.Sub(t.Start) / time.Second)
			moving := int(t.MovingS)
			j.Start, j.End, j.DurationS, j.MovingS = &start, &end, &dur, &moving
		}
		out.Tracks = append(out.Tracks, j)
	}
	// newest tracks first, tracks without timestamps last
	sortTracks(out.Tracks)

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, r, out)
}

func sortTracks(ts []trackJSON) {
	key := func(t trackJSON) string {
		if t.Start == nil {
			return ""
		}
		return *t.Start
	}
	slices.SortStableFunc(ts, func(a, b trackJSON) int {
		if c := strings.Compare(key(b), key(a)); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
}

func (s *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, map[string]any{
		"version":          version,
		"tile_url":         s.cfg.TileURL,
		"tile_attribution": s.cfg.TileAttribution,
		"tile_max_zoom":    s.cfg.TileMaxZoom,
	})
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, map[string]any{"ok": true, "version": version})
}

// handleStats reports memory usage – handy for estimating RAM requirements.
func (s *server) handleStats(w http.ResponseWriter, r *http.Request) {
	runtime.GC() // so that heap_mb shows the memory actually in use
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	thumbs, thumbBytes := s.thumbs.Stats()
	var display, timed, n int
	for _, t := range s.loadTracks() {
		d, tm := t.PointCount()
		display, timed, n = display+d, timed+tm, n+1
	}
	writeJSON(w, r, map[string]any{
		"heap_mb":            float64(ms.HeapAlloc) / (1 << 20),
		"sys_mb":             float64(ms.Sys) / (1 << 20),
		"thumbs_cached":      thumbs,
		"thumbs_cache_mb":    float64(thumbBytes) / (1 << 20),
		"tracks":             n,
		"track_points_map":   display,
		"track_points_timed": timed,
		"goroutines":         runtime.NumGoroutine(),
	})
}

// ---------------------------------------------------------------- Files

func (s *server) handleThumb(w http.ResponseWriter, r *http.Request) {
	f, info, err := files.Open(s.cfg.PhotoDir, r.URL.Query().Get("path"), photo.Extensions)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	// The thumbnail depends not only on the file, but also on the configured
	// size and on the code that renders it (version).
	etag := `"` + strconv.FormatInt(info.ModTime().UnixNano(), 36) + "-" + strconv.FormatInt(info.Size(), 36) +
		"-" + strconv.Itoa(s.cfg.ThumbSize) + "-" + version + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	data, err := s.thumbs.Thumbnail(r.Context(), f, info)
	if err != nil {
		if r.Context().Err() == nil {
			slog.Warn("Thumbnail failed", "file", r.URL.Query().Get("path"), "error", err)
			http.Error(w, "could not create thumbnail", http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

func (s *server) handlePhoto(download bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, info, err := files.Open(s.cfg.PhotoDir, r.URL.Query().Get("path"), photo.Extensions)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		if download {
			setAttachment(w, info.Name())
		} else {
			w.Header().Set("Cache-Control", "private, max-age=86400")
		}
		http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	}
}

func (s *server) handleTrackDownload(w http.ResponseWriter, r *http.Request) {
	f, info, err := files.Open(s.cfg.GPXDir, r.URL.Query().Get("file"), gpxExtensions)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/gpx+xml")
	setAttachment(w, info.Name())
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

func setAttachment(w http.ResponseWriter, name string) {
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+ascii+`"; filename*=UTF-8''`+url.PathEscape(name))
}

// ---------------------------------------------------------------- Helpers

// gzip writers are reused: each one allocates several hundred KB.
var gzipPool = sync.Pool{New: func() any {
	gz, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)
	return gz
}}

// writeJSON encodes v directly into the response (gzip-compressed if the
// client accepts it), so no extra copy of large responses is kept in memory.
func writeJSON(w http.ResponseWriter, r *http.Request, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Add("Vary", "Accept-Encoding")
	var out io.Writer = w
	// tracks compress very well (~5×)
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzipPool.Get().(*gzip.Writer)
		gz.Reset(w)
		defer func() {
			_ = gz.Close()
			gzipPool.Put(gz)
		}()
		out = gz
	}
	// The status line has already been sent, so errors can only be logged.
	if err := json.NewEncoder(out).Encode(v); err != nil && r.Context().Err() == nil {
		slog.Warn("Writing JSON response failed", "path", r.URL.Path, "error", err)
	}
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
