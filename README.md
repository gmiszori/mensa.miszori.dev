# Mensaplan Zürich

Mensaplan Zürich shows the menus of the ETH, UZH and other canteens in the city (and ETH Basel)
on one page, with prices, photos and allergens. It started as *UniEats* at VISCon Hackathon 2026
(team VISard Lizards). It was then rewritten from scratch as one small Go program with no
database and no accounts. The code was written with AI under my direction.

![Opening two dishes, starring one so it moves to the top, then picking another day in the calendar](docs/images/demo.gif)

[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
![Go](https://img.shields.io/badge/server-Go-00ADD8)
![Docker](https://img.shields.io/badge/runs%20in-Docker-2496ED)

## Features

- **Every menu on one page.** 34 canteens from ETH Zurich and the ZFV (UZH and others), with
  real prices for students, staff and external guests.
- **Dish details.** Tap a dish to see its photo large, its ingredients, allergens and all
  prices. Only one dish is open at a time, centred on the screen.
- **Your campuses, your diet.** Settings show only the campuses you pick, only vegetarian or
  vegan dishes, and hide dishes with the allergens you choose. Changes apply immediately.
- **Your budget.** A slider hides dishes that cost more than you want to pay, for your price
  group; tick a box to see them below the others instead, under "Over budget".
- **Nearest first.** Sort the canteens by distance, with the distance shown on each one. The
  browser asks for your location on each visit; it is never stored or sent.
- **Favourites and starred dishes.** A starred canteen stays at the top of the page. A starred
  dish is listed first and marked wherever it is on the menu, on any day.
- **Any day, lunch or dinner.** The calendar button next to the search shows menus up to two
  weeks ahead. After 15:00 it switches to dinner on its own.
- **No account, nothing sent.** Settings stay in your browser (localStorage) and never reach the
  server: there are no cookies.
- **German and English, light and dark.** The language follows the browser until you pick one.

## Quick start

You need Go 1.27 or newer.

```bash
go run .
```

Open http://localhost:8080. The first start fetches all menus, which takes about a minute.
UZH prices take a few minutes the very first time and are cached after that. Menus are saved in
`./data`, so later starts show the last known menus right away.

The program reads four optional environment variables: `ADDR` (default `:8080`), `DATA_DIR`
(default `./data`), `REFRESH_EVERY` (default `1h`) and `DOMAIN`, the public domain used for
absolute links in link previews (default: the address of each request).

Run the tests with:

```bash
go test ./...
```

## How it works

```
                                                      every hour
Browser ─► proxy ─► Caddy (HTTPS) ─► Mensaplan Zürich ◄──────────── ETH menu API
                                     (Go, one binary) ◄──────────── app.food2050.ch (UZH and other ZFV canteens)
                                            │
                                            └─ data/menus.json, data/photos/
```

1. **Fetching.** On start and every hour, the program asks both sources for the current week and
   the two following ones. ETH has a JSON API. FOOD2050 has no API, so the program reads the data
   its pages embed for the browser. If a source is down, the menus it delivered earlier stay.
2. **Storing.** Menus live in memory and are saved to `data/menus.json` after every refresh. A
   few weeks of menus for ~35 canteens is well under a megabyte, so there is no database.
3. **Showing.** Every page request renders the HTML from the stored menus, the same for every
   visitor, with every text in English and German. In the browser, `boot.js` sets the saved
   language and theme before anything is drawn. `app.js` then applies the other settings
   (prices, filters, favourites, starred dishes) while placeholder cards show, filters
   instantly as you type or change settings, and animates opening dishes.
4. **Photos.** The first request for a photo downloads the original once. It saves a 168 px
   thumbnail and a 1080 px wide version in `data/photos/`. Photos of dishes that are no longer
   on any menu are deleted after each refresh.

| Directory | What |
|-----------|------|
| `main.go` | Starts everything: the refresher in the background and the web server. |
| `internal/catalog/` | The list of canteens (`canteens.json`): name, campus, address and where each menu comes from. **Add or fix a canteen here.** |
| `internal/menu/` | The data model (day, meal, dish), the store that saves `data/menus.json`, and clean-up of allergen names and ALL-CAPS names. |
| `internal/eth/` | Reads ETH's weekly menu plans in English and German. |
| `internal/food2050/` | Reads FOOD2050 weekly pages and dish pages (prices, photo) by decoding the Next.js data in them. |
| `internal/refresh/` | Runs both sources on a timer and writes into the store. |
| `internal/photos/` | Downloads, resizes and caches dish photos. It serves only photos of dishes on the menus. |
| `internal/web/` | The web server: routes, all texts in English and German, the page template, and `static/` (CSS, JavaScript, fonts). |
| `docs/` | The demo GIF, and `og-image.html`, the source of the image shown when a link to the site is shared. |

Routes: `/` is the page (`?day=YYYY-MM-DD&meal=lunch|dinner`).
`/photos/<key>.jpg` and `/photos/<key>-large.jpg` serve photos. `/healthz` answers while the
server runs (Docker's health check uses it) and lists sources that haven't refreshed for 6 hours
as `stale`. Caddy keeps it inside the Docker network; to see it on the server, run
`docker compose exec mensaplan /mensaplan healthcheck`. An outside uptime monitor watches the
home page.

## Deploying

Everything runs in Docker on one server, behind the shared
[webserver-proxy](https://github.com/gmiszori/webserver-proxy). The proxy owns ports 80 and 443
and passes this domain's traffic to Caddy without decrypting it. Caddy gets and renews the HTTPS
certificate by itself and passes requests on to Mensaplan Zürich. Neither is reachable from outside
except through the proxy.

1. Point the domain's DNS A (and AAAA) record at the server.
2. Open inbound TCP 80 and 443, and make sure the proxy is running with `MENSA_DOMAIN` set to
   your domain.
3. On the server:
   ```bash
   git clone https://github.com/gmiszori/mensa.miszori.dev && cd mensa.miszori.dev
   cp .env.example .env   # set DOMAIN
   docker compose run --rm --no-deps --user root --cap-add CHOWN --entrypoint chown caddy -R 1000:1000 /data /config
   docker compose up --build -d
   ```
   The second command runs once: Caddy runs as user 1000 and needs its certificate volumes to
   belong to it. Servers set up before this change need it once too.

To update, pull the new code and run `docker compose up --build -d` again. Menus and photos live
in the `data` volume and certificates in `caddy_data`, so restarts and updates lose nothing.
All images are pinned by digest: Caddy in `docker-compose.yml`, Go and the runtime image in the
`Dockerfile`. To update one, change its tag and digest there
(`docker buildx imagetools inspect <image>` shows the digest).

Logs: `docker compose logs -f`. Caddy writes one line per request. Docker keeps at most
30 MB of logs per container.
To try the production setup locally, start the proxy first with `PUBLIC_BIND_IP=127.0.0.1` and
`MENSA_DOMAIN=localhost` in its `.env`. Without a `.env` here, Caddy then serves https://localhost
with a local certificate.

### Configuration (`.env`)

| Variable | Default | |
|----------|---------|--|
| `DOMAIN` | `localhost` | The domain Caddy serves and gets the certificate for. Must match `MENSA_DOMAIN` in the proxy. |
| `REFRESH_EVERY` | `1h` | How often menus are fetched again. |

## Security

- **Strict content security policy.** The page loads scripts, styles, fonts and images only from
  its own origin. This blocks injected scripts and third-party tracking.
- **Escaped output.** Menu texts come from other websites and are always HTML-escaped by Go's
  templates, so a dish name can't inject markup.
- **No cookies, no settings on the server.** The page is the same for everyone, apart from its
  default language, taken from the browser's `Accept-Language` header; settings stay in the
  browser. There is nothing for the server to parse or store per visitor, and request
  headers are capped at 32 KB.
- **No open photo proxy.** `/photos/` serves only photos of dishes on the menus, looked up by a
  hash. Anyone else's URLs, and paths that try to leave the cache folder, get a 404. Photos are
  downloaded only from ETH's and FOOD2050's image addresses, redirects included, so a broken
  or hostile source can't make the server call other addresses. A failed download is retried
  after 5 minutes at the earliest.
- **Size limits on photos.** A source's image may be at most 15 MB and 12 megapixels. Its size is
  checked before decoding, so one oversized image can't exhaust the memory. At most four photos
  are downloaded at a time, and each only once.
- **No accounts, no personal data.** Nothing on the server can be changed from outside:
  refreshes run only on the timer. Responses from the sources are size-limited.
- **Locked-down containers.** The image holds only the program (distroless, about 20 MB). Both
  containers run as unprivileged users on a read-only filesystem with a memory limit, without
  Linux capabilities (Caddy keeps only the one to listen on ports 80 and 443), and can't gain
  privileges.
- **Pinned dependencies.** Images are pinned by digest and CI actions by commit, so a changed
  upstream release can't slip in unnoticed.
- **Absolute links from configuration.** Link previews use `DOMAIN`, not the request's `Host`
  header.
- **HTTPS only.** Caddy redirects HTTP to HTTPS and sends HSTS, so browsers keep using HTTPS.
- **Real addresses only from the proxy.** Caddy trusts the visitor address in a PROXY header only
  from the proxy's fixed address (`172.31.250.2`), so another container can't fake one in the
  logs.
- **Timeouts.** The server limits how long reading and writing a request may take. This stops
  slow clients from holding connections open.

Keep the server and the images up to date. Caddy's access log contains visitors' IP addresses;
Docker keeps only the last 30 MB of it, so state that in a privacy notice if you need one. ETH's
API is called with the client id that ethz.ch itself uses. FOOD2050 is read from its website, so
if the site changes, UZH menus stop updating while the tests still pass. Watch the logs for
`FOOD2050 refresh failed`.

## License

[MIT](LICENSE)
