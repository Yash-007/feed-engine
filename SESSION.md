# SESSION.md — handoff context for feed-engine

Written 2026-08-30. Read this before touching the code; it records the things
that are not derivable from the source, and the traps that already cost hours.

## What this is

A read-only X (Twitter) idea harvester for Yash (backend engineer, CoinSwitch /
Lemonn / PeepalCo, Bangalore). It scrolls his X home feed in a real logged-in
Chrome profile, drops the posts not worth a model call, sends the keepers
through `claude -p`, and banks the resulting idea seeds in local SQLite.

It never posts, likes, follows, replies, bookmarks or DMs. There is no code path
that writes to X. The one outbound write it makes is to the Feed Runner backend,
and only when switched on: see "The push".

Credentials can now be configured (see "Credentials"), but the supported path is
still signing in by hand, once, into the persistent Chrome profile.

## Status: working end to end, verified against live X AND the bank

`POST /seeds` is no longer the open integration: harvested seeds reach the Feed
Runner backend, and from there the phone. See "The push" below.

Last verified numbers (2026-08-31, this Windows laptop):

```
scrape    50 posts, 4 shots, 25 scrolls, 2m48s
health    articles=202 misses=map[text:23 time:37]
prefilter kept 20 (16 text, 4 visual) - 21 promoted, 7 too short, 1 no text
claude    16 -> 5 seeds  |  4 visual -> 2 seeds
banked    7 inserted
pushed    7 sent, 0 duplicate, 0 failed
```

Every stage has been exercised on real data. `go build`, `go vet` and
`go test ./...` are all clean.

## The push

Seeds go to the backend after they are banked locally, never before: SQLite is
the source of truth, so a crash mid-push costs a retry rather than a seed.

- Off by default (`bank.enabled`), and startup fails if it is on with no token.
  This is the only outbound write in an otherwise read-only tool, and it writes
  into a live personal account.
- `posted_at` is the outbox. A run that finds the backend asleep leaves rows
  unstamped; the next run picks them up. `-stage push` drains the queue with no
  browser and no model calls.
- Delivery is at-least-once, made safe by `client_seed_id`: the server answers
  `duplicate: true` rather than storing a second copy. Verified by un-stamping a
  delivered row and re-pushing.
- A rejected token stops the run rather than failing once per seed.
- The token lives in `config.local.yaml` (gitignored) or
  `FEED_ENGINE_BANK_TOKEN`. Currently the shared Render API_TOKEN, which the
  backend resolves to OWNER_USERNAME — chosen over a phone session token because
  a session token dies when you sign out on the phone.

## The repost category

`repost` marks a post worth QUOTE-TWEETING rather than writing around. It is the
one exception to transform-never-copy, since the original stays visible beside
the line, so the prompts hold it to a higher bar: punch up or sideways only,
never a small account or someone's honest work, and prefer `take` when unsure.

A repost seed needs its permalink to be actionable, so `assemble` demotes one
without a URL to `take` rather than banking an action with no target. The
backend's ideation prompt has a matching QUOTE_POST play that writes the line
sitting above the quoted post rather than a standalone one.

`visual` now means the ORIGINAL CARRIES MEDIA, not that this engine
screenshotted it. A text post with a chart attached counts: what the reader
needs to know is that there is an image to go and look at before writing.

## Credentials

The X account is `<redacted>`, in `config.local.yaml` (gitignored). Plaintext on
disk, so rotate when convenient.

`login.auto_login` is false and should usually stay that way. Assisted login
fills the username and password and then waits for a human, because X
interleaves a phone/handle challenge or a 2FA code often enough that pretending
to fully automate it just fails confusingly. `-login` remains the supported
path: sign in by hand once, into the persistent profile.

## Scheduling

Registered on this laptop: 09:17 / 14:43 / 21:09 daily, Interactive logon.

One double-click each, in `scripts/`:

| File | Does |
|---|---|
| `feed-engine-enable.cmd` | preflight, then register the task |
| `feed-engine-disable.cmd` | unregister it; touches nothing else |
| `feed-engine-run-now.cmd` | one full harvest in this window, no jitter |
| `feed-engine-status.cmd` | preflight + last/next run + last 20 log lines |

`preflight.ps1` is what makes those safe to double-click: it checks the binary,
the driver, `claude` on PATH, Chrome, the profile, the bank token and the task
state, and refuses to enable while anything is missing. Those failures otherwise
surface as a silent no-op three hours later in `data/run.log`.

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

- **Nothing sends status back.** The app can mark a seed posted or skipped, and
  the engine never learns: `posted_at` tracks delivery to the backend, not what
  happened to the idea afterwards. The local bank therefore only grows.
- **No aging.** Related to the above: at roughly 20 seeds a day, the bank and
  the app's list both grow without bound. Marking seeds posted/skipped in the
  app is the current pressure valve, and it is manual.

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
