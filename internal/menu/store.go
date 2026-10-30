package menu

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Store keeps all known menus in memory and saves them to a JSON file, so a restart (or a
// menu website being down) does not leave the app empty.
//
// Dates are "YYYY-MM-DD" strings in Zurich time; they sort correctly as plain strings.
type Store struct {
	path string

	mu     sync.RWMutex
	data   storeData
	photos map[string]string // PhotoKey -> photo URL, for every dish in the store
}

// storeData is everything that gets written to the JSON file.
type storeData struct {
	// Version is the file's format, storeVersion when this code wrote it. Files written by
	// older code are brought up to date by migrations when they are opened.
	Version int `json:"version"`
	// Days maps date -> canteen id -> what that canteen serves that day.
	Days map[string]map[string]Day `json:"days"`
	// Updated records when each source last refreshed successfully, for /healthz.
	Updated map[string]time.Time `json:"updated"`
	// Details caches what FOOD2050 dish detail pages said, by page URL, so each page is
	// downloaded once (or until its photo appears, see refresh.go).
	Details map[string]Detail `json:"details"`
}

// Detail is what a FOOD2050 dish detail page adds to the weekly menu: prices, a photo and
// nutrition values.
type Detail struct {
	Prices    Prices     `json:"prices"`
	Photo     string     `json:"photo,omitempty"`
	Nutrition *Nutrition `json:"nutrition,omitempty"`
}

// storeVersion is the format this code writes. To change the format, add a step to
// migrations and raise storeVersion, so files written by older code still open.
const storeVersion = 1

// migrations[v] turns a file of version v into version v+1.
var migrations = []func(*storeData){
	// 0 -> 1: cached detail pages were read without their nutrition values. Forgetting them
	// makes the next refresh read every page again.
	func(d *storeData) { d.Details = nil },
}

// Open loads the store from path. A missing file is fine: the store starts empty.
func Open(path string) (*Store, error) {
	s := &Store{path: path, data: storeData{
		Days:    map[string]map[string]Day{},
		Updated: map[string]time.Time{},
		Details: map[string]Detail{},
	}, photos: map[string]string{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		s.data.Version = storeVersion
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		return nil, err
	}
	// A file from newer code (after a rollback) is used as it is: unknown fields are ignored.
	for ; s.data.Version < storeVersion; s.data.Version++ {
		migrations[s.data.Version](&s.data)
	}
	// Files written by older versions may lack a map.
	if s.data.Days == nil {
		s.data.Days = map[string]map[string]Day{}
	}
	if s.data.Updated == nil {
		s.data.Updated = map[string]time.Time{}
	}
	if s.data.Details == nil {
		s.data.Details = map[string]Detail{}
	}
	for _, canteens := range s.data.Days {
		for _, day := range canteens {
			s.indexPhotos(day)
		}
	}
	return s, nil
}

// Replace sets one canteen's menus for every date from..to (inclusive). Dates in that range
// that are missing from days, or empty, are removed: the source says nothing is served.
func (s *Store) Replace(canteen, from, to string, days map[string]Day) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for date := range s.data.Days {
		if date >= from && date <= to {
			delete(s.data.Days[date], canteen)
		}
	}
	for date, day := range days {
		if date < from || date > to || day.Empty() {
			continue
		}
		if s.data.Days[date] == nil {
			s.data.Days[date] = map[string]Day{}
		}
		s.data.Days[date][canteen] = day
		s.indexPhotos(day)
	}
}

// indexPhotos remembers the photo URLs of a day's dishes. Keys of menus that were replaced
// stay until the next Prune, which rebuilds the index.
func (s *Store) indexPhotos(day Day) {
	for _, meal := range []Meal{day.Lunch, day.Dinner} {
		for _, d := range meal.Dishes {
			if d.Photo != "" {
				s.photos[PhotoKey(d.Photo)] = d.Photo
			}
		}
	}
}

// PhotoURL returns the source URL of the photo with this key. Only photos of dishes in the
// store are known, so the photo handler cannot be used to fetch arbitrary URLs.
func (s *Store) PhotoURL(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	url, ok := s.photos[key]
	return url, ok
}

// MarkUpdated records a successful refresh of a source ("eth" or "food2050").
func (s *Store) MarkUpdated(source string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Updated[source] = at
}

// Updated returns when each source last refreshed successfully.
func (s *Store) Updated() map[string]time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]time.Time{}
	for k, v := range s.data.Updated {
		out[k] = v
	}
	return out
}

// CachedDetail returns what a FOOD2050 detail page said last time it was fetched.
func (s *Store) CachedDetail(url string) (Detail, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.data.Details[url]
	return d, ok
}

// CacheDetail remembers what a FOOD2050 detail page said.
func (s *Store) CacheDetail(url string, d Detail) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Details[url] = d
}

// On returns every canteen's menu for one date. It returns a copy: a refresh may change the
// store while the page is being rendered, and Go maps must not be read and written at the
// same time. The Day values themselves are never changed after they are stored.
func (s *Store) On(date string) map[string]Day {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]Day, len(s.data.Days[date]))
	for canteen, day := range s.data.Days[date] {
		out[canteen] = day
	}
	return out
}

// Dates returns all dates that have at least one menu, sorted.
func (s *Store) Dates() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var dates []string
	for date, canteens := range s.data.Days {
		if len(canteens) > 0 {
			dates = append(dates, date)
		}
	}
	sort.Strings(dates)
	return dates
}

// Prune forgets menus before the given date and cached detail pages of those days, and
// rebuilds the photo index, so photos of dishes no longer on any menu become unknown (and
// their cached files can be deleted, see photos.Handler.Prune).
func (s *Store) Prune(before string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for date := range s.data.Days {
		if date < before {
			delete(s.data.Days, date)
		}
	}
	for url := range s.data.Details {
		// FOOD2050 detail URLs end in the date, e.g. ".../garden/2026-10-26".
		if len(url) >= 10 && url[len(url)-10:] < before {
			delete(s.data.Details, url)
		}
	}
	s.photos = map[string]string{}
	for _, canteens := range s.data.Days {
		for _, day := range canteens {
			s.indexPhotos(day)
		}
	}
}

// Save writes the store to its file. It writes a temporary file first and renames it, so a
// crash never leaves a half-written file behind.
func (s *Store) Save() error {
	s.mu.RLock()
	raw, err := json.Marshal(s.data)
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
