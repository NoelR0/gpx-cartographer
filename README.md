# Cartographer

Zeigt Fotos (anhand ihrer EXIF-GPS-Daten) und GPX-Tracks auf einer OpenStreetMap-Karte.
Beide Ordner werden bei jedem Seitenaufruf bzw. Klick auf ⟳ neu eingelesen – ideal für per
Syncthing befüllte Ordner.

Eine einzige statische Binary (~8 MB) ohne externe Abhängigkeiten; die Weboberfläche ist
eingebettet. Es wird nichts auf die Platte geschrieben.

## Funktionen

- Fotos als Vorschaubilder auf der Karte, nahe beieinander liegende werden zu einem Stapel gruppiert
- Klick auf ein Foto → Vollbild mit Vor/Zurück (Pfeiltasten, Wischen) und Filmleiste
- Klick auf einen Stapel → hineinzoomen, bzw. Fotos direkt ansehen, wenn sie am selben Ort liegen
- GPX-Tracks und -Routen farbig, Liste mit Datum, Distanz und Höhenmetern
- Klick auf einen Track → Statistik, „Fotos ansehen" (alle Fotos, die während des Tracks
  aufgenommen wurden), GPX-Download
- Fotos **ohne** GPS werden über ihre Aufnahmezeit auf einem GPX-Track verortet (gestrichelter Rahmen)
- JPEG und PNG; Unterordner werden mit durchsucht, versteckte Ordner wie `.stversions` ignoriert

## Schnellstart ohne Docker

```sh
go build -o cartographer .
./cartographer -photos ~/Bilder/Karte -gpx ~/gpx
```

→ http://localhost:8080

## Docker

In `docker-compose.yml` die beiden Volumes auf deine Syncthing-Ordner anpassen, dann:

```sh
docker compose up -d --build
```

Das Image basiert auf `scratch` (nur die Binary, ~8 MB) und läuft als Benutzer `nobody`.
Die Ordner werden nur lesend (`:ro`) eingebunden. Sind die Dateien für `nobody` nicht lesbar,
in der Compose-Datei `user: "<uid>:<gid>"` des Syncthing-Benutzers setzen.

## Konfiguration

Umgebungsvariablen (die ersten vier auch als Flags `-photos`, `-gpx`, `-addr`, `-tz`):

| Variable | Standard | Bedeutung |
|---|---|---|
| `PHOTO_DIR` | `/data/photos` | Ordner mit den Fotos |
| `GPX_DIR` | `/data/gpx` | Ordner mit den GPX-Dateien |
| `ADDR` | `:8080` | Adresse und Port (alternativ `PORT`) |
| `CAMERA_TZ` | `Europe/Zurich` | Zeitzone für Fotos ohne EXIF-Zeitzonen-Angabe |
| `MATCH_PHOTOS_TO_TRACKS` | `true` | Fotos ohne GPS über GPX-Zeitstempel verorten |
| `MATCH_MAX_GAP` | `300` | max. Abstand in Sekunden zwischen Foto und Trackpunkt |
| `TRACK_SIMPLIFY_M` | `3` | Toleranz der Track-Vereinfachung für die Karte in Metern (0 = aus) |
| `THUMB_SIZE` | `160` | Kantenlänge der Vorschaubilder in px |
| `THUMB_WORKERS` | `2` | wie viele Fotos gleichzeitig vollständig dekodiert werden dürfen |
| `THUMB_CACHE_MB` | `32` | Obergrenze für Vorschaubilder im Speicher |
| `TILE_URL` | OSM-Standard | Kachel-Server, z. B. `https://tile.opentopomap.org/{z}/{x}/{y}.png` |
| `TILE_ATTRIBUTION` | OSM | Quellenangabe der Kacheln |
| `TILE_MAX_ZOOM` | `19` | maximaler Zoom |
| `GOMEMLIMIT` | im Image `64MiB` | weiche Speichergrenze der Go-Laufzeit |

## Speicherbedarf

Gemessen mit 2000 Fotos und 100 Tracks à 10'000 Punkten (1 Mio. Punkte, 93 MB GPX):

| | |
|---|---|
| Leerlauf | ~9 MB |
| tatsächlich belegte Daten | ~16 MB |
| Spitze mit `GOMEMLIMIT=64MiB` | ~66 MB |
| Spitze ohne Limit | ~130 MB |

Wie das erreicht wird:

- **Tracks:** Für die Karte wird jedes Segment mit Douglas-Peucker vereinfacht (1 Mio. → ~22'000
  Punkte). Für die Foto-Verortung bleibt ein kompakter Zeitindex mit allen Punkten
  (12 Byte pro Punkt).
- **Vorschaubilder:** Die meisten Handy-JPEGs enthalten bereits ein kleines Vorschaubild in den
  EXIF-Daten. Das wird verwendet, das eigentliche Foto wird dann gar nicht dekodiert (~2 ms statt
  ~170 ms). Nur ohne eingebettetes Vorschaubild wird das Foto dekodiert, höchstens
  `THUMB_WORKERS` gleichzeitig (ein 12-MP-Foto belegt dabei ~18 MB).
- Metadaten und Vorschaubilder werden im RAM gehalten, solange sich die Datei nicht ändert.

`/api/stats` zeigt den aktuellen Verbrauch.

## API

- `GET /api/data` – alle Fotos und Tracks (JSON, gzip)
- `GET /api/photo/thumb?path=…`, `/api/photo/full?path=…`, `/api/photo/original?path=…`
- `GET /api/track/download?file=…`
- `GET /api/stats` – Speicherverbrauch
- `GET /healthz`

## Entwicklung

```sh
go test ./...
go run . -photos ./beispiel/fotos -gpx ./beispiel/gpx
```

Die Versionsnummer wird beim Bauen gesetzt: `go build -ldflags "-X main.version=1.2.3"`.

## Lizenzen von Drittanbietern

Im Verzeichnis `web/vendor` liegen unverändert:

- [Leaflet](https://leafletjs.com) 1.9.4 – BSD-2-Clause, siehe `web/vendor/LICENSE-leaflet.txt`
- [Leaflet.markercluster](https://github.com/Leaflet/Leaflet.markercluster) 1.5.3 – MIT,
  siehe `web/vendor/LICENSE-leaflet.markercluster.txt`

Der Go-Code verwendet ausschliesslich die Standardbibliothek.
