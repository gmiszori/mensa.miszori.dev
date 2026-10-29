// The service worker: keeps the menus of the next three days with menus in this browser, so the
// site opens and works without a connection (on a train, in a basement canteen).
//
// While online, app.js sends it the next three days with menus. For each it saves the page,
// every photo on it (small and large) and the CSS, JavaScript and fonts, at most once an hour.
// Each day has its own cache, "mp-day-YYYY-MM-DD", deleted together with its photos once the
// day has passed or is no longer one of the next three.
//
// Pages always come from the network first; the saved copy is only used when that fails.
// Photos and static files come from the saved copy first, since they never change.
'use strict';

const HOUR = 60 * 60 * 1000;
const DAY_PREFIX = 'mp-day-';
const STATIC_PREFIX = 'mp-static-'; // + the static files' version, see staticVersion in server.go

// Today in Zurich as YYYY-MM-DD, like the server's dates.
const today = () => new Intl.DateTimeFormat('en-CA', { timeZone: 'Europe/Zurich' }).format(new Date());
const pageURL = (day) => new URL(`/?day=${day}`, self.location).href;

self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim().then(() => removeDays([]))));

// ---------- saving ----------

// Saves run one after another, so two open tabs don't download the same photos twice.
let saving = Promise.resolve();
self.addEventListener('message', (e) => {
  const days = e.data?.save;
  if (!Array.isArray(days) || !days.every((d) => /^\d{4}-\d{2}-\d{2}$/.test(d))) return;
  saving = saving.then(() => save(days.slice(0, 3))).catch(() => { /* offline or flaky: try next time */ });
  e.waitUntil(saving);
});

async function save(days) {
  for (const day of days) await saveDay(day);
  await removeDays(days);
}

// saveDay saves one day's page with its photos and static files, unless it was saved within the
// last hour. The page is saved last, so a saved page always has its photos.
async function saveDay(day) {
  const cache = await caches.open(DAY_PREFIX + day);
  const saved = await cache.match(pageURL(day));
  if (saved && Date.now() - Number(saved.headers.get('x-saved')) < HOUR) return;

  const response = await fetch(pageURL(day), { cache: 'no-store' });
  // A redirect means the day has passed.
  if (!response.ok || response.redirected) return;
  const html = await response.text();
  await saveStatic(html);

  const photos = new Set((html.match(/\/photos\/[0-9a-f]+(?:-large)?\.jpg/g) || [])
    .map((path) => new URL(path, self.location).href));
  const have = new Set((await cache.keys()).map((request) => request.url));
  const missing = [...photos].filter((url) => !have.has(url));
  await inParallel(missing, 4, async (url) => {
    // A dish served on several days: copy its photo from another day instead of downloading it.
    const photo = await caches.match(url) || await fetch(url).catch(() => null);
    if (photo?.ok) await cache.put(url, photo);
  });
  for (const url of have) {
    if (url.includes('/photos/') && !photos.has(url)) await cache.delete(url); // no longer on the menu
  }
  await cache.put(pageURL(day), new Response(html, {
    headers: { 'Content-Type': 'text/html; charset=utf-8', 'x-saved': String(Date.now()) },
  }));
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

// removeDays deletes the caches of days that have passed and, when keep isn't empty, of days
// not in it.
async function removeDays(keep) {
  const now = today();
  for (const key of await caches.keys()) {
    if (!key.startsWith(DAY_PREFIX)) continue;
    const day = key.slice(DAY_PREFIX.length);
    if (day < now || (keep.length > 0 && !keep.includes(day))) await caches.delete(key);
  }
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
    e.respondWith(page(e.request, url));
  } else if (url.pathname.startsWith('/static/') || url.pathname.startsWith('/photos/')) {
    e.respondWith(caches.match(e.request, { ignoreVary: true }).then((saved) => saved || fetch(e.request)));
  }
});

// page answers from the network and falls back to a saved page when that fails.
// A server error (during a deploy, say) counts as failing too.
async function page(request, url) {
  let response = null;
  try {
    response = await fetch(request);
    if (response.status < 500) return response;
  } catch { /* offline */ }
  await removeDays([]);
  const saved = await savedPage(url);
  if (saved) return saved;
  // A day that isn't saved: go to the first saved day instead, keeping the meal.
  if (url.searchParams.has('day') && (await savedDays()).length > 0) {
    const meal = url.searchParams.get('meal');
    return Response.redirect(meal ? `/?meal=${encodeURIComponent(meal)}` : '/', 302);
  }
  return response || Response.error();
}

// savedPage is the saved page for the day in url, or without a day, for today or the next
// saved day. It is marked with data-offline, the time it was saved, for app.js to show.
async function savedPage(url) {
  let day = url.searchParams.get('day');
  if (!day) {
    day = (await savedDays())[0];
    if (!day) return null;
  }
  const saved = await caches.match(pageURL(day), { cacheName: DAY_PREFIX + day });
  if (!saved) return null;
  const html = (await saved.text()).replace('<body ', `<body data-offline="${Number(saved.headers.get('x-saved'))}" `);
  return new Response(html, { headers: { 'Content-Type': 'text/html; charset=utf-8' } });
}

// savedDays lists the days with a saved page, from today on, in order.
async function savedDays() {
  const now = today();
  const days = (await caches.keys())
    .filter((key) => key.startsWith(DAY_PREFIX))
    .map((key) => key.slice(DAY_PREFIX.length))
    .filter((day) => day >= now)
    .sort();
  const withPage = [];
  for (const day of days) {
    if (await caches.match(pageURL(day), { cacheName: DAY_PREFIX + day })) withPage.push(day);
  }
  return withPage;
}
