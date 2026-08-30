// Command feed-engine harvests idea seeds from an X List: scroll the timeline
// in a real logged-in Chrome profile, drop the posts not worth a model call,
// send the keepers through `claude -p`, and bank what comes back in SQLite.
//
// It is read-only on X. Nothing here posts, likes, follows, replies, or DMs.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

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
		cfgPath  = flag.String("config", "config.yaml", "path to config.yaml")
		stage    = flag.String("stage", "all", "all | scrape | seed")
		doLogin  = flag.Bool("login", false, "open the browser so the alt account can be signed in by hand, then exit")
		noJitter = flag.Bool("no-jitter", false, "skip the random start delay")
		debug    = flag.Bool("debug", false, "debug logging")
		listFlag = flag.String("list", "", "scrape this list URL instead of the configured ones")
	)
	flag.Parse()

	switch *stage {
	case "all", "scrape", "seed":
	default:
		return fmt.Errorf("unknown -stage %q (want all, scrape or seed)", *stage)
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

	if *doLogin {
		return scraper.Login(log, cfg, waitForEnter)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("feed-engine starting", "stage", *stage, "selectors", selectors.Version, "pid", os.Getpid())

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
		"too_short", st.TooShort, "no_text", st.NoText, "already_seen", st.Seen)

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
	seeds, unknown := assemble(responses, sent)
	if unknown > 0 {
		log.Warn("dropped seeds pointing at post ids we never sent", "n", unknown)
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

	bank, _ := db.Count("new")
	log.Info("run complete",
		"scraped", len(posts), "sent", len(sent), "seeds_new", ins.Inserted,
		"bank_new_total", bank, "seen_ids", seenStore.Len())
	return nil
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

	posts, err := scrapeLists(ctx, log, cfg, filter.WantShot, listFlag)
	// Whatever came back is worth keeping even if the run died partway.
	if werr := savePosts(cfg.Paths.PostsJSON, posts); werr != nil {
		log.Error("could not write posts file", "err", werr)
	}
	return posts, err
}

func scrapeLists(ctx context.Context, log *slog.Logger, cfg *config.Config, want scraper.WantShot, override string) ([]model.Post, error) {
	targets := pickLists(cfg, override)
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

// pickLists honours rotate_lists: one list at random per run, or every list in
// order. The session caps in config apply per list, not per run.
func pickLists(cfg *config.Config, override string) []string {
	switch {
	case override != "":
		return []string{override}
	case cfg.RotateLists:
		return []string{cfg.ListURLs[rand.Intn(len(cfg.ListURLs))]}
	default:
		return cfg.ListURLs
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

// assemble turns Claude's replies back into bankable seeds, pairing each one
// with the post it came from. A reply naming a post we never sent is dropped:
// the model invented the id, and a seed with no source is worthless.
func assemble(resp []model.SeedResponse, sent []model.Post) ([]model.IdeaSeed, int) {
	byID := make(map[string]model.Post, len(sent))
	for _, p := range sent {
		byID[p.ID] = p
	}

	now := time.Now().UTC()
	seeds := make([]model.IdeaSeed, 0, len(resp))
	unknown := 0

	for _, r := range resp {
		if r.Skip {
			continue
		}
		p, ok := byID[r.PostID]
		if !ok {
			unknown++
			continue
		}
		if strings.TrimSpace(r.Tension) == "" && strings.TrimSpace(r.AngleHint) == "" {
			continue // nothing usable came back for this one
		}
		seeds = append(seeds, model.IdeaSeed{
			ID:             "seed_" + p.ID,
			CreatedAt:      now,
			Category:       r.Category,
			ThemeTags:      r.ThemeTags,
			Tension:        r.Tension,
			AngleHint:      r.AngleHint,
			ShelfLife:      r.ShelfLife,
			SourceType:     "engine",
			SourceAuthor:   p.Author,
			SourcePostText: p.Text,
			SourcePostURL:  p.URL,
			SourcePostID:   p.ID,
			Visual:         p.HasVisual,
			Status:         "new",
		})
	}
	return seeds, unknown
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

func waitForEnter() {
	fmt.Fprintln(os.Stderr, "\n>>> sign the alt account in in the browser window, then press Enter here <<<")
	bufio.NewReader(os.Stdin).ReadString('\n')
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
