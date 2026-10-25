// Mensaplan Zürich in the browser.
//
// The server renders the same page for everyone: every menu of the day, every text in English
// and German. The visitor's settings never leave the browser; they live in localStorage
// ("mp"), and this script:
//   1. applies them when the page loads (boot.js has already set language and theme): prices,
//      filters, budget, favourite (or nearest) canteens first, starred dishes marked and first,
//   2. filters instantly (search, lunch/dinner, and the diet, campus, allergen and "open now"
//      settings) by hiding elements,
//   3. opens one dish at a time, animated, centred on screen, with its photo large,
//   4. applies settings the moment they change and saves them.
(() => {
  'use strict';

  const body = document.body;
  const root = document.documentElement;
  const allTexts = JSON.parse(body.dataset.texts || '{}'); // {en: {...}, de: {...}}
  const grid = document.getElementById('canteens');
  const cards = [...document.querySelectorAll('.canteen')];
  const isToday = body.dataset.today === 'true';
  const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)');

  // ---------- the settings, kept in this browser only ----------

  function readSettings() {
    try {
      const parsed = JSON.parse(localStorage.getItem('mp') || '{}');
      return parsed && typeof parsed === 'object' ? parsed : {};
    } catch {
      return {};
    }
  }

  function saveSettings() {
    try { localStorage.setItem('mp', JSON.stringify(settings)); } catch { /* not saved, still applied */ }
  }

  const settings = readSettings();
  for (const key of ['campuses', 'hide', 'fav', 'star']) {
    if (!Array.isArray(settings[key])) settings[key] = [];
  }
  settings.lang = settings.lang === 'de' || settings.lang === 'en' ? settings.lang : root.lang;

  // ---------- texts in both languages ----------
  //
  // Every text on the page is there in English and German, each in a <span lang>; app.css
  // shows the spans of the page's language. Texts this script writes follow the same rule, so
  // switching the language only has to change <html lang> and a few attributes.

  function setText(el, key, count) {
    el.replaceChildren(...['en', 'de'].map((lang) => Object.assign(document.createElement('span'), {
      lang, textContent: (allTexts[lang]?.[key] || '').replace('%d', count),
    })));
  }

  // Attributes can't hold two languages; they are set to the current one.
  // data-i18n="aria-label:settings" sets aria-label to the text "settings".
  function showLanguage() {
    root.lang = settings.lang;
    const texts = allTexts[settings.lang] || {};
    for (const el of document.querySelectorAll('[data-i18n]')) {
      const [attr, key] = el.dataset.i18n.split(':');
      el.setAttribute(attr, (texts[key] || '') + (el.dataset.i18nSuffix || ''));
    }
  }

  // Starred dishes are kept by name. Case and spacing do not matter, and a dish matches by its
  // English or its German name.
  const starKey = (name) => name.toLowerCase().split(/\s+/).filter(Boolean).join(' ');
  const dishKeys = (dish) => [dish.dataset.name, dish.dataset.nameDe].filter(Boolean).map(starKey);
  const isStarred = (dish) => dishKeys(dish).some((k) => settings.star.some((n) => starKey(n) === k));

  // ---------- filtering ----------

  const state = {
    meal: body.dataset.meal, // "lunch" or "dinner"
    query: '',
  };

  // Lowercase and without accents, so "creme" finds "Crème".
  const fold = (s) => s.normalize('NFD').replace(/\p{Diacritic}/gu, '').toLowerCase();

  // What the search looks in: each dish's name and description, in both languages. Built here
  // from the page's text rather than sent by the server a second time.
  const bothLanguages = (el) => {
    const spans = el.querySelectorAll('span[lang]');
    return spans.length ? [...spans].map((span) => span.textContent).join(' ') : el.textContent;
  };
  const searchText = new Map([...document.querySelectorAll('.dish')].map((dish) => [dish,
    fold(`${bothLanguages(dish.querySelector('.dish-name'))} ${bothLanguages(dish.querySelector('.dish-desc'))}`)]));

  // No campus selected in the settings means all campuses.
  const campusMatches = (campus) => settings.campuses.length === 0 || settings.campuses.includes(campus);

  function dietMatches(diet) {
    if (settings.diet === 'vegan') return diet === 'vegan';
    if (settings.diet === 'vegetarian') return diet === 'vegan' || diet === 'vegetarian';
    return true;
  }

  const allergenHidden = (dish) => dish.dataset.allergens.split(' ').some((a) => a && settings.hide.includes(a));

  // The budget is a price in CHF for the chosen price group, or not set for no limit.
  // Dishes without a price, or priced per 100g, are always within it.
  const BUDGET_OFF = 15.5; // the slider's right end
  const priceGroup = () => (['staff', 'external'].includes(settings.price) ? settings.price : 'student');
  const hasBudget = () => typeof settings.budget === 'number' && settings.budget < BUDGET_OFF;
  function overBudget(dish) {
    if (!hasBudget() || dish.querySelector('.price small')) return false;
    const price = parseFloat(dish.dataset[priceGroup()]);
    return price > settings.budget;
  }

  // The dishes of a list, without its "Over budget" label.
  const dishesOf = (list) => list.querySelectorAll(':scope > .dish');

  // Current time in Zurich as "HH:MM", whatever the visitor's time zone.
  function zurichClock() {
    return new Intl.DateTimeFormat('en-GB', {
      timeZone: 'Europe/Zurich', hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
    }).format(new Date());
  }

  // hours looks like "11:15-14:00 17:30-19:30".
  // closingTime is when the current opening window ends ("13:30"), or "" while closed.
  function closingTime(hours) {
    const now = zurichClock();
    for (const window of hours.split(' ')) {
      const [from, to] = window.split('-');
      if (from && now >= from && now < to) return to;
    }
    return '';
  }

  function isOpen(hours) {
    return closingTime(hours) !== '';
  }

  // timeLeft formats the time until a clock time as "1h 20" or "45 min".
  function timeLeft(to) {
    const minutes = (clock) => { const [h, m] = clock.split(':'); return +h * 60 + +m; };
    const left = minutes(to) - minutes(zurichClock());
    return left < 60 ? `${left} min` : `${Math.floor(left / 60)}h ${String(left % 60).padStart(2, '0')}`;
  }

  function notice(id, key, count) {
    const el = document.getElementById(id);
    el.hidden = count === 0;
    setText(el, key, count);
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
        let overShown = false;
        for (const dish of dishesOf(list)) {
          const allergenOk = !allergenHidden(dish);
          if (campusOk && !allergenOk) hiddenByAllergens++;
          if (campusOk && allergenOk && dish.hasAttribute('data-starred')) starredHits++;
          if (active && allergenOk) mealHasDishes = true;
          const over = overBudget(dish);
          const ok = allergenOk && dietMatches(dish.dataset.diet) && (!over || settings.overBelow === true)
            && (query === '' || nameHit || searchText.get(dish).includes(query));
          dish.hidden = !ok;
          if (ok && over) overShown = true;
          if (active && ok) visibleDishes++;
        }
        list.querySelector(':scope > .over-budget').hidden = !overShown;
        // The divider under a starred dish's band goes, also when hidden dishes sit between.
        let previous = null;
        for (const item of list.children) {
          if (item.hidden) continue;
          item.classList.toggle('after-starred', previous?.hasAttribute('data-starred') === true);
          previous = item;
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
    setText(noMenu.querySelector('summary'), state.meal === 'dinner' ? 'noDinner' : 'noLunch', missing);
    document.getElementById('no-results').hidden = shown > 0 || cards.length === 0;
    notice('hidden-count', 'hiddenDishes', hiddenByAllergens);
    notice('starred-hits', 'starredToday', starredHits);

    for (const button of document.querySelectorAll('[data-meal-button]')) {
      button.setAttribute('aria-pressed', String(button.dataset.mealButton === state.meal));
    }
    placeCards();
  }

  // ---------- open / closed labels, today only, kept current while the page is open ----------

  function refreshStatus() {
    if (!isToday) return;
    for (const card of cards) {
      const label = card.querySelector('[data-status]');
      if (!label) continue;
      const closes = closingTime(card.dataset.hours);
      label.className = `status ${closes ? 'open' : 'closed'}`;
      setText(label, closes ? 'open' : 'closed');
      if (closes) label.append(` · ${timeLeft(closes)}`);
    }
    if (settings.open === true) apply();
  }

  // ---------- moving things, animated ----------

  // flip runs change(), which reorders elements, and lets each of them glide from its old
  // place to its new one (the "FLIP" technique: First, Last, Invert, Play).
  // A dish is measured relative to its canteen, which may be moving itself.
  // anchor, if given, is what the visitor tapped: the page scrolls with it, so it stays where it
  // was on screen while the rest moves around it.
  function flip(elements, change, anchor) {
    const where = (el) => {
      const r = el.getBoundingClientRect();
      if (!el.closest('.dishes')) return r;
      const card = el.closest('.canteen').getBoundingClientRect();
      return { left: r.left - card.left, top: r.top - card.top };
    };
    const before = new Map(elements.map((el) => [el, where(el)]));
    const anchorTop = anchor?.getBoundingClientRect().top;
    change();
    if (anchor) window.scrollBy({ top: anchor.getBoundingClientRect().top - anchorTop, behavior: 'instant' });
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

  // Dishes within the budget first, then the "Over budget" label and the others. In each part
  // starred dishes first, otherwise in the source's order (data-order).
  function sortDishes(list) {
    const order = [...dishesOf(list)].sort((a, b) =>
      (Number(overBudget(a)) - Number(overBudget(b)))
      || (Number(b.hasAttribute('data-starred')) - Number(a.hasAttribute('data-starred')))
      || (a.dataset.order - b.dataset.order));
    const label = list.querySelector(':scope > .over-budget');
    const firstOver = order.findIndex(overBudget);
    if (firstOver < 0) order.push(label);
    else order.splice(firstOver, 0, label);
    list.append(...order);
  }
  const sortAllDishes = () => { for (const list of document.querySelectorAll('.dishes')) sortDishes(list); };

  const isFavourite = (card) => card.querySelector('[data-favourite]').getAttribute('aria-pressed') === 'true';
  const starredCount = (card) => card.querySelectorAll(`.dishes[data-meal="${state.meal}"] .dish[data-starred]`).length;

  // Sorted by distance: nearest first. Otherwise favourite canteens first, those with more
  // starred dishes in this meal before the others; then the catalog's order.
  function rankCards() {
    if (settings.near === true && here) return [...cards].sort((a, b) => distance(a) - distance(b));
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

  // markStarred shows the given dishes as starred or not; the button's text follows by CSS.
  function markStarred(dishes) {
    for (const dish of dishes) {
      const on = isStarred(dish);
      dish.toggleAttribute('data-starred', on);
      dish.querySelector('[data-star]').setAttribute('aria-pressed', String(on));
    }
  }

  // The list of starred dishes in the settings sheet, each a button that unstars it.
  function showStarredList() {
    const group = document.querySelector('[data-setting="star"]');
    group.querySelectorAll('button').forEach((chip) => chip.remove());
    for (const name of settings.star) {
      const chip = Object.assign(document.createElement('button'), { className: 'chip', type: 'button', value: name });
      chip.setAttribute('aria-pressed', 'true');
      chip.textContent = `${name} ✕`;
      group.append(chip);
    }
    group.querySelector('[data-none]').hidden = settings.star.length > 0;
  }

  // name is one of the dish's names; keys are all its names, so unstarring removes the entry
  // whichever language it was starred in. anchor is the dish tapped, if any (see flip).
  function setStarred(name, on, keys = [starKey(name)], anchor = undefined) {
    settings.star = settings.star.filter((n) => !keys.includes(starKey(n)));
    if (on) settings.star.unshift(name);
    saveSettings();
    // Every copy of this dish on the page (it may be served in several canteens) moves.
    const dishes = [...document.querySelectorAll('.dish')].filter((d) => dishKeys(d).some((k) => keys.includes(k)));
    const lists = new Set(dishes.map((d) => d.parentElement));
    flip([...cards, ...[...lists].flatMap((list) => [...list.children])], () => {
      markStarred(dishes);
      for (const list of lists) sortDishes(list);
      apply(); // also moves canteens whose number of starred dishes changed
    }, anchor);
    showStarredList();
  }

  // ---------- sorting canteens by distance ----------
  //
  // The browser is asked for the visitor's location on each visit while the setting is on. It
  // is only used here: never stored, never sent.

  let here = null; // {lat, lng}
  const nearError = document.getElementById('near-error');

  // Metres between the visitor and a canteen (haversine formula).
  function distance(card) {
    const lat = Number(card.dataset.lat);
    const lng = Number(card.dataset.lng);
    if (!here || !lat) return Infinity;
    const rad = Math.PI / 180;
    const a = Math.sin((lat - here.lat) * rad / 2) ** 2
      + Math.cos(here.lat * rad) * Math.cos(lat * rad) * Math.sin((lng - here.lng) * rad / 2) ** 2;
    return 2 * 6371e3 * Math.asin(Math.sqrt(a));
  }

  // "350 m", "1.2 km", "74 km".
  function formatDistance(m) {
    if (m < 1000) return `${Math.round(m / 10) * 10} m`;
    return `${(m / 1000).toFixed(m < 10e3 ? 1 : 0)} km`;
  }

  function showDistances() {
    for (const card of cards) {
      const label = card.querySelector('[data-distance]');
      const m = settings.near === true ? distance(card) : Infinity;
      label.hidden = m === Infinity;
      label.textContent = m === Infinity ? '' : ` · ${formatDistance(m)}`;
    }
  }

  // The canteens glide to their new places.
  function reorderCards() {
    flip(cards, () => {
      showDistances();
      apply();
    });
  }

  function locationFailed() {
    settings.near = false;
    saveSettings();
    here = null;
    showSettingsSheet();
    setText(nearError, 'nearFailed');
    nearError.hidden = false;
    reorderCards();
  }

  function locate() {
    nearError.hidden = true;
    if (settings.near !== true) {
      here = null;
      reorderCards();
      return;
    }
    if (!navigator.geolocation) {
      locationFailed();
      return;
    }
    navigator.geolocation.getCurrentPosition((position) => {
      if (settings.near !== true) return; // turned off meanwhile
      here = { lat: position.coords.latitude, lng: position.coords.longitude };
      reorderCards();
    }, locationFailed, { maximumAge: 5 * 60 * 1000, timeout: 15 * 1000 });
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

  // Touching or scrolling during the animation hands the scrolling back to the user.
  let userScrolled = false;
  for (const type of ['touchstart', 'wheel']) {
    window.addEventListener(type, () => { userScrolled = true; }, { passive: true });
  }

  async function toggleDish(details) {
    if (animating) return;
    const opening = details.open ? null : details;
    const closing = details.open ? details : grid.querySelector('details[open]');
    const thumbRect = opening?.querySelector('.thumb')?.getBoundingClientRect();
    const photoReady = opening ? await loadPhoto(opening, 400) : false;
    animating = true;
    userScrolled = false;

    const dishes = [opening, closing].filter(Boolean);
    const startHeight = new Map(dishes.map((d) => [d, d.offsetHeight]));
    const anchor = opening?.closest('.dish');
    const scrollStart = window.scrollY;

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
    const target = anchor ? Math.round(centredScroll(anchor)) : window.scrollY;

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
        d.style.height = `${Math.round(startHeight.get(d) + (endHeight.get(d) - startHeight.get(d)) * e)}px`;
      }
      // Scroll along the same curve as the heights change, so the opened dish glides to the
      // centre. The scroll position is computed, never measured: on iPhones the page reports
      // its position a frame late, and correcting by measurement made the page shake.
      if (anchor && !userScrolled) {
        window.scrollTo(0, Math.round(scrollStart + (target - scrollStart) * e));
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
    for (const label of document.querySelectorAll('[data-meal-label]')) label.hidden = label.dataset.mealLabel !== meal;
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
      flip(cards, placeCards, favourite.closest('.canteen'));
      return;
    }
    const star = e.target.closest('[data-star]');
    if (star) {
      const dish = star.closest('.dish');
      const name = settings.lang === 'de' ? dish.dataset.nameDe || dish.dataset.name : dish.dataset.name;
      setStarred(name, star.getAttribute('aria-pressed') !== 'true', dishKeys(dish), dish);
    }
  });

  // ---------- the settings sheet ----------
  //
  // Every setting takes effect immediately and is saved in this browser.

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
  function showPrices() {
    const group = priceGroup();
    for (const dish of document.querySelectorAll('.dish')) {
      dish.querySelector('.price-value').textContent = dish.dataset[group];
    }
  }

  // ---------- the budget ----------

  const budgetSlider = document.getElementById('budget');
  const budgetValue = document.getElementById('budget-value');
  const overBelow = document.getElementById('over-below');

  // Shows the budget in the sheet: the slider, its value and the tick box.
  function showBudget() {
    budgetSlider.value = hasBudget() ? settings.budget : BUDGET_OFF;
    if (hasBudget()) budgetValue.textContent = `CHF ${settings.budget.toFixed(2)}`;
    else setText(budgetValue, 'noLimit');
    budgetSlider.setAttribute('aria-valuetext', hasBudget() ? budgetValue.textContent : allTexts[settings.lang]?.noLimit || '');
    overBelow.checked = settings.overBelow === true;
    overBelow.disabled = !hasBudget();
  }

  // The dishes move between the two parts of each list as the budget or price group changes.
  function applyBudget() {
    sortAllDishes();
    apply();
  }

  budgetSlider.addEventListener('input', () => {
    const value = Number(budgetSlider.value);
    if (value >= BUDGET_OFF) delete settings.budget;
    else settings.budget = value;
    showBudget();
    applyBudget();
  });
  budgetSlider.addEventListener('change', saveSettings);
  overBelow.addEventListener('change', () => {
    settings.overBelow = overBelow.checked;
    saveSettings();
    apply();
  });

  function showTheme() {
    const root = document.documentElement;
    if (settings.theme === 'light' || settings.theme === 'dark') root.dataset.theme = settings.theme;
    else delete root.dataset.theme;
  }

  const applySetting = {
    price: () => { showPrices(); applyBudget(); },
    theme: showTheme,
    lang: () => { showLanguage(); showBudget(); },
    near: locate,
  };

  // Shows the saved settings in the sheet's buttons.
  function showSettingsSheet() {
    for (const group of dialog.querySelectorAll('[data-setting]')) {
      const key = group.dataset.setting;
      if (key === 'star') continue;
      let value = settings[key];
      if (key === 'open' || key === 'near') value = value === true ? '1' : '';
      if (key === 'price' && !['staff', 'external'].includes(value)) value = 'student';
      if (key === 'diet' && !['vegetarian', 'vegan'].includes(value)) value = '';
      if (key === 'theme' && !['light', 'dark'].includes(value)) value = 'system';
      for (const b of group.querySelectorAll('button')) {
        b.setAttribute('aria-pressed', String(Array.isArray(value) ? value.includes(b.value) : b.value === value));
      }
    }
    showStarredList();
    showBudget();
  }

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
        // One value: price, diet, open now and sort by distance (on/off), language, theme.
        for (const b of group.querySelectorAll('button')) b.setAttribute('aria-pressed', String(b === button));
        settings[key] = key === 'open' || key === 'near' ? button.value === '1' : button.value;
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
  //
  // Apply the saved settings while the placeholder cards are showing, then swap them for the
  // menus. This all happens before the browser draws again, so nothing visibly jumps.

  showLanguage();
  showPrices();
  showSettingsSheet();
  for (const card of cards) {
    card.querySelector('[data-favourite]').setAttribute('aria-pressed', String(settings.fav.includes(card.dataset.id)));
  }
  const allDishes = [...document.querySelectorAll('.dish')];
  markStarred(allDishes);
  for (const list of document.querySelectorAll('.dishes')) {
    const label = Object.assign(document.createElement('li'), { className: 'over-budget', hidden: true });
    setText(label, 'overBudget');
    list.append(label);
  }
  sortAllDishes();
  refreshStatus();
  apply();
  root.classList.remove('loading');
  lastLayout = '';
  placeCards(); // again, now that the cards can be measured
  setInterval(refreshStatus, 60 * 1000);
  if (settings.near === true) locate();
})();
