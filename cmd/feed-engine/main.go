// Command feed-engine harvests idea seeds from an X List: scroll the timeline
// in a real logged-in Chrome profile, drop the posts not worth a model call,
// send the keepers through `claude -p`, and bank what comes back in SQLite.
//
// It is read-only on X. Nothing here posts, likes, follows, replies, or DMs.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Yash-007/feed-engine/internal/bank"
	"github.com/Yash-007/feed-engine/internal/claudecli"
	"github.com/Yash-007/feed-engine/internal/config"
	"github.com/Yash-007/feed-engine/internal/logx"
	"github.com/Yash-007/feed-engine/internal/model"
	"github.com/Yash-007/feed-engine/internal/prefilter"
	"github.com/Yash-007/feed-engine/internal/scraper"
	"github.com/Yash-007/feed-engine/internal/seen"
	"github.com/Yash-007/feed-engine/internal/selectors"
	"github.com/Yash-007/feed-engine/internal/store"
)

// brokenRatio: a field empty on more than this share of posts is selector drift,
// not thin content.
const brokenRatio = 0.8

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "feed-engine: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	var (
		cfgPath   = flag.String("config", "config.yaml", "path to config.yaml")
		stage     = flag.String("stage", "all", "all | scrape | seed | push")
		doLogin   = flag.Bool("login", false, "open the browser so the alt account can be signed in by hand, then exit")
		noJitter  = flag.Bool("no-jitter", false, "skip the random start delay")
		debug     = flag.Bool("debug", false, "debug logging")
		listFlag  = flag.String("list", "", "scrape this list URL instead of the configured ones")
		loginWait = flag.Duration("login-timeout", 8*time.Minute,
			"how long -login waits for the sign-in to land")
	)
	flag.Parse()

	switch *stage {
	case "all", "scrape", "seed", "push":
	default:
		return fmt.Errorf("unknown -stage %q (want all, scrape, seed or push)", *stage)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}

	log, closer, err := logx.New(cfg.Paths.Log, *debug)
	if err != nil {
		return err
	}
	defer closer.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *doLogin {
		return scraper.Login(ctx, log, cfg, *loginWait)
	}

	log.Info("feed-engine starting", "stage", *stage, "selectors", selectors.Version, "pid", os.Getpid())

	// Drain the outbox and stop. No browser, no model calls: this is how you
	// retry a push that failed because the backend was asleep, without paying
	// to harvest the same feed again.
	if *stage == "push" {
		db, err := store.Open(cfg.Paths.DB)
		if err != nil {
			return err
		}
		defer db.Close()

		if !cfg.Bank.Enabled {
			pending, _ := db.PendingCount()
			return fmt.Errorf("bank.enabled is false, so there is nothing to push to "+
				"(%d seeds are waiting locally)", pending)
		}
		pushed := pushToBank(ctx, log, cfg, db)
		pending, _ := db.PendingCount()
		log.Info("push stage done", "pushed", pushed, "awaiting_push", pending)
		return nil
	}

	// A corrupt store comes back empty with an error attached — worth a warning,
	// not worth killing the run over.
	seenStore, err := seen.Load(cfg.Paths.SeenIDs)
	if seenStore == nil {
		return fmt.Errorf("seen store: %w", err)
	}
	if err != nil {
		log.Warn("seen store reset", "err", err)
	}
	filter := prefilter.New(cfg.Prefilter, seenStore)

	posts, err := collect(ctx, log, cfg, filter, *stage, *listFlag, *noJitter)
	if err != nil {
		return err
	}

	if *stage == "scrape" {
		log.Info("scrape stage done — nothing sent to claude, nothing banked",
			"posts", len(posts), "file", cfg.Paths.PostsJSON)
		return nil
	}
	if ctx.Err() != nil {
		log.Warn("interrupted before the model pass; posts are on disk, rerun with -stage seed",
			"posts", len(posts), "file", cfg.Paths.PostsJSON)
		return nil
	}

	textPosts, visualPosts, st := filter.Apply(posts)
	log.Info("prefilter",
		"in", st.In, "kept", st.Kept, "text", st.KeptText, "visual", st.KeptShots,
		"own", st.Own, "promoted", st.Promoted, "too_short", st.TooShort,
		"no_text", st.NoText, "already_seen", st.Seen)

	cc := claudecli.New(cfg.Claude, log)
	var responses []model.SeedResponse
	if r, err := cc.SeedText(ctx, textPosts); err != nil {
		log.Error("text pass failed", "err", err)
	} else {
		responses = append(responses, r...)
	}
	if r, err := cc.SeedVisual(ctx, visualPosts); err != nil {
		log.Error("visual pass failed", "err", err)
	} else {
		responses = append(responses, r...)
	}

	sent := append(append([]model.Post{}, textPosts...), visualPosts...)
	seeds, report := assemble(responses, sent)
	if report.Unknown > 0 {
		log.Warn("dropped seeds pointing at post ids we never sent", "n", report.Unknown)
	}
	if len(report.BadCategory) > 0 {
		log.Warn("categories outside the prompt's vocabulary, blanked",
			"values", report.BadCategory)
	}
	if report.Demoted > 0 {
		log.Warn("repost seeds without a permalink, banked as takes instead",
			"n", report.Demoted)
	}

	seeds, capped := capCategories(seeds, cfg.Dedupe.CategoryCap)
	if len(capped) > 0 {
		log.Info("category cap applied across the run", "cap", cfg.Dedupe.CategoryCap, "dropped", capped)
	}

	db, err := store.Open(cfg.Paths.DB)
	if err != nil {
		return err
	}
	defer db.Close()

	ins, err := db.Insert(seeds, cfg.Dedupe.RecentSeeds, cfg.Dedupe.ThemeOverlap)
	if err != nil {
		return fmt.Errorf("bank seeds: %w", err)
	}
	log.Info("banked", "inserted", ins.Inserted, "dup_theme", ins.DupTheme, "dup_post", ins.DupPost)

	// Every post we evaluated is spent now — keepers and rejects alike. Rejects
	// especially: re-reading them next run just burns the same prefilter rules.
	ids := make([]string, 0, len(posts))
	for _, p := range posts {
		ids = append(ids, p.ID)
	}
	seenStore.Add(ids...)
	if n := seenStore.Prune(cfg.Prefilter.SeenRetentionDays); n > 0 {
		log.Info("pruned seen ids", "dropped", n)
	}
	if err := seenStore.Save(); err != nil {
		log.Error("could not save seen store", "err", err)
	}

	pushed := pushToBank(ctx, log, cfg, db)

	bankTotal, _ := db.Count("new")
	pending, _ := db.PendingCount()
	log.Info("run complete",
		"scraped", len(posts), "sent", len(sent), "seeds_new", ins.Inserted,
		"bank_new_total", bankTotal, "pushed", pushed, "awaiting_push", pending,
		"seen_ids", seenStore.Len())
	return nil
}

/*
pushToBank drains the outbox to the Feed Runner backend.

Runs after banking, never before: SQLite is the source of truth and a seed is
only pushed once it is safely on disk, so a crash mid-push costs a retry rather
than a seed. Every failure here is non-fatal to the run — the rows stay
unstamped and the next run picks them up.
*/
func pushToBank(ctx context.Context, log *slog.Logger, cfg *config.Config, db *store.DB) int {
	if !cfg.Bank.Enabled {
		if n, _ := db.PendingCount(); n > 0 {
			log.Info("bank push is off; seeds are banked locally only", "awaiting_push", n)
		}
		return 0
	}

	queued, err := db.Unposted(cfg.Bank.MaxPerRun)
	if err != nil {
		log.Error("could not read the push queue", "err", err)
		return 0
	}
	if len(queued) == 0 {
		return 0
	}

	client := bank.New(cfg.Bank, log)
	// One cheap probe first. The free tier sleeps, and finding that out via
	// fifty timed-out POSTs takes fifty minutes instead of one request.
	if err := client.Health(ctx); err != nil {
		log.Warn("backend is not answering, leaving seeds queued for the next run",
			"queued", len(queued), "err", err)
		return 0
	}

	res := client.Push(ctx, queued)
	if err := db.MarkPosted(res.Delivered); err != nil {
		// The seeds are on the server; failing to stamp them locally only means
		// the next run re-pushes and the backend answers duplicate.
		log.Error("pushed seeds but could not stamp them locally", "err", err)
	}
	log.Info("pushed to the bank",
		"sent", res.Sent, "duplicate", res.Duplicate, "failed", res.Failed)
	if res.Fatal != nil {
		log.Error("push stopped early", "err", res.Fatal)
	}
	return res.Sent
}

// collect either scrapes the configured lists or, for -stage seed, reads back
// the posts an earlier scrape left on disk.
func collect(ctx context.Context, log *slog.Logger, cfg *config.Config, filter *prefilter.Filter, stage, listFlag string, noJitter bool) ([]model.Post, error) {
	if stage == "seed" {
		posts, err := loadPosts(cfg.Paths.PostsJSON)
		if err != nil {
			return nil, err
		}
		log.Info("reusing posts from disk", "file", cfg.Paths.PostsJSON, "posts", len(posts))
		return posts, nil
	}

	if !noJitter {
		startJitter(ctx, log, cfg.Session.StartJitterMaxSec)
	}

	targets, err := pickLists(cfg, listFlag)
	if err != nil {
		return nil, err
	}

	posts, err := scrapeLists(ctx, log, cfg, filter.WantShot, targets)
	// Whatever came back is worth keeping even if the run died partway.
	if werr := savePosts(cfg.Paths.PostsJSON, posts); werr != nil {
		log.Error("could not write posts file", "err", werr)
	}
	return posts, err
}

func scrapeLists(ctx context.Context, log *slog.Logger, cfg *config.Config, want scraper.WantShot, targets []string) ([]model.Post, error) {
	var all []model.Post

	for i, u := range targets {
		log.Info("scraping list", "url", u, "n", i+1, "of", len(targets))
		res, err := scraper.Run(ctx, log, cfg, u, want)
		if res != nil {
			all = append(all, res.Posts...)
			log.Info("list done",
				"posts", len(res.Posts), "shots", res.Shots, "scrolls", res.Scrolls,
				"took", res.Duration.Round(time.Second), "stopped", res.StopWhy)
			logHealth(log, res.Health)
		}
		if err != nil {
			return dedupe(all), fmt.Errorf("scrape %s: %w", u, err)
		}
		if ctx.Err() != nil {
			break
		}
	}
	return dedupe(all), nil
}

// pickLists honours rotate_lists: one timeline at random per run, or every one
// in order. The session caps in config apply per timeline, not per run.
func pickLists(cfg *config.Config, override string) ([]string, error) {
	switch {
	case override != "":
		// The config file's URLs are checked at load; -list has to clear the
		// same bar, or a typo opens a browser onto a page that never scrolls.
		if err := config.CheckTimelineURL(override); err != nil {
			return nil, fmt.Errorf("-list: %w", err)
		}
		return []string{override}, nil
	case cfg.RotateLists:
		return []string{cfg.ListURLs[rand.Intn(len(cfg.ListURLs))]}, nil
	default:
		return cfg.ListURLs, nil
	}
}

// dedupe drops posts seen twice inside one run — the same post can sit in two
// lists, and the scraper only dedupes within a single list.
func dedupe(in []model.Post) []model.Post {
	seenID := make(map[string]bool, len(in))
	out := make([]model.Post, 0, len(in))
	for _, p := range in {
		if p.ID == "" || seenID[p.ID] {
			continue
		}
		seenID[p.ID] = true
		out = append(out, p)
	}
	return out
}

// assembled reports what had to be corrected on the way in, so a drifting
// prompt shows up in the run log instead of quietly reshaping the bank.
type assembled struct {
	Unknown     int      // replies naming a post we never sent
	BadCategory []string // categories outside the prompt's vocabulary
	Demoted     int      // "repost" seeds with no permalink, downgraded to a take
}

// assemble turns Claude's replies back into bankable seeds, pairing each one
// with the post it came from. A reply naming a post we never sent is dropped:
// the model invented the id, and a seed with no source is worthless.
func assemble(resp []model.SeedResponse, sent []model.Post) ([]model.IdeaSeed, assembled) {
	byID := make(map[string]model.Post, len(sent))
	for _, p := range sent {
		byID[p.ID] = p
	}

	now := time.Now().UTC()
	seeds := make([]model.IdeaSeed, 0, len(resp))
	var report assembled

	for _, r := range resp {
		// client_seed_id is "harvest-<post id>"; strip the prefix to find the
		// post. A reply naming a post we never sent is dropped.
		postID := strings.TrimPrefix(strings.TrimSpace(r.ClientSeedID), model.SeedIDPrefix)
		p, ok := byID[postID]
		if !ok {
			report.Unknown++
			continue
		}
		if strings.TrimSpace(r.Tension) == "" && strings.TrimSpace(r.AngleHint) == "" {
			continue // nothing usable came back for this one
		}

		category, known := model.NormalizeCategory(r.Category)
		if !known && strings.TrimSpace(r.Category) != "" {
			report.BadCategory = append(report.BadCategory, r.Category)
		}
		// A quote post needs something to quote. If the permalink never made it
		// off the DOM the seed is still worth banking, just not as a repost:
		// left alone it would reach the app as an action with no target.
		if category == model.CategoryRepost && p.URL == "" {
			category = "take"
			report.Demoted++
		}

		// The model is told to echo these, but the engine owns them: it knows the
		// real handle, url and timestamp, and must not bank an invented one.
		author := strings.TrimSpace(r.PostAuthor)
		if author == "" {
			author = "@" + p.Author
		}
		text := strings.TrimSpace(r.PostText)
		if text == "" {
			text = p.Text
		}

		seeds = append(seeds, model.IdeaSeed{
			ID:               model.SeedIDPrefix + p.ID,
			CreatedAt:        now,
			CapturedAtMillis: capturedMillis(p),
			Source:           "harvest",
			Platform:         "x",
			Category:         category,
			ThemeTags:        leadWithCategory(r.ThemeTags, category, r.Category),
			Tension:          scrub(r.Tension),
			AngleHint:        scrub(r.AngleHint),
			ShelfLife:        r.ShelfLife,
			PostAuthor:       author,
			// The post text is someone else's words, quoted. Scrubbed anyway:
			// it is fed to the ideation model as an example of how the idea was
			// said, and an em dash in the example comes back in the output.
			PostText:      truncate(scrub(text), 300),
			SourcePostURL: p.URL,
			SourcePostID:  p.ID,
			// Media, not "we screenshotted it": what the reader needs to know
			// is that there is an image to go and look at before writing.
			Visual: p.HasMedia,
			Status: "new",
		})
	}
	return seeds, report
}

/*
leadWithCategory keeps the first theme tag equal to the final category.

The prompt is told to lead with it and the app filters on theme tags, so a
category the engine corrected has to be corrected in the tags too, or the two
disagree and a chip filters on a word the seed no longer claims.

claimed is what the model said before correction. It is dropped separately from
the known-category list because an invented category is not in that list: left
alone, a rejected "hot_take" survives as an ordinary topic tag and becomes
exactly the phantom filter chip that rejecting it was supposed to prevent.
*/
func leadWithCategory(tags []string, category, claimed string) []string {
	rest := make([]string, 0, len(tags)+1)
	for _, t := range tags {
		trimmed := strings.TrimSpace(t)
		switch {
		case trimmed == "":
		case strings.EqualFold(trimmed, category):
		case strings.EqualFold(trimmed, strings.TrimSpace(claimed)):
		case isCategoryWord(trimmed):
		default:
			rest = append(rest, trimmed)
		}
	}
	if category == "" {
		return rest
	}
	return append([]string{category}, rest...)
}

// isCategoryWord spots a stale category left in the tag list after a
// correction, so it does not survive as an ordinary topic tag.
func isCategoryWord(tag string) bool {
	_, known := model.NormalizeCategory(tag)
	return known
}

// capCategories enforces the per-category cap across the whole run. The prompt
// caps categories per model call, but a run makes several calls — text batches
// plus vision batches — so without this the variety rule holds inside each batch
// and quietly fails across the run: six text batches could bank 24 "take" seeds
// and nothing else. Keeps the first n of each category, in the order returned.
func capCategories(seeds []model.IdeaSeed, n int) ([]model.IdeaSeed, map[string]int) {
	dropped := map[string]int{}
	if n <= 0 {
		return seeds, dropped
	}
	count := map[string]int{}
	out := make([]model.IdeaSeed, 0, len(seeds))
	for _, s := range seeds {
		if count[s.Category] >= n {
			dropped[s.Category]++
			continue
		}
		count[s.Category]++
		out = append(out, s)
	}
	return out, dropped
}

// capturedMillis is the post's own timestamp in unix millis. The model has no
// reliable clock, so the engine fills this in. Falls back to scrape time.
func capturedMillis(p model.Post) int64 {
	if t, err := time.Parse(time.RFC3339, p.Timestamp); err == nil {
		return t.UnixMilli()
	}
	return p.ScrapedAt.UnixMilli()
}

/*
scrub strips the em dashes out of a seed's own prose.

Every prompt in this project bans them as the single clearest AI tell, and the
backend already scrubs the posts it generates. Seed text was the gap: it comes
back from `claude -p` here, never passes through the backend's scrubber, and
then does two things that matter. It is shown in the app, where a stray em dash
in an angle hint is visible next to drafts that carefully have none. And it is
fed to the ideation model as raw material, where the surest way to get an em
dash back out is to put one in.

Spaced double hyphens go too: once em dashes are banned the model reaches for
those instead, which is the same tell in a different costume. Unspaced ones
survive, so a command line flag in a technical tension is left alone.
*/
func scrub(s string) string {
	replaced := emDashes.Replace(strings.TrimSpace(s))
	return doubleComma.ReplaceAllString(replaced, ",")
}

var (
	emDashes = strings.NewReplacer(
		" -- ", ", ",
		" — ", ", ",
		"— ", ", ",
		" —", ",",
		"—", ",",
	)
	// Left over when an em dash sat next to a comma already: "a, , b".
	doubleComma = regexp.MustCompile(`,\s*,`)
)

// truncate cuts to n runes, not bytes, so a multi-byte character can't be split
// in half and land in the bank as a replacement glyph.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n]))
}

func logHealth(log *slog.Logger, h *selectors.Health) {
	if h == nil {
		return
	}
	log.Info("selector health", "version", selectors.Version, "articles", h.Articles, "misses", h.Miss)
	if broken := h.Broken(brokenRatio); len(broken) > 0 {
		sort.Strings(broken)
		log.Error("SELECTOR LIKELY BROKEN: " + strings.Join(broken, ",") +
			" — fix internal/selectors/selectors.go and bump Version")
	}
}

func startJitter(ctx context.Context, log *slog.Logger, maxSec int) {
	if maxSec <= 0 {
		return
	}
	d := time.Duration(rand.Intn(maxSec+1)) * time.Second
	log.Info("start jitter", "sleeping", d.Round(time.Second))
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func savePosts(path string, posts []model.Post) error {
	b, err := json.MarshalIndent(posts, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadPosts(path string) ([]model.Post, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s (run -stage scrape first): %w", path, err)
	}
	var posts []model.Post
	if err := json.Unmarshal(b, &posts); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return posts, nil
}
