// Package refresh keeps the menu store up to date: it asks every source for the current and
// next week, on start and then at a fixed interval.
//
// A source that fails leaves the menus it delivered earlier untouched, so a website being
// down for a while only means slightly older data, never an empty app. A source that answers
// but has no menus at all for three weeks counts as failed too: that happens when its API or
// website changed, not because every canteen closed.
package refresh

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"mensafinder/internal/catalog"
	"mensafinder/internal/eth"
	"mensafinder/internal/food2050"
	"mensafinder/internal/menu"
	"mensafinder/internal/photos"
)

// Refresher holds everything a refresh needs.
type Refresher struct {
	Catalog  *catalog.Catalog
	Store    *menu.Store
	ETH      *eth.Client
	Food2050 *food2050.Client
	Photos   *photos.Handler // optional: its cached photos are pruned after each refresh
	Zone     *time.Location  // Europe/Zurich
	Log      *slog.Logger
}

// Run refreshes once, then again every interval, until ctx is cancelled.
func (r *Refresher) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		r.Once(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// weeks is how many weeks are fetched, starting with the current one. Menus are usually
// published one or two weeks ahead.
const weeks = 3

// Once refreshes all sources for the coming weeks, then saves the store.
func (r *Refresher) Once(ctx context.Context) {
	now := time.Now().In(r.Zone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, r.Zone)
	monday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	lastDay := monday.AddDate(0, 0, 7*weeks-1)

	started := time.Now()
	r.refreshETH(ctx, monday, lastDay)
	r.refreshFood2050(ctx, monday)

	r.Store.Prune(today.AddDate(0, 0, -1).Format(time.DateOnly))
	if err := r.Store.Save(); err != nil {
		r.Log.Error("saving menus failed", "err", err)
	}
	if r.Photos != nil {
		if removed, err := r.Photos.Prune(); err != nil {
			r.Log.Error("pruning photos failed", "err", err)
		} else if removed > 0 {
			r.Log.Info("pruned photos of past dishes", "files", removed)
		}
	}
	r.Log.Info("refresh done", "took", time.Since(started).Round(time.Millisecond), "days", len(r.Store.Dates()))
}

// refreshETH fetches every ETH canteen in one go (the API returns all facilities at once).
func (r *Refresher) refreshETH(ctx context.Context, from, to time.Time) {
	menus, err := r.ETH.Menus(ctx, from, to)
	if err == nil && len(menus) == 0 {
		err = errors.New("no menus at all")
	}
	if err != nil {
		r.Log.Warn("ETH refresh failed, keeping previous menus", "err", err)
		return
	}
	for _, c := range r.Catalog.Canteens {
		if c.ETH != 0 {
			r.Store.Replace(c.ID, from.Format(time.DateOnly), to.Format(time.DateOnly), menus[c.ETH])
		}
	}
	r.Store.MarkUpdated("eth", time.Now())
}

// refreshFood2050 fetches each FOOD2050 canteen, week by week. A canteen-week is only
// replaced when all its pages loaded.
func (r *Refresher) refreshFood2050(ctx context.Context, monday time.Time) {
	type result struct {
		canteen   string
		weekStart time.Time
		days      map[string]menu.Day
	}
	var results []result
	failures, dishes := 0, 0
	for _, c := range r.Catalog.Canteens {
		for w := range weeks {
			weekStart := monday.AddDate(0, 0, 7*w)
			days, err := r.food2050Week(ctx, c, weekStart)
			if err != nil {
				failures++
				r.Log.Warn("FOOD2050 refresh failed, keeping previous menus", "canteen", c.ID, "week", weekStart.Format(time.DateOnly), "err", err)
				continue
			}
			if days != nil {
				results = append(results, result{c.ID, weekStart, days})
				for _, day := range days {
					dishes += len(day.Lunch.Dishes) + len(day.Dinner.Dishes)
				}
			}
		}
	}
	if len(results) > 0 && dishes == 0 {
		r.Log.Warn("FOOD2050 refresh failed, keeping previous menus", "err", "no menus at all")
		return
	}
	for _, res := range results {
		r.Store.Replace(res.canteen, res.weekStart.Format(time.DateOnly), res.weekStart.AddDate(0, 0, 6).Format(time.DateOnly), res.days)
	}
	if failures == 0 {
		r.Store.MarkUpdated("food2050", time.Now())
	}
}

// food2050Week builds one canteen's menus for one week. It returns nil, nil for canteens that
// are not on FOOD2050.
func (r *Refresher) food2050Week(ctx context.Context, c catalog.Canteen, weekStart time.Time) (map[string]menu.Day, error) {
	if len(c.Food2050) == 0 {
		return nil, nil
	}
	days := map[string]menu.Day{}
	for _, page := range c.Food2050 {
		items, err := r.Food2050.Week(ctx, page.URL, weekStart)
		if err != nil {
			return nil, err
		}
		now := time.Now().In(r.Zone)
		r.addDetails(ctx, items, now.Format(time.DateOnly), now.AddDate(0, 0, 1).Format(time.DateOnly))
		for date, list := range items {
			day := days[date]
			meal := &day.Lunch
			hours := c.Lunch
			if page.Meal == "dinner" {
				meal, hours = &day.Dinner, c.Dinner
			}
			meal.From, meal.To, _ = strings.Cut(hours, "-")
			for _, it := range list {
				meal.Dishes = append(meal.Dishes, it.Dish)
			}
			days[date] = day
		}
	}
	return days, nil
}

// addDetails fills in prices and photos from the dishes' detail pages, four pages at a time.
// Detail pages are cached in the store and normally downloaded only once. The exception is
// dishes for today or tomorrow that have no photo yet: canteens upload photos shortly before
// or on the day, so those pages are checked again on every refresh. A failed page just
// leaves the dish as it is.
func (r *Refresher) addDetails(ctx context.Context, items map[string][]food2050.Item, today, tomorrow string) {
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for date, list := range items {
		for i := range list {
			it := &list[i]
			if it.DetailURL == "" {
				continue
			}
			cached, ok := r.Store.CachedDetail(it.DetailURL)
			if ok && (cached.Photo != "" || (date != today && date != tomorrow)) {
				it.Dish.Prices, it.Dish.Photo = cached.Prices, cached.Photo
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				slots <- struct{}{}
				defer func() { <-slots }()
				d, err := r.Food2050.Detail(ctx, it.DetailURL)
				if err != nil {
					r.Log.Debug("detail page failed", "url", it.DetailURL, "err", err)
					it.Dish.Prices = cached.Prices // keep what we knew, if anything
					return
				}
				it.Dish.Prices, it.Dish.Photo = d.Prices, d.Photo
				r.Store.CacheDetail(it.DetailURL, d)
			}()
		}
	}
	wg.Wait()
}
