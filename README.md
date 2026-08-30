# feed-engine

Read-only X (Twitter) idea harvester. Scrolls your home feed or an X List in a
real logged-in Chrome profile, pulls post text off the DOM, sends the keepers
through `claude -p`, and files the resulting idea seeds in local SQLite.

It never posts, likes, follows, replies, bookmarks, or DMs. There is no code
path that writes to X. The one outbound write it makes is pushing the seeds it
finds to the Feed Runner backend, which is off unless you switch it on.

Credentials can be configured so the sign-in form gets filled for you, but the
supported path is still logging the alt account in by hand, once, into the
persistent profile.

## Setup

Needs Go 1.25+, Google Chrome, Node.js, and the `claude` CLI on PATH.

```powershell
go build -o bin\feed-engine.exe .\cmd\feed-engine

# once per machine: playwright-go cannot fetch its own driver any more
.\scripts\install-driver.ps1

# 1. log the alt account in, once:
.\bin\feed-engine.exe -login
# 2. dry run: scrape only, nothing sent to claude and nothing banked
.\bin\feed-engine.exe -stage scrape -no-jitter
# 3. full run
.\bin\feed-engine.exe
```

On macOS or Linux the build is `go build -o bin/feed-engine ./cmd/feed-engine`
and the rest is identical.

`list_urls` in `config.yaml` defaults to `https://x.com/home`. It also takes X
List URLs — a List is quieter and higher signal, so switch once you have one
built. Profiles, searches, and single posts are rejected at startup: they are
not endless timelines and would scroll to nothing.

The home feed is algorithmic and carries ads. Promoted posts are detected and
dropped in the prefilter, before any model call, and never get screenshotted.
That detection leans on X's ad container markup, which drifts — if ad copy ever
shows up in the bank, `Promoted` in `internal/selectors/selectors.go` is what
needs fixing.

## Stages

| `-stage` | Does |
|---|---|
| `all` (default) | scrape → prefilter → claude → SQLite → push to the bank |
| `scrape` | scrape and write `data/posts.json`, nothing else. Costs no model calls |
| `seed` | skip the browser, re-read `data/posts.json`, run the model pass |
| `push` | only drain the outbox to the backend. No browser, no model calls |

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
| `scripts/install-driver.ps1` | installs the Playwright driver npm-side |
| `scripts/run.ps1` | Task Scheduler wrapper (Windows) |
| `scripts/install-task.ps1` | registers/removes the scheduled task |
| `scripts/run.sh` | cron/launchd wrapper (macOS/Linux) |

## Pushing seeds to the app

Harvested seeds only reach your phone if the push is on. It is off by default:
this is the one outbound write here, and it writes into a live account.

Put the two secrets in `config.local.yaml`, which is gitignored, or in the
environment, which leaves nothing on disk at all:

```yaml
bank:
  enabled: true
  token: "..."      # or export FEED_ENGINE_BANK_TOKEN
```

Then `-stage push` drains whatever is queued, with no browser and no model
calls, which is also how you retry after the backend was asleep. A seed is only
stamped as delivered once the server confirms it, and re-sending one it already
has is safe: the id makes it answer `duplicate` rather than storing a copy.

## Scheduling

Four double-clickable files in `scripts/`, if you would rather not use a shell:

| File | Does |
|---|---|
| `feed-engine-enable.cmd` | check prerequisites, then register the scheduled task |
| `feed-engine-disable.cmd` | unregister it; nothing else is touched |
| `feed-engine-run-now.cmd` | one full harvest right now, in this window |
| `feed-engine-status.cmd` | what is set up, when it last ran, last 20 log lines |

`scripts/preflight.ps1` backs those up. It checks the binary, the Playwright
driver, `claude` on PATH, Chrome, the profile and the bank token, and refuses
to enable while anything is missing — those all otherwise fail as a silent
no-op hours later in `data/run.log`.

Both cron wrappers add their own random delay on top of `start_jitter_max_sec`
and hold a PID lock, so two runs a day never land on the same minute and a long
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

## Playwright driver

`playwright-go` is pinned at v0.5200.1 — do not bump it to v0.6201.x, whose
module path still declares `github.com/mxschmitt/playwright-go` and breaks
`go mod tidy`.

That pinned version downloads its driver from `playwright.azureedge.net` and two
sibling mirrors, all of which were retired and now 404. Its built-in auto-install
therefore always fails, with a misleading "please install the driver first".
`scripts/install-driver.ps1` builds the driver directory from npm instead, which
is the same content by a route that still works. Run it once per machine, and
again if you ever clear `%LOCALAPPDATA%\ms-playwright-go`.

## When X changes its DOM

The run log ends with a health line: `articles=N misses=map[...]` and, if a
field is empty on most posts, `SELECTOR LIKELY BROKEN: text,time`. Open
`internal/selectors/selectors.go`, fix the named field, bump `Version`.
