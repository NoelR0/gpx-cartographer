// Package files walks directories and keeps one result per file in memory
// for as long as its modification time and size do not change. Nothing is
// written to disk.
package files

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Entry is a file that was found.
type Entry struct {
	Path    string // absolute path
	Rel     string // relative path with '/'
	Size    int64
	ModTime time.Time
}

func (e Entry) stamp() stamp { return stamp{e.ModTime.UnixNano(), e.Size} }

// Walk returns all files under root with one of the extensions (lower case,
// with dot). Hidden files and directories – e.g. Syncthing's .stversions and
// .stfolder – are skipped.
func Walk(root string, exts map[string]bool) ([]Entry, error) {
	var out []Entry
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil // ignore unreadable subdirectories
		}
		if p != root && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !exts[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		out = append(out, Entry{Path: p, Rel: filepath.ToSlash(rel), Size: info.Size(), ModTime: info.ModTime()})
		return nil
	})
	return out, err
}

// Open opens rel inside root. Paths that lead outside of root (including via
// symlinks), hidden paths and other extensions are rejected.
func Open(root, rel string, exts map[string]bool) (*os.File, fs.FileInfo, error) {
	rel = path.Clean("/" + rel)[1:]
	if rel == "" || !exts[strings.ToLower(path.Ext(rel))] {
		return nil, nil, fs.ErrNotExist
	}
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") {
			return nil, nil, fs.ErrNotExist
		}
	}
	f, err := os.OpenInRoot(root, filepath.FromSlash(rel))
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, fs.ErrNotExist
	}
	return f, info, nil
}

type stamp struct {
	mtime int64
	size  int64
}

type cached[T any] struct {
	stamp stamp
	value T
}

// Cache holds one result of type T per file path.
type Cache[T any] struct {
	mu sync.Mutex
	m  map[string]cached[T]
}

func NewCache[T any]() *Cache[T] { return &Cache[T]{m: map[string]cached[T]{}} }

// Get returns the stored value if the file is unchanged.
func (c *Cache[T]) Get(e Entry) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[e.Path]
	if !ok || v.stamp != e.stamp() {
		var zero T
		return zero, false
	}
	return v.value, true
}

func (c *Cache[T]) Put(e Entry, v T) {
	c.mu.Lock()
	c.m[e.Path] = cached[T]{e.stamp(), v}
	c.mu.Unlock()
}

// Prune removes entries for files that no longer exist.
func (c *Cache[T]) Prune(alive []Entry) {
	keep := make(map[string]bool, len(alive))
	for _, e := range alive {
		keep[e.Path] = true
	}
	c.mu.Lock()
	for p := range c.m {
		if !keep[p] {
			delete(c.m, p)
		}
	}
	c.mu.Unlock()
}

// Parallel calls fn(i) for all i in [0, n) using at most workers concurrent
// goroutines and waits for all of them.
func Parallel(n, workers int, fn func(i int)) {
	workers = min(n, max(1, workers))
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				fn(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	wg.Wait()
}
