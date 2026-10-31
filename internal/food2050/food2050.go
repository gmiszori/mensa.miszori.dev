// Package food2050 reads menus from app.food2050.ch, the menu website of ZFV, which runs the
// UZH canteens and several others (PHZH, ZHdK, ZHAW).
//
// The site has no public API, so we read the data its pages embed for the browser (see
// nextdata.go) and pick out the objects we need by their "__typename".
//
// Two kinds of pages are used:
//   - the weekly page of an outlet: dishes per day, without prices
//   - a dish's detail page: its prices and, once the canteen has uploaded one on the day
//     itself, a photo
package food2050

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"mensaplan/internal/menu"
)

// PhotoURL is where FOOD2050's dish photos are; the photo handler downloads only from here.
const PhotoURL = "https://storage.googleapis.com/dish-images-prod/"

// Client fetches pages. Use New.
type Client struct {
	http *http.Client
}

// New returns a client.
func New(httpClient *http.Client) *Client { return &Client{http: httpClient} }

// Item is one dish from a weekly page, plus the detail page that has its prices.
type Item struct {
	Dish      menu.Dish
	DetailURL string
}

// Week returns the dishes on one weekly page for the ISO week containing day, keyed by date
// ("YYYY-MM-DD"). Dates outside that week are dropped (the page shows neighbouring days too).
func (c *Client) Week(ctx context.Context, pageURL string, day time.Time) (map[string][]Item, error) {
	year, week := day.ISOWeek()
	u := fmt.Sprintf("%s?week=%d&year=%d", pageURL, week, year)
	data, err := c.fetch(ctx, u)
	if err != nil {
		return nil, err
	}
	monday := day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7)).Format(time.DateOnly)
	sunday := day.AddDate(0, 0, 6-((int(day.Weekday())+6)%7)).Format(time.DateOnly)

	result := map[string][]Item{}
	seen := map[string]bool{} // the same item can appear in the data more than once
	for _, raw := range data.find("OutletMenuCategoryCalendarRangeDay") {
		var d weekDay
		if json.Unmarshal(raw, &d) != nil || len(d.From.DateLocal) < 10 {
			continue
		}
		date := d.From.DateLocal[:10]
		if date < monday || date > sunday {
			continue
		}
		for _, it := range d.MenuItems {
			if it.Dish == nil || seen[it.ID] {
				continue
			}
			seen[it.ID] = true
			if dish, ok := it.Dish.toDish(); ok {
				result[date] = append(result[date], Item{Dish: dish, DetailURL: it.DetailURL})
			}
		}
	}
	return result, nil
}

// Detail returns the prices, photo URL and nutrition values on a dish's detail page.
func (c *Client) Detail(ctx context.Context, detailURL string) (menu.Detail, error) {
	data, err := c.fetch(ctx, detailURL)
	if err != nil {
		return menu.Detail{}, err
	}
	for _, raw := range data.find("OutletMenuItemDish") {
		var it menuItem
		if json.Unmarshal(raw, &it) != nil {
			continue
		}
		d := menu.Detail{Prices: prices(it)}
		if it.Dish != nil {
			d.Photo = it.Dish.ImageURL
			d.Nutrition = it.Dish.Stats.nutrition()
		}
		if d != (menu.Detail{}) {
			return d, nil
		}
	}
	return menu.Detail{}, nil
}

// prices sorts a menu item's prices into the three customer groups.
func prices(it menuItem) menu.Prices {
	var p menu.Prices
	for _, price := range it.Prices {
		amount, err := parseAmount(price.Amount)
		if err != nil || amount <= 0 || price.Currency != "" && price.Currency != "CHF" {
			continue
		}
		switch group := strings.ToLower(price.Category.Name); {
		case strings.Contains(group, "stud"):
			p.Student = amount
		case strings.Contains(group, "mitarb") || strings.Contains(group, "angest") || strings.Contains(group, "staff"):
			p.Staff = amount
		case strings.Contains(group, "extern") || strings.Contains(group, "gast") || strings.Contains(group, "gäst") || strings.Contains(group, "guest"):
			p.External = amount
		case len(it.Prices) == 1: // a single price applies to everyone
			p = menu.Prices{Student: amount, Staff: amount, External: amount}
		}
	}
	return p
}

// ---- page data shapes (only the fields we use) ----

type weekDay struct {
	From struct {
		DateLocal string `json:"dateLocal"` // "2026-10-26T00:00:00+01:00"
	} `json:"from"`
	MenuItems []menuItem `json:"menuItems"`
}

type menuItem struct {
	ID        string `json:"id"`
	DetailURL string `json:"detailUrl"`
	Dish      *dish  `json:"dish"`
	Prices    []struct {
		Amount   string `json:"amount"` // a string, e.g. "9" or "7.50"
		Currency string `json:"currency"`
		Category struct {
			Name string `json:"name"` // "Studierende", "Mitarbeitende", "Externe"
		} `json:"priceCategory"`
	} `json:"prices"`
}

type dish struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	IsVegan      bool   `json:"isVegan"`
	IsVegetarian bool   `json:"isVegetarian"`
	ImageURL     string `json:"imageUrl"` // only on detail pages, and only once a photo was uploaded
	Stats        *stats `json:"stats"`    // only on detail pages
	Allergens    []struct {
		Allergen struct {
			ExternalID string `json:"externalId"` // "cerealsContainingGluten", "soybeans", ...
		} `json:"allergen"`
	} `json:"allergens"`
}

// stats holds a dish's nutrition values, each per 100 g and for the whole portion.
type stats struct {
	ServingWeight *struct {
		Amount float64 `json:"amount"`
		Unit   string  `json:"unit"` // "g"
	} `json:"servingWeight"`
	Energy        *amount `json:"energy"`
	Fat           *amount `json:"fat"`
	SaturatedFat  *amount `json:"saturatedFat"`
	Carbohydrates *amount `json:"carbohydrates"`
	Sugar         *amount `json:"sugar"`
	Fibers        *amount `json:"fibers"`
	Protein       *amount `json:"protein"`
	Salt          *amount `json:"salt"`
}

type amount struct {
	Per100g float64 `json:"amountPer100g"`
	Total   float64 `json:"amount"` // in the whole portion
	Unit    string  `json:"unit"`   // "kcal" for energy, "g" for the rest
}

// nutrition returns the dish's nutrition values, or nil if the page has none.
func (s *stats) nutrition() *menu.Nutrition {
	if s == nil || s.Energy == nil || s.Energy.Per100g <= 0 {
		return nil
	}
	n := &menu.Nutrition{Per100g: menu.Nutrients{}}
	if s.ServingWeight != nil && s.ServingWeight.Unit == "g" && s.ServingWeight.Amount > 0 {
		n.PortionGrams = s.ServingWeight.Amount
		n.Portion = menu.Nutrients{}
	}
	for key, a := range map[string]*amount{"energy": s.Energy, "fat": s.Fat, "saturates": s.SaturatedFat,
		"carbohydrates": s.Carbohydrates, "sugars": s.Sugar, "fibre": s.Fibers, "protein": s.Protein, "salt": s.Salt} {
		if a == nil {
			continue
		}
		scale := 1.0 // to kcal for energy, to grams for the rest
		switch strings.ToLower(a.Unit) {
		case "kcal", "g", "":
		case "kj":
			scale = 1 / menu.KJPerKcal
		case "mg":
			scale = 0.001
		default:
			continue
		}
		if a.Per100g < 0 || a.Total < 0 {
			continue
		}
		n.Per100g[key] = a.Per100g * scale
		if n.Portion != nil {
			n.Portion[key] = a.Total * scale
		}
	}
	return n
}

// meatless matches words that suggest a dish has no meat, in English and German.
var meatless = regexp.MustCompile(`(?i)vegan|vegi|veggie|vegetar|tofu|seitan|tempeh`)

func (d *dish) toDish() (menu.Dish, bool) {
	name := menu.TidyName(d.Name)
	if name == "" {
		return menu.Dish{}, false
	}
	var names []string
	for _, a := range d.Allergens {
		names = append(names, a.Allergen.ExternalID)
	}
	out := menu.Dish{
		Name:        menu.Same(name),
		Description: menu.Same(menu.CleanText(d.Description)),
		Allergens:   menu.AllergenSet(names),
	}
	switch {
	case d.IsVegan:
		out.Diet = "vegan"
	case d.IsVegetarian:
		out.Diet = "vegetarian"
	case meatless.MatchString(name + " " + d.Description):
		// The source doesn't flag every vegetarian dish ("Vegane Hackbällchen", "Butter
		// Tofu"). Calling those meat would be wrong, and the words alone don't say which of
		// vegan or vegetarian, so the dish gets no tag.
	default:
		// Not vegetarian means meat or fish; the fish allergen tells them apart.
		out.Diet = "meat"
		for _, a := range out.Allergens {
			if a == "fish" {
				out.Diet = "fish"
			}
		}
	}
	return out, true
}

// fetch downloads a page and returns its embedded Next.js data.
func (c *Client) fetch(ctx context.Context, pageURL string) (*nextData, error) {
	if _, err := url.Parse(pageURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mensaplan/1.0")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", pageURL, resp.Status)
	}
	html, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	data := parseNextData(html)
	if len(data.rows) == 0 {
		return nil, fmt.Errorf("GET %s: no page data found (did the site change?)", pageURL)
	}
	return data, nil
}

func parseAmount(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscan(strings.ReplaceAll(s, ",", "."), &f)
	return f, err
}
