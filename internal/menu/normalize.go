package menu

import (
	"strings"
	"unicode"
)

// Allergens are the 14 allergens EU law requires canteens to declare, in display order.
// Sources name them differently; Allergen maps their names to these ids.
var Allergens = []string{
	"gluten", "crustaceans", "eggs", "fish", "peanuts", "soy", "milk",
	"nuts", "celery", "mustard", "sesame", "sulphites", "lupin", "molluscs",
}

// allergenAliases maps source spellings (ETH in English/German, FOOD2050 ids) to our ids.
var allergenAliases = map[string]string{
	"gluten wheat": "gluten", "cerealscontaininggluten": "gluten", "wheat": "gluten", "spelt": "gluten",
	"rye": "gluten", "barley": "gluten", "kamut": "gluten", "oats": "gluten", "glutenhaltiges getreide": "gluten",
	"milk, lactose": "milk", "milch, laktose": "milk", "milch": "milk", "lactose": "milk",
	"soya": "soy", "soybeans": "soy", "soja": "soy",
	"egg": "eggs", "eier": "eggs",
	"fisch":      "fish",
	"krebstiere": "crustaceans",
	"weichtiere": "molluscs",
	"erdnüsse":   "peanuts",
	"almond":     "nuts", "hazel": "nuts", "hazelnut": "nuts", "walnut": "nuts", "cashew": "nuts", "brazilnut": "nuts",
	"macadamia": "nuts", "pecan": "nuts", "pistachio": "nuts", "schalenfrüchte": "nuts", "nüsse": "nuts",
	"sellerie": "celery",
	"senf":     "mustard",
	"sesam":    "sesame",
	"sulfites": "sulphites", "sulfite": "sulphites", "schwefeldioxid und sulfite": "sulphites",
	"lupine": "lupin",
}

// Allergen turns a source's allergen name into one of our ids. ok is false for names that
// are not one of the 14 (e.g. "histamine"), which we ignore.
func Allergen(name string) (id string, ok bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if alias, found := allergenAliases[key]; found {
		return alias, true
	}
	for _, a := range Allergens {
		if a == key {
			return a, true
		}
	}
	return "", false
}

// AllergenSet converts source names to our ids, without duplicates, in display order.
func AllergenSet(names []string) []string {
	found := map[string]bool{}
	for _, n := range names {
		if id, ok := Allergen(n); ok {
			found[id] = true
		}
	}
	var out []string
	for _, a := range Allergens {
		if found[a] {
			out = append(out, a)
		}
	}
	return out
}

// CleanText collapses runs of whitespace and trims the ends.
func CleanText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// smallWords stay lowercase inside a title, e.g. "Pasta with Red Pesto".
var smallWords = map[string]bool{
	"a": true, "an": true, "and": true, "at": true, "for": true, "in": true, "of": true, "on": true,
	"or": true, "the": true, "to": true, "with": true, "mit": true, "und": true,
	"am": true, "im": true, "vom": true, "von": true, "zu": true, "auf": true, "nach": true, "aus": true,
	"de": true, "di": true, "al": true, "alla": true, "la": true, "le": true,
}

// TidyName fixes dish names that sources publish in ALL CAPS ("PASTA WITH RED PESTO" becomes
// "Pasta with Red Pesto"). Names with normal casing are left alone.
func TidyName(s string) string {
	s = CleanText(s)
	if !shouting(s) {
		return s
	}
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		if i > 0 && smallWords[w] {
			continue
		}
		words[i] = capitalize(w)
	}
	return strings.Join(words, " ")
}

// shouting reports whether a string has letters and none of them are lowercase.
func shouting(s string) bool {
	letters := 0
	for _, r := range s {
		if unicode.IsLower(r) {
			return false
		}
		if unicode.IsLetter(r) {
			letters++
		}
	}
	return letters > 1
}

// capitalize uppercases the first letter, and the letter after a hyphen ("Chili-Sin-Carne").
func capitalize(w string) string {
	r := []rune(w)
	for i := range r {
		if i == 0 || r[i-1] == '-' || r[i-1] == '(' {
			r[i] = unicode.ToUpper(r[i])
		}
	}
	return string(r)
}
