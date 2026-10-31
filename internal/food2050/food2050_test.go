package food2050

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mensaplan/internal/menu"
)

// The testdata files are real pages of the Untere Mensa (UZH) for the week of 2026-10-26.
func TestWeekAndPrices(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/weekly":
			http.ServeFile(w, r, "testdata/week.html")
		case "/detail":
			http.ServeFile(w, r, "testdata/detail.html")
		case "/detail-with-photo":
			http.ServeFile(w, r, "testdata/detail-with-photo.html")
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	c := New(site.Client())

	days, err := c.Week(context.Background(), site.URL+"/weekly", time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	monday := days["2026-10-26"]
	if len(monday) == 0 {
		t.Fatalf("no dishes on Monday; got days %v", keys(days))
	}
	nasi := monday[0].Dish
	if nasi.Name.DE != "Nasi Goreng" || nasi.Diet != "vegan" {
		t.Errorf("first dish: %+v", nasi)
	}
	if len(nasi.Allergens) == 0 || nasi.Allergens[0] != "gluten" {
		t.Errorf("allergens: %v", nasi.Allergens)
	}
	if monday[0].DetailURL == "" {
		t.Error("missing detail URL")
	}
	for date := range days {
		if date < "2026-10-26" || date > "2026-11-01" {
			t.Errorf("date %s is outside the requested week", date)
		}
	}

	// A detail page for a future day: prices, but no photo yet.
	detail, err := c.Detail(context.Background(), site.URL+"/detail")
	if err != nil {
		t.Fatal(err)
	}
	if p := detail.Prices; p.Student != 9 || p.Staff != 11 || p.External != 15 {
		t.Errorf("prices: %+v", p)
	}
	if detail.Photo != "" {
		t.Errorf("unexpected photo %q", detail.Photo)
	}
	if n := detail.Nutrition; n == nil {
		t.Error("nutrition missing")
	} else {
		if n.PortionGrams != 487 || math.Round(n.Portion["energy"]) != 737 || math.Round(n.Per100g["energy"]) != 151 {
			t.Errorf("energy: %v kcal per 100 g, %v kcal in %v g", n.Per100g["energy"], n.Portion["energy"], n.PortionGrams)
		}
		for _, key := range menu.NutrientKeys {
			if _, ok := n.Per100g[key]; !ok {
				t.Errorf("%s missing", key)
			}
		}
	}

	// The same kind of page on the day itself, after the canteen uploaded a photo.
	detail, err = c.Detail(context.Background(), site.URL+"/detail-with-photo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(detail.Photo, "https://storage.googleapis.com/dish-images-prod/") {
		t.Errorf("photo URL: %q", detail.Photo)
	}
	if detail.Prices.Student == 0 {
		t.Errorf("prices missing: %+v", detail.Prices)
	}
}

func keys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Dishes the source flags neither vegan nor vegetarian are meat, unless their text says
// otherwise: then they get no tag.
func TestUnflaggedDiet(t *testing.T) {
	for _, c := range []struct{ name, description, diet string }{
		{"HACKBÄLLCHEN Vegane", "Pilz-Erbsen-Risotto, Rucola, Parmesan", ""},
		{"Butter Tofu", "Cashew Tomaten, Jasminreis", ""},
		{"Pasta", "Jeden Tag eine neue Kreation, Vegetarisch oder mit Fleisch", ""},
		{"Rindsgeschnetzeltes", "Rösti, Rahmsauce", "meat"},
	} {
		d, _ := (&dish{Name: c.name, Description: c.description}).toDish()
		if d.Diet != c.diet {
			t.Errorf("%s: diet %q, want %q", c.name, d.Diet, c.diet)
		}
	}
}
