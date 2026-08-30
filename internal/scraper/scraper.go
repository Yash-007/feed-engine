// Package scraper drives a persistent, headed Chrome profile over an X List and
// reads posts off the DOM. It is strictly read-only: it navigates, scrolls, and
// screenshots. No click, no key press, no network write to X.
package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"path/filepath"
	"strings"
	"time"

	"github.com/playwright-community/playwright-go"

	"github.com/Yash-007/feed-engine/internal/config"
	"github.com/Yash-007/feed-engine/internal/model"
	"github.com/Yash-007/feed-engine/internal/selectors"
)

// WantShot decides whether a post earns an element screenshot. The scraper asks
// this while the post is still mounted; the policy itself lives in prefilter.
type WantShot func(model.Post) bool

type Result struct {
	ListURL  string
	Posts    []model.Post
	Health   *selectors.Health
	StopWhy  string
	Scrolls  int
	Duration time.Duration
	Shots    int
}

// rawPost mirrors the object extractScript returns.
type rawPost struct {
	ID         string   `json:"id"`
	URL        string   `json:"url"`
	Author     string   `json:"author"`
	AuthorName string   `json:"author_name"`
	Text       string   `json:"text"`
	Timestamp  string   `json:"timestamp"`
	HasMedia   bool     `json:"has_media"`
	IsRepost   bool     `json:"is_repost"`
	IsPromoted bool     `json:"is_promoted"`
	Visible    bool     `json:"visible"`
	Miss       []string `json:"miss"`
}

type session struct {
	log  *slog.Logger
	cfg  *config.Config
	pw   *playwright.Playwright
	bctx playwright.BrowserContext
	page playwright.Page
}

func start(log *slog.Logger, cfg *config.Config) (*session, error) {
	pw, err := playwright.Run()
	if err != nil {
		// Driver missing on first run: pull it once, then retry.
		log.Warn("playwright driver not ready, installing", "err", err)
		if ierr := playwright.Install(&playwright.RunOptions{Browsers: []string{"chromium"}}); ierr != nil {
			return nil, fmt.Errorf("playwright install: %w (original: %v)", ierr, err)
		}
		if pw, err = playwright.Run(); err != nil {
			return nil, fmt.Errorf("playwright run: %w", err)
		}
	}

	opts := playwright.BrowserTypeLaunchPersistentContextOptions{
		Headless: playwright.Bool(cfg.Browser.Headless),
		Viewport: &playwright.Size{Width: cfg.Browser.ViewportWidth, Height: cfg.Browser.ViewportHeight},
		// Pin to the top-left so a headed window can't open part-way off the
		// desktop and hide the bottom of the page.
		Args: []string{
			"--disable-blink-features=AutomationControlled",
			"--window-position=0,0",
		},
		// Playwright adds --enable-automation by default, which sets
		// navigator.webdriver and shows the "controlled by automated software"
		// infobar. X treats that as a bot signal and can stall the sign-in flow,
		// so drop it. Read-only scrolling is unaffected either way.
		IgnoreDefaultArgs: []string{"--enable-automation"},
	}
	if cfg.Browser.Channel != "" {
		opts.Channel = playwright.String(cfg.Browser.Channel)
	}
	bctx, err := pw.Chromium.LaunchPersistentContext(cfg.Browser.ProfileDir, opts)
	if err != nil {
		pw.Stop()
		// "target closed" right after <launched> almost always means another
		// Chrome still holds this profile. A failed launch leaves its browser
		// running, so one failure locks the profile and every later run fails the
		// same way until the stray processes are killed.
		return nil, fmt.Errorf("launch persistent context (profile %s): %w\n"+
			"if Chrome exited immediately, a stray Chrome is probably still holding the profile — kill any\n"+
			"chrome.exe whose command line contains %s, then retry",
			cfg.Browser.ProfileDir, err, cfg.Browser.ProfileDir)
	}
	bctx.SetDefaultTimeout(float64(cfg.Browser.NavTimeoutMS))

	page := bctx.Pages()
	var p playwright.Page
	if len(page) > 0 {
		p = page[0]
	} else if p, err = bctx.NewPage(); err != nil {
		bctx.Close()
		pw.Stop()
		return nil, fmt.Errorf("new page: %w", err)
	}
	return &session{log: log, cfg: cfg, pw: pw, bctx: bctx, page: p}, nil
}

func (s *session) close() {
	if s.bctx != nil {
		s.bctx.Close()
	}
	if s.pw != nil {
		s.pw.Stop()
	}
}

// Login opens x.com so the alt account can be signed in by hand, then watches
// the page until the session appears. The script never sees a username or
// password; it only holds the window open and polls.
//
// It watches rather than waiting on a keypress so it works with no console
// attached — under a scheduler, or driven by another process whose stdin is
// closed, where a read would return EOF instantly and close the browser.
func Login(ctx context.Context, log *slog.Logger, cfg *config.Config, timeout time.Duration) error {
	s, err := start(log, cfg)
	if err != nil {
		return err
	}
	defer s.close()

	if _, err := s.page.Goto("https://x.com/home", playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	}); err != nil {
		return fmt.Errorf("goto x.com: %w", err)
	}

	if err := s.settle(); err == nil && !s.loggedOut() {
		log.Info("already signed in, nothing to do", "profile", cfg.Browser.ProfileDir)
		return nil
	}

	log.Info("browser open — sign the alt account in by hand; this will notice and exit on its own",
		"giving_up_after", timeout)

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !sleepCtx(ctx, 3*time.Second) {
			return ctx.Err()
		}
		if err := s.settle(); err != nil {
			continue // mid-navigation, neither timeline nor form is mounted
		}
		if s.loggedOut() {
			continue
		}
		// Give the browser a moment to flush the session cookies into the
		// profile directory before the context closes underneath it.
		sleepCtx(ctx, 3*time.Second)
		log.Info("session detected and saved", "profile", cfg.Browser.ProfileDir)
		return nil
	}
	return fmt.Errorf("gave up after %s without seeing a signed-in session", timeout)
}

// settle waits for X's client-side render to produce either the timeline or the
// sign-in form. Any login-state check made before this is looking at an empty
// DOM and will answer "logged in" no matter what the truth is.
func (s *session) settle() error {
	probe := strings.Join([]string{selectors.S.Tweet, selectors.S.LoginLink, selectors.S.LoginForm}, ", ")
	return s.page.Locator(probe).First().WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateAttached,
		Timeout: playwright.Float(float64(s.cfg.Browser.NavTimeoutMS)),
	})
}

func (s *session) loggedOut() bool {
	for _, sel := range []string{selectors.S.LoginLink, selectors.S.LoginForm} {
		if n, err := s.page.Locator(sel).Count(); err == nil && n > 0 {
			return true
		}
	}
	return false
}

// Run scrolls listURL and returns everything it read.
func Run(ctx context.Context, log *slog.Logger, cfg *config.Config, listURL string, want WantShot) (*Result, error) {
	s, err := start(log, cfg)
	if err != nil {
		return nil, err
	}
	defer s.close()

	res := &Result{ListURL: listURL, Health: selectors.NewHealth()}
	began := time.Now()

	if _, err := s.page.Goto(listURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(float64(cfg.Browser.NavTimeoutMS)),
	}); err != nil {
		return res, fmt.Errorf("goto %s: %w", listURL, err)
	}

	// X renders client-side: at domcontentloaded neither the timeline nor the
	// sign-in form exists yet, so probing for either one immediately always
	// comes back empty. Wait for whichever lands first, then decide. Checking
	// login state too early is what makes a logged-out profile look like DOM
	// drift 45 seconds later.
	if err := s.settle(); err != nil {
		if n, _ := s.page.Locator(selectors.S.EmptyState).Count(); n > 0 {
			return res, fmt.Errorf("timeline rendered but is empty: %s", listURL)
		}
		return res, fmt.Errorf("neither posts nor a sign-in form appeared at %s — selector drift: %w",
			listURL, err)
	}

	if s.loggedOut() {
		return res, fmt.Errorf("profile is logged out — run with -login and sign the alt account in once")
	}

	if n, _ := s.page.Locator(selectors.S.Tweet).Count(); n == 0 {
		if e, _ := s.page.Locator(selectors.S.EmptyState).Count(); e > 0 {
			return res, fmt.Errorf("timeline rendered but is empty: %s", listURL)
		}
		return res, fmt.Errorf("signed in, but no posts matched %q — selector drift or a bad timeline URL: %s",
			selectors.S.Tweet, listURL)
	}

	deadline := began.Add(time.Duration(cfg.Session.MaxMinutes) * time.Minute)
	seenRun := map[string]bool{}
	idle := 0

	for {
		switch {
		case ctx.Err() != nil:
			res.StopWhy = "cancelled"
		case len(res.Posts) >= cfg.Session.MaxPosts:
			res.StopWhy = "post cap"
		case time.Now().After(deadline):
			res.StopWhy = "time cap"
		case idle >= cfg.Session.IdleScrollsBeforeStop:
			res.StopWhy = "timeline stopped yielding new posts"
		}
		if res.StopWhy != "" {
			break
		}

		found, err := s.harvest(res, seenRun, want)
		if err != nil {
			return res, err
		}
		if found == 0 {
			idle++
		} else {
			idle = 0
		}

		px := randRange(cfg.Session.ScrollPxMin, cfg.Session.ScrollPxMax)
		if err := s.page.Mouse().Wheel(0, float64(px)); err != nil {
			return res, fmt.Errorf("scroll: %w", err)
		}
		res.Scrolls++

		pause := time.Duration(randRange(cfg.Session.ScrollMinMS, cfg.Session.ScrollMaxMS)) * time.Millisecond
		log.Debug("scrolled", "px", px, "pause", pause, "posts", len(res.Posts), "idle", idle)
		if !sleepCtx(ctx, pause) {
			res.StopWhy = "cancelled"
			break
		}
	}

	res.Duration = time.Since(began)
	return res, nil
}

// harvest reads the mounted posts once and appends the new ones.
func (s *session) harvest(res *Result, seenRun map[string]bool, want WantShot) (int, error) {
	// map[string]any, not map[string]string: playwright-go's argument serializer
	// only handles the former and panics outright on the latter.
	arg := map[string]any{
		"tweet": selectors.S.Tweet, "text": selectors.S.Text, "time": selectors.S.Time,
		"permalink": selectors.S.PermalinkAnc, "username": selectors.S.UserName,
		"social": selectors.S.SocialContext, "photo": selectors.S.Photo,
		"video": selectors.S.Video, "card": selectors.S.Card, "poll": selectors.S.Poll,
		"promoted": selectors.S.Promoted,
	}
	raw, err := s.page.Evaluate(extractScript, arg)
	if err != nil {
		return 0, fmt.Errorf("extract script failed (selectors likely stale): %w", err)
	}
	blob, err := json.Marshal(raw)
	if err != nil {
		return 0, fmt.Errorf("marshal extract result: %w", err)
	}
	var rows []rawPost
	if err := json.Unmarshal(blob, &rows); err != nil {
		return 0, fmt.Errorf("decode extract result: %w", err)
	}

	added := 0
	for _, r := range rows {
		res.Health.Articles++
		for _, f := range r.Miss {
			res.Health.Note(f, false)
		}
		if r.ID == "" || seenRun[r.ID] {
			continue
		}
		seenRun[r.ID] = true

		p := model.Post{
			ID: r.ID, URL: r.URL, Author: r.Author, AuthorName: r.AuthorName,
			Text: strings.TrimSpace(r.Text), Timestamp: r.Timestamp,
			WordCount: wordCount(r.Text), HasMedia: r.HasMedia, IsRepost: r.IsRepost,
			IsPromoted: r.IsPromoted,
			ListURL:    res.ListURL, ScrapedAt: time.Now().UTC(),
		}

		if want != nil && r.Visible && want(p) {
			path, err := s.shoot(p.ID)
			switch {
			case err != nil:
				s.log.Warn("screenshot failed, keeping text only", "post", p.ID, "err", err)
			default:
				p.HasVisual, p.ImagePath = true, path
				res.Shots++
			}
		}

		res.Posts = append(res.Posts, p)
		added++
		if len(res.Posts) >= s.cfg.Session.MaxPosts {
			break
		}
	}
	return added, nil
}

// shoot crops a screenshot to the single post element — no surrounding timeline.
func (s *session) shoot(id string) (string, error) {
	loc := s.page.Locator(selectors.S.TweetByID(id)).First()
	path := filepath.Join(s.cfg.Paths.Screenshots, id+".png")
	if _, err := loc.Screenshot(playwright.LocatorScreenshotOptions{
		Path:    playwright.String(path),
		Timeout: playwright.Float(8000),
	}); err != nil {
		return "", err
	}
	return path, nil
}

func wordCount(s string) int { return len(strings.Fields(s)) }

func randRange(lo, hi int) int {
	if hi <= lo {
		return lo
	}
	return lo + rand.Intn(hi-lo+1)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
