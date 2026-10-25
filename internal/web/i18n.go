package web

import (
	"fmt"
	"time"
)

// texts holds every interface text in English and German. Menu texts come from the
// sources and are not translated here. The page carries all of them, for app.js.
var texts = map[string]map[string]string{
	"en": {
		"search":          "Search dishes",
		"pageTitle":       "Mensaplan Zürich",
		"pageDescription": "Today's menus of 34 canteens at ETH, UZH and around Zurich on one page, with prices, photos and allergens. Filter for vegetarian and vegan, star your favourites.",
		"settings":        "Settings",
		"close":           "Close",
		"day":             "Day",
		"meal":            "Meal",
		"dayMeal":         "Day, meal",
		"lunch":           "Lunch",
		"dinner":          "Dinner",
		"vegan":           "Vegan",
		"vegetarian":      "Vegetarian",
		"open":            "Open",
		"closed":          "Closed",
		"favourite":       "Favourite",
		"star":            "Star dish",
		"starred":         "Starred",
		"allergens":       "Allergens",
		"noAllergens":     "None declared",
		"prices":          "Prices",
		"student":         "Student",
		"staff":           "Staff",
		"external":        "External",
		"map":             "Map",
		"noLunch":         "%d canteens serve no lunch this day",
		"noDinner":        "%d canteens serve no dinner this day",
		"noResults":       "Nothing matches your filters.",
		"hiddenDishes":    "%d dishes hidden by your allergen settings.",
		"starredToday":    "★ %d starred dishes on this day's menus.",
		"noMenusToday":    "No menus for %s. Showing %s.",
		"nothingYet":      "No menus are available yet. They usually appear on Friday for the next week.",
		"setPrices":       "Prices",
		"setCampuses":     "My campuses",
		"setCampusesHelp": "Only canteens on these campuses are shown. Select none to see all.",
		"setDiet":         "Show dishes",
		"dietAll":         "All",
		"setOpenNow":      "Only canteens open now",
		"setOpenNowHelp":  "Applies to today's menus.",
		"off":             "Off",
		"on":              "On",
		"setAllergens":    "Hide dishes containing",
		"setStarred":      "Starred dishes",
		"setStarredHelp":  "Star a dish in its details; it is highlighted whenever it is on a menu.",
		"setNoStarred":    "No starred dishes yet.",
		"setLanguage":     "Language",
		"setTheme":        "Theme",
		"themeSystem":     "System",
		"themeLight":      "Light",
		"themeDark":       "Dark",
		"footer":          "Menus from ETH Zurich and FOOD2050 (ZFV). Updated %s.",
		"never":           "not yet",
	},
	"de": {
		"search":          "Gerichte suchen",
		"pageTitle":       "Mensaplan Zürich",
		"pageDescription": "Die heutigen Menüs von 34 Mensen der ETH, UZH und in Zürich auf einer Seite, mit Preisen, Fotos und Allergenen. Vegetarisch und vegan filtern, Favoriten markieren.",
		"settings":        "Einstellungen",
		"close":           "Schliessen",
		"day":             "Tag",
		"meal":            "Mahlzeit",
		"dayMeal":         "Tag, Mahlzeit",
		"lunch":           "Mittag",
		"dinner":          "Abend",
		"vegan":           "Vegan",
		"vegetarian":      "Vegetarisch",
		"open":            "Offen",
		"closed":          "Geschlossen",
		"favourite":       "Favorit",
		"star":            "Gericht markieren",
		"starred":         "Markiert",
		"allergens":       "Allergene",
		"noAllergens":     "Keine deklariert",
		"prices":          "Preise",
		"student":         "Studierende",
		"staff":           "Mitarbeitende",
		"external":        "Externe",
		"map":             "Karte",
		"noLunch":         "%d Mensen ohne Mittagessen an diesem Tag",
		"noDinner":        "%d Mensen ohne Abendessen an diesem Tag",
		"noResults":       "Nichts passt zu deinen Filtern.",
		"hiddenDishes":    "%d Gerichte wegen deiner Allergen-Einstellungen ausgeblendet.",
		"starredToday":    "★ %d markierte Gerichte auf den Menüs dieses Tages.",
		"noMenusToday":    "Keine Menüs für %s. Angezeigt wird %s.",
		"nothingYet":      "Noch keine Menüs verfügbar. Sie erscheinen meist am Freitag für die nächste Woche.",
		"setPrices":       "Preise",
		"setCampuses":     "Meine Campusse",
		"setCampusesHelp": "Nur Mensen auf diesen Campussen werden angezeigt. Keiner ausgewählt: alle.",
		"setDiet":         "Gerichte anzeigen",
		"dietAll":         "Alle",
		"setOpenNow":      "Nur jetzt geöffnete Mensen",
		"setOpenNowHelp":  "Gilt für die Menüs von heute.",
		"off":             "Aus",
		"on":              "Ein",
		"setAllergens":    "Gerichte ausblenden mit",
		"setStarred":      "Markierte Gerichte",
		"setStarredHelp":  "Markiere ein Gericht in seinen Details; es wird hervorgehoben, wann immer es auf einem Menü steht.",
		"setNoStarred":    "Noch keine markierten Gerichte.",
		"setLanguage":     "Sprache",
		"setTheme":        "Darstellung",
		"themeSystem":     "System",
		"themeLight":      "Hell",
		"themeDark":       "Dunkel",
		"footer":          "Menüs von der ETH Zürich und FOOD2050 (ZFV). Aktualisiert %s.",
		"never":           "noch nie",
	},
}

// translate returns the text for key in lang, formatted with args like fmt.Sprintf.
func translate(lang, key string, args ...any) string {
	s, ok := texts[lang][key]
	if !ok {
		s = texts["en"][key]
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

var allergenNames = map[string]map[string]string{
	"en": {"gluten": "Gluten", "crustaceans": "Crustaceans", "eggs": "Eggs", "fish": "Fish", "peanuts": "Peanuts",
		"soy": "Soy", "milk": "Milk", "nuts": "Nuts", "celery": "Celery", "mustard": "Mustard", "sesame": "Sesame",
		"sulphites": "Sulphites", "lupin": "Lupin", "molluscs": "Molluscs"},
	"de": {"gluten": "Gluten", "crustaceans": "Krebstiere", "eggs": "Eier", "fish": "Fisch", "peanuts": "Erdnüsse",
		"soy": "Soja", "milk": "Milch", "nuts": "Nüsse", "celery": "Sellerie", "mustard": "Senf", "sesame": "Sesam",
		"sulphites": "Sulfite", "lupin": "Lupinen", "molluscs": "Weichtiere"},
}

var dietNames = map[string]map[string]string{
	"en": {"vegan": "vegan", "vegetarian": "vegetarian", "meat": "meat", "fish": "fish"},
	"de": {"vegan": "vegan", "vegetarian": "vegetarisch", "meat": "Fleisch", "fish": "Fisch"},
}

var (
	weekdaysShort = map[string][]string{
		"en": {"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"},
		"de": {"So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"},
	}
	weekdaysLong = map[string][]string{
		"en": {"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"},
		"de": {"Sonntag", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag"},
	}
	months = map[string][]string{
		"en": {"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
		"de": {"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"},
	}
)

// shortDay formats a date for the day strip: "Mon 26" / "Mo 26".
func shortDay(lang string, d time.Time) string {
	return fmt.Sprintf("%s %d", weekdaysShort[lang][d.Weekday()], d.Day())
}

// longDay formats a date in a sentence: "Monday 26 October" / "Montag, 26. Oktober".
func longDay(lang string, d time.Time) string {
	if lang == "de" {
		return fmt.Sprintf("%s, %d. %s", weekdaysLong[lang][d.Weekday()], d.Day(), months[lang][d.Month()-1])
	}
	return fmt.Sprintf("%s %d %s", weekdaysLong[lang][d.Weekday()], d.Day(), months[lang][d.Month()-1])
}
