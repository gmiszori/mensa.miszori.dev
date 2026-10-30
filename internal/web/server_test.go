package web

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mensaplan/internal/catalog"
	"mensaplan/internal/menu"
)

func TestPastDayRedirects(t *testing.T) {
	zone, _ := time.LoadLocation("Europe/Zurich")
	store, err := menu.Open(filepath.Join(t.TempDir(), "menus.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Zone: zone, Store: store}
	today := time.Now().In(zone).Format(time.DateOnly)
	location := func() string {
		w := httptest.NewRecorder()
		s.home(w, httptest.NewRequest("GET", "/?day=2020-01-06&meal=dinner", nil))
		if w.Code != http.StatusFound {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
		}
		return w.Header().Get("Location")
	}

	// No menus today: drop the day, so the page shows the next day with menus.
	if got, want := location(), "/?meal=dinner"; got != want {
		t.Errorf("without menus today: Location = %q, want %q", got, want)
	}

	// Menus today: go to today.
	store.Replace("a", today, today, map[string]menu.Day{
		today: {Lunch: menu.Meal{Dishes: []menu.Dish{{Name: menu.Same("Soup")}}}},
	})
	if got, want := location(), "/?day="+today+"&meal=dinner"; got != want {
		t.Errorf("with menus today: Location = %q, want %q", got, want)
	}
}

// The page must be the same for every visitor, whatever their browser sends, since Cloudflare
// keeps one copy of it for everyone; redirects must not be kept at all.
func TestPageIsSharedCache(t *testing.T) {
	zone, _ := time.LoadLocation("Europe/Zurich")
	store, err := menu.Open(filepath.Join(t.TempDir(), "menus.json"))
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	h := (&Server{Zone: zone, Store: store, Catalog: cat, Photos: http.NotFoundHandler(), Log: slog.Default()}).Handler()
	get := func(target, acceptLanguage string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", target, nil)
		r.Header.Set("Accept-Language", acceptLanguage)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	en, de := get("/", "en-US,en;q=0.9"), get("/", "de-CH,de;q=0.9")
	if en.Code != http.StatusOK {
		t.Fatalf("status = %d", en.Code)
	}
	if !strings.Contains(de.Body.String(), `<html lang="en">`) {
		t.Error(`a German browser didn't get <html lang="en">`)
	}
	if en.Body.String() != de.Body.String() {
		t.Error("the page differs by Accept-Language")
	}
	if got := de.Header().Get("CDN-Cache-Control"); got != "max-age=60" {
		t.Errorf("page: CDN-Cache-Control = %q", got)
	}
	if got := de.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("page: Cache-Control = %q", got)
	}

	redirect := get("/?day=2020-01-06", "")
	if redirect.Code != http.StatusFound {
		t.Fatalf("past day: status = %d", redirect.Code)
	}
	if got := redirect.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("redirect: Cache-Control = %q", got)
	}
	if got := redirect.Header().Get("CDN-Cache-Control"); got != "" {
		t.Errorf("redirect: CDN-Cache-Control = %q", got)
	}
}

func TestSiteURL(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "evil.example"
	if got := (&Server{Domain: "mensa.miszori.dev"}).siteURL(r); got != "https://mensa.miszori.dev" {
		t.Errorf("with a domain: %q", got)
	}
	if got := (&Server{}).siteURL(r); got != "http://evil.example" {
		t.Errorf("without a domain: %q", got)
	}
}
