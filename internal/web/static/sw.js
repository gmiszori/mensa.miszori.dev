// The service worker: keeps the menus of the next three days with menus in this browser, so the
// site opens and works without a connection (on a train, in a basement canteen).
//
// While online, app.js sends it the next three days with menus, and it saves, in this order:
//   1. the three pages and the CSS, JavaScript and fonts (a few hundred KB, a second or two),
//   2. the small photos (about 1 MB a day),
//   3. the large photos, one at a time and at low priority.
// Whenever the visitor opens a dish, saving pauses for a moment, so its photo has the
// connection to itself and the dish opens smoothly.
// Phones stop service workers soon after the browser goes to the background, so the important
// parts come first; whatever was missed is fetched on the next visit. Pages are fetched again
// at most once an hour.
//
// Pages live in one cache, photos in another. A photo is kept as long as a saved page shows it,
// so a day's photos go once the day has passed or is no longer one of the next three.
//
// A saved page opens at once, without waiting for the network. app.js then asks this worker to
// fetch the page again (refreshPage); if the menus changed, it reloads (from the copy just saved),
// and if there is no answer within a few seconds, it says the visitor is offline. Days that
// weren't saved come from the network. Photos and static files come from the saved copy first,
// since they never change.
'use strict';

const HOUR = 60 * 60 * 1000;
const PAGES = 'mp-pages';
const PHOTOS = 'mp-photos';
const STATIC_PREFIX = 'mp-static-'; // + the static files' version, see staticVersion in server.go

// Today in Zurich as YYYY-MM-DD, like the server's dates.
function today() {
  const parts = Object.fromEntries(new Intl.DateTimeFormat('en', {
    timeZone: 'Europe/Zurich', year: 'numeric', month: '2-digit', day: '2-digit',
  }).formatToParts(new Date()).map((p) => [p.type, p.value]));
  return `${parts.year}-${parts.month}-${parts.day}`;
}
// The server's protective headers (see securityHeaders in server.go), kept with a saved page so
// it is just as protected offline.
const SECURITY_HEADERS = ['Content-Security-Policy', 'X-Content-Type-Options', 'Referrer-Policy', 'Permissions-Policy'];
const pageURL = (day) => new URL(`/?day=${day}`, self.location).href;
const dayOf = (url) => new URL(url).searchParams.get('day');
const photosOn = (html) => (html.match(/\/photos\/[0-9a-f]+(?:-large)?\.jpg/g) || [])
  .map((path) => new URL(path, self.location).href);

self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (e) => e.waitUntil((async () => {
  await self.clients.claim();
  // The first version kept a cache per day.
  for (const key of await caches.keys()) {
    if (key.startsWith('mp-day-')) await caches.delete(key);
  }
})()));

// ---------- saving ----------

const isDay = (d) => typeof d === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(d);

// Saves run one after another, so two open tabs don't download the same photos twice.
let saving = Promise.resolve();
self.addEventListener('message', (e) => {
  if (isDay(e.data?.refresh)) {
    e.waitUntil(refreshPage(e.data.refresh).then((answer) => e.ports[0]?.postMessage(answer)));
    return;
  }
  const days = e.data?.save;
  if (!Array.isArray(days) || !days.every(isDay)) return;
  saving = saving.then(() => save(days.slice(0, 3))).catch(() => { /* offline or flaky: next time */ });
  e.waitUntil(saving);
});

async function save(days) {
  const pages = await caches.open(PAGES);
  for (const day of days) await savePage(pages, day);
  await removePages(days);
  const { photos, wanted, have } = await prunePhotos();
  const missing = [...wanted].filter((url) => !have.has(url));
  await inParallel(missing.filter((url) => !url.includes('-large')), 4, (url) => savePhoto(photos, url));
  await inParallel(missing.filter((url) => url.includes('-large')), 1, (url) => savePhoto(photos, url));
}

// savePage saves one day's page, and with it the static files, unless it was saved within the
// last hour. Pages saved without the security headers (by the first versions) are saved again.
async function savePage(pages, day) {
  const saved = await pages.match(pageURL(day));
  if (saved && Date.now() - Number(saved.headers.get('x-saved')) < HOUR && saved.headers.has('Content-Security-Policy')) return;
  const response = await fetch(pageURL(day), { cache: 'no-store' });
  // A redirect means the day has passed.
  if (!response.ok || response.redirected) return;
  await saveStatic(await storePage(pages, day, response));
}

// storePage saves a page fetched from the server, as saved at the time given, and returns its
// HTML.
async function storePage(pages, day, response, saved = Date.now()) {
  const html = await response.text();
  const headers = pageHeaders(response.headers);
  headers.set('x-saved', String(saved));
  await pages.put(pageURL(day), new Response(html, { headers }));
  return html;
}

// refreshPage fetches a day's page again for app.js, which is showing the saved copy, and saves
// it. It answers {online: false} when the server can't be reached, else whether the menus (or
// the days, or the version of the CSS and JavaScript) differ from the copy shown, and when the
// new copy was saved.
async function refreshPage(day) {
  try {
    const pages = await caches.open(PAGES);
    const shown = await pages.match(pageURL(day));
    const response = await fetch(pageURL(day), { cache: 'no-store' });
    if (response.status >= 500) return { online: false };
    // A redirect means the day has passed while the page was open: reloading takes care of it.
    if (!response.ok || response.redirected) return { online: true, changed: true };
    const saved = Date.now();
    const html = await storePage(pages, day, response, saved);
    saving = saving.then(() => saveStatic(html)).catch(() => {});
    return { online: true, changed: !shown || content(await shown.text()) !== content(html), saved };
  } catch {
    return { online: false };
  }
}

// content is the part of a page that matters for comparing two copies: the menus, the list of
// days and the version of the static files. Not the footer, whose time changes with every
// refresh of the server, or the meal shown first, which depends on the time of day.
function content(html) {
  return [
    html.slice(html.indexOf('<main'), html.indexOf('</main>')),
    html.match(/<nav class="day-list"[\s\S]*?<\/nav>/)?.[0],
    html.match(/\/static\/app\.js\?v=[0-9a-f]+/)?.[0],
  ].join('\n');
}

// quietUntil is when saving may go on after the visitor opened a dish (see photo).
let quietUntil = 0;
const QUIET = 3000;

async function savePhoto(photos, url) {
  while (Date.now() < quietUntil) await new Promise((resolve) => setTimeout(resolve, quietUntil - Date.now()));
  const response = await fetch(url, { priority: 'low' }).catch(() => null);
  if (response?.ok) await photos.put(url, response);
}

// saveStatic saves the CSS, JavaScript, icons and fonts the page uses, once per version of
// the static files, and deletes those of older versions.
async function saveStatic(html) {
  const version = html.match(/\/static\/app\.js\?v=([0-9a-f]+)/)?.[1];
  if (!version) return;
  const name = STATIC_PREFIX + version;
  const cache = await caches.open(name);
  const urls = new Set((html.match(/\/static\/[^"'\s]+/g) || []).map((path) => new URL(path, self.location).href));
  // The fonts are linked from app.css, relative to it.
  for (const css of [...urls].filter((url) => new URL(url).pathname.endsWith('.css'))) {
    const text = await (await fetch(css)).text();
    for (const [, path] of text.matchAll(/url\(['"]?([^'")]+)['"]?\)/g)) urls.add(new URL(path, css).href);
  }
  for (const url of urls) {
    if (!(await cache.match(url))) await cache.add(url).catch(() => { /* next time */ });
  }
  for (const key of await caches.keys()) {
    if (key.startsWith(STATIC_PREFIX) && key !== name) await caches.delete(key);
  }
}

// removePages deletes the pages of days that have passed and, when keep isn't empty, of days
// not in it, and says whether it deleted any. Their photos go with prunePhotos.
async function removePages(keep) {
  const now = today();
  const pages = await caches.open(PAGES);
  let removed = false;
  for (const request of await pages.keys()) {
    const day = dayOf(request.url);
    if (day < now || (keep.length > 0 && !keep.includes(day))) removed = await pages.delete(request) || removed;
  }
  return removed;
}

// prunePhotos deletes the photos no saved page shows any more. It returns the photo cache, the
// photos the saved pages show, and those that were in the cache.
async function prunePhotos() {
  const pages = await caches.open(PAGES);
  const wanted = new Set();
  for (const request of await pages.keys()) {
    for (const url of photosOn(await (await pages.match(request)).text())) wanted.add(url);
  }
  const photos = await caches.open(PHOTOS);
  const have = new Set((await photos.keys()).map((r) => r.url));
  for (const url of have) {
    if (!wanted.has(url)) await photos.delete(url);
  }
  return { photos, wanted, have };
}

// inParallel runs work for every item, at most limit at a time.
async function inParallel(items, limit, work) {
  const queue = [...items];
  await Promise.all(Array.from({ length: limit }, async () => {
    while (queue.length) await work(queue.shift());
  }));
}

// ---------- answering requests ----------

self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url);
  if (e.request.method !== 'GET' || url.origin !== self.location.origin) return;
  if (e.request.mode === 'navigate' && url.pathname === '/') {
    e.respondWith(page(e, url));
  } else if (url.pathname.startsWith('/photos/')) {
    e.respondWith(photo(e.request));
  } else if (url.pathname.startsWith('/static/')) {
    e.respondWith(caches.match(e.request, { ignoreVary: true }).then((saved) => saved || fetch(e.request)));
  }
});

// photo answers from the saved photos, or else from the network. A photo from the network is
// kept too, so a photo the visitor opened isn't downloaded again by the next save.
async function photo(request) {
  if (request.url.includes('-large')) quietUntil = Date.now() + QUIET;
  const photos = await caches.open(PHOTOS);
  const saved = await photos.match(request, { ignoreVary: true });
  if (saved) return saved;
  const response = await fetch(request).catch(() => Response.error()); // offline, not saved
  if (response.ok) photos.put(request, response.clone()).catch(() => {});
  return response;
}

// page answers with the saved copy of the day when there is one, at once (app.js then checks
// it is current, see refreshPage). Without the day in the address, that is the first saved day,
// the one the server would show too. Other days come from the network, falling back to the first
// saved day when that fails or takes too long: a server error (during a deploy, say) counts as
// failing, and so does no answer within NETWORK_WAIT, since in flight mode iPhones don't fail the
// request, they keep waiting for a connection. When the device says it is offline, the network
// isn't tried at all.
const NETWORK_WAIT = 3000;

async function page(e, url) {
  if (await removePages([])) e.waitUntil(prunePhotos()); // after answering, so the page isn't slowed down
  const days = await savedDays();
  const day = url.searchParams.get('day') || days[0];
  if (days.includes(day)) return savedPage(day);
  const network = self.navigator.onLine === false ? Promise.reject(new Error('offline')) : fetch(e.request);
  const slow = new Promise((resolve) => setTimeout(resolve, NETWORK_WAIT, 'slow'));
  const first = await Promise.race([network.catch(() => null), slow]);
  if (first instanceof Response && first.status < 500) return first;
  // A day that isn't saved: go to the first saved day instead, keeping the meal.
  if (days.length > 0) {
    const meal = url.searchParams.get('meal');
    return Response.redirect(new URL(meal ? `/?meal=${encodeURIComponent(meal)}` : '/', self.location).href, 302);
  }
  // Nothing saved: wait for the network after all.
  return network.catch(() => Response.error());
}

// savedPage is the saved page of a day, marked with data-saved, the time it was saved, so app.js
// knows to check it is current.
async function savedPage(day) {
  const saved = await (await caches.open(PAGES)).match(pageURL(day));
  const html = (await saved.text()).replace('<body ', `<body data-saved="${Number(saved.headers.get('x-saved'))}" `);
  return new Response(html, { headers: pageHeaders(saved.headers) });
}

// pageHeaders is the content type plus the security headers copied from the given ones.
function pageHeaders(from) {
  const headers = new Headers({ 'Content-Type': 'text/html; charset=utf-8' });
  for (const name of SECURITY_HEADERS) {
    if (from.has(name)) headers.set(name, from.get(name));
  }
  return headers;
}

// savedDays lists the days with a saved page, in order.
async function savedDays() {
  return (await (await caches.open(PAGES)).keys()).map((r) => dayOf(r.url)).sort();
}
