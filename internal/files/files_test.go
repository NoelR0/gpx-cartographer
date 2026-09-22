package files

import (
	"os"
	"path/filepath"
	"testing"
)

func setup(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{
		"a.jpg", "sub/b.JPG", "notes.txt", ".hidden.jpg", ".stversions/old.jpg", "sub/.stfolder/x.jpg",
	} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "secret.jpg")
	_ = os.WriteFile(outside, []byte("geheim"), 0o644)
	_ = os.Symlink(outside, filepath.Join(root, "link.jpg"))
	return root
}

var jpg = map[string]bool{".jpg": true}

func TestWalk(t *testing.T) {
	root := setup(t)
	entries, err := Walk(root, jpg)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Rel)
	}
	// Symlinks auf Dateien sind keine regulären Dateien -> werden übersprungen
	want := []string{"a.jpg", "sub/b.JPG"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Walk = %v, erwartet %v", got, want)
	}
	if _, err := Walk(filepath.Join(root, "gibtsnicht"), jpg); err == nil {
		t.Error("Fehler für fehlenden Ordner erwartet")
	}
}

func TestOpen(t *testing.T) {
	root := setup(t)
	ok := []string{"a.jpg", "sub/b.JPG", "/a.jpg", "sub/../a.jpg"}
	bad := []string{"", "notes.txt", ".hidden.jpg", ".stversions/old.jpg", "../secret.jpg",
		"../../../../etc/passwd", "link.jpg", "sub", "fehlt.jpg"}
	for _, p := range ok {
		f, _, err := Open(root, p, jpg)
		if err != nil {
			t.Errorf("%q: %v", p, err)
			continue
		}
		f.Close()
	}
	for _, p := range bad {
		if f, _, err := Open(root, p, jpg); err == nil {
			f.Close()
			t.Errorf("%q hätte abgelehnt werden müssen", p)
		}
	}
}

func TestCache(t *testing.T) {
	c := NewCache[int]()
	e := Entry{Path: "/x", Size: 1}
	c.Put(e, 42)
	if v, ok := c.Get(e); !ok || v != 42 {
		t.Error("Treffer erwartet")
	}
	changed := e
	changed.Size = 2
	if _, ok := c.Get(changed); ok {
		t.Error("geänderte Datei darf kein Treffer sein")
	}
	c.Prune(nil)
	if _, ok := c.Get(e); ok {
		t.Error("Prune hat nicht aufgeräumt")
	}
}
