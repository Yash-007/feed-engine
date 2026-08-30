# feed-engine

Read-only X (Twitter) idea harvester. Scrolls an X List in a real logged-in
Chrome profile, pulls post text off the DOM, sends the keepers through
`claude -p`, and files the resulting idea seeds in local SQLite.

It never posts, likes, follows, replies, bookmarks, or DMs. There is no code
path that writes to X. Credentials are never touched by the script — you log
the alt account in by hand, once, into the persistent profile.

## Setup

```sh
go build -o bin/feed-engine ./cmd/feed-engine

# 1. put your List URL in config.yaml (or config.local.yaml)
# 2. log the alt account in, once:
bin/feed-engine -login
# 3. dry run: scrape only, nothing written to the DB
bin/feed-engine -stage scrape -no-jitter
# 4. full run
bin/feed-engine
```

## Layout

| Path | What |
|---|---|
| `cmd/feed-engine` | entrypoint, stage wiring, cron jitter |
| `internal/selectors` | **every X DOM selector**; fix drift here only |
| `internal/scraper` | persistent Chrome, scroll loop, DOM extraction, screenshots |
| `internal/prefilter` | word-count / no-text / already-seen drops |
| `internal/seen` | seen-post-id store, pruned by age |
| `internal/claudecli` | `claude -p` invocation, JSON guard, one retry |
| `internal/store` | SQLite `idea_seeds`, theme dedupe |
| `prompts/` | the filter-and-seed prompts, edit freely |
| `scripts/run.sh` | cron/launchd wrapper |

## Cron

`scripts/run.sh` adds its own random delay on top of `start_jitter_max_sec`,
so 2-3 runs a day never land at the same minute.

```cron
17 9  * * * /path/to/feed-engine/scripts/run.sh
43 14 * * * /path/to/feed-engine/scripts/run.sh
09 21 * * * /path/to/feed-engine/scripts/run.sh
```

Headed browser means the run needs a logged-in desktop session — on macOS
prefer launchd (or just leave the laptop unlocked) over plain cron.

## When X changes its DOM

The run log ends with a health line: `articles=N misses=map[...]` and, if a
field is empty on most posts, `SELECTOR LIKELY BROKEN: text,time`. Open
`internal/selectors/selectors.go`, fix the named field, bump `Version`.
