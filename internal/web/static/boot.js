// Runs in the page's <head>, before anything is drawn, so the page appears in the visitor's
// language and theme right away instead of switching after a moment.
//
// The visitor's settings live only in this browser (localStorage "mp"); the server never sees
// them. app.js applies the rest of them (filters, prices, favourites, starred dishes) and then
// swaps the placeholder cards for the menus. If it does not get that far, the menus appear
// after 3 seconds anyway.
(() => {
  'use strict';
  const root = document.documentElement;
  let settings = {};
  try {
    // Earlier versions kept the settings in a cookie, which was sent to the server with every
    // request. Move them here once and delete the cookie.
    const cookie = document.cookie.match(/(?:^|;\s*)mp=([^;]*)/);
    if (cookie) {
      if (!localStorage.getItem('mp')) localStorage.setItem('mp', decodeURIComponent(cookie[1]));
      document.cookie = 'mp=; path=/; max-age=0';
    }
    settings = JSON.parse(localStorage.getItem('mp') || '{}') || {};
  } catch { /* storage unavailable or broken: defaults */ }

  // The server sends every page in English, the same for everyone so Cloudflare can keep a
  // copy; until the visitor picks a language, it follows the browser's.
  if (settings.lang === 'en' || settings.lang === 'de') root.lang = settings.lang;
  else if ((navigator.languages?.[0] || navigator.language || '').toLowerCase().startsWith('de')) root.lang = 'de';
  if (settings.theme === 'light' || settings.theme === 'dark') root.dataset.theme = settings.theme;
  root.classList.add('loading');
  setTimeout(() => root.classList.remove('loading'), 3000);
})();
