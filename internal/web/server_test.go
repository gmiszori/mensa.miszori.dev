package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"mensaplan/internal/menu"
)

func TestTimeLeft(t *testing.T) {
	for _, c := range []struct{ from, to, want string }{
		{"12:10", "13:30", "1h 20"},
		{"12:45", "13:30", "45 min"},
		{"11:00", "14:00", "3h 00"},
		{"13:29", "13:30", "1 min"},
	} {
		if got := timeLeft(c.from, c.to); got != c.want {
			t.Errorf("timeLeft(%q, %q) = %q, want %q", c.from, c.to, got, c.want)
		}
	}
}

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
