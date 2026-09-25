# GPX Cartographer

>[!CAUTION]
>**Warning beforehand: mostly/fully vibe-coded.**
>I was watching a video of Matthias random stuff
>and he build an app for displaying his images on a map.
>I found this concept cool and thought that one can expand it
>for gpx tracks.
>Enjoy!

Shows your photos (based on their EXIF GPS data) and GPX tracks on an OpenStreetMap map.
Both folders are re-scanned on every page load or when you click ⟳, which makes it a good fit
for folders filled by Syncthing.

It ships as a single static binary (~11 MB) with no external dependencies; the web interface is
embedded. Nothing is ever written to disk.

> [!WARNING]
> GPX Cartographer has **no authentication**. Anyone who can reach the page can see all photos
> including where they were taken, and all tracks, which makes it easy to work out e.g. where
> you live. Only run it on your home network or behind a reverse proxy with authentication
> (e.g. Caddy/Traefik/nginx with basic auth, Authelia, Authentik, or via a VPN such as
> WireGuard/Tailscale).

## Features

- Photos appear as thumbnails on the map; photos close to each other are grouped into a stack
- Click a photo → full-screen view with previous/next (arrow keys, swipe) and a filmstrip
- Click a stack → zoom in, or view the photos directly if they were all taken at the same spot
- GPX tracks and routes in different colours, with a list showing date, distance and elevation gain
- Click a track → statistics, "view photos" (all photos taken while the track was recorded),
  GPX download
- Explorer mode (on by default): the map starts out black, and every zoom-14 map tile one of
  your tracks passes through is uncovered together with its eight neighbours. The panel on the
  right shows how many tiles you have discovered and how much of the state/province, country,
  continent and world (land area) at the map center that is
- Photos tab: all photos as a grid, newest first and grouped by day, filterable by
  whether they have a position (so photos missing from the map can be found too)
- Statistics tab: all-time totals and records (including the discovered share of the world's
  land area from explorer mode), the year compared with the two before it,
  months, weeks of a month, an activity calendar (one square per day), weekday and start time;
  average speed both in motion and including breaks
- Photos **without** GPS data are placed on a GPX track based on when they were taken
  (shown with a dashed border)
- JPEG and PNG; subfolders are included, hidden folders such as `.stversions` are ignored

## Quick start without Docker

```sh
go build -o gpx-cartographer .
./gpx-cartographer -photos ~/Pictures/map -gpx ~/gpx
```

→ <http://localhost:8080>

Or install it directly (Go ≥ 1.25):

```sh
go install github.com/NoelR0/gpx-cartographer@latest
```

## Docker

Prebuilt images for amd64 and arm64 are published for every release:

```yaml
services:
  gpx-cartographer:
    image: ghcr.io/noelr0/gpx-cartographer:0.1   # or a full version such as 0.1.0, or latest
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - /path/to/photos:/data/photos:ro
      - /path/to/gpx:/data/gpx:ro
```

Update with `docker compose pull && docker compose up -d`.

To build the image yourself instead, point the two volumes in `docker-compose.yml` to your
folders and run:

```sh
docker compose up -d --build
```

The image is based on `scratch` (just the binary, ~11 MB) and runs as user `nobody`. The folders
are mounted read-only (`:ro`). If `nobody` cannot read the files, set `user: "<uid>:<gid>"` of
your Syncthing user in the compose file.

## Configuration

Environment variables (`PHOTO_DIR`, `GPX_DIR`, `ADDR`, `CAMERA_TZ` and `EXPLORER` are also
available as the flags `-photos`, `-gpx`, `-addr`, `-tz` and `-explorer`, e.g. `-explorer=false`):

| Variable | Default | Description |
| --- | --- | --- |
| `PHOTO_DIR` | `/data/photos` | Folder containing the photos |
| `GPX_DIR` | `/data/gpx` | Folder containing the GPX files |
| `ADDR` | `:8080` | Listen address and port (alternatively `PORT`) |
| `CAMERA_TZ` | `Europe/Zurich` | Time zone for photos without an EXIF time zone offset |
| `MATCH_PHOTOS_TO_TRACKS` | `true` | Place photos without GPS using GPX timestamps |
| `MATCH_MAX_GAP` | `300` | Maximum time in seconds between a photo and a track point |
| `TRACK_SIMPLIFY_M` | `3` | Tolerance in metres for simplifying tracks on the map (0 = off) |
| `THUMB_SIZE` | `160` | Edge length of thumbnails in px |
| `THUMB_WORKERS` | `2` | How many photos may be fully decoded at the same time |
| `THUMB_CACHE_MB` | `32` | Memory limit for cached thumbnails |
| `TILE_URL` | OSM default | Tile server, e.g. `https://tile.opentopomap.org/{z}/{x}/{y}.png` (see below) |
| `TILE_ATTRIBUTION` | OSM | Attribution shown for the tiles |
| `TILE_MAX_ZOOM` | `19` | Maximum zoom level |
| `EXPLORER` | `true` | Start in [explorer mode](#explorer-mode); `false` starts with the normal map |
| `GOMEMLIMIT` | `64MiB` in the image | Soft memory limit of the Go runtime |

### Map tiles

By default, tiles are loaded from `tile.openstreetmap.org`. That is fine for personal use, but
the OpenStreetMap Foundation's [Tile Usage Policy](https://operations.osmfoundation.org/policies/tiles/)
prohibits heavy use. If you run the application for many users, set `TILE_URL` to a different
provider or your own tile server and adjust `TILE_ATTRIBUTION` accordingly.

## Explorer mode

Explorer mode turns your tracks into a map you uncover, like the fog of war in a game:

- The whole map starts out **black**.
- The world is divided into the map tiles of **zoom level 14** (about 2.4 × 2.4 km at the
  equator, ~1.7 × 1.7 km in Central Europe).
- Every tile one of your tracks passes through is uncovered **together with its eight
  neighbours**, so a single ride reveals a strip about three tiles wide.
- Tracks and photos stay visible on top of the black area. Once zoomed in far enough, a thin
  grid shows the tile borders.
- Only tracks **with timestamps** count; planned routes without times do not uncover anything.

The panel on the top right switches the mode on and off and shows

- how many tiles you have discovered, and
- how much of the **state/province, country, continent and world** (land area) at the map
  center that is, in % and km². Tiles at sea are not counted.

The statistics tab shows the discovered share of the world's land area in the *All time* card,
whether explorer mode is on or not.

### Starting with the normal map

Explorer mode is **on by default**, i.e. every time the page is loaded. To start with the normal
map instead, set the environment variable `EXPLORER=false`, for example in `docker-compose.yml`:

```yaml
    environment:
      EXPLORER: "false"
```

or pass the flag when starting the binary:

```sh
./gpx-cartographer -explorer=false
```

The switch in the panel still turns explorer mode on for the current visit; the setting only
decides how the map starts.

### What it costs

- **Memory:** the region outlines needed for the area statistics (~7 MB, embedded in the
  binary) are loaded the first time the statistics are requested (explorer mode switched on or
  statistics tab opened) and then stay in memory. With `EXPLORER=false` and the statistics tab
  never opened, they are not loaded at all.
- **CPU:** the discovered area is computed once per set of tracks and cached; it is only
  recomputed when GPX files are added, changed or removed.
- **Browser:** the discovered tiles are computed and drawn in the browser from the track data
  that is loaded anyway; no extra requests to the tile server are made.
- **Binary size:** the embedded region data (Natural Earth, public domain) adds ~3 MB.

## Memory usage

Measured with 2,000 photos and 100 tracks of 10,000 points each (1 million points, 93 MB of GPX):

| | |
| --- | --- |
| Idle | ~9 MB |
| Data actually in use | ~16 MB |
| Peak with `GOMEMLIMIT=64MiB` | ~66 MB |
| Peak without a limit | ~130 MB |

How this is achieved:

- **Tracks:** For the map, each segment is simplified using Douglas-Peucker (1 million →
  ~22,000 points). A compact time index with all points (12 bytes per point) is kept for
  placing photos.
- **Thumbnails:** Most phone JPEGs already contain a small preview image in their EXIF data.
  This is used instead, so the actual photo is not decoded at all (~2 ms instead of ~170 ms).
  Only photos without an embedded preview are decoded, at most `THUMB_WORKERS` at a time
  (a 12 MP photo takes up ~18 MB while being decoded).
- Metadata and thumbnails are kept in memory as long as the file does not change.
- **Region outlines** for the explorer statistics (~7 MB) are only loaded once the
  statistics are requested for the first time (when explorer mode is switched on or the
  statistics tab is opened).

`/api/stats` shows the current memory usage.

## API

- `GET /api/data` – all photos and tracks (JSON, gzip)
- `GET /api/photo/thumb?path=…`, `/api/photo/full?path=…`, `/api/photo/original?path=…`
- `GET /api/track/download?file=…`
- `GET /api/coverage?lat=…&lon=…` – discovered share (explorer mode) of the world and of the
  continent, country and state at lat/lon
- `GET /api/stats` – memory usage
- `GET /healthz`

## Development

```sh
go test ./...
go run . -photos ./example/photos -gpx ./example/gpx
```

The version number is set at build time: `go build -ldflags "-X main.version=v1.2.3"`.

### Releases

Releases are annotated git tags following [semantic versioning](https://semver.org)
(`vMAJOR.MINOR.PATCH`). Pushing a tag builds and publishes the Docker image via GitHub Actions:

```sh
git log --oneline v0.1.0..HEAD          # changes since the last release
git tag -a v0.2.0 -m "Short description"
git push origin v0.2.0
```

The image is tagged `0.2.0`, `0.2` and `latest` (from v1 on additionally `1`).
`go install …@latest` also resolves to the newest tag.

## License

GPX Cartographer is licensed under the [MIT License](LICENSE).

### Third-party software

| Component | License | Notes |
| --- | --- | --- |
| [Go](https://go.dev) standard library | BSD-3-Clause | linked into the binary, [license text](https://go.dev/LICENSE) |
| [Leaflet](https://leafletjs.com) 1.9.4 | BSD-2-Clause | unmodified in `web/vendor/`, see `web/vendor/LICENSE-leaflet.txt` |
| [Leaflet.markercluster](https://github.com/Leaflet/Leaflet.markercluster) 1.5.3 | MIT | unmodified in `web/vendor/`, see `web/vendor/LICENSE-leaflet.markercluster.txt` |

Apart from the Go standard library, no Go modules are used. The Docker image contains all
license texts in `/licenses`. If you redistribute the binary yourself, include the license
texts as well.

| Data | License | Notes |
| --- | --- | --- |
| [Natural Earth](https://www.naturalearthdata.com) 1:10m admin 0 and admin 1 | Public domain | simplified and embedded as `internal/regions/regions.bin.gz`, regenerate with `go generate ./internal/regions` |

Map data © [OpenStreetMap](https://www.openstreetmap.org/copyright) contributors (ODbL).
