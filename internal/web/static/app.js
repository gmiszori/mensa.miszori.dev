// Mensa Finder in the browser.
//
// The server already renders the whole page with every menu of the day. This script only:
//   1. filters instantly (search, lunch/dinner, and the diet, campus, allergen and "open now"
//      settings) by hiding elements,
//   2. opens one dish at a time, animated, centred on screen, with its photo large,
//   3. applies settings the moment they change and saves them, with favourites and starred
//      dishes, in the "mf" cookie, which the server reads on the next page load
//      (see internal/web/settings.go).
(() => {
  'use strict';

  const body = document.body;
  const texts = JSON.parse(body.dataset.texts || '{}');
  const grid = document.getElementById('canteens');
  const cards = [...document.querySelectorAll('.canteen')];
  const isToday = body.dataset.today === 'true';
  const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)');

  // ---------- the settings cookie ----------

  function readSettings() {
    const match = document.cookie.match(/(?:^|;\s*)mf=([^;]*)/);
    try {
      return match ? JSON.parse(decodeURIComponent(match[1])) : {};
    } catch {
      return {};
    }
  }

  function saveSettings() {
    const year = 60 * 60 * 24 * 400;
    document.cookie = `mf=${encodeURIComponent(JSON.stringify(settings))}; path=/; max-age=${year}; samesite=lax`;
  }

  const settings = readSettings();
  for (const key of ['campuses', 'hide', 'fav', 'star']) {
    if (!Array.isArray(settings[key])) settings[key] = [];
  }
  // The server picks the language from the browser until one is saved; keep that choice.
  settings.lang ||= document.documentElement.lang;

  // Same rule as starKey in settings.go: case and spacing do not matter.
  const starKey = (name) => name.toLowerCase().split(/\s+/).filter(Boolean).join(' ');

  // ---------- filtering ----------

  const state = {
    meal: body.dataset.meal, // "lunch" or "dinner"
    query: '',
  };

  // Lowercase and without accents, so "creme" finds "Crème".
  const fold = (s) => s.normalize('NFD').replace(/\p{Diacritic}/gu, '').toLowerCase();

  // No campus selected in the settings means all campuses.
  const campusMatches = (campus) => settings.campuses.length === 0 || settings.campuses.includes(campus);

  function dietMatches(diet) {
    if (settings.diet === 'vegan') return diet === 'vegan';
    if (settings.diet === 'vegetarian') return diet === 'vegan' || diet === 'vegetarian';
    return true;
  }

  const allergenHidden = (dish) => dish.dataset.allergens.split(' ').some((a) => a && settings.hide.includes(a));

  // Current time in Zurich as "HH:MM", whatever the visitor's time zone.
  function zurichClock() {
    return new Intl.DateTimeFormat('en-GB', {
      timeZone: 'Europe/Zurich', hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
    }).format(new Date());
  }

  // hours looks like "11:15-14:00 17:30-19:30".
  function isOpen(hours) {
    const now = zurichClock();
    return hours.split(' ').some((window) => {
      const [from, to] = window.split('-');
      return from && now >= from && now < to;
    });
  }

  function notice(id, template, count) {
    const el = document.getElementById(id);
    el.hidden = count === 0;
    el.textContent = (template || '%d').replace('%d', count);
  }

  function apply() {
    const query = fold(state.query.trim());
    const openNow = settings.open === true && isToday;
    let shown = 0;
    let hiddenByAllergens = 0;
    let starredHits = 0;

    for (const card of cards) {
      const campusOk = campusMatches(card.dataset.campus);
      const openOk = !openNow || isOpen(card.dataset.hours);
      const nameHit = query !== '' && fold(card.dataset.name).includes(query);
      let visibleDishes = 0;
      let mealHasDishes = false;

      for (const list of card.querySelectorAll('.dishes')) {
        const active = list.dataset.meal === state.meal;
        list.hidden = !active;
        for (const dish of list.children) {
          const allergenOk = !allergenHidden(dish);
          if (campusOk && !allergenOk) hiddenByAllergens++;
          if (campusOk && allergenOk && dish.hasAttribute('data-starred')) starredHits++;
          if (active && allergenOk) mealHasDishes = true;
          const ok = allergenOk && dietMatches(dish.dataset.diet) && (query === '' || nameHit || fold(dish.dataset.search).includes(query));
          dish.hidden = !ok;
          if (active && ok) visibleDishes++;
        }
      }
      for (const span of card.querySelectorAll('[data-for-meal]')) {
        span.hidden = span.dataset.forMeal !== state.meal;
      }

      card.hidden = !(campusOk && openOk && visibleDishes > 0);
      if (!card.hidden) shown++;
      // Canteens that serve nothing for this meal go to the "no menu" list instead.
      const item = document.querySelector(`#no-menu li[data-for="${card.dataset.id}"]`);
      if (item) item.hidden = !(campusOk && !mealHasDishes);
    }

    for (const item of document.querySelectorAll('#no-menu li[data-always]')) {
      item.hidden = !campusMatches(item.dataset.campus);
    }
    const noMenu = document.getElementById('no-menu');
    const missing = noMenu.querySelectorAll('li:not([hidden])').length;
    noMenu.hidden = missing === 0;
    const template = state.meal === 'dinner' ? texts.noDinner : texts.noLunch;
    noMenu.querySelector('summary').textContent = (template || '%d').replace('%d', missing);
    document.getElementById('no-results').hidden = shown > 0 || cards.length === 0;
    notice('hidden-count', texts.hidden, hiddenByAllergens);
    notice('starred-hits', texts.starHits, starredHits);

    for (const button of document.querySelectorAll('[data-meal-button]')) {
      button.setAttribute('aria-pressed', String(button.dataset.mealButton === state.meal));
    }
    placeCards();
  }

  // ---------- open / closed labels stay current while the page is open ----------

  function refreshStatus() {
    if (!isToday) return;
    for (const card of cards) {
      const label = card.querySelector('[data-status]');
      if (!label) continue;
      const open = isOpen(card.dataset.hours);
      label.className = `status ${open ? 'open' : 'closed'}`;
      label.textContent = open ? texts.open : texts.closed;
    }
    if (settings.open === true) apply();
  }

  // ---------- moving things, animated ----------

  // flip runs change(), which reorders elements, and lets each of them glide from its old
  // place to its new one (the "FLIP" technique: First, Last, Invert, Play).
  // A dish is measured relative to its canteen, which may be moving itself.
  function flip(elements, change) {
    const where = (el) => {
      const r = el.getBoundingClientRect();
      if (!el.classList.contains('dish')) return r;
      const card = el.closest('.canteen').getBoundingClientRect();
      return { left: r.left - card.left, top: r.top - card.top };
    };
    const before = new Map(elements.map((el) => [el, where(el)]));
    change();
    if (reduceMotion.matches) return;
    for (const [el, old] of before) {
      const now = where(el);
      const dx = old.left - now.left;
      const dy = old.top - now.top;
      if (dx === 0 && dy === 0) continue;
      el.animate([{ transform: `translate(${dx}px, ${dy}px)` }, { transform: 'none' }], {
        duration: 400, easing: 'cubic-bezier(0.2, 0, 0, 1)',
      });
    }
  }

  // Starred dishes first, otherwise in the source's order (data-order).
  function sortDishes(list) {
    const order = [...list.children].sort((a, b) =>
      (Number(b.hasAttribute('data-starred')) - Number(a.hasAttribute('data-starred'))) || (a.dataset.order - b.dataset.order));
    for (const dish of order) list.append(dish);
  }

  const isFavourite = (card) => card.querySelector('[data-favourite]').getAttribute('aria-pressed') === 'true';
  const starredCount = (card) => card.querySelectorAll(`.dishes[data-meal="${state.meal}"] .dish[data-starred]`).length;

  // Favourite canteens first, those with more starred dishes in this meal before the others;
  // then the catalog's order. Same rule as in server.go.
  function rankCards() {
    return [...cards].sort((a, b) => {
      const fa = isFavourite(a);
      const fb = isFavourite(b);
      if (fa !== fb) return fa ? -1 : 1;
      return (fa ? starredCount(b) - starredCount(a) : 0) || (a.dataset.order - b.dataset.order);
    });
  }

  // On wide screens the canteens are laid out in two columns, each card going to the shorter
  // one, so there are no gaps under short cards. The layout is only redone when the order or
  // what is shown changes, so opening a dish never makes a canteen jump to the other column.
  const twoColumns = window.matchMedia('(min-width: 900px)');
  let lastLayout = '';
  function placeCards() {
    const order = rankCards();
    const visibleDishes = (card) => card.querySelectorAll(`.dishes[data-meal="${state.meal}"] > .dish:not([hidden])`).length;
    const layout = `${twoColumns.matches}|${order.map((c) => (c.hidden ? '-' : `${c.dataset.id}:${visibleDishes(c)}`)).join(',')}`;
    if (layout === lastLayout) return;
    lastLayout = layout;
    const focused = document.activeElement; // moving an element takes the focus away
    if (!twoColumns.matches) {
      grid.replaceChildren(...order);
    } else {
      const columns = [0, 1].map(() => Object.assign(document.createElement('div'), { className: 'column' }));
      grid.replaceChildren(...columns);
      const heights = [0, 0];
      for (const card of order) {
        const i = heights[0] <= heights[1] ? 0 : 1;
        columns[i].append(card);
        if (!card.hidden) heights[i] += card.offsetHeight + 16; // 16: the gap between cards
      }
    }
    if (focused && focused !== document.activeElement && grid.contains(focused)) focused.focus({ preventScroll: true });
  }
  twoColumns.addEventListener('change', placeCards);
  document.fonts?.ready.then(() => { lastLayout = ''; placeCards(); });

  function setStarred(name, on) {
    const key = starKey(name);
    settings.star = settings.star.filter((n) => starKey(n) !== key);
    if (on) settings.star.push(name);
    saveSettings();
    // Every copy of this dish on the page (it may be served in several canteens) moves.
    const dishes = [...document.querySelectorAll('.dish')].filter((d) => starKey(d.dataset.name) === key);
    const lists = new Set(dishes.map((d) => d.parentElement));
    flip([...cards, ...[...lists].flatMap((list) => [...list.children])], () => {
      for (const dish of dishes) {
        dish.toggleAttribute('data-starred', on);
        const button = dish.querySelector('[data-star]');
        button.setAttribute('aria-pressed', String(on));
        button.textContent = on ? texts.starred : texts.star;
      }
      for (const list of lists) sortDishes(list);
      apply(); // also moves canteens whose number of starred dishes changed
    });
    // Keep the list of starred dishes in the settings sheet up to date.
    const group = document.querySelector('[data-setting="star"]');
    for (const chip of group.querySelectorAll('button')) {
      if (starKey(chip.value) === key) chip.remove();
    }
    if (on) {
      const chip = document.createElement('button');
      chip.className = 'chip';
      chip.type = 'button';
      chip.value = name;
      chip.setAttribute('aria-pressed', 'true');
      chip.textContent = `${name} ✕`;
      group.prepend(chip);
    }
    group.querySelector('[data-none]').hidden = settings.star.length > 0;
  }

  // ---------- opening a dish ----------
  //
  // Only one dish is open at a time. Opening one closes the open one; both change height
  // smoothly while the page scrolls so the opened dish ends up in the middle of the screen.
  // If the dish has a photo, its large version grows out of the thumbnail.

  const DURATION = 320;
  const ease = (t) => (t < 0.5 ? 4 * t * t * t : 1 - (-2 * t + 2) ** 3 / 2);
  let animating = false;

  // Loads the large photo; resolves true when it is there, false if it fails or is too slow.
  function loadPhoto(details, wait) {
    const photo = details.querySelector('.photo');
    if (!photo || !details.querySelector('.thumb')) return Promise.resolve(false);
    photo.src ||= photo.dataset.src;
    return new Promise((resolve) => {
      if (photo.complete) resolve(photo.naturalWidth > 0);
      photo.addEventListener('load', () => resolve(true), { once: true });
      photo.addEventListener('error', () => resolve(false), { once: true });
      if (wait) setTimeout(() => resolve(false), wait);
    });
  }

  function setExpanded(details, on) {
    details.classList.toggle('expanded', on);
    const photo = details.querySelector('.photo');
    if (photo) photo.hidden = !on;
  }

  // The large photo starts at the thumbnail's place and size and grows into its own.
  function growPhoto(details, from) {
    const photo = details.querySelector('.photo');
    if (!photo || !from || reduceMotion.matches) return;
    const to = photo.getBoundingClientRect();
    photo.animate([
      {
        transformOrigin: 'top left',
        transform: `translate(${from.left - to.left}px, ${from.top - to.top}px) scale(${from.width / to.width}, ${from.height / to.height})`,
        borderRadius: '12px',
      },
      { transformOrigin: 'top left', transform: 'none' },
    ], { duration: DURATION, easing: 'cubic-bezier(0.65, 0, 0.35, 1)' });
  }

  // Where to scroll so that el sits in the middle of the area below the sticky toolbar; an
  // element taller than that area starts right below the toolbar instead.
  function centredScroll(el) {
    const top = document.querySelector('.toolbar').getBoundingClientRect().bottom;
    const rect = el.getBoundingClientRect();
    const room = window.innerHeight - top;
    const offset = rect.height > room - 16 ? top + 8 : top + (room - rect.height) / 2;
    const max = document.documentElement.scrollHeight - window.innerHeight;
    return Math.max(0, Math.min(max, window.scrollY + rect.top - offset));
  }

  async function toggleDish(details) {
    if (animating) return;
    const opening = details.open ? null : details;
    const closing = details.open ? details : grid.querySelector('details[open]');
    const thumbRect = opening?.querySelector('.thumb')?.getBoundingClientRect();
    const photoReady = opening ? await loadPhoto(opening, 400) : false;
    animating = true;

    const dishes = [opening, closing].filter(Boolean);
    const startHeight = new Map(dishes.map((d) => [d, d.offsetHeight]));
    const anchor = opening?.closest('.dish');
    const anchorStart = anchor?.getBoundingClientRect().top;

    // Lay out the end state, measure it, then go back to the start and animate in between.
    const closingExpanded = closing?.classList.contains('expanded');
    if (closing) {
      closing.open = false;
      setExpanded(closing, false);
    }
    if (opening) {
      opening.open = true;
      setExpanded(opening, photoReady);
    }
    const endHeight = new Map(dishes.map((d) => [d, d.offsetHeight]));
    const target = anchor ? centredScroll(anchor) : window.scrollY;
    const anchorEnd = anchor ? anchor.getBoundingClientRect().top - (target - window.scrollY) : 0;

    const finish = () => {
      for (const d of dishes) {
        d.style.height = '';
        d.style.clipPath = '';
      }
      animating = false;
      // A photo that took longer than the wait above appears when it arrives.
      if (opening && !photoReady) {
        loadPhoto(opening, 0).then((ok) => {
          if (!ok || !opening.open) return;
          const from = opening.querySelector('.thumb').getBoundingClientRect();
          setExpanded(opening, true);
          growPhoto(opening, from);
        });
      }
    };

    if (reduceMotion.matches) {
      window.scrollTo(0, target);
      finish();
      return;
    }

    let fades = [];
    if (closing) {
      // The closing dish keeps showing its content while it shrinks, fading out.
      closing.open = true;
      setExpanded(closing, closingExpanded);
      fades = [...closing.querySelectorAll('.dish-detail, .photo')].map((part) =>
        part.animate([{ opacity: 1 }, { opacity: 0 }], { duration: DURATION * 0.6, fill: 'forwards' }));
    }
    for (const d of dishes) {
      // Clip only at the top and bottom: the large photo reaches past the dish's sides to
      // the card's edges, and cutting those off would make it jump wider at the end.
      d.style.clipPath = 'inset(0 -20px)';
      d.style.height = `${startHeight.get(d)}px`;
    }
    if (opening && photoReady) growPhoto(opening, thumbRect);

    const start = performance.now();
    const step = (now) => {
      const p = Math.min(1, (now - start) / DURATION);
      const e = ease(p);
      for (const d of dishes) {
        d.style.height = `${startHeight.get(d) + (endHeight.get(d) - startHeight.get(d)) * e}px`;
      }
      // Keep the opened dish gliding steadily towards the centre, whatever moves above it.
      if (anchor) {
        const wanted = anchorStart + (anchorEnd - anchorStart) * e;
        window.scrollBy(0, anchor.getBoundingClientRect().top - wanted);
      }
      if (p < 1) {
        requestAnimationFrame(step);
        return;
      }
      if (closing) {
        closing.open = false;
        setExpanded(closing, false);
        for (const fade of fades) fade.cancel();
        closing.querySelector('summary').animate([{ opacity: 0 }, { opacity: 1 }], { duration: 150 });
      }
      finish();
    };
    requestAnimationFrame(step);
  }

  grid.addEventListener('click', (e) => {
    const summary = e.target.closest('.dish summary');
    if (!summary) return;
    e.preventDefault(); // the browser would open it instantly; toggleDish animates instead
    toggleDish(summary.parentElement);
  });

  // ---------- the day and meal picker ----------

  const whenButton = document.getElementById('when');
  const whenPanel = document.getElementById('when-panel');

  function showPicker(open) {
    whenPanel.hidden = !open;
    whenButton.setAttribute('aria-expanded', String(open));
    if (open && !reduceMotion.matches) {
      whenPanel.animate([{ opacity: 0, transform: 'translateY(-6px)' }, { opacity: 1, transform: 'none' }], {
        duration: 160, easing: 'ease-out',
      });
    }
  }

  function setMeal(meal) {
    state.meal = meal;
    document.getElementById('when-label').textContent = `${whenButton.dataset.day} · ${texts[meal]}`;
    // Switching day keeps the meal.
    for (const link of document.querySelectorAll('[data-day-link]')) {
      const url = new URL(link.href);
      url.searchParams.set('meal', meal);
      link.href = url.pathname + url.search;
    }
    apply();
  }

  whenButton.addEventListener('click', () => showPicker(whenPanel.hidden));
  document.addEventListener('click', (e) => {
    if (!whenPanel.hidden && !e.target.closest('.when')) showPicker(false);
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && !whenPanel.hidden) {
      showPicker(false);
      whenButton.focus();
    }
  });
  for (const button of document.querySelectorAll('[data-meal-button]')) {
    button.addEventListener('click', () => setMeal(button.dataset.mealButton));
  }

  // ---------- favourites and stars ----------

  document.getElementById('search').addEventListener('input', (e) => {
    state.query = e.target.value;
    apply();
  });

  // Favourite canteens and starred dishes are saved right away and move into place.
  grid.addEventListener('click', (e) => {
    const favourite = e.target.closest('[data-favourite]');
    if (favourite) {
      const id = favourite.closest('.canteen').dataset.id;
      const on = favourite.getAttribute('aria-pressed') !== 'true';
      favourite.setAttribute('aria-pressed', String(on));
      settings.fav = settings.fav.filter((x) => x !== id);
      if (on) settings.fav.push(id);
      saveSettings();
      flip(cards, placeCards);
      return;
    }
    const star = e.target.closest('[data-star]');
    if (star) {
      const dish = star.closest('.dish');
      setStarred(dish.dataset.name, star.getAttribute('aria-pressed') !== 'true');
    }
  });

  // ---------- the settings sheet ----------
  //
  // Every setting takes effect immediately. Only the language needs the page from the
  // server again; the sheet reopens after that reload.

  const dialog = document.getElementById('settings');
  const sheetIsCentred = window.matchMedia('(min-width: 700px)'); // see dialog.sheet in app.css

  // On phones the sheet slides up from the bottom; on wide screens it fades and grows in the
  // middle. Closing plays the same backwards.
  function animateSheet(opening) {
    const hidden = sheetIsCentred.matches
      ? { opacity: 0, transform: 'scale(0.96)' }
      : { transform: 'translateY(100%)' };
    const shown = sheetIsCentred.matches ? { opacity: 1, transform: 'none' } : { transform: 'none' };
    const options = { duration: opening ? 280 : 200, easing: opening ? 'cubic-bezier(0.2, 0, 0, 1)' : 'cubic-bezier(0.4, 0, 1, 1)', fill: 'forwards' };
    const frames = opening ? [hidden, shown] : [shown, hidden];
    const fade = opening ? [{ opacity: 0 }, { opacity: 1 }] : [{ opacity: 1 }, { opacity: 0 }];
    const animations = [
      dialog.animate(frames, options),
      dialog.animate(fade, { ...options, pseudoElement: '::backdrop' }),
    ];
    // Resolves when both are done, then removes them so they don't hold their end state.
    return Promise.all(animations.map((a) => a.finished)).then(() => animations);
  }

  function openSettings() {
    dialog.showModal();
    if (reduceMotion.matches) return;
    animateSheet(true).then((animations) => animations.forEach((a) => a.cancel()));
  }

  let closingSheet = false;
  async function closeSettings() {
    if (closingSheet || !dialog.open) return;
    closingSheet = true;
    const animations = reduceMotion.matches ? [] : await animateSheet(false);
    dialog.close();
    animations.forEach((a) => a.cancel());
    closingSheet = false;
  }

  document.getElementById('open-settings').addEventListener('click', openSettings);
  document.getElementById('close-settings').addEventListener('click', closeSettings);
  // Escape closes the sheet with the same animation.
  dialog.addEventListener('cancel', (e) => {
    e.preventDefault();
    closeSettings();
  });
  try {
    if (sessionStorage.getItem('mf-settings-open')) {
      sessionStorage.removeItem('mf-settings-open');
      dialog.showModal(); // no animation: it was already open before the reload
    }
  } catch { /* storage unavailable: the sheet just stays closed */ }

  function showPrices() {
    const group = ['student', 'staff', 'external'].includes(settings.price) ? settings.price : 'student';
    for (const dish of document.querySelectorAll('.dish')) {
      dish.querySelector('.price-value').textContent = dish.dataset[group];
    }
  }

  function showTheme() {
    const root = document.documentElement;
    if (settings.theme === 'light' || settings.theme === 'dark') root.dataset.theme = settings.theme;
    else delete root.dataset.theme;
  }

  const applySetting = {
    price: showPrices,
    theme: showTheme,
    lang: () => {
      try { sessionStorage.setItem('mf-settings-open', '1'); } catch { /* see above */ }
      location.reload();
    },
    star: () => {}, // handled below
  };

  for (const group of dialog.querySelectorAll('[data-setting]')) {
    const key = group.dataset.setting;
    group.addEventListener('click', (e) => {
      const button = e.target.closest('button');
      if (!button) return;
      if (key === 'star') {
        // Unstarring from the list of starred dishes.
        button.remove();
        setStarred(button.value, false);
        return;
      }
      if (Array.isArray(settings[key])) {
        // Several values: campuses, hidden allergens.
        const on = button.getAttribute('aria-pressed') !== 'true';
        button.setAttribute('aria-pressed', String(on));
        settings[key] = settings[key].filter((v) => v !== button.value);
        if (on) settings[key].push(button.value);
      } else {
        // One value: price, diet, open now (on/off), language, theme.
        for (const b of group.querySelectorAll('button')) b.setAttribute('aria-pressed', String(b === button));
        settings[key] = key === 'open' ? button.value === '1' : button.value;
      }
      saveSettings();
      (applySetting[key] || apply)();
    });
  }

  // A photo that cannot be loaded (or turned out to be a logo) is removed, so the dish row
  // looks like one without a photo instead of showing an empty square.
  function dropPhoto(img) {
    const row = img.closest('.dish-row');
    row?.classList.remove('has-photo');
    row?.querySelector('.photo')?.remove();
    img.remove();
  }
  document.addEventListener('error', (e) => {
    if (e.target instanceof HTMLImageElement && e.target.classList.contains('thumb')) dropPhoto(e.target);
  }, true);
  // Photos that already failed before this script ran.
  for (const img of document.querySelectorAll('.thumb')) {
    if (img.complete && img.naturalWidth === 0) dropPhoto(img);
  }

  // ---------- start ----------

  apply();
  setInterval(refreshStatus, 60 * 1000);
})();
