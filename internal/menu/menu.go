// Package menu holds the menu data model shared by the menu sources (eth, food2050), the
// store that keeps it, and the web pages that show it.
package menu

import (
	"crypto/sha256"
	"encoding/hex"
)

// A Day is what one canteen serves on one date.
type Day struct {
	Lunch  Meal `json:"lunch"`
	Dinner Meal `json:"dinner"`
}

// Empty reports whether nothing is served at all.
func (d Day) Empty() bool { return len(d.Lunch.Dishes) == 0 && len(d.Dinner.Dishes) == 0 }

// Meal is one service (lunch or dinner) with its serving times, e.g. From "11:15", To "14:00".
type Meal struct {
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Dishes []Dish `json:"dishes,omitempty"`
}

// Dish is one item on a menu.
type Dish struct {
	Name        Text       `json:"name"`
	Description Text       `json:"description"`
	Diet        string     `json:"diet,omitempty"`      // vegan, vegetarian, meat, fish, or "" when unknown
	Allergens   []string   `json:"allergens,omitempty"` // ids from the Allergens list
	Prices      Prices     `json:"prices"`
	Per100g     bool       `json:"per100g,omitempty"`   // buffets are sold by weight
	Photo       string     `json:"photo,omitempty"`     // URL of the full-size photo at the source, "" if none
	Nutrition   *Nutrition `json:"nutrition,omitempty"` // nil if the source published none
}

// Nutrition is what a dish contains per 100 g and, when the source knows the portion's
// weight, in the whole portion.
type Nutrition struct {
	Per100g      Nutrients `json:"per100g"`
	PortionGrams float64   `json:"portionGrams,omitempty"`
	Portion      Nutrients `json:"portion,omitempty"`
}

// Nutrients maps a nutrient (one of NutrientKeys) to its amount: energy in kcal, the others
// in grams. Nutrients the source didn't publish are missing.
type Nutrients map[string]float64

// KJPerKcal converts between the two units of energy.
const KJPerKcal = 4.184

// NutrientKeys are the nutrients in the order of a Swiss food label.
var NutrientKeys = []string{"energy", "fat", "saturates", "carbohydrates", "sugars", "fibre", "protein", "salt"}

// Text is a string in English and German. Sources that only publish one language fill both.
type Text struct {
	EN string `json:"en,omitempty"`
	DE string `json:"de,omitempty"`
}

// In returns the text in the given language ("en" or "de"), falling back to the other one.
func (t Text) In(lang string) string {
	if lang == "de" && t.DE != "" || t.EN == "" {
		return t.DE
	}
	return t.EN
}

// Same returns a Text with the same string in both languages.
func Same(s string) Text { return Text{EN: s, DE: s} }

// Prices in CHF. Zero means the source did not publish that price.
type Prices struct {
	Student  float64 `json:"student,omitempty"`
	Staff    float64 `json:"staff,omitempty"`
	External float64 `json:"external,omitempty"`
}

// For returns the price for "student", "staff" or "external".
func (p Prices) For(group string) float64 {
	switch group {
	case "staff":
		return p.Staff
	case "external":
		return p.External
	default:
		return p.Student
	}
}

// PhotoKey turns a photo URL into the short name the website uses for it, /photos/<key>.jpg.
// Pages never contain the source URL itself; the photo handler looks the key up in the store.
func PhotoKey(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:8])
}

// HasAny reports whether any of the given allergens is in the dish.
func (d Dish) HasAny(allergens []string) bool {
	for _, a := range d.Allergens {
		for _, b := range allergens {
			if a == b {
				return true
			}
		}
	}
	return false
}
