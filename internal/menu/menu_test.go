package menu

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTidyName(t *testing.T) {
	for in, want := range map[string]string{
		"PASTA WITH RED PESTO":     "Pasta with Red Pesto",
		"CHILI-SIN-CARNE":          "Chili-Sin-Carne",
		"Seitan Stroganoff":        "Seitan Stroganoff",
		"food market  bowl":        "food market bowl",
		"KÄSESPÄTZLE MIT ZWIEBELN": "Käsespätzle mit Zwiebeln",
	} {
		if got := TidyName(in); got != want {
			t.Errorf("TidyName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAllergenSet(t *testing.T) {
	got := AllergenSet([]string{"milk, lactose", "gluten wheat", "soybeans", "almond", "histamine", "milk"})
	want := []string{"gluten", "soy", "milk", "nuts"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AllergenSet = %v, want %v", got, want)
	}
}

func TestStoreReplaceAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "menus.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	dish := Dish{Name: Same("Soup")}
	s.Replace("a", "2026-10-26", "2026-11-01", map[string]Day{
		"2026-10-26": {Lunch: Meal{Dishes: []Dish{dish}}},
		"2026-10-27": {Lunch: Meal{Dishes: []Dish{dish}}},
	})
	// A later refresh without Tuesday removes Tuesday.
	s.Replace("a", "2026-10-26", "2026-11-01", map[string]Day{
		"2026-10-26": {Lunch: Meal{Dishes: []Dish{dish}}},
	})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Dates(); !reflect.DeepEqual(got, []string{"2026-10-26"}) {
		t.Errorf("dates after reload = %v", got)
	}
}

// A page render reads a day's menus while a refresh replaces them; run with -race.
func TestOnWhileReplacing(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "menus.json"))
	if err != nil {
		t.Fatal(err)
	}
	days := map[string]Day{"2026-10-26": {Lunch: Meal{Dishes: []Dish{{Name: Same("Soup")}}}}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			s.Replace("a", "2026-10-26", "2026-10-26", days)
		}
	}()
	for {
		select {
		case <-done:
			return
		default:
			_ = s.On("2026-10-26")["a"]
		}
	}
}

func TestPruneForgetsOldPhotos(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "menus.json"))
	if err != nil {
		t.Fatal(err)
	}
	photo := func(url string) map[string]Day {
		return map[string]Day{"2026-10-26": {Lunch: Meal{Dishes: []Dish{{Name: Same("Soup"), Photo: url}}}}}
	}
	s.Replace("a", "2026-10-26", "2026-10-26", photo("https://example.com/old.jpg"))
	s.Replace("a", "2026-10-26", "2026-10-26", photo("https://example.com/new.jpg"))
	s.Prune("2026-10-01")
	if _, ok := s.PhotoURL(PhotoKey("https://example.com/old.jpg")); ok {
		t.Error("photo of a replaced dish is still known")
	}
	if _, ok := s.PhotoURL(PhotoKey("https://example.com/new.jpg")); !ok {
		t.Error("photo of a current dish is unknown")
	}
}

// Files written by older code still open, brought up to date by the migrations.
func TestOpenOldFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "menus.json")
	old := `{"days":{"2026-10-26":{"a":{"lunch":{"dishes":[{"name":{"en":"Soup","de":"Suppe"},"prices":{"student":5}}]}}}},
		"details":{"https://example.com/dish":{"prices":{"student":9}}}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.data.Version != storeVersion {
		t.Errorf("version %d, want %d", s.data.Version, storeVersion)
	}
	if _, ok := s.CachedDetail("https://example.com/dish"); ok {
		t.Error("detail pages cached without nutrition should be read again")
	}
	if got := s.data.Days["2026-10-26"]["a"].Lunch.Dishes; len(got) != 1 || got[0].Name.EN != "Soup" {
		t.Errorf("menus lost: %+v", got)
	}

	// A file from newer code (after a rollback) opens as it is.
	newer := `{"version":99,"days":{},"details":{"https://example.com/dish":{"prices":{"student":9}}},"future":true}`
	if err := os.WriteFile(path, []byte(newer), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.CachedDetail("https://example.com/dish"); !ok {
		t.Error("a newer file lost its cached details")
	}
}
