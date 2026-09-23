// Package web contains the web UI that is embedded into the binary.
package web

import "embed"

//go:embed index.html app.js stats.js gallery.js style.css vendor
var FS embed.FS
