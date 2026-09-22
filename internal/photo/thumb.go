package photo

import (
	"bytes"
	"container/list"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // register PNG decoder
	"io"
	"os"
	"sync"

	"github.com/NoelR0/gpx-cartographer/internal/exif"
)

// Thumbnailer creates small JPEG thumbnails.
//
// To avoid memory spikes:
//   - the thumbnail embedded in the EXIF data is preferred
//     (a few KB; the actual photo is then not decoded at all),
//   - at most Workers photos may be fully decoded at the same time
//     (a 12 MP JPEG takes ~18 MB while doing so),
//   - the cache of finished thumbnails is limited in bytes.
type Thumbnailer struct {
	Size int
	sem  chan struct{}

	mu       sync.Mutex
	lru      *list.List
	items    map[string]*list.Element
	bytes    int
	maxBytes int
}

type thumbItem struct {
	key  string
	data []byte
}

func NewThumbnailer(size, workers, cacheBytes int) *Thumbnailer {
	return &Thumbnailer{
		Size:     size,
		sem:      make(chan struct{}, max(1, workers)),
		lru:      list.New(),
		items:    map[string]*list.Element{},
		maxBytes: cacheBytes,
	}
}

// Thumbnail returns the thumbnail for the already opened file f.
func (t *Thumbnailer) Thumbnail(ctx context.Context, f *os.File, info os.FileInfo) ([]byte, error) {
	key := fmt.Sprintf("%s|%d|%d", f.Name(), info.ModTime().UnixNano(), info.Size())
	if data, ok := t.get(key); ok {
		return data, nil
	}
	data, err := t.render(ctx, f)
	if err != nil {
		return nil, err
	}
	t.put(key, data)
	return data, nil
}

func (t *Thumbnailer) render(ctx context.Context, f *os.File) ([]byte, error) {
	m, err := exif.Read(f)
	if err != nil {
		return nil, err
	}

	// 1st attempt: embedded thumbnail, if it is large enough.
	if len(m.Thumbnail) > 0 {
		if img, err := jpeg.Decode(bytes.NewReader(m.Thumbnail)); err == nil {
			b := img.Bounds()
			if min(b.Dx(), b.Dy()) >= t.Size*6/10 {
				img = cropToAspect(img, m.Width, m.Height)
				return encode(orient(downscale(img, t.Size), m.Orientation))
			}
		}
	}

	// 2nd attempt: decode the whole photo – only a limited number at a time.
	select {
	case t.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err() // browser canceled the request (scrolled away)
	}
	defer func() { <-t.sem }()

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	return encode(orient(downscale(img, t.Size), m.Orientation))
}

func encode(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (t *Thumbnailer) get(key string) ([]byte, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if el, ok := t.items[key]; ok {
		t.lru.MoveToFront(el)
		return el.Value.(*thumbItem).data, true
	}
	return nil, false
}

func (t *Thumbnailer) put(key string, data []byte) {
	if len(data) > t.maxBytes {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.items[key]; ok {
		return
	}
	t.items[key] = t.lru.PushFront(&thumbItem{key, data})
	t.bytes += len(data)
	for t.bytes > t.maxBytes {
		el := t.lru.Back()
		it := el.Value.(*thumbItem)
		t.lru.Remove(el)
		delete(t.items, it.key)
		t.bytes -= len(it.data)
	}
}

// Stats returns the count and size of the cached thumbnails.
func (t *Thumbnailer) Stats() (count, bytes int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.items), t.bytes
}
