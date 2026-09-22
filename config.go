package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	PhotoDir string
	GPXDir   string
	Addr     string

	ThumbSize    int
	ThumbWorkers int
	ThumbCacheMB int

	MatchPhotos bool
	MatchMaxGap time.Duration
	CameraTZ    *time.Location

	SimplifyM float64

	TileURL         string
	TileAttribution string
	TileMaxZoom     int

	// Nur über die Kommandozeile
	Healthcheck bool
	ShowVersion bool
}

// loadConfig liest Umgebungsvariablen; Kommandozeilen-Flags haben Vorrang.
func loadConfig(args []string) (*Config, error) {
	var errs []string
	envStr := func(key, def string) string {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			return v
		}
		return def
	}
	envInt := func(key string, def int) int {
		v := envStr(key, "")
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s=%q ist keine Zahl", key, v))
			return def
		}
		return n
	}
	envFloat := func(key string, def float64) float64 {
		v := envStr(key, "")
		if v == "" {
			return def
		}
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s=%q ist keine Zahl", key, v))
			return def
		}
		return n
	}
	envBool := func(key string, def bool) bool {
		switch strings.ToLower(envStr(key, "")) {
		case "":
			return def
		case "1", "true", "yes", "on", "ja":
			return true
		case "0", "false", "no", "off", "nein":
			return false
		}
		errs = append(errs, fmt.Sprintf("%s: erwartet true oder false", key))
		return def
	}

	addr := envStr("ADDR", ":8080")
	if port := envStr("PORT", ""); port != "" && os.Getenv("ADDR") == "" {
		addr = ":" + port
	}

	c := &Config{
		PhotoDir:        envStr("PHOTO_DIR", "/data/photos"),
		GPXDir:          envStr("GPX_DIR", "/data/gpx"),
		Addr:            addr,
		ThumbSize:       envInt("THUMB_SIZE", 160),
		ThumbWorkers:    envInt("THUMB_WORKERS", 2),
		ThumbCacheMB:    envInt("THUMB_CACHE_MB", 32),
		MatchPhotos:     envBool("MATCH_PHOTOS_TO_TRACKS", true),
		MatchMaxGap:     time.Duration(envInt("MATCH_MAX_GAP", 300)) * time.Second,
		SimplifyM:       envFloat("TRACK_SIMPLIFY_M", 3),
		TileURL:         envStr("TILE_URL", "https://tile.openstreetmap.org/{z}/{x}/{y}.png"),
		TileAttribution: envStr("TILE_ATTRIBUTION", `&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>-Mitwirkende`),
		TileMaxZoom:     envInt("TILE_MAX_ZOOM", 19),
	}
	tzName := envStr("CAMERA_TZ", envStr("TZ", "Europe/Zurich"))

	fs := flag.NewFlagSet("cartographer", flag.ContinueOnError)
	fs.StringVar(&c.PhotoDir, "photos", c.PhotoDir, "Foto-Ordner (PHOTO_DIR)")
	fs.StringVar(&c.GPXDir, "gpx", c.GPXDir, "GPX-Ordner (GPX_DIR)")
	fs.StringVar(&c.Addr, "addr", c.Addr, "Adresse, auf der der Server lauscht (ADDR)")
	fs.StringVar(&tzName, "tz", tzName, "Zeitzone für Fotos ohne EXIF-Offset (CAMERA_TZ)")
	fs.BoolVar(&c.Healthcheck, "healthcheck", false, "prüft, ob ein laufender Server antwortet (für Docker)")
	fs.BoolVar(&c.ShowVersion, "version", false, "Version ausgeben")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	loc, err := time.LoadLocation(tzName)
	if err != nil {
		errs = append(errs, fmt.Sprintf("unbekannte Zeitzone %q", tzName))
		loc = time.UTC
	}
	c.CameraTZ = loc

	if c.ThumbSize < 32 || c.ThumbSize > 1024 {
		errs = append(errs, "THUMB_SIZE muss zwischen 32 und 1024 liegen")
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("ungültige Konfiguration:\n  %s", strings.Join(errs, "\n  "))
	}
	return c, nil
}
