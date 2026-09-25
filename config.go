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

	Explorer bool // start with the explorer fog on

	// Command line only
	Healthcheck bool
	ShowVersion bool
}

// loadConfig reads environment variables; command-line flags take precedence.
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
			errs = append(errs, fmt.Sprintf("%s=%q is not a number", key, v))
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
			errs = append(errs, fmt.Sprintf("%s=%q is not a number", key, v))
			return def
		}
		return n
	}
	envBool := func(key string, def bool) bool {
		switch strings.ToLower(envStr(key, "")) {
		case "":
			return def
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
		errs = append(errs, fmt.Sprintf("%s: expected true or false", key))
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
		TileAttribution: envStr("TILE_ATTRIBUTION", `&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors`),
		TileMaxZoom:     envInt("TILE_MAX_ZOOM", 19),
		Explorer:        envBool("EXPLORER", true),
	}
	tzName := envStr("CAMERA_TZ", envStr("TZ", "Europe/Zurich"))

	fs := flag.NewFlagSet("gpx-cartographer", flag.ContinueOnError)
	fs.StringVar(&c.PhotoDir, "photos", c.PhotoDir, "photo directory (PHOTO_DIR)")
	fs.StringVar(&c.GPXDir, "gpx", c.GPXDir, "GPX directory (GPX_DIR)")
	fs.StringVar(&c.Addr, "addr", c.Addr, "address the server listens on (ADDR)")
	fs.StringVar(&tzName, "tz", tzName, "time zone for photos without an EXIF offset (CAMERA_TZ)")
	fs.BoolVar(&c.Explorer, "explorer", c.Explorer, "start in explorer mode; -explorer=false starts with the full map (EXPLORER)")
	fs.BoolVar(&c.Healthcheck, "healthcheck", false, "check whether a running server responds (for Docker)")
	fs.BoolVar(&c.ShowVersion, "version", false, "print version")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	loc, err := time.LoadLocation(tzName)
	if err != nil {
		errs = append(errs, fmt.Sprintf("unknown time zone %q", tzName))
		loc = time.UTC
	}
	c.CameraTZ = loc

	if c.ThumbSize < 32 || c.ThumbSize > 1024 {
		errs = append(errs, "THUMB_SIZE must be between 32 and 1024")
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid configuration:\n  %s", strings.Join(errs, "\n  "))
	}
	return c, nil
}
