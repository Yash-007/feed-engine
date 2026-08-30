# feed-engine

Read-only X (Twitter) idea harvester. Scrolls an X List in a real logged-in
Chrome profile, pulls post text off the DOM, sends the keepers through
`claude -p`, and files the resulting idea seeds in local SQLite.

It never posts, likes, follows, replies, bookmarks, or DMs. There is no code
path that writes to X. Credentials are never touched by the script — you log
the alt account in by hand, once, into the persistent profile.

## Setup

Needs Go 1.25+, Google Chrome, and the `claude` CLI on PATH.

```powershell
go build -o bin\feed-engine.exe .\cmd\feed-engine

# 1. put your List URL in config.yaml (or config.local.yaml)
# 2. log the alt account in, once:
.\bin\feed-engine.exe -login
# 3. dry run: scrape only, nothing sent to claude and nothing banked
.\bin\feed-engine.exe -stage scrape -no-jitter
# 4. full run
.\bin\feed-engine.exe
```

On macOS or Linux the build is `go build -o bin/feed-engine ./cmd/feed-engine`
and the rest is identical.

The List URL has to go in before `-login`: a placeholder or non-list URL is a
hard startup error, and it fires before the browser opens.

## Stages

| `-stage` | Does |
|---|---|
| `all` (default) | scrape → prefilter → claude → SQLite |
| `scrape` | scrape and write `data/posts.json`, nothing else. Costs no model calls |
| `seed` | skip the browser, re-read `data/posts.json`, run the model pass |

`seed` is the loop for editing prompts: scrape once, then re-seed as often as
you like. Other flags: `-config`, `-list <url>` to override the configured
lists, `-no-jitter` to skip the random start delay, `-debug`.

## Layout

| Path | What |
|---|---|
| `cmd/feed-engine` | entrypoint, stage wiring, start jitter, seed assembly |
| `internal/selectors` | **every X DOM selector**; fix drift here only |
| `internal/scraper` | persistent Chrome, scroll loop, DOM extraction, screenshots |
| `internal/prefilter` | word-count / no-text / already-seen drops |
| `internal/seen` | seen-post-id store, pruned by age |
| `internal/claudecli` | `claude -p` invocation, JSON guard, one retry |
| `internal/store` | SQLite `idea_seeds`, theme dedupe |
| `prompts/` | the filter-and-seed prompts, edit freely |
| `scripts/run.ps1` | Task Scheduler wrapper (Windows) |
| `scripts/install-task.ps1` | registers/removes the scheduled task |
| `scripts/run.sh` | cron/launchd wrapper (macOS/Linux) |

## Scheduling

Both wrappers add their own random delay on top of `start_jitter_max_sec` and
hold a PID lock, so two runs a day never land on the same minute and a long
scroll session never overlaps the next tick.

Windows, once a manual run has worked:

```powershell
.\scripts\install-task.ps1                      # 09:17, 14:43, 21:09 daily
.\scripts\install-task.ps1 -Times 08:12,19:37   # or pick your own
Start-ScheduledTask -TaskName feed-engine       # fire one now
```

The task is registered `Interactive`, so it only runs while you are logged in.
That is deliberate — the run drives a visible Chrome window and cannot work on
the lock screen or from a service account. Leave the laptop awake and unlocked
at those times, or expect `StartWhenAvailable` to pick the run up late.

macOS/Linux use `scripts/run.sh` from cron, or launchd on macOS:

```cron
17 9  * * * /path/to/feed-engine/scripts/run.sh
43 14 * * * /path/to/feed-engine/scripts/run.sh
09 21 * * * /path/to/feed-engine/scripts/run.sh
```

## When X changes its DOM

The run log ends with a health line: `articles=N misses=map[...]` and, if a
field is empty on most posts, `SELECTOR LIKELY BROKEN: text,time`. Open
`internal/selectors/selectors.go`, fix the named field, bump `Version`.
