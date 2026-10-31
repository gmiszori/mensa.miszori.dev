// Package web serves the Mensaplan Zürich website: one HTML page rendered on every request from
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
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"mensaplan/internal/catalog"
	"mensaplan/internal/menu"
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
	// StaleAfter is how old a source's last successful refresh may be before /healthz lists
	// it as stale.
	StaleAfter time.Duration
	// Domain is the public domain (e.g. "mensa.miszori.dev") for absolute links in link
	// previews. Empty or "localhost": the address of the request is used.
	Domain string
	// Release is the version from the VERSION file, e.g. "1.0.0", shown by /healthz.
	Release string

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
	// Browsers, bookmarks and link previews ask for /favicon.ico on their own.
	mux.Handle("GET /favicon.ico", cacheStatic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, static, "favicon.ico")
	})))
	// The service worker (offline copies of the menus) must come from the root to act on
	// every page. Browsers check it for a new version on each visit.
	mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, static, "sw.js")
	})
	mux.Handle("GET /photos/{file}", s.Photos)
	mux.HandleFunc("GET /healthz", s.health)
	return securityHeaders(mux)
}

// home renders the main page for the requested (or best) day.
func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := time.Now().In(s.Zone)
	// A link to a day that has passed (an old bookmark or shared link) moves on to today.
	// Without menus today, it drops the day instead, so the page picks the next day with
	// menus and says so. The redirect is temporary, because "today" changes.
	today := now.Format(time.DateOnly)
	if day := q.Get("day"); day != "" && day < today {
		if _, err := time.Parse(time.DateOnly, day); err == nil {
			if slices.Contains(s.Store.Dates(), today) {
				q.Set("day", today)
			} else {
				q.Del("day")
			}
			// Not kept by Cloudflare either: "today" changes at midnight.
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, "/?"+q.Encode(), http.StatusFound)
			return
		}
	}
	data := s.buildPage(pageLang, q.Get("day"), q.Get("meal"), now)
	data.SiteURL = s.siteURL(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Browsers always ask again, since menus change. Cloudflare may answer from a copy up to a
	// minute old, which is faster than asking the server when others just opened the page. The
	// page is the same for everyone (see pageLang); what depends on the time of day, like which
	// canteens are open, app.js works out in the browser.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("CDN-Cache-Control", "max-age=60")
	if err := s.page.Execute(w, data); err != nil {
		s.Log.Error("rendering page", "err", err)
	}
}

// health answers as long as the server runs; Docker's health check uses it. It also says when
// each source last refreshed, and lists as "stale" those that have not refreshed successfully
// for StaleAfter (or never). Stale menus still count as healthy: restarting the container
// would not bring a source back.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	updated := s.Store.Updated()
	stale := []string{}
	for _, source := range []string{"eth", "food2050"} {
		if at, ok := updated[source]; !ok || time.Since(at) > s.StaleAfter {
			stale = append(stale, source)
		}
	}
	writeJSON(w, map[string]any{"status": "ok", "version": s.Release, "stale": stale, "updated": updated, "days": s.Store.Dates()})
}

// ---- building the page ----

// pageData is everything the template shows. It is the same for every visitor: their
// settings are applied in the browser.
type pageData struct {
	Lang    string                                      // pageLang: the language until boot.js knows better
	T       func(key string, args ...any) string        // a text in Lang, for attributes and meta tags
	B       func(key string, args ...any) template.HTML // a text in both languages, see tr
	Version string                                      // appended to CSS and JS links, so browsers fetch new versions
	SiteURL string                                      // e.g. "https://mensa.miszori.dev", for link previews

	Date      string // the day shown, YYYY-MM-DD
	IsToday   bool
	Notice    template.HTML // e.g. "No menus for Saturday 24 October. Showing Monday 26 October."
	Days      []dayLink
	DayLabel  template.HTML // the day shown, short: "Mon 26"
	Meal      string        // "lunch" or "dinner": which one to show first
	Campuses  []option
	Allergens []option
	Canteens  []canteenView
	NoMenu    []canteenView
	Footer    template.HTML
	JSTexts   string // every interface text in both languages, as JSON, for app.js
}

type dayLink struct {
	Date     string
	Label    template.HTML
	Selected bool
}

type option struct {
	ID   string
	Name template.HTML
}

type canteenView struct {
	ID, Name, Campus, CampusName, Address, MapURL string
	Order                                         int        // position in the catalog
	Hours                                         string     // e.g. "11:15-14:00 17:30-19:30", for app.js
	Lat, Lng                                      float64    // for app.js to sort by distance
	Meals                                         []mealView // lunch, then dinner
}

type mealView struct {
	Kind     string // "lunch" or "dinner"
	From, To string
	Dishes   []dishView
}

type dishView struct {
	NameEN, NameDE               string // for app.js to recognise starred dishes
	Name, Description, DietLabel template.HTML
	Diet                         string
	Allergens                    template.HTML // display names, comma separated
	AllergenIDs                  string        // space separated, for app.js to hide dishes
	Per100g                      bool
	Prices                       menu.Prices
	Photo                        string
	Order                        int // position on the source's menu
	// Nutrition values per 100 g for app.js, see nutrients; "" when the source published
	// none. PortionGrams is the portion's weight, "" when unknown.
	Nutrition, PortionGrams string
}

// buildPage decides which day and meal to show and turns the store's data into pageData.
// lang only picks the language of attributes and meta tags; every visible text is in both.
func (s *Server) buildPage(lang, requested, meal string, now time.Time) pageData {
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

	p := pageData{
		Lang:    lang,
		T:       func(key string, args ...any) string { return translate(lang, key, args...) },
		B:       tr,
		Version: s.version,
		Date:    date, IsToday: date == today,
		DayLabel: both(shortDay("en", day), shortDay("de", day)),
	}
	switch {
	case len(dates) == 0:
		p.Notice = tr("nothingYet")
	case requested == "" && date != today:
		todayTime, _ := time.ParseInLocation(time.DateOnly, today, s.Zone)
		p.Notice = both(
			translate("en", "noMenusToday", longDay("en", todayTime), longDay("en", day)),
			translate("de", "noMenusToday", longDay("de", todayTime), longDay("de", day)))
	}
	for _, d := range dates {
		dt, _ := time.ParseInLocation(time.DateOnly, d, s.Zone)
		p.Days = append(p.Days, dayLink{Date: d, Label: both(shortDay("en", dt), shortDay("de", dt)), Selected: d == date})
	}
	for _, c := range s.Catalog.Campuses {
		p.Campuses = append(p.Campuses, option{ID: c.ID, Name: both(c.Name, c.Name)})
	}
	for _, a := range menu.Allergens {
		p.Allergens = append(p.Allergens, option{ID: a, Name: both(allergenNames["en"][a], allergenNames["de"][a])})
	}

	// One view per canteen, in catalog order; app.js puts the visitor's favourites first.
	menus := s.Store.On(date)
	hasDinner := false
	for i, c := range s.Catalog.Canteens {
		v := s.canteenView(c, menus[c.ID])
		v.Order = i
		if len(v.Meals[0].Dishes)+len(v.Meals[1].Dishes) == 0 {
			p.NoMenu = append(p.NoMenu, v)
			continue
		}
		hasDinner = hasDinner || len(v.Meals[1].Dishes) > 0
		p.Canteens = append(p.Canteens, v)
	}

	// The meal asked for (the picker keeps it when switching days); otherwise lunch, except
	// after 15:00 today, when dinner is what people are looking for. app.js decides the same
	// way for pages saved for offline use.
	switch {
	case meal == "lunch" || meal == "dinner":
		p.Meal = meal
	case p.IsToday && hasDinner && now.Hour() >= 15:
		p.Meal = "dinner"
	default:
		p.Meal = "lunch"
	}

	updated := ""
	if times := s.Store.Updated(); len(times) > 0 {
		var latest time.Time
		for _, u := range times {
			if u.After(latest) {
				latest = u
			}
		}
		updated = latest.In(s.Zone).Format("02.01.2006 15:04")
	}
	if updated == "" {
		p.Footer = both(translate("en", "footer", texts["en"]["never"]), translate("de", "footer", texts["de"]["never"]))
	} else {
		p.Footer = tr("footer", updated)
	}

	js, _ := json.Marshal(texts)
	p.JSTexts = string(js)
	return p
}

// canteenView prepares one canteen's menu in both languages. Every dish is included: app.js
// hides those the visitor's settings filter out and puts starred ones first.
func (s *Server) canteenView(c catalog.Canteen, day menu.Day) canteenView {
	v := canteenView{
		ID: c.ID, Name: c.Name, Campus: c.Campus, CampusName: s.Catalog.CampusName(c.Campus),
		Address: c.Address, Lat: c.Lat, Lng: c.Lng,
		MapURL: "https://www.google.com/maps/search/?api=1&query=" + url.QueryEscape(c.Name+", "+c.Address),
	}
	convert := func(kind string, m menu.Meal) mealView {
		out := mealView{Kind: kind, From: m.From, To: m.To}
		for i, d := range m.Dishes {
			var en, de []string
			for _, a := range d.Allergens {
				en = append(en, allergenNames["en"][a])
				de = append(de, allergenNames["de"][a])
			}
			dv := dishView{
				NameEN:      d.Name.In("en"),
				NameDE:      d.Name.In("de"),
				Name:        both(d.Name.In("en"), d.Name.In("de")),
				Description: both(d.Description.In("en"), d.Description.In("de")),
				Diet:        d.Diet,
				DietLabel:   both(dietNames["en"][d.Diet], dietNames["de"][d.Diet]),
				Allergens:   both(strings.Join(en, ", "), strings.Join(de, ", ")),
				AllergenIDs: strings.Join(d.Allergens, " "),
				Per100g:     d.Per100g,
				Prices:      d.Prices,
				Photo:       photoKey(d.Photo),
				Order:       i,
			}
			if n := d.Nutrition; n != nil {
				dv.Nutrition = nutrients(n.Per100g)
				if n.PortionGrams > 0 {
					dv.PortionGrams = strconv.FormatFloat(math.Round(n.PortionGrams), 'f', -1, 64)
				}
			}
			out.Dishes = append(out.Dishes, dv)
		}
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
// (Open Graph) need absolute URLs. It comes from Domain, so a request's Host header can't put
// another address into the page. Only without one (trying it out locally) is the request's
// address used.
func (s *Server) siteURL(r *http.Request) string {
	if s.Domain != "" && s.Domain != "localhost" {
		return "https://" + s.Domain
	}
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
		w.Header().Set("Permissions-Policy", "geolocation=(self), camera=(), microphone=()") // location: sorting by distance
		h.ServeHTTP(w, r)
	})
}

// nutrients writes nutrition values as app.js reads them: the amounts in the order of
// menu.NutrientKeys, separated by spaces, with "-" for one the source didn't publish. Energy is
// in whole kcal, salt in grams with two decimals, the rest in grams with one.
func nutrients(n menu.Nutrients) string {
	parts := make([]string, len(menu.NutrientKeys))
	for i, key := range menu.NutrientKeys {
		v, ok := n[key]
		decimals := 1
		switch key {
		case "energy":
			decimals = 0
		case "salt":
			decimals = 2
		}
		parts[i] = "-"
		if ok {
			parts[i] = strconv.FormatFloat(v, 'f', decimals, 64)
		}
	}
	return strings.Join(parts, " ")
}
