package refresh

import (
	"path/filepath"
	"testing"
	"time"

	"mensaplan/internal/menu"
)

func TestNext(t *testing.T) {
	zone, _ := time.LoadLocation("Europe/Zurich")
	store, err := menu.Open(filepath.Join(t.TempDir(), "menus.json"))
	if err != nil {
		t.Fatal(err)
	}
	soup := []menu.Dish{{Name: menu.Same("Soup")}}
	store.Replace("a", "2026-10-29", "2026-10-29", map[string]menu.Day{"2026-10-29": {
		Lunch:  menu.Meal{From: "11:15", To: "14:00", Dishes: soup},
		Dinner: menu.Meal{From: "17:30", To: "19:30"}, // no dinner menu: not an opening
	}})
	store.Replace("b", "2026-10-29", "2026-10-29", map[string]menu.Day{"2026-10-29": {
		Lunch:  menu.Meal{From: "11:00", To: "13:30", Dishes: soup},
		Dinner: menu.Meal{From: "16:45", To: "19:00", Dishes: soup},
	}})
	r := &Refresher{Store: store, Zone: zone}
	at := func(clock string) time.Time {
		t, _ := time.ParseInLocation("2006-01-02 15:04:05", "2026-10-29 "+clock, zone)
		return t
	}
	for _, c := range []struct{ now, want string }{
		{"08:00:00", "09:00:00"},
		{"10:59:00", "11:00:00"}, // an opening on the full hour
		{"11:00:30", "11:15:00"},
		{"11:15:00", "12:00:00"}, // just refreshed for the 11:15 opening
		{"16:01:10", "16:45:00"},
		{"17:10:00", "18:00:00"}, // the 17:30 dinner has no menu
		{"23:20:00", "00:00:00"},
	} {
		want := at(c.want)
		if c.want == "00:00:00" {
			want = want.AddDate(0, 0, 1)
		}
		if got := r.next(at(c.now)); !got.Equal(want) {
			t.Errorf("next(%s) = %s, want %s", c.now, got.In(zone).Format(time.DateTime), want.Format(time.DateTime))
		}
	}
}
