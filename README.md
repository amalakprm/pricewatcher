# PriceWatcher

PriceWatcher is a self-hosted price-drop tracker written in Go. It operates as a single statically-linked binary with zero external runtime dependencies and exposes a modern embedded Web UI.

## Features

- **Built-in Cron Scheduler**: Automatically schedules scrape runs based on CRON expressions.
- **Embedded Web UI**: Served directly from the compiled binary via `html/template` and `embed`. Features dynamic charts (Chart.js), interactive elements (HTMX), and custom styling (Tailwind CSS Play CDN).
- **SQLite Storage**: Powered by `modernc.org/sqlite` (no CGO, static binary compilation).
- **Multi-layer Scraper**:
  - **Layer 1**: Direct HTTP fetch with custom browser headers and `goquery` parsing (Amazon, Flipkart selectors, and generic heuristics).
  - **Layer 2**: Secondary parser fallback scanning already-fetched HTML for JSON-LD, OpenGraph tags, and JS State Blobs (regex key-walking).
  - **Layer 3**: Remote CDP control over an existing `CloakBrowser` instance with serialised execution.
- **Apprise API Integration**: Sends Telegram alerts for price drops and scrape failures with automatic cooldown gate-keeping.
- **Legacy Migration**: Import alert history from old python script JSON files on first run.

## Build

To compile a single static binary with no runtime dependencies for your Linux deployment (e.g. Sura):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o pricewatcher .
```

To compile for the host machine locally:

```bash
go build -o pricewatcher .
```

## Configuration

All configuration is managed via environment variables:

| Variable | Description | Default |
|---|---|---|
| `PRICEWATCHER_FEED_URL` | Google Apps Script JSON url containing product list feed | (Required) |
| `APPRISE_URL` | Apprise notification POST endpoint | `http://localhost:8000/notify/apprise` |
| `CLOAKBROWSER_CDP` | CloakBrowser remote debugging port endpoint | `http://192.168.1.9:9222` |
| `DB_PATH` | SQLite DB path | `/app/data/pricewatcher.db` |
| `WEB_PORT` | HTTP Web UI listening port | `:8420` |
| `CRON_SCHEDULE` | Scraper scheduling CRON pattern | `0 3,12 * * *` |
| `ALERT_COOLDOWN_HOURS` | Hour-based alert delay to prevent alert floods | `20` |
| `HTTP_TIMEOUT_SEC` | Page timeout for direct HTTP requests | `15` |
| `CDP_TIMEOUT_SEC` | Page timeout for remote CDP scraper | `30` |
| `MAX_HTTP_CONCURRENCY` | Maximum concurrent L1/L2 scrapes | `5` |
| `LEGACY_ALERT_FILE` | Source path of legacy alerts JSON file to migrate | (Optional) |

## Quick Start

1. Start PriceWatcher:
   ```bash
   ./pricewatcher
   ```
2. Navigate to `http://localhost:8420` to view the Dashboard.
3. Use `POST /api/run` to manually trigger scraping cycles.
