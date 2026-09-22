// Package web enthält die Weboberfläche, die in die Binary eingebettet wird.
package web

import "embed"

//go:embed index.html app.js style.css vendor
var FS embed.FS
