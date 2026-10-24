// Package web serves the Mensa Finder website: one HTML page rendered on every request from
// the current menus, plus its CSS, JavaScript, fonts and dish photos.
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"mensafinder/internal/catalog"
	"mensafinder/internal/menu"
)

//go:embed templates static
var files embed.FS

// Server holds what the handlers need.
type Server struct {
	Catalog *catalog.Catalog
	Store   *menu.Store
	Photos  http.Handler
	Zone    *time.Location
	Log     *slog.Logger
	// StaleAfter is how old a source's last successful refresh may be before
	// /healthz/menus reports a problem.
	StaleAfter time.Duration

	page    *template.Template
	version string // changes whenever a static file changes; see staticVersion
}

// Handler returns the router with all routes.
func (s *Server) Handler() http.Handler {
	s.page = template.Must(template.New("page.html").Funcs(template.FuncMap{
		"price": formatPrice,
	}).ParseFS(files, "templates/page.html"))

	static, _ := fs.Sub(files, "static")
	s.version = staticVersion(static)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.home)
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(static))))
	mux.Handle("GET /photos/{file}", s.Photos)
	mux.HandleFunc("GET /api/menu", s.apiMenu)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /healthz/menus", s.menuHealth)
	return securityHeaders(mux)
}

// home renders the main page for the requested (or best) day.
func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	settings := readSettings(r)
	q := r.URL.Query()
	data := s.buildPage(settings, q.Get("day"), q.Get("meal"), time.Now().In(s.Zone))
	data.SiteURL = siteURL(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store") // menus and settings change; always render fresh
	if err := s.page.Execute(w, data); err != nil {
		s.Log.Error("rendering page", "err", err)
	}
}

// apiMenu returns one day's menus as JSON (?day=YYYY-MM-DD, default today). Handy for
// debugging and for anyone who wants the data.
func (s *Server) apiMenu(w http.ResponseWriter, r *http.Request) {
	day := r.URL.Query().Get("day")
	if day == "" {
		day = time.Now().In(s.Zone).Format(time.DateOnly)
	}
	if _, err := time.Parse(time.DateOnly, day); err != nil {
		http.Error(w, "day must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"day": day, "canteens": s.Store.On(day)})
}

// health answers as long as the server runs; Docker's health check uses it. It also says when
// each source last refreshed.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"status": "ok", "updated": s.Store.Updated(), "days": s.Store.Dates()})
}

// menuHealth is for outside monitoring: it answers 503 when a source has not refreshed
// successfully for StaleAfter (or never), i.e. when menus are getting old.
func (s *Server) menuHealth(w http.ResponseWriter, r *http.Request) {
	updated := s.Store.Updated()
	var stale []string
	for _, source := range []string{"eth", "food2050"} {
		if at, ok := updated[source]; !ok || time.Since(at) > s.StaleAfter {
			stale = append(stale, source)
		}
	}
	status := "ok"
	if len(stale) > 0 {
		status = "stale"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	writeJSON(w, map[string]any{"status": status, "stale": stale, "updated": updated})
}

// ---- building the page ----

// pageData is everything the template shows.
type pageData struct {
	Lang, Theme, Price string
	Diet               string
	OpenNow            bool
	T                  func(key string, args ...any) string
	Version            string // appended to CSS and JS links, so browsers fetch new versions
	SiteURL            string // e.g. "https://mensa.miszori.dev", for link previews

	Date      string // the day shown, YYYY-MM-DD
	IsToday   bool
	Notice    string // e.g. "No menus for Saturday 24 October. Showing Monday 26 October."
	Days      []dayLink
	DayLabel  string // the day shown, short: "Mon 26"
	Meal      string // "lunch" or "dinner": which one to show first
	Campuses  []option
	Allergens []option
	Starred   []string
	Canteens  []canteenView
	NoMenu    []canteenView
	Footer    string
	JSTexts   string // texts app.js needs, as a JSON object
}

type dayLink struct {
	Date, Label string
	Selected    bool
}

type option struct {
	ID, Name string
	On       bool
}

type canteenView struct {
	ID, Name, Campus, CampusName, Address, MapURL string
	Favourite                                     bool
	Order                                         int        // position in the catalog
	Status                                        string     // "open", "closed" or "" (not today)
	Hours                                         string     // e.g. "11:15-14:00 17:30-19:30", for app.js
	Meals                                         []mealView // lunch, then dinner
}

type mealView struct {
	Kind     string // "lunch" or "dinner"
	From, To string
	Dishes   []dishView
}

type dishView struct {
	Name, Description, Diet, DietLabel string
	Allergens                          string // display names, comma separated
	AllergenIDs                        string // space separated, for app.js to hide dishes
	Price                              float64
	Per100g                            bool
	Prices                             menu.Prices
	Photo                              string
	Starred                            bool
	Order                              int    // position on the source's menu
	Search                             string // lowercase text app.js searches in
}

// buildPage decides which day and meal to show and turns the store's data into pageData.
func (s *Server) buildPage(set Settings, requested, meal string, now time.Time) pageData {
	lang := set.Lang
	t := func(key string, args ...any) string { return translate(lang, key, args...) }
	today := now.Format(time.DateOnly)

	// Days that have menus, from today on.
	var dates []string
	for _, d := range s.Store.Dates() {
		if d >= today {
			dates = append(dates, d)
		}
	}
	if len(dates) > 10 {
		dates = dates[:10]
	}

	// Which day: the requested one, else today, else the next day with menus.
	date := today
	if _, err := time.Parse(time.DateOnly, requested); err == nil {
		date = requested
	} else if !slices.Contains(dates, today) && len(dates) > 0 {
		date = dates[0]
	}
	day, _ := time.ParseInLocation(time.DateOnly, date, s.Zone)
	dayLabel := shortDay(lang, day)

	p := pageData{
		Lang: lang, Theme: set.Theme, Price: set.Price, Diet: set.Diet, OpenNow: set.OpenNow,
		T: t, Version: s.version,
		Date: date, IsToday: date == today, DayLabel: dayLabel,
		Starred: set.Starred,
	}
	switch {
	case len(dates) == 0:
		p.Notice = t("nothingYet")
	case requested == "" && date != today:
		todayTime, _ := time.ParseInLocation(time.DateOnly, today, s.Zone)
		p.Notice = t("noMenusToday", longDay(lang, todayTime), longDay(lang, day))
	}
	for _, d := range dates {
		dt, _ := time.ParseInLocation(time.DateOnly, d, s.Zone)
		p.Days = append(p.Days, dayLink{Date: d, Label: shortDay(lang, dt), Selected: d == date})
	}
	for _, c := range s.Catalog.Campuses {
		p.Campuses = append(p.Campuses, option{ID: c.ID, Name: c.Name, On: slices.Contains(set.Campuses, c.ID)})
	}
	for _, a := range menu.Allergens {
		p.Allergens = append(p.Allergens, option{ID: a, Name: allergenNames[lang][a], On: slices.Contains(set.Hide, a)})
	}

	// One view per canteen; favourites first, otherwise in catalog order.
	menus := s.Store.On(date)
	hasDinner := false
	for i, c := range s.Catalog.Canteens {
		v := s.canteenView(c, menus[c.ID], set)
		v.Order = i
		v.Favourite = slices.Contains(set.Favourites, c.ID)
		if p.IsToday {
			v.Status = status(v, now)
		}
		if len(v.Meals[0].Dishes)+len(v.Meals[1].Dishes) == 0 {
			p.NoMenu = append(p.NoMenu, v)
			continue
		}
		hasDinner = hasDinner || len(v.Meals[1].Dishes) > 0
		p.Canteens = append(p.Canteens, v)
	}

	// The meal asked for (the picker keeps it when switching days); otherwise lunch, except
	// after 15:00 today, when dinner is what people are looking for.
	switch {
	case meal == "lunch" || meal == "dinner":
		p.Meal = meal
	case p.IsToday && hasDinner && now.Hour() >= 15:
		p.Meal = "dinner"
	default:
		p.Meal = "lunch"
	}

	// Favourite canteens first, those with more starred dishes in this meal before the
	// others; otherwise catalog order. app.js applies the same rule when things change.
	slices.SortStableFunc(p.Canteens, func(a, b canteenView) int {
		switch {
		case a.Favourite != b.Favourite && a.Favourite:
			return -1
		case a.Favourite != b.Favourite:
			return 1
		case a.Favourite:
			return b.starredIn(p.Meal) - a.starredIn(p.Meal)
		default:
			return 0
		}
	})

	updated := t("never")
	if times := s.Store.Updated(); len(times) > 0 {
		var latest time.Time
		for _, u := range times {
			if u.After(latest) {
				latest = u
			}
		}
		updated = latest.In(s.Zone).Format("02.01.2006 15:04")
	}
	p.Footer = t("footer", updated)

	js, _ := json.Marshal(map[string]string{
		"noLunch":  texts[lang]["noLunch"],
		"noDinner": texts[lang]["noDinner"],
		"lunch":    t("lunch"),
		"dinner":   t("dinner"),
		"hidden":   texts[lang]["hiddenDishes"],
		"starHits": texts[lang]["starredToday"],
		"open":     t("open"),
		"closed":   t("closed"),
		"star":     "☆ " + t("star"),
		"starred":  "★ " + t("starred"),
	})
	p.JSTexts = string(js)
	return p
}

// canteenView prepares one canteen's menu, starred dishes first. Dishes with allergens the
// visitor hides are included too: app.js hides them, so changing that setting needs no reload.
func (s *Server) canteenView(c catalog.Canteen, day menu.Day, set Settings) canteenView {
	v := canteenView{
		ID: c.ID, Name: c.Name, Campus: c.Campus, CampusName: s.Catalog.CampusName(c.Campus),
		Address: c.Address,
		MapURL:  "https://www.google.com/maps/search/?api=1&query=" + url.QueryEscape(c.Name+", "+c.Address),
	}
	convert := func(kind string, m menu.Meal) mealView {
		out := mealView{Kind: kind, From: m.From, To: m.To}
		for i, d := range m.Dishes {
			var names []string
			for _, a := range d.Allergens {
				names = append(names, allergenNames[set.Lang][a])
			}
			dv := dishView{
				Name:        d.Name.In(set.Lang),
				Description: d.Description.In(set.Lang),
				Diet:        d.Diet,
				DietLabel:   dietNames[set.Lang][d.Diet],
				Allergens:   strings.Join(names, ", "),
				AllergenIDs: strings.Join(d.Allergens, " "),
				Price:       d.Prices.For(set.Price),
				Per100g:     d.Per100g,
				Prices:      d.Prices,
				Photo:       photoKey(d.Photo),
				Starred:     set.isStarred(d),
				Order:       i,
			}
			dv.Search = strings.ToLower(dv.Name + " " + dv.Description)
			out.Dishes = append(out.Dishes, dv)
		}
		slices.SortStableFunc(out.Dishes, func(a, b dishView) int {
			switch {
			case a.Starred == b.Starred:
				return 0
			case a.Starred:
				return -1
			default:
				return 1
			}
		})
		return out
	}
	v.Meals = []mealView{convert("lunch", day.Lunch), convert("dinner", day.Dinner)}
	for _, m := range v.Meals {
		if m.From != "" && len(m.Dishes) > 0 {
			v.Hours = strings.TrimSpace(v.Hours + " " + m.From + "-" + m.To)
		}
	}
	return v
}

// starredIn counts the starred dishes in one meal ("lunch" or "dinner").
func (v canteenView) starredIn(kind string) int {
	n := 0
	for _, m := range v.Meals {
		if m.Kind != kind {
			continue
		}
		for _, d := range m.Dishes {
			if d.Starred {
				n++
			}
		}
	}
	return n
}

// status says whether a canteen is serving right now, from its lunch and dinner times.
func status(v canteenView, now time.Time) string {
	if v.Hours == "" {
		return ""
	}
	clock := now.Format("15:04")
	for _, window := range strings.Fields(v.Hours) {
		from, to, _ := strings.Cut(window, "-")
		if clock >= from && clock < to {
			return "open"
		}
	}
	return "closed"
}

// photoKey is the name a photo has in /photos/<key>.jpg, or "" for dishes without a photo.
func photoKey(url string) string {
	if url == "" {
		return ""
	}
	return menu.PhotoKey(url)
}

// formatPrice shows 7 as "7.00" and an unknown (zero) price as "–".
func formatPrice(f float64) string {
	if f <= 0 {
		return "–"
	}
	return fmt.Sprintf("%.2f", f)
}

// siteURL is the address visitors use, e.g. "https://mensa.miszori.dev". Link previews
// (Open Graph) need absolute URLs. In production Caddy passes the visitor's host and sets
// X-Forwarded-Proto itself (it ignores values sent by visitors).
func siteURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// staticVersion is a short hash of all static files. The page links to
// /static/app.css?v=<version>, so after a deploy that changed a file, browsers load the new
// one instead of an outdated copy from their cache.
func staticVersion(static fs.FS) string {
	sum := sha256.New()
	_ = fs.WalkDir(static, ".", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := fs.ReadFile(static, path)
			sum.Write([]byte(path))
			sum.Write(data)
		}
		return nil
	})
	return hex.EncodeToString(sum.Sum(nil)[:6])
}

// cacheStatic lets browsers keep versioned links (?v=...) for a year, since their content
// never changes, and anything else (fonts, the icon) for a day.
func cacheStatic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("v") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=86400")
		}
		h.ServeHTTP(w, r)
	})
}

// securityHeaders adds standard protective headers to every response. The page loads
// nothing from other sites, so the content security policy can be strict.
func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self'; style-src 'self'; script-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "geolocation=(), camera=(), microphone=()")
		h.ServeHTTP(w, r)
	})
}
