// Package catalog is the fixed list of canteens the app knows about: where they are and
// where their menus come from. The list lives in canteens.json, which is compiled into the
// binary, so adding a canteen means editing that file and rebuilding.
package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed canteens.json
var canteensJSON []byte

// Campus groups canteens that are close to each other.
type Campus struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Canteen is one place that serves food. Exactly one of ETH or Food2050 tells us where its
// menus come from.
type Canteen struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Campus  string  `json:"campus"`
	Address string  `json:"address"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`

	// ETH is the facility id in ETH's menu API (0 if the canteen is not run by ETH).
	ETH int `json:"eth,omitempty"`

	// Food2050 lists the weekly menu pages on app.food2050.ch (used by UZH/ZFV canteens).
	Food2050 []Food2050Page `json:"food2050,omitempty"`

	// Lunch and Dinner are the usual serving times, e.g. "11:00-14:00". ETH's API publishes
	// times per day, so these are only needed for Food2050 canteens.
	Lunch  string `json:"lunch,omitempty"`
	Dinner string `json:"dinner,omitempty"`
}

// Food2050Page is one weekly menu page. Canteens with lunch and dinner have one page each.
type Food2050Page struct {
	URL  string `json:"url"`
	Meal string `json:"meal"` // "lunch" or "dinner"
}

// Catalog is the parsed canteens.json.
type Catalog struct {
	Campuses []Campus  `json:"campuses"`
	Canteens []Canteen `json:"canteens"`
}

// Load parses the embedded canteens.json and checks that it is consistent.
func Load() (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(canteensJSON, &c); err != nil {
		return nil, fmt.Errorf("canteens.json: %w", err)
	}
	campuses := map[string]bool{}
	for _, campus := range c.Campuses {
		campuses[campus.ID] = true
	}
	seen := map[string]bool{}
	for _, canteen := range c.Canteens {
		switch {
		case seen[canteen.ID]:
			return nil, fmt.Errorf("canteens.json: duplicate id %q", canteen.ID)
		case !campuses[canteen.Campus]:
			return nil, fmt.Errorf("canteens.json: %s has unknown campus %q", canteen.ID, canteen.Campus)
		case (canteen.ETH == 0) == (len(canteen.Food2050) == 0):
			return nil, fmt.Errorf("canteens.json: %s needs exactly one menu source", canteen.ID)
		}
		seen[canteen.ID] = true
	}
	return &c, nil
}

// CampusName returns the display name of a campus id.
func (c *Catalog) CampusName(id string) string {
	for _, campus := range c.Campuses {
		if campus.ID == id {
			return campus.Name
		}
	}
	return id
}
