// Package eth reads menus from ETH Zurich's public menu API ("Cookpit"), the same API that
// ethz.ch uses for its canteen pages.
//
// The API returns "weekly rotas": per canteen (facility) and validity period, the meals of
// each weekday, nested as day -> opening hours -> meal time (lunch/dinner) -> line -> meal.
// Menus come in English and German; we fetch both and match meals by their line id.
package eth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mensafinder/internal/menu"
)

// BaseURL is the API root. clientID is the identification ETH's own website sends; the API
// rejects requests without one.
const (
	BaseURL  = "https://idapps.ethz.ch/cookpit-pub-services/v1"
	clientID = "ethz-wcms"
	pageSize = 50
)

// Client fetches menus. The zero value is not usable; use New.
type Client struct {
	http *http.Client
	base string
}

// New returns a client for the ETH API at base (normally BaseURL).
func New(httpClient *http.Client, base string) *Client {
	return &Client{http: httpClient, base: strings.TrimRight(base, "/")}
}

// Menus returns the menus of all facilities for the days from..to (inclusive), as
// facility id -> date ("YYYY-MM-DD") -> menu.
func (c *Client) Menus(ctx context.Context, from, to time.Time) (map[int]map[string]menu.Day, error) {
	english, err := c.rotas(ctx, "en", from)
	if err != nil {
		return nil, fmt.Errorf("eth: english menus: %w", err)
	}
	german, err := c.rotas(ctx, "de", from)
	if err != nil {
		return nil, fmt.Errorf("eth: german menus: %w", err)
	}

	result := map[int]map[string]menu.Day{}
	for date := from; !date.After(to); date = date.AddDate(0, 0, 1) {
		for _, facility := range facilities(english) {
			en := rotaFor(english, facility, date)
			if en == nil {
				continue
			}
			day := buildDay(en, rotaFor(german, facility, date), date)
			if day.Empty() {
				continue
			}
			if result[facility] == nil {
				result[facility] = map[string]menu.Day{}
			}
			result[facility][date.Format(time.DateOnly)] = day
		}
	}
	return result, nil
}

// ---- API response shapes (only the fields we use) ----

type rotaPage struct {
	Rotas []rota `json:"weekly-rota-array"`
}

type rota struct {
	Facility  int    `json:"facility-id"`
	ValidFrom string `json:"valid-from"`
	ValidTo   string `json:"valid-to"`
	Days      []struct {
		Weekday int `json:"day-of-week-code"` // 1 = Monday ... 7 = Sunday
		Hours   []struct {
			MealTimes []mealTime `json:"meal-time-array"`
		} `json:"opening-hour-array"`
	} `json:"day-of-week-array"`
}

type mealTime struct {
	Name  string `json:"name"` // "Lunch", "Dinner", "Mittag", "Abendessen", "Pranzo (Lunch)", ...
	From  string `json:"time-from"`
	To    string `json:"time-to"`
	Lines []struct {
		Meal *meal `json:"meal"`
	} `json:"line-array"`
}

type meal struct {
	LineID      int    `json:"line-id"`
	Name        string `json:"name"`
	Description string `json:"description"` // components separated by "|"
	Unit        string `json:"price-unit-desc"`
	Prices      []struct {
		Price float64 `json:"price"`
		Group int     `json:"customer-group-code"`
	} `json:"meal-price-array"`
	Classes []struct {
		Desc string `json:"desc"`
	} `json:"meal-class-array"`
	Allergens []struct {
		Desc string `json:"desc"`
	} `json:"allergen-array"`
	MeatTypes []json.RawMessage `json:"meat-type-array"`
	ImageURL  string            `json:"image-url"`
}

// rotas downloads every page of rotas valid after the given date, in one language.
func (c *Client) rotas(ctx context.Context, lang string, after time.Time) ([]rota, error) {
	var all []rota
	for first := 0; first < 100*pageSize; first += pageSize {
		q := url.Values{
			"client-id":   {clientID},
			"lang":        {lang},
			"rs-first":    {fmt.Sprint(first)},
			"rs-size":     {fmt.Sprint(pageSize)},
			"valid-after": {after.Format(time.DateOnly)},
		}
		var page rotaPage
		if err := c.getJSON(ctx, c.base+"/weeklyrotas?"+q.Encode(), &page); err != nil {
			return nil, err
		}
		all = append(all, page.Rotas...)
		if len(page.Rotas) < pageSize {
			return all, nil
		}
	}
	return nil, fmt.Errorf("more than 100 pages, giving up")
}

func (c *Client) getJSON(ctx context.Context, u string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

// facilities lists the distinct facility ids that appear in the rotas.
func facilities(rotas []rota) []int {
	seen := map[int]bool{}
	var ids []int
	for _, r := range rotas {
		if !seen[r.Facility] {
			seen[r.Facility] = true
			ids = append(ids, r.Facility)
		}
	}
	return ids
}

// rotaFor finds the rota of a facility that is valid on a date. Rotas can overlap (a
// long-running default plan and a specific week); the one with the shortest validity wins.
func rotaFor(rotas []rota, facility int, date time.Time) *rota {
	day := date.Format(time.DateOnly)
	var best *rota
	for i := range rotas {
		r := &rotas[i]
		if r.Facility != facility || day < r.ValidFrom || day > r.ValidTo {
			continue
		}
		if best == nil || validity(r) < validity(best) {
			best = r
		}
	}
	return best
}

// validity is how long a rota is valid for.
func validity(r *rota) time.Duration {
	from, err1 := time.Parse(time.DateOnly, r.ValidFrom)
	to, err2 := time.Parse(time.DateOnly, r.ValidTo)
	if err1 != nil || err2 != nil {
		return 1 << 62
	}
	return to.Sub(from)
}

// mealTimesOn returns the meal times (lunch, dinner) of a rota on the weekday of date.
func mealTimesOn(r *rota, date time.Time) []mealTime {
	if r == nil {
		return nil
	}
	weekday := int(date.Weekday())
	if weekday == 0 {
		weekday = 7 // Go counts Sunday as 0, the API as 7
	}
	var out []mealTime
	for _, d := range r.Days {
		if d.Weekday != weekday {
			continue
		}
		for _, h := range d.Hours {
			out = append(out, h.MealTimes...)
		}
	}
	return out
}

// buildDay turns one facility's English and German rota into the menu of one date.
func buildDay(en, de *rota, date time.Time) menu.Day {
	germanByLine := map[int]*meal{}
	for _, mt := range mealTimesOn(de, date) {
		for _, line := range mt.Lines {
			if line.Meal != nil {
				germanByLine[line.Meal.LineID] = line.Meal
			}
		}
	}
	untranslated := placeholderNames(mealTimesOn(en, date), germanByLine)

	var day menu.Day
	for _, mt := range mealTimesOn(en, date) {
		target := &day.Lunch
		if name := strings.ToLower(mt.Name); strings.Contains(name, "dinner") || strings.Contains(name, "abend") {
			target = &day.Dinner
		}
		if target.From == "" {
			target.From, target.To = mt.From, mt.To
		}
		for _, line := range mt.Lines {
			if line.Meal == nil {
				continue
			}
			if dish, ok := toDish(line.Meal, germanByLine[line.Meal.LineID], untranslated); ok {
				target.Dishes = append(target.Dishes, dish)
			}
		}
	}
	return day
}

// placeholderNames finds English names that ETH's translation reuses for several different
// German dishes (a known bug in the API); those dishes are shown with their German text.
func placeholderNames(times []mealTime, german map[int]*meal) map[string]bool {
	germanNames := map[string]map[string]bool{}
	for _, mt := range times {
		for _, line := range mt.Lines {
			if line.Meal == nil || german[line.Meal.LineID] == nil {
				continue
			}
			en := clean(line.Meal.Name)
			if germanNames[en] == nil {
				germanNames[en] = map[string]bool{}
			}
			germanNames[en][clean(german[line.Meal.LineID].Name)] = true
		}
	}
	out := map[string]bool{}
	for en, names := range germanNames {
		if len(names) > 1 {
			out[en] = true
		}
	}
	return out
}

// toDish converts an API meal. ok is false for empty or placeholder entries.
func toDish(en, de *meal, untranslated map[string]bool) (menu.Dish, bool) {
	// Names sometimes use "|" like descriptions do ("Venison Burger | Cranberry Mayo").
	name := menu.Text{EN: components(en.Name)}
	desc := menu.Text{EN: components(en.Description)}
	if de != nil {
		name.DE = components(de.Name)
		desc.DE = components(de.Description)
	}
	if untranslated[name.EN] {
		name.EN, desc.EN = name.DE, desc.DE
	}
	if name.EN == "" {
		name.EN, desc.EN = name.DE, desc.DE
	}
	if name.DE == "" {
		name.DE, desc.DE = name.EN, desc.EN
	}
	if name.EN == "" {
		return menu.Dish{}, false
	}
	name.EN, name.DE = uncut(name.EN, name.DE)
	desc.EN, desc.DE = uncut(desc.EN, desc.DE)
	name.EN, name.DE = menu.TidyName(name.EN), menu.TidyName(name.DE)

	var allergenNames []string
	for _, a := range en.Allergens {
		allergenNames = append(allergenNames, a.Desc)
	}
	dish := menu.Dish{
		Name:        name,
		Description: desc,
		Allergens:   menu.AllergenSet(allergenNames),
		Per100g:     strings.Contains(en.Unit, "100"),
		Photo:       photoURL(en.ImageURL),
	}
	dish.Diet = diet(en, dish.Allergens)
	for _, p := range en.Prices {
		if p.Price <= 0 {
			continue
		}
		// Customer groups: 10 students, 11 staff, 12 students & staff, 13 external, 14 everyone.
		switch p.Group {
		case 10:
			dish.Prices.Student = p.Price
		case 11:
			dish.Prices.Staff = p.Price
		case 12:
			dish.Prices.Student, dish.Prices.Staff = p.Price, p.Price
		case 13:
			dish.Prices.External = p.Price
		case 14:
			dish.Prices = menu.Prices{Student: p.Price, Staff: p.Price, External: p.Price}
		}
	}
	return dish, true
}

// diet reads the meal classes ("Vegan", "Meat", ...). Meals classed only as "Pasta" or
// "Salad" count as meat if a meat type is listed and fish if they contain fish; otherwise
// we do not know, and say nothing rather than guess.
func diet(m *meal, allergens []string) string {
	classes := map[string]bool{}
	for _, c := range m.Classes {
		classes[strings.ToLower(c.Desc)] = true
	}
	for _, d := range []string{"vegan", "vegetarian", "fish", "meat"} {
		if classes[d] {
			return d
		}
	}
	if len(m.MeatTypes) > 0 {
		return "meat"
	}
	for _, a := range allergens {
		if a == "fish" {
			return "fish"
		}
	}
	return ""
}

// components turns "Rice | Chicken | novalue" into "Rice, Chicken".
func components(s string) string {
	var parts []string
	for _, p := range strings.Split(s, "|") {
		if p = clean(p); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ", ")
}

// clean tidies whitespace and drops the API's "novalue" placeholder.
// uncut repairs texts that ETH's system cut off at a length limit, such as the name
// "Cappellacci with Vegetables and ..." (in German, the same dish is the complete
// "Cappellacci Verdure Ricotta di Bufala"). The two languages are rarely cut both, so a cut
// text is replaced by the other language's complete one. A text cut in both languages keeps
// what there is and ends in a proper "…".
func uncut(en, de string) (string, string) {
	switch {
	case isCut(en) && !isCut(de) && de != "":
		en = de
	case isCut(de) && !isCut(en) && en != "":
		de = en
	}
	return ellipsis(en), ellipsis(de)
}

// isCut reports whether a text ends in "..", "..." or "…", the way ETH marks cut texts.
func isCut(s string) bool {
	return strings.HasSuffix(s, "..") || strings.HasSuffix(s, "…")
}

// ellipsis turns the dots at the end of a cut text into a single "…".
func ellipsis(s string) string {
	if !isCut(s) {
		return s
	}
	return strings.TrimRight(strings.TrimRight(s, ".…"), " ,") + "…"
}

func clean(s string) string {
	s = menu.CleanText(s)
	if strings.EqualFold(s, "novalue") {
		return ""
	}
	return s
}

// photoURL turns the API's image link into one that can be downloaded: the image endpoint
// also wants the client id. Returns "" when the link is missing or odd.
func photoURL(imageURL string) string {
	id := photoID(imageURL)
	if id == "" {
		return ""
	}
	return BaseURL + "/images/" + id + "?client-id=" + clientID
}

// photoID extracts "77759" from ".../v1/images/77759".
func photoID(u string) string {
	i := strings.LastIndex(u, "/images/")
	if i < 0 {
		return ""
	}
	id := u[i+len("/images/"):]
	for _, r := range id {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return id
}
