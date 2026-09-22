# ---- Build ----
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN go test ./... \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/gpx-cartographer . \
 # Lizenzen für das Image: eigene, Go-Standardbibliothek (in die Binary gelinkt), Frontend-Bibliotheken
 && mkdir -p /out/licenses \
 && cp LICENSE /out/licenses/LICENSE \
 && cp "$(go env GOROOT)/LICENSE" /out/licenses/LICENSE-go \
 && cp web/vendor/LICENSE-*.txt /out/licenses/

# ---- Laufzeit: nur die statische Binary ----
FROM scratch
COPY --from=build /out/gpx-cartographer /gpx-cartographer
COPY --from=build /out/licenses /licenses

ENV PHOTO_DIR=/data/photos \
    GPX_DIR=/data/gpx \
    ADDR=:8080 \
    # Weiche Speichergrenze: Go räumt früher auf, statt den Heap wachsen zu lassen.
    # Bei sehr grossen Sammlungen erhöhen (siehe /api/stats -> heap_mb).
    GOMEMLIMIT=64MiB

USER 65534:65534
EXPOSE 8080
HEALTHCHECK --interval=60s --timeout=5s CMD ["/gpx-cartographer", "-healthcheck"]
ENTRYPOINT ["/gpx-cartographer"]
