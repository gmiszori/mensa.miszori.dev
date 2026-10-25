package eth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The testdata files are real API responses for the week of 2026-10-12.
func TestMenus(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("client-id") == "" {
			http.Error(w, "missing client id", http.StatusBadRequest)
			return
		}
		http.ServeFile(w, r, "testdata/rotas-"+r.URL.Query().Get("lang")+".json")
	}))
	defer api.Close()

	monday := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	menus, err := New(api.Client(), api.URL).Menus(context.Background(), monday, monday.AddDate(0, 0, 6))
	if err != nil {
		t.Fatal(err)
	}

	day, ok := menus[9]["2026-10-12"] // Mensa Polyterrasse, Monday
	if !ok {
		t.Fatal("no menu for Polyterrasse on Monday")
	}
	if day.Lunch.From != "11:15" || day.Dinner.From == "" {
		t.Errorf("serving times: lunch %s-%s, dinner %s-%s", day.Lunch.From, day.Lunch.To, day.Dinner.From, day.Dinner.To)
	}
	first := day.Lunch.Dishes[0]
	if first.Name.EN != "Arancini Bolognese" || first.Name.DE == "" {
		t.Errorf("first lunch dish: %+v", first.Name)
	}
	if first.Prices.Student <= 0 || first.Prices.External <= first.Prices.Student {
		t.Errorf("prices: %+v", first.Prices)
	}
	if len(day.Dinner.Dishes) != 4 {
		t.Errorf("got %d dinner dishes, want 4", len(day.Dinner.Dishes))
	}
	if _, ok := menus[9]["2026-10-17"]; ok {
		t.Error("Saturday should have no menu")
	}
}

func TestPhotoID(t *testing.T) {
	for in, want := range map[string]string{
		"https://idapps.ethz.ch/cookpit-pub-services/v1/images/77759": "77759",
		"":                                     "",
		"https://example.com/images/../../etc": "",
	} {
		if got := photoID(in); got != want {
			t.Errorf("photoID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUncut(t *testing.T) {
	for _, c := range []struct{ en, de, wantEN, wantDE string }{
		// English cut, German complete: both get the German text.
		{"Cappellacci with Vegetables and ...", "Cappellacci Verdure Ricotta di Bufala",
			"Cappellacci Verdure Ricotta di Bufala", "Cappellacci Verdure Ricotta di Bufala"},
		// Cut in both: keep each, ending in one "…".
		{"Golden Bun, Lettuce, F...", "Golden Bun, Lattich, Farmer Pom..",
			"Golden Bun, Lettuce, F…", "Golden Bun, Lattich, Farmer Pom…"},
		// Nothing cut: unchanged.
		{"Soup of the day", "Tagessuppe", "Soup of the day", "Tagessuppe"},
	} {
		en, de := uncut(c.en, c.de)
		if en != c.wantEN || de != c.wantDE {
			t.Errorf("uncut(%q, %q) = %q, %q; want %q, %q", c.en, c.de, en, de, c.wantEN, c.wantDE)
		}
	}
}
