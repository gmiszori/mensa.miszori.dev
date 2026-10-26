// Package photos serves dish photos in two sizes: a small square thumbnail for the dish list
// and a large one, in the photo's original shape, for an opened dish.
//
// The sources' photos are large (ETH 1920x1280, UZH about 1400x1400, a few hundred KB each),
// far too big for a phone. The first request for a photo downloads it once and saves both
// sizes on disk; every later request is served from those files.
//
// Pages link to /photos/<key>.jpg (thumbnail) and /photos/<key>-large.jpg, where key is
// menu.PhotoKey of the source URL. The handler only fetches URLs it can look up by key, i.e.
// photos of dishes on the menus, so it cannot be abused to download arbitrary addresses.
package photos

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // sources may send PNG or WebP instead of JPEG
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Size is the thumbnail edge length in pixels: three times the 56px it is shown at, so it
// stays sharp on high-density phone screens.
const Size = 168

// LargeWidth is the width of the large photo: an opened dish shows it nearly as wide as a
// phone screen (about 350px), so this is sharp at three times that.
const LargeWidth = 1080

// minSide sorts out placeholders: for some dishes ETH returns a small caterer logo instead of
// a photo. The logos seen so far are 326 pixels on their short side, the smallest real ETH
// photos 365. Only sources listed in Handler.LogoSources are checked: UZH sends real photos
// as small as 480x270.
const minSide = 350

// Limits for what a source may send. Decoding needs about 4 bytes per pixel, so a huge image
// could use gigabytes of memory; real photos are 2-3 megapixels and well under 1 MB.
const (
	maxBytes  = 15 << 20   // 15 MB
	maxPixels = 12_000_000 // e.g. 4000 x 3000
)

// retryAfter is how long a photo whose download failed is not tried again: every request for
// it would otherwise start a new download from a source that is failing.
const retryAfter = 5 * time.Minute

// maxDownloads is how many photos are downloaded and resized at the same time. The first
// visit to a page asks for dozens of photos at once; the rest wait their turn.
const maxDownloads = 4

var (
	errNotAPhoto      = errors.New("image is a placeholder, not a photo")
	errRecentlyFailed = errors.New("photo download failed recently")
)

// Handler serves GET /photos/{file}, where file is "<key>.jpg" or "<key>-large.jpg".
type Handler struct {
	Dir    string                                 // where thumbnails are cached
	Lookup func(key string) (url string, ok bool) // source URL of a photo key (menu.Store.PhotoURL)
	HTTP   *http.Client
	// Allowed lists the URL prefixes photos may be downloaded from, redirects included. The
	// URLs come from the menu sources; this keeps a broken or hostile source from making the
	// server request other addresses, such as ones inside the Docker network.
	Allowed []string
	// LogoSources lists the URL prefixes whose small images are logos rather than photos
	// (see minSide). Images from other sources are shown whatever their size.
	LogoSources []string

	mu       sync.Mutex
	inFlight map[string]*download // photos being downloaded right now, by key
	failed   map[string]time.Time // when a photo's last download failed, by key
	slots    chan struct{}        // one per running download, at most maxDownloads
}

// download is one photo being fetched; requests for the same photo wait for it instead of
// downloading it again.
type download struct {
	done chan struct{}
	err  error
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name, ok := strings.CutSuffix(r.PathValue("file"), ".jpg")
	key, _ := strings.CutSuffix(name, "-large")
	if !ok || !validKey(key) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(h.Dir, name+".jpg")
	none := filepath.Join(h.Dir, key+".logo") // marks keys that turned out not to be photos
	if _, err := os.Stat(none); err == nil {
		http.NotFound(w, r)
		return
	}
	if _, err := os.Stat(path); err != nil {
		url, known := h.Lookup(key)
		if !known {
			http.NotFound(w, r)
			return
		}
		err := h.fetchOnce(url, key)
		if errors.Is(err, errNotAPhoto) {
			_ = os.WriteFile(none, nil, 0o644)
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "photo unavailable", http.StatusBadGateway)
			return
		}
	}
	// A key always stands for the same source URL, so browsers may keep it for a long time.
	w.Header().Set("Cache-Control", "public, max-age=2592000, immutable")
	http.ServeFile(w, r, path)
}

// fetchOnce makes sure each photo is downloaded once at a time, and at most maxDownloads
// photos in total.
func (h *Handler) fetchOnce(url, key string) error {
	h.mu.Lock()
	if h.inFlight == nil {
		h.inFlight = map[string]*download{}
		h.slots = make(chan struct{}, maxDownloads)
	}
	if d, ok := h.inFlight[key]; ok {
		h.mu.Unlock()
		<-d.done
		return d.err
	}
	if at, ok := h.failed[key]; ok && time.Since(at) < retryAfter {
		h.mu.Unlock()
		return errRecentlyFailed
	}
	d := &download{done: make(chan struct{})}
	h.inFlight[key] = d
	slots := h.slots
	h.mu.Unlock()

	// Another request may have finished this photo between our caller's check and now.
	if _, err := os.Stat(filepath.Join(h.Dir, key+".jpg")); err != nil {
		slots <- struct{}{}
		d.err = h.fetch(url, key)
		<-slots
	}

	h.mu.Lock()
	delete(h.inFlight, key)
	if d.err != nil && !errors.Is(d.err, errNotAPhoto) {
		if h.failed == nil {
			h.failed = map[string]time.Time{}
		}
		h.failed[key] = time.Now()
	} else {
		delete(h.failed, key)
	}
	h.mu.Unlock()
	close(d.done)
	return d.err
}

// allowed reports whether a photo may be downloaded from url.
func (h *Handler) allowed(url string) bool {
	return hasPrefix(url, h.Allowed)
}

func hasPrefix(url string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(url, prefix) {
			return true
		}
	}
	return false
}

// fetch downloads one photo and stores both sizes.
func (h *Handler) fetch(url, key string) error {
	if !h.allowed(url) {
		return fmt.Errorf("photo %s: not from an allowed source", url)
	}
	client := *h.HTTP
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !h.allowed(req.URL.String()) {
			return fmt.Errorf("photo %s: redirect to %s refused", url, req.URL)
		}
		return nil
	}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("photo %s: %s", url, resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return fmt.Errorf("photo %s: %w", url, err)
	}
	if len(raw) > maxBytes {
		return fmt.Errorf("photo %s: larger than %d bytes", url, maxBytes)
	}
	// Check the dimensions in the header before decoding the pixels.
	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("photo %s: %w", url, err)
	}
	if config.Width*config.Height > maxPixels {
		return fmt.Errorf("photo %s: %dx%d is too large", url, config.Width, config.Height)
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("photo %s: %w", url, err)
	}

	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	if side < minSide && hasPrefix(url, h.LogoSources) {
		return errNotAPhoto
	}

	// Thumbnail: crop the largest centred square, then scale it down.
	crop := image.Rect(0, 0, side, side).Add(image.Pt(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2))
	thumb := image.NewRGBA(image.Rect(0, 0, Size, Size))
	draw.CatmullRom.Scale(thumb, thumb.Bounds(), src, crop, draw.Src, nil)

	// Large: the whole photo, scaled down to LargeWidth (never up).
	width := min(b.Dx(), LargeWidth)
	height := (b.Dy()*width + b.Dx()/2) / b.Dx() // rounded
	large := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(large, large.Bounds(), src, b, draw.Src, nil)

	if err := os.MkdirAll(h.Dir, 0o755); err != nil {
		return err
	}
	// The thumbnail is written last: pages only link to the large photo after showing the
	// thumbnail, so by then both exist.
	if err := save(large, filepath.Join(h.Dir, key+"-large.jpg")); err != nil {
		return err
	}
	return save(thumb, filepath.Join(h.Dir, key+".jpg"))
}

// save writes img as a JPEG. It writes to a temporary name first so a half-written file is
// never served.
func save(img image.Image, path string) error {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Prune deletes cached photos whose key Lookup no longer knows, i.e. photos of dishes that
// are no longer on any menu, and temporary files left behind by a crash.
func (h *Handler) Prune() (removed int, err error) {
	entries, err := os.ReadDir(h.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		name := e.Name()
		stale := false
		if strings.HasSuffix(name, ".tmp") {
			info, err := e.Info()
			stale = err == nil && time.Since(info.ModTime()) > time.Hour
		} else if strings.HasSuffix(name, ".none") {
			// Markers of earlier versions, which also turned away small real photos: removed,
			// so those photos are tried again.
			stale = true
		} else {
			key, _, _ := strings.Cut(name, ".")
			key = strings.TrimSuffix(key, "-large")
			_, known := h.Lookup(key)
			stale = !known
		}
		if stale && os.Remove(filepath.Join(h.Dir, name)) == nil {
			removed++
		}
	}
	return removed, nil
}

// validKey accepts only lowercase hex, so a key can never escape the cache folder.
func validKey(key string) bool {
	if key == "" || len(key) > 64 {
		return false
	}
	for _, r := range key {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
