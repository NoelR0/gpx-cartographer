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
 # Licenses for the image: our own, Go standard library (linked into the binary), frontend libraries
 && mkdir -p /out/licenses \
 && cp LICENSE /out/licenses/LICENSE \
 && cp "$(go env GOROOT)/LICENSE" /out/licenses/LICENSE-go \
 && cp web/vendor/LICENSE-*.txt /out/licenses/

# ---- Runtime: only the static binary ----
FROM scratch
COPY --from=build /out/gpx-cartographer /gpx-cartographer
COPY --from=build /out/licenses /licenses

ENV PHOTO_DIR=/data/photos \
    GPX_DIR=/data/gpx \
    ADDR=:8080 \
    # Soft memory limit: Go collects garbage earlier instead of growing the heap.
    # Increase for very large collections (see /api/stats -> heap_mb).
    GOMEMLIMIT=64MiB

USER 65534:65534
EXPOSE 8080
HEALTHCHECK --interval=60s --timeout=5s CMD ["/gpx-cartographer", "-healthcheck"]
ENTRYPOINT ["/gpx-cartographer"]
