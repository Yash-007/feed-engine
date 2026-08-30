# SESSION.md — handoff context for feed-engine

Written 2026-08-30. Read this before touching the code; it records the things
that are not derivable from the source, and the traps that already cost hours.

## What this is

A read-only X (Twitter) idea harvester for Yash (backend engineer, CoinSwitch /
Lemonn / PeepalCo, Bangalore). It scrolls his X home feed in a real logged-in
Chrome profile, drops the posts not worth a model call, sends the keepers
through `claude -p`, and banks the resulting idea seeds in local SQLite.

It never posts, likes, follows, replies, bookmarks or DMs. There is no code path
that writes to X. Credentials are never touched by the code — the alt account is
signed in by hand, once, into a persistent Chrome profile.

## Status: working end to end, verified against live X

A full `-stage all` run has been executed successfully against the real feed.
Last verified numbers (50-post run):

```
scrape    50 posts, 3 shots, 20 scrolls, 1m40s
health    articles=209 misses=map[text:16 time:43]
prefilter kept 25 (22 text, 3 visual) - 19 promoted, 6 too short
claude    22 -> 6 seeds  |  3 visual -> 1 seed
banked    7 inserted
```

Every stage has been exercised on real data. `go build`, `go vet` and
`go test ./...` are all clean.

## Layout

| Path | What |
|---|---|
| `cmd/feed-engine` | entrypoint, stage wiring, start jitter, seed assembly, per-run category cap |
| `cmd/seeds-export` | dumps the bank as JSON in the wire format |
| `internal/selectors` | **every X DOM selector**; fix drift here only |
| `internal/scraper` | persistent Chrome, scroll loop, DOM extraction, screenshots |
| `internal/prefilter` | own-post / ad / word-count / already-seen drops |
| `internal/seen` | seen-post-id store, pruned by age |
| `internal/claudecli` | `claude -p` invocation, JSON salvage, one retry |
| `internal/store` | SQLite `idea_seeds`, theme dedupe, schema migrations |
| `prompts/` | the filter-and-seed prompts (text + vision) |
| `scripts/` | driver install, Task Scheduler wrapper + installer |

## Environment (Windows 11, this laptop)

These are non-obvious and will waste your time if you rediscover them:

- **Go is at `%LOCALAPPDATA%\Programs\go`**, NOT `C:\Program Files\Go`. The MSI
  needed UAC and the prompt was not clickable, so Go 1.27.0 was installed from
  the official zip and added to the *user* PATH. A shell started before that
  edit will not find `go` — prepend `$env:LOCALAPPDATA\Programs\go\bin`.
- **The Playwright driver cannot self-install.** All three CDN mirrors baked
  into playwright-go v0.5200.1 (`playwright.azureedge.net` and two siblings)
  were retired and 404. `PLAYWRIGHT_DOWNLOAD_HOST` is not a fix either — the
  current Microsoft hosts 400 on that path. Run `scripts/install-driver.ps1`,
  which builds the driver directory from npm (`playwright-core` + `node.exe`).
  Needs Node on PATH. Re-run if `%LOCALAPPDATA%\ms-playwright-go` is cleared.
- **Do NOT bump playwright-go.** v0.6201.x still declares the old
  `github.com/mxschmitt/playwright-go` module path and breaks `go mod tidy`.
- **Keep `.ps1` files pure ASCII.** Windows PowerShell 5.1 reads UTF-8-without-
  BOM as cp1252, where an em dash's trailing byte becomes a curly closing quote
  that PowerShell honours as a string delimiter. The script then dies with
  "The string is missing the terminator" pointing at an unrelated line. Check
  with `grep -nP '[^\x00-\x7F]' scripts/*.ps1`.
- **Never round-trip Go files through PowerShell `Get-Content`/`Set-Content`.**
  It reads UTF-8 as ANSI and will corrupt every em dash into `â€"`. Use the
  editor tools. (This already happened once to `cmd/feed-engine/main.go`.)
- `core.autocrlf` is on, so `gofmt -l` lists every file regardless. It is not a
  formatting problem. Only trust `gofmt -w` on files you actually edited, and
  check `git diff --ignore-cr-at-eol` before concluding anything changed.
- Chrome is at `C:\Program Files\Google\Chrome\Application\chrome.exe`; config
  uses `channel: chrome`, so no Playwright-managed browser is downloaded.

## Traps in the runtime behaviour

- **A failed browser launch orphans Chrome**, which then holds the profile lock
  and makes every subsequent launch fail identically with "target closed". If
  launches start failing, kill the strays first:
  ```powershell
  Get-CimInstance Win32_Process | Where-Object { $_.Name -eq 'chrome.exe' -and $_.CommandLine -like "*\.feed-engine\*" } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force }
  ```
  Filter on the profile path — do not kill the user's personal Chrome.
- **Never force-kill a run mid-session.** That is what orphans the processes.
- **X renders client-side.** Any login-state check made right after
  `domcontentloaded` reads an empty DOM and always answers "logged in". The
  scraper waits for the timeline OR the sign-in form to appear first
  (`session.settle`), then decides. Do not reorder that.
- **The timeline is virtualized.** Rows mount *below* the fold, so a post is
  almost never visible at the moment it first enters the DOM. Screenshots are
  queued in `pending` and taken on a later pass once scrolling brings the post
  into view. Removing that queue silently drops the entire vision pipeline to
  zero shots — that bug shipped and was only caught on live data.
- **`Screenshot()` waits for the element to hold still**, and a live feed never
  does. `Animations: disabled` is required or ~half the captures time out.
- **The viewport must fit the physical screen** because the run is headed. This
  laptop is 1920x1080 at 125% scaling = 1536x864 logical, 816 usable. Chrome's
  chrome eats ~110px above the viewport, hence `viewport_height: 700`.

## The seed contract

The prompts in `prompts/` were supplied by Yash and are the source of truth for
the output format. They emit the idea bank's **wire format** directly:

```json
{
  "client_seed_id": "harvest-<post id>",
  "source": "harvest",
  "platform": "x",
  "post_author": "@handle",
  "post_text": "verbatim, <=300 chars",
  "category": "take | shitpost | banter | war_story | thought | trend",
  "theme_tags": ["<category>", "2-4 topic tags"],
  "tension": "the generalized pattern, useful without the original post",
  "angle_hint": "Yash's insider entry point, distinct from the tension",
  "shelf_life": "evergreen | timely"
}
```

Engine-side rules that back the prompt up (do not remove these):

- `client_seed_id` is the SQLite primary key, so re-harvesting a post collides
  instead of duplicating. Idempotency is structural, not prompt-dependent.
- `captured_at_millis` is filled by the engine from the post's timestamp — the
  model has no reliable clock.
- `post_author` / `post_text` from the model are **fallbacks only**; the engine
  prefers what it read off the DOM, and truncates to 300 **runes** (not bytes,
  which would split a multi-byte character).
- `category_cap` (config, default 4) enforces the per-category cap **across the
  whole run**. The prompt caps each model call, but a run makes several (text
  batches + vision), so without this the spread silently fails at scale.
- The prefilter drops posts by `own_handle` before any model call.

## Not built

- **`POST /seeds` to the Feed Runner backend.** The prompt's "FOR THE ENGINE"
  section describes posting each seed to a backend with a Bearer token. No base
  URL or credential was supplied, and it is an outbound write to a live account,
  so it was deliberately not implemented. Rows are stored in the wire format and
  are POST-ready. This is the main open integration.
- Nothing consumes the bank. No seed ever leaves `status = 'new'`.

## Open, low-priority

- `is_repost` came back 0/150 and 0/50. Plausible on an algorithmic feed, but
  `socialContext` may be stale. Nothing filters on it.
- ~20% of posts have no timestamp; ads explain only a third of those. Well under
  the 80% drift alarm, and timestamps are context only.
- One screenshot per run still times out occasionally (row unmounts mid-capture).
  Degrades to text-only.

## Housekeeping

- `data/` and `*.db` are gitignored — the bank is local to this laptop only and
  will not survive a fresh clone. Back it up if it matters.
- `data/ideas.db.oldschema` holds 8 seeds from the previous (pre-wire-format)
  category/shelf_life vocabularies. Not migrated on purpose.
- **The GitHub PAT is plaintext in `.git/config`.** Rotate it and move to a
  credential helper.
- Commits are small and incremental, lowercase one-line subjects, no bullet
  bodies, `Co-Authored-By: Claude` trailer kept. No backdated commit dates.
- Scheduling is ready but **not installed**: `scripts/install-task.ps1`
  registers 09:17 / 14:43 / 21:09 daily, `Interactive` logon type because
  headed Chrome needs an unlocked desktop session.

## Run it

```powershell
$env:Path = "$env:LOCALAPPDATA\Programs\go\bin;$env:Path"
go build -o bin\feed-engine.exe .\cmd\feed-engine

.\scripts\install-driver.ps1                      # once per machine
.\bin\feed-engine.exe -login                      # once; polls until signed in
.\bin\feed-engine.exe -stage scrape -no-jitter    # no model calls, checks selectors
.\bin\feed-engine.exe -stage seed                 # no browser, replays posts.json
.\bin\feed-engine.exe                             # full run
go run .\cmd\seeds-export .\data\ideas.db .\data\seeds.json
```

`-stage seed` is the cheap loop for editing prompts: scrape once, then re-seed
as often as you like without touching the browser.

## When X changes its DOM

The run log ends with `articles=N misses=map[...]`. If a field is empty on more
than 80% of posts you get `SELECTOR LIKELY BROKEN: text,time`. Fix
`internal/selectors/selectors.go` — the only place selectors live — and bump
`Version`. Note the health check cannot see ad-detection drift: a feed with no
ads and a broken `Promoted` selector look identical from there.
