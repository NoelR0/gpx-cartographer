package web

import (
	"io/fs"
	"regexp"
	"testing"
)

// Every file index.html loads must be embedded, otherwise the page breaks
// with a 404 only in the built binary.
func TestIndexAssetsEmbedded(t *testing.T) {
	index, err := fs.ReadFile(FS, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	refs := regexp.MustCompile(`(?:src|href)="static/([^"]+)"`).FindAllSubmatch(index, -1)
	if len(refs) == 0 {
		t.Fatal("no static references found in index.html")
	}
	for _, m := range refs {
		if _, err := fs.Stat(FS, string(m[1])); err != nil {
			t.Errorf("index.html references %s, which is not embedded (see embed.go)", m[1])
		}
	}
}
