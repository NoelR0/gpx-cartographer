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
	_ = os.WriteFile(outside, []byte("secret"), 0o644)
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
	// symlinks to files are not regular files -> skipped
	want := []string{"a.jpg", "sub/b.JPG"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Walk = %v, want %v", got, want)
	}
	if _, err := Walk(filepath.Join(root, "doesnotexist"), jpg); err == nil {
		t.Error("expected an error for a missing directory")
	}
}

func TestOpen(t *testing.T) {
	root := setup(t)
	ok := []string{"a.jpg", "sub/b.JPG", "/a.jpg", "sub/../a.jpg"}
	bad := []string{"", "notes.txt", ".hidden.jpg", ".stversions/old.jpg", "../secret.jpg",
		"../../../../etc/passwd", "link.jpg", "sub", "missing.jpg"}
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
			t.Errorf("%q should have been rejected", p)
		}
	}
}

func TestCache(t *testing.T) {
	c := NewCache[int]()
	e := Entry{Path: "/x", Size: 1}
	c.Put(e, 42)
	if v, ok := c.Get(e); !ok || v != 42 {
		t.Error("expected a hit")
	}
	changed := e
	changed.Size = 2
	if _, ok := c.Get(changed); ok {
		t.Error("a changed file must not be a hit")
	}
	c.Prune(nil)
	if _, ok := c.Get(e); ok {
		t.Error("Prune did not clean up")
	}
}
