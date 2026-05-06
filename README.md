# Fireflies Downloader

Fireflies Downloader is a local-first Go tool for exporting, indexing, browsing, and querying your Fireflies.ai call archive.

It pulls transcript data from the Fireflies GraphQL API, writes full-fidelity JSON files, generates normalized CSV files, stores searchable records in SQLite, serves a clean local web UI, and exposes a read-only MCP endpoint so AI clients can analyze the archive.

The project is designed for people who want to:

- Keep a private backup of Fireflies transcripts.
- Search across calls, summaries, speakers, and attendees.
- Browse people and account history.
- Inspect sales/coaching signals from Fireflies analytics.
- Use Claude, ChatGPT, or another MCP client against local call data.
- Later delete old Fireflies transcripts with a dry-run-first workflow.

## Contents

- [Features](#features)
- [What Gets Exported](#what-gets-exported)
- [Requirements](#requirements)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [Command Overview](#command-overview)
- [Downloading Transcripts](#downloading-transcripts)
- [Throttling and Resuming](#throttling-and-resuming)
- [SQLite Database](#sqlite-database)
- [Web Interface](#web-interface)
- [MCP Endpoint](#mcp-endpoint)
- [Deleting Old Transcripts](#deleting-old-transcripts)
- [Limitations](#limitations)
- [Security and Privacy](#security-and-privacy)
- [Development](#development)
- [Troubleshooting](#troubleshooting)
- [License](#license)

## Features

- Full transcript archive: stores the complete Fireflies transcript JSON returned by the selected query profile.
- CSV exports: writes spreadsheet-friendly CSV files for transcripts, sentences, summaries, speakers, attendees, analytics, sharing, channels, app outputs, and media download status.
- SQLite archive: creates normalized relational tables, raw JSON storage, indexes, and an FTS5 search table.
- Local web UI: dashboard, transcript browser, search filters, transcript details, people browser, sales insights, rendered Markdown summaries, and dark mode.
- MCP server endpoint: exposes read-only tools over Streamable HTTP for AI clients.
- Optional media download: downloads audio/video when Fireflies returns media URLs and `--include-media` is enabled.
- API throttling support: global request pacing, conservative defaults, retry/backoff, and resume-friendly output behavior.
- Cleanup workflow: `delete-old` supports dry-run plans before deleting transcripts from Fireflies.

## What Gets Exported

By default, the downloader uses the `complete` profile. It asks Fireflies for the richest documented transcript payload available to the API key, including:

- Transcript metadata: ID, title, date, duration, privacy, organizer, host, calendar data, meeting link, Fireflies URL, live status, user metadata.
- Participants and users: participants, Fireflies users, workspace users, meeting attendees, attendance records.
- Transcript content: sentences, speaker names, timestamps, raw text, and sentence-level AI filters when present.
- Summary fields: keywords, action items, outline, overview, gist, bullet gist, short summary, short overview, meeting type, topics discussed, transcript chapters, notes, and extended sections.
- Analytics: sentiment percentages, category counts, and speaker analytics such as talk time, word count, longest monologue, filler words, questions, duration percentage, and words per minute.
- Collaboration metadata: channels and shared-with records.
- Fireflies app outputs: app preview output title, prompt, response, app ID, and creation time.
- Media URLs: audio and video URLs when available to the plan and permissions.

The tool writes these outputs:

```text
fireflies_export/
  transcripts/<transcript_id>.json      # Full transcript JSON payload
  csv/*.csv                             # Normalized CSV exports
  fireflies.sqlite                      # Local SQLite archive
  index.json                            # Paginated transcript listing metadata
  manifest.json                         # Export run status, filters, warnings, failures
  media/*                               # Optional audio/video downloads
```

JSON is the source of truth. CSV and SQLite files are generated from the downloaded JSON so you can use spreadsheet tools, SQL, and the web UI without losing the original Fireflies payload.

## Requirements

- Go 1.25 or newer.
- A Fireflies.ai API key.
- Optional: `sqlite3` CLI for direct database inspection.
- Optional: an MCP client that supports Streamable HTTP if you want AI-client integration.

## Installation

Get a Fireflies API key from your Fireflies account settings, then set it in your shell:

```sh
export FIREFLIES_API_KEY="your_api_key"
```

Do not commit real API keys. `.env`, `.env.*`, and `.mise.local.toml` are ignored for local secrets. `.env.example` is safe to commit.

If you keep secrets in `.env`, load them into your shell before running the tool. The binary does not automatically parse `.env` files:

```sh
set -a
. ./.env
set +a
```

Run from source:

```sh
go run ./cmd/fireflies-downloader help
```

Build a binary:

```sh
make build
./bin/fireflies-downloader help
```

## Quick Start

Download the archive:

```sh
go run ./cmd/fireflies-downloader download --output fireflies_export
```

Start the local web UI:

```sh
go run ./cmd/fireflies-downloader serve --db fireflies_export/fireflies.sqlite
```

Open:

```text
http://127.0.0.1:8080
```

Build a reusable binary:

```sh
make build
./bin/fireflies-downloader download --output fireflies_export
./bin/fireflies-downloader serve --db fireflies_export/fireflies.sqlite
```

## Configuration

Environment variables:

| Variable | Purpose |
| --- | --- |
| `FIREFLIES_API_KEY` | Fireflies API key. Required for `download`, `list`, and `delete-old` unless `--api-key` is passed. |
| `FIREFLIES_API_URL` | Optional GraphQL endpoint override. Defaults to `https://api.fireflies.ai/graphql`. |
| `FIREFLIES_MCP_TOKEN` | Optional bearer token for the local `/mcp` endpoint. |

Common API flags:

| Flag | Default | Purpose |
| --- | --- | --- |
| `--api-key` | `FIREFLIES_API_KEY` | Override the API key. |
| `--endpoint` | Fireflies GraphQL API | Override the API endpoint. |
| `--timeout` | `60s` | HTTP timeout. |
| `--page-size` | `50` | Fireflies transcript list page size. |
| `--request-delay` | `1100ms` | Minimum delay between Fireflies API requests. |
| `--max-retries` | `8` | Retries for throttling/transient failures. |
| `--retry-min-wait` | `10s` | Minimum retry wait when Fireflies does not return `retryAfter`. |
| `--retry-max-wait` | `5m` | Maximum retry wait. |

Common filters:

| Flag | Purpose |
| --- | --- |
| `--from` / `--to` | Date bounds, using `YYYY-MM-DD` or RFC3339 timestamps. |
| `--mine` | Only transcripts organized by the API key owner. |
| `--user-id` | Fireflies user ID filter. |
| `--organizers` | Comma-separated organizer emails. |
| `--participants` | Comma-separated participant emails. |
| `--channel-id` | Fireflies channel ID. |
| `--keyword` | Fireflies keyword search. |
| `--scope` | Keyword scope: `title`, `sentences`, or `all`. |
| `--max` | Maximum transcripts to process. `0` means no cap. |

## Command Overview

```text
fireflies-downloader download [flags]
fireflies-downloader serve [flags]
fireflies-downloader list [flags]
fireflies-downloader delete-old [flags]
```

| Command | Purpose |
| --- | --- |
| `download` | Export transcript JSON, CSV, SQLite, manifest, and optional media. |
| `serve` | Start the local web UI and MCP endpoint for an existing SQLite archive. |
| `list` | Print accessible transcript IDs, dates, organizers, and titles without writing an archive. |
| `delete-old` | Build a deletion plan, or delete old transcripts when `--confirm` is passed. |

List visible transcripts:

```sh
go run ./cmd/fireflies-downloader list --max 20
```

## Downloading Transcripts

Export everything the API key can access:

```sh
go run ./cmd/fireflies-downloader download
```

Include audio/video downloads when Fireflies returns media URLs:

```sh
go run ./cmd/fireflies-downloader download --include-media
```

Filter by date:

```sh
go run ./cmd/fireflies-downloader download --from 2026-01-01 --to 2026-04-01
```

Filter by organizer or participant:

```sh
go run ./cmd/fireflies-downloader download --organizers alice@example.com,bob@example.com
go run ./cmd/fireflies-downloader download --participants buyer@example.com
```

Use Fireflies keyword search:

```sh
go run ./cmd/fireflies-downloader download --keyword renewal --scope all
```

Write SQLite somewhere else:

```sh
go run ./cmd/fireflies-downloader download --sqlite ./calls.sqlite
```

Skip CSV or SQLite generation:

```sh
go run ./cmd/fireflies-downloader download --no-csv
go run ./cmd/fireflies-downloader download --no-sqlite
```

Replace existing transcript JSON files:

```sh
go run ./cmd/fireflies-downloader download --overwrite
```

### Data Profiles

`--profile` controls how much data the transcript detail query requests:

- `complete`: richest profile, including summaries, sentences, analytics, attendees, sharing, channels, app outputs, and media URLs.
- `portable`: smaller profile for accounts/plans where some richer fields are unavailable.
- `minimal`: smallest fallback profile for basic metadata and transcript content.

If `complete` fails for a transcript, the downloader retries with smaller profiles unless `--strict-profile` is passed. This is useful because Fireflies fields can vary by plan, permissions, API behavior, or transcript state.

## Throttling and Resuming

Fireflies currently documents these API limits:

- Free / Pro: 50 requests per day.
- Business / Enterprise: 60 requests per minute.
- `deleteTranscript`: 10 requests per minute across all tiers.

References:

- https://docs.fireflies.ai/fundamentals/limits
- https://docs.fireflies.ai/graphql-api/mutation/delete-transcript

The downloader defaults are intentionally conservative:

```text
--concurrency 1
--request-delay 1100ms
--max-retries 8
--retry-min-wait 10s
--retry-max-wait 5m
```

If Fireflies throttles you, slow the run down:

```sh
go run ./cmd/fireflies-downloader download \
  --request-delay 2s \
  --max-retries 12 \
  --retry-max-wait 15m
```

If you are on a daily limit, retries cannot bypass the daily quota. Use `--max` for smaller batches, keep the same `--output` directory, and resume later. Existing transcript JSON files are skipped unless `--overwrite` is passed.

## SQLite Database

The default database is:

```text
fireflies_export/fireflies.sqlite
```

Important tables:

| Table | Purpose |
| --- | --- |
| `export_runs` | Export metadata and filters. |
| `transcripts` | One row per transcript, including metadata, raw JSON path, and raw JSON. |
| `summaries` | Fireflies summary fields. Many values are Markdown. |
| `sentences` | Sentence-level transcript text, speaker, timestamps, raw text, and AI filters. |
| `speakers` | Speaker IDs and names. |
| `meeting_attendees` | Fireflies attendee records. |
| `meeting_attendance` | Join/leave attendance records. |
| `analytics_overview` | Sentiment and category counts. |
| `analytics_speakers` | Speaker-level talk metrics. |
| `channels` | Fireflies channel metadata. |
| `shared_with` | External sharing metadata. |
| `app_outputs` | Fireflies app preview outputs. |
| `downloaded_media` | Downloaded media file paths and media errors. |
| `transcript_search_docs` | Text assembled for search. |
| `transcript_search_fts` | FTS5 full-text index. |

Open the database:

```sh
sqlite3 fireflies_export/fireflies.sqlite
```

Recent calls:

```sql
SELECT id, date_string, organizer_email, title
FROM transcripts
ORDER BY date_ms DESC
LIMIT 20;
```

Search sentence text:

```sql
SELECT transcript_id, start_time, speaker_name, text
FROM sentences
WHERE text LIKE '%budget%'
ORDER BY transcript_id, sentence_index;
```

Use the FTS index:

```sql
SELECT d.transcript_id, d.title
FROM transcript_search_fts f
JOIN transcript_search_docs d ON d.rowid = f.rowid
WHERE transcript_search_fts MATCH 'renewal OR budget'
LIMIT 20;
```

Find high-friction calls:

```sql
SELECT t.id, t.title, t.date_string, a.negative_pct, s.short_summary
FROM analytics_overview a
JOIN transcripts t ON t.id = a.transcript_id
LEFT JOIN summaries s ON s.transcript_id = t.id
ORDER BY a.negative_pct DESC
LIMIT 20;
```

## Web Interface

Start the web UI:

```sh
go run ./cmd/fireflies-downloader serve --db fireflies_export/fireflies.sqlite
```

Open:

```text
http://127.0.0.1:8080
```

Serve flags:

| Flag | Default | Purpose |
| --- | --- | --- |
| `--db` | `fireflies_export/fireflies.sqlite` | SQLite archive path. |
| `--addr` | `127.0.0.1:8080` | HTTP listen address. |
| `--mcp-token` | `FIREFLIES_MCP_TOKEN` | Optional bearer token for `/mcp`. |

The UI includes:

- Dashboard: totals, activity over time, top organizers, meeting types, recent transcripts.
- Insights: sentiment trends, high-friction calls, high-positive calls, talk dominance, question gaps, and coaching suggestions.
- People: organizer/host/attendee/participant/speaker rollups with call counts, sentiment, talk share, words per minute, questions, and last seen date.
- Transcript search: full-text search plus filters for organizer, person, meeting type, and sentiment.
- Transcript detail: rendered Markdown summaries, metadata, sentiment, speakers, attendees, media links, app outputs, raw JSON, and sentence-level transcript text.
- Theme support: light, dark, and local preference storage.

The insight view is a coaching aid, not a scientific assessment. It uses Fireflies analytics and simple heuristics to surface calls worth reviewing.

## MCP Endpoint

The `serve` command exposes a read-only MCP endpoint:

```text
http://127.0.0.1:8080/mcp
```

It implements JSON-RPC over MCP Streamable HTTP. MCP clients that support Streamable HTTP can connect directly to this URL.

Available tools:

| Tool | Purpose |
| --- | --- |
| `search_transcripts` | Full-text transcript search with person, organizer, meeting type, and sentiment filters. |
| `get_transcript` | One transcript with Markdown summaries, analytics, speakers, attendees, media links, app outputs, sentence text, and optional raw JSON. |
| `list_people` | People browser data for model-side analysis. |
| `get_person` | One person's calls, sentiment, and speaking metrics. |
| `get_insights` | Sales/coaching signals from the archive. |
| `database_schema` | SQLite tables and columns. |
| `query_database` | Bounded read-only `SELECT` / `WITH` queries. |

Example:

```sh
curl -sS http://127.0.0.1:8080/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

With bearer authentication:

```sh
export FIREFLIES_MCP_TOKEN="choose-a-long-local-token"
go run ./cmd/fireflies-downloader serve --db fireflies_export/fireflies.sqlite

curl -sS http://127.0.0.1:8080/mcp \
  -H "Authorization: Bearer $FIREFLIES_MCP_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

Example tool call:

```sh
curl -sS http://127.0.0.1:8080/mcp \
  -H 'Content-Type: application/json' \
  -d '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "tools/call",
    "params": {
      "name": "search_transcripts",
      "arguments": {
        "query": "renewal budget",
        "sentiment": "mixed",
        "limit": 5
      }
    }
  }'
```

### Connecting AI Clients

For clients that support Streamable HTTP MCP, use:

```text
URL: http://127.0.0.1:8080/mcp
Authorization: Bearer <FIREFLIES_MCP_TOKEN>   # only if configured
```

Client-specific MCP configuration changes frequently. If a client only supports stdio MCP, use a local Streamable HTTP bridge/proxy or wait for native HTTP transport support.

## Deleting Old Transcripts

Deletion is dry-run by default:

```sh
go run ./cmd/fireflies-downloader delete-old --older-than 3m
```

This writes a plan file, defaulting to:

```text
fireflies_delete_plan.json
```

Actually delete only after reviewing the plan:

```sh
go run ./cmd/fireflies-downloader delete-old --older-than 3m --confirm
```

Retention values:

- `3m` or `3mo`: three calendar months.
- `90d`: ninety days.
- `12w`: twelve weeks.
- `1y`: one calendar year.
- Go durations such as `720h`.

Deletion has its own pacing:

```sh
go run ./cmd/fireflies-downloader delete-old --older-than 3m --delete-delay 10s
```

Use filters with deletion to reduce scope:

```sh
go run ./cmd/fireflies-downloader delete-old \
  --older-than 6m \
  --organizers alice@example.com \
  --plan-file alice_old_calls.json
```

## Limitations

- Fireflies API limits apply. Free/Pro accounts currently have a daily API cap; Business/Enterprise accounts have a per-minute cap. See the Fireflies limits docs linked above.
- Some fields are plan-gated or permission-gated. For example, Fireflies documents audio URL and meeting analytics availability as requiring Pro or higher in the transcript schema.
- Media URLs can expire. Fireflies documents `audio_url` as a secure hashed URL that expires after 24 hours. Re-run the export if you need fresh media URLs.
- Media downloads only happen when `--include-media` is set and Fireflies returns `audio_url` or `video_url`.
- The archive can only export transcripts visible to the API key.
- Fireflies may return different fields for live, processing, private, deleted, or restricted transcripts.
- CSV and SQLite are normalized convenience exports. Full fidelity remains in `transcripts/<id>.json` and the `transcripts.raw_json` SQLite column.
- The web UI and MCP endpoint are local tools. They are not multi-user applications and do not implement account-level authorization.
- MCP tools are read-only, but they can expose sensitive call data to any connected MCP client. Use a token if there is any chance the endpoint is reachable by other software.
- Sentiment and coaching insights are heuristic. They reflect Fireflies analytics plus simple local aggregation, not a definitive assessment of people, intent, or relationship health.
- `query_database` accepts only bounded `SELECT` / `WITH` statements, but complex queries can still be slow on large archives.
- This project is not affiliated with Fireflies.ai.

## Security and Privacy

The exported data may include customer names, emails, meeting links, transcript text, summaries, app prompts/responses, and media files. Treat the output directory as sensitive.

Recommended practices:

- Keep `fireflies_export/` out of git.
- Do not commit `.env`, `.env.*`, `.mise.local.toml`, SQLite files, JSON exports, CSV exports, or media files.
- Bind the server to `127.0.0.1` unless you have a specific reason not to.
- Set `FIREFLIES_MCP_TOKEN` before exposing `/mcp` to any non-local client.
- Be careful when connecting the MCP endpoint to hosted AI products. Tool responses can include transcript text and customer data.
- Rotate your Fireflies API key if it was ever committed or shared.

The server also enforces local-origin checks for `/mcp` to reduce DNS rebinding risk.

## Development

Run tests:

```sh
make test
```

Build:

```sh
make build
```

Run the CLI:

```sh
make run
```

Run the web UI against the default archive:

```sh
make serve
```

Tidy dependencies:

```sh
make tidy
```

Project layout:

```text
cmd/fireflies-downloader/     CLI entrypoint
internal/cli/                 Command parsing and command orchestration
internal/fireflies/           Fireflies GraphQL client and query profiles
internal/app/                 Download and delete workflows
internal/archive/             JSON, CSV, SQLite export logic
internal/web/                 Web UI, templates, static assets, MCP endpoint
```

## Troubleshooting

### `missing API key`

Set `FIREFLIES_API_KEY` or pass `--api-key`:

```sh
export FIREFLIES_API_KEY="your_api_key"
```

### Fireflies throttles the run

Slow the downloader down:

```sh
go run ./cmd/fireflies-downloader download --request-delay 2s --max-retries 12 --retry-max-wait 15m
```

If your plan has a daily cap, resume the next day with the same output directory.

### Some transcript fields are missing

The API key may not have access, the plan may not expose the field, or the transcript may not have finished processing. The downloader keeps the raw JSON it receives and records fallback warnings in `manifest.json`.

### Media did not download

Check all of the following:

- You passed `--include-media`.
- Fireflies returned `audio_url` or `video_url`.
- Your plan/permissions allow media URLs.
- The URL did not expire before download.

### Web UI cannot open the database

Make sure the download produced SQLite:

```sh
ls fireflies_export/fireflies.sqlite
```

If you downloaded with `--no-sqlite`, rerun without it.

### MCP client cannot connect

Check:

- `serve` is running.
- The client URL is `http://127.0.0.1:8080/mcp`.
- If `FIREFLIES_MCP_TOKEN` or `--mcp-token` is set, the client sends `Authorization: Bearer <token>`.
- The client supports Streamable HTTP MCP, or you are using a compatible bridge.

### Port already in use

Use another address:

```sh
go run ./cmd/fireflies-downloader serve --addr 127.0.0.1:8090
```

## License

No license file is included yet. Add a license before publishing this repository as open source.
