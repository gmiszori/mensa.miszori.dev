package photos

import (
	"bytes"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestHandler(t *testing.T) {
	// A fake photo server: a large picture, and small images under /small/ (a logo, if the
	// source is one that sends logos).
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		side := 1400
		if strings.HasPrefix(r.URL.Path, "/small/") {
			side = 300
		}
		_ = jpeg.Encode(w, image.NewRGBA(image.Rect(0, 0, side, side*2/3)), nil)
	}))
	defer source.Close()

	known := map[string]string{
		"aa11": source.URL + "/photo",
		"bb22": source.URL + "/small/logos/sv",    // from a source that sends logos
		"dd44": source.URL + "/small/photos/soup", // a small real photo from another source
	}
	h := &Handler{
		Dir:         t.TempDir(),
		Lookup:      func(key string) (string, bool) { url, ok := known[key]; return url, ok },
		HTTP:        source.Client(),
		Allowed:     []string{source.URL + "/"},
		LogoSources: []string{source.URL + "/small/logos/"},
	}
	mux := http.NewServeMux()
	mux.Handle("GET /photos/{file}", h)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	rec := get("/photos/aa11.jpg")
	if rec.Code != http.StatusOK {
		t.Fatalf("photo: status %d", rec.Code)
	}
	img, err := jpeg.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != Size || b.Dy() != Size {
		t.Errorf("thumbnail is %v, want %dx%d", b, Size, Size)
	}

	// The large photo was saved by the same download and keeps the original 3:2 shape.
	rec = get("/photos/aa11-large.jpg")
	if rec.Code != http.StatusOK {
		t.Fatalf("large photo: status %d", rec.Code)
	}
	img, err = jpeg.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != LargeWidth || b.Dy() != LargeWidth*2/3 {
		t.Errorf("large photo is %v, want %dx%d", b, LargeWidth, LargeWidth*2/3)
	}

	for path, want := range map[string]int{
		"/photos/dd44.jpg":       http.StatusOK,
		"/photos/bb22.jpg":       http.StatusNotFound, // a logo, not a photo
		"/photos/cc33.jpg":       http.StatusNotFound, // not a dish on the menu
		"/photos/..%2Fetc.jpg":   http.StatusNotFound, // not a valid key
		"/photos/aa11.png":       http.StatusNotFound,
		"/photos/bb22-large.jpg": http.StatusNotFound,
		"/photos/aa11-small.jpg": http.StatusNotFound,
	} {
		if got := get(path).Code; got != want {
			t.Errorf("%s: status %d, want %d", path, got, want)
		}
	}
}

func TestTooLarge(t *testing.T) {
	// A picture whose header says 5000x5000 (25 megapixels) is refused before decoding.
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = jpeg.Encode(w, image.NewGray(image.Rect(0, 0, 5000, 5000)), nil)
	}))
	defer source.Close()
	h := &Handler{Dir: t.TempDir(), HTTP: source.Client(), Allowed: []string{source.URL},
		Lookup: func(string) (string, bool) { return source.URL, true }}
	if err := h.fetchOnce(source.URL, "aa"); err == nil {
		t.Error("a 25 megapixel image was accepted")
	}
}

func TestOneDownloadPerPhoto(t *testing.T) {
	var downloads atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		_ = jpeg.Encode(w, image.NewRGBA(image.Rect(0, 0, 800, 600)), nil)
	}))
	defer source.Close()
	h := &Handler{Dir: t.TempDir(), HTTP: source.Client(), Allowed: []string{source.URL},
		Lookup: func(string) (string, bool) { return source.URL, true }}
	mux := http.NewServeMux()
	mux.Handle("GET /photos/{file}", h)

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/photos/ab12.jpg", nil))
			if rec.Code != http.StatusOK {
				t.Errorf("status %d", rec.Code)
			}
		}()
	}
	wg.Wait()
	// Requests that arrive after the download finished are served from disk, so at most a
	// handful of downloads can happen; without deduplication it would be 20.
	if n := downloads.Load(); n != 1 {
		t.Errorf("%d downloads for one photo, want 1", n)
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	// cc.logo marks a dish no longer on the menu; aa.none is a marker of an earlier version.
	for _, name := range []string{"aa.jpg", "aa-large.jpg", "aa.none", "bb.jpg", "bb-large.jpg", "cc.logo"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h := &Handler{Dir: dir, Lookup: func(key string) (string, bool) { return "", key == "aa" }}
	removed, err := h.Prune()
	if err != nil {
		t.Fatal(err)
	}
	left, _ := os.ReadDir(dir)
	if removed != 4 || len(left) != 2 {
		t.Errorf("removed %d, %d files left; want 4 removed and aa's two photos kept", removed, len(left))
	}
}

func TestOnlyAllowedSources(t *testing.T) {
	var asked int
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		if r.URL.Path == "/elsewhere" {
			http.Redirect(w, r, "http://127.0.0.1:1/internal", http.StatusFound)
			return
		}
		_ = jpeg.Encode(w, image.NewRGBA(image.Rect(0, 0, 800, 600)), nil)
	}))
	defer source.Close()
	h := &Handler{Dir: t.TempDir(), HTTP: source.Client(), Allowed: []string{source.URL + "/photos/"},
		Lookup: func(string) (string, bool) { return "", true }}

	if err := h.fetchOnce(source.URL+"/other/1.jpg", "aa"); err == nil || asked != 0 {
		t.Errorf("a URL outside the allowed prefix was fetched (err %v, %d requests)", err, asked)
	}
	if err := h.fetchOnce(source.URL+"/photos/2.jpg", "bb"); err != nil {
		t.Errorf("an allowed URL failed: %v", err)
	}
	h.Allowed = []string{source.URL + "/elsewhere"}
	if err := h.fetchOnce(source.URL+"/elsewhere", "cc"); err == nil {
		t.Error("a redirect to another address was followed")
	}
}

func TestFailedPhotoWaits(t *testing.T) {
	var asked int
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer source.Close()
	h := &Handler{Dir: t.TempDir(), HTTP: source.Client(), Allowed: []string{source.URL},
		Lookup: func(string) (string, bool) { return source.URL, true }}
	for range 3 {
		if err := h.fetchOnce(source.URL, "aa"); err == nil {
			t.Fatal("a failing download succeeded")
		}
	}
	if asked != 1 {
		t.Errorf("the source was asked %d times within retryAfter, want 1", asked)
	}
}
