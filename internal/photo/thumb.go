package photo

import (
	"bytes"
	"container/list"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // PNG-Decoder registrieren
	"io"
	"os"
	"sync"

	"github.com/NoelR0/gpx-cartographer/internal/exif"
)

// Thumbnailer erzeugt kleine JPEG-Vorschaubilder.
//
// Um Speicherspitzen zu vermeiden:
//   - wird bevorzugt das in der EXIF eingebettete Vorschaubild verwendet
//     (wenige KB, das eigentliche Foto wird dann gar nicht dekodiert),
//   - dürfen nur Workers Fotos gleichzeitig vollständig dekodiert werden
//     (ein 12-MP-JPEG belegt dabei ~18 MB),
//   - ist der Cache der fertigen Vorschaubilder in Bytes begrenzt.
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

// Thumbnail liefert das Vorschaubild für die bereits geöffnete Datei f.
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

	// 1. Versuch: eingebettetes Vorschaubild, wenn es gross genug ist.
	if len(m.Thumbnail) > 0 {
		if img, err := jpeg.Decode(bytes.NewReader(m.Thumbnail)); err == nil {
			b := img.Bounds()
			if min(b.Dx(), b.Dy()) >= t.Size*6/10 {
				img = cropToAspect(img, m.Width, m.Height)
				return encode(orient(downscale(img, t.Size), m.Orientation))
			}
		}
	}

	// 2. Versuch: ganzes Foto dekodieren – nur begrenzt viele gleichzeitig.
	select {
	case t.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err() // Browser hat die Anfrage abgebrochen (weggescrollt)
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

// Stats liefert Anzahl und Grösse der gecachten Vorschaubilder.
func (t *Thumbnailer) Stats() (count, bytes int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.items), t.bytes
}
