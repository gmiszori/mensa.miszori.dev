package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"mensafinder/internal/menu"
)

// cookieName is the cookie that holds a visitor's settings. There are no accounts: settings
// live only in the visitor's browser. app.js writes the cookie, the server reads it to render
// the page in the visitor's language, with their prices, favourites first, and so on.
const cookieName = "mf"

// Settings is the cookie's content, stored as URL-encoded JSON.
type Settings struct {
	Lang       string   `json:"lang"`     // "en" or "de"
	Price      string   `json:"price"`    // "student", "staff" or "external"
	Theme      string   `json:"theme"`    // "system", "light" or "dark"
	Diet       string   `json:"diet"`     // "" (all dishes), "vegetarian" or "vegan"
	OpenNow    bool     `json:"open"`     // today, show only canteens that are open right now
	Campuses   []string `json:"campuses"` // only canteens on these campuses are shown; none = all
	Hide       []string `json:"hide"`     // allergen ids whose dishes are hidden
	Favourites []string `json:"fav"`      // canteen ids, pinned to the top
	Starred    []string `json:"star"`     // dish names, highlighted on menus
}

// readSettings reads the cookie and repairs anything invalid, so a broken or hand-edited
// cookie never breaks the page. Without a cookie the language follows the browser.
func readSettings(r *http.Request) Settings {
	var s Settings
	if c, err := r.Cookie(cookieName); err == nil {
		if raw, err := url.QueryUnescape(c.Value); err == nil {
			_ = json.Unmarshal([]byte(raw), &s)
		}
	}
	if s.Lang != "en" && s.Lang != "de" {
		s.Lang = "en"
		if strings.HasPrefix(strings.ToLower(r.Header.Get("Accept-Language")), "de") {
			s.Lang = "de"
		}
	}
	if s.Price != "staff" && s.Price != "external" {
		s.Price = "student"
	}
	if s.Diet != "vegetarian" && s.Diet != "vegan" {
		s.Diet = ""
	}
	if s.Theme != "light" && s.Theme != "dark" {
		s.Theme = "system"
	}
	s.Hide = slices.DeleteFunc(s.Hide, func(a string) bool { return !slices.Contains(menu.Allergens, a) })
	return s
}

// starKey is how starred dish names are compared: case and spacing do not matter.
// app.js uses the same rule.
func starKey(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}

// isStarred reports whether a dish matches one of the starred names, in either language.
func (s Settings) isStarred(d menu.Dish) bool {
	for _, name := range s.Starred {
		k := starKey(name)
		if k == starKey(d.Name.EN) || k == starKey(d.Name.DE) {
			return true
		}
	}
	return false
}
