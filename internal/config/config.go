// Package config loads feed-engine's YAML config and normalizes paths.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Session struct {
	MaxMinutes            int `yaml:"max_minutes"`
	MaxPosts              int `yaml:"max_posts"`
	ScrollMinMS           int `yaml:"scroll_min_ms"`
	ScrollMaxMS           int `yaml:"scroll_max_ms"`
	ScrollPxMin           int `yaml:"scroll_px_min"`
	ScrollPxMax           int `yaml:"scroll_px_max"`
	IdleScrollsBeforeStop int `yaml:"idle_scrolls_before_stop"`
	StartJitterMaxSec     int `yaml:"start_jitter_max_sec"`
}

type Browser struct {
	ProfileDir     string `yaml:"profile_dir"`
	Channel        string `yaml:"channel"`
	Headless       bool   `yaml:"headless"`
	ViewportWidth  int    `yaml:"viewport_width"`
	ViewportHeight int    `yaml:"viewport_height"`
	NavTimeoutMS   int    `yaml:"nav_timeout_ms"`
}

type Paths struct {
	DB          string `yaml:"db"`
	PostsJSON   string `yaml:"posts_json"`
	SeenIDs     string `yaml:"seen_ids"`
	Screenshots string `yaml:"screenshots"`
	Log         string `yaml:"log"`
}

type Prefilter struct {
	MinWords          int    `yaml:"min_words"`
	VisualMaxWords    int    `yaml:"visual_max_words"`
	SeenRetentionDays int    `yaml:"seen_retention_days"`
	OwnHandle         string `yaml:"own_handle"`
}

type Claude struct {
	Bin             string   `yaml:"bin"`
	TextPrompt      string   `yaml:"text_prompt"`
	VisualPrompt    string   `yaml:"visual_prompt"`
	TextBatchSize   int      `yaml:"text_batch_size"`
	VisualBatchSize int      `yaml:"visual_batch_size"`
	TimeoutSec      int      `yaml:"timeout_sec"`
	ExtraArgs       []string `yaml:"extra_args"`
	VisualExtraArgs []string `yaml:"visual_extra_args"`
}

type Dedupe struct {
	RecentSeeds  int     `yaml:"recent_seeds"`
	ThemeOverlap float64 `yaml:"theme_overlap"`
	CategoryCap  int     `yaml:"category_cap"`
}

// Bank is the Feed Runner backend the harvested seeds are pushed to.
//
// Disabled by default and by absence of a token: this is the one outbound write
// in an otherwise read-only tool, and it writes into a live personal account,
// so it has to be turned on deliberately rather than by forgetting to turn it
// off. The local SQLite bank is written either way, so nothing is lost while
// it is off.
type Bank struct {
	Enabled bool   `yaml:"enabled"`
	BaseURL string `yaml:"base_url"`
	// Token is a session token from the app ("fr_" + 64 hex). Keep it out of
	// config.yaml, which is committed: put it in config.local.yaml, or leave it
	// empty and export FEED_ENGINE_BANK_TOKEN instead.
	Token      string `yaml:"token"`
	TimeoutSec int    `yaml:"timeout_sec"`
	// MaxPerRun bounds a backlog flush, so a month of unpushed seeds does not
	// become one enormous burst at the free tier.
	MaxPerRun int `yaml:"max_per_run"`
}

// Login holds the X credentials used to sign the harvesting account in when the
// profile's session has lapsed.
//
// Same rule as the bank token: never in config.yaml. Leaving these empty is the
// supported path, and the safer one, since -login lets you sign in by hand
// once into the persistent profile and X is markedly less suspicious of that.
type Login struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	// AutoLogin lets a scheduled run type the credentials in when it finds the
	// profile signed out. Off by default: an unattended login is the single
	// most likely thing here to trip X's automation checks, and the cost of
	// tripping it is the account, not the run.
	AutoLogin bool `yaml:"auto_login"`
}

type Config struct {
	ListURLs    []string  `yaml:"list_urls"`
	RotateLists bool      `yaml:"rotate_lists"`
	Session     Session   `yaml:"session"`
	Browser     Browser   `yaml:"browser"`
	Paths       Paths     `yaml:"paths"`
	Prefilter   Prefilter `yaml:"prefilter"`
	Claude      Claude    `yaml:"claude"`
	Dedupe      Dedupe    `yaml:"dedupe"`
	Bank        Bank      `yaml:"bank"`
	Login       Login     `yaml:"login"`
}

// Load reads path, then overlays <dir>/config.local.yaml if it exists.
func Load(path string) (*Config, error) {
	c := defaults()
	if err := decodeInto(path, c); err != nil {
		return nil, err
	}
	local := filepath.Join(filepath.Dir(path), "config.local.yaml")
	if _, err := os.Stat(local); err == nil {
		if err := decodeInto(local, c); err != nil {
			return nil, fmt.Errorf("local override: %w", err)
		}
	}
	c.expand()
	c.applyEnv()
	return c, c.validate()
}

/*
applyEnv lets the two secrets stay out of every file on disk.

config.yaml is committed and config.local.yaml is only gitignored, which is one
`git add -f` away from being a mistake. An environment variable is the option
that leaves no copy behind, so it wins over both files when set.
*/
func (c *Config) applyEnv() {
	for _, o := range []struct {
		key  string
		into *string
	}{
		{"FEED_ENGINE_BANK_TOKEN", &c.Bank.Token},
		{"FEED_ENGINE_X_USERNAME", &c.Login.Username},
		{"FEED_ENGINE_X_PASSWORD", &c.Login.Password},
	} {
		if v := strings.TrimSpace(os.Getenv(o.key)); v != "" {
			*o.into = v
		}
	}
	c.Bank.Token = strings.TrimSpace(c.Bank.Token)
	c.Bank.BaseURL = strings.TrimRight(strings.TrimSpace(c.Bank.BaseURL), "/")
}

func decodeInto(path string, c *Config) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(b, c); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return nil
}

func defaults() *Config {
	return &Config{
		RotateLists: true,
		Session: Session{
			MaxMinutes: 13, MaxPosts: 150,
			ScrollMinMS: 2000, ScrollMaxMS: 8000,
			ScrollPxMin: 500, ScrollPxMax: 1400,
			IdleScrollsBeforeStop: 6, StartJitterMaxSec: 420,
		},
		Browser: Browser{
			ProfileDir: "~/.feed-engine/chrome-profile", Channel: "chrome",
			ViewportWidth: 1280, ViewportHeight: 1000, NavTimeoutMS: 45000,
		},
		Paths: Paths{
			DB: "./data/ideas.db", PostsJSON: "./data/posts.json",
			SeenIDs: "./data/seen_ids.json", Screenshots: "./data/shots",
			Log: "./data/run.log",
		},
		Prefilter: Prefilter{MinWords: 10, VisualMaxWords: 15, SeenRetentionDays: 90},
		// OwnHandle intentionally has no default: guessing a handle would
		// silently drop a real author's posts.
		Claude: Claude{
			Bin: "claude", TextBatchSize: 25, VisualBatchSize: 4, TimeoutSec: 300,
			TextPrompt: "./prompts/filter_and_seed.md", VisualPrompt: "./prompts/filter_and_seed_visual.md",
		},
		Dedupe: Dedupe{RecentSeeds: 200, ThemeOverlap: 0.6, CategoryCap: 4},
		Bank: Bank{
			BaseURL:    "https://feed-runner-backend.onrender.com",
			TimeoutSec: 60,
			MaxPerRun:  50,
		},
	}
}

func (c *Config) expand() {
	for _, p := range []*string{
		&c.Browser.ProfileDir, &c.Paths.DB, &c.Paths.PostsJSON, &c.Paths.SeenIDs,
		&c.Paths.Screenshots, &c.Paths.Log, &c.Claude.TextPrompt, &c.Claude.VisualPrompt,
	} {
		*p = expandHome(*p)
	}
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}

func (c *Config) validate() error {
	if len(c.ListURLs) == 0 {
		return fmt.Errorf("list_urls is empty")
	}
	for _, u := range c.ListURLs {
		if strings.Contains(u, "REPLACE_WITH_LIST_ID") {
			return fmt.Errorf("list_urls still holds the placeholder: %s", u)
		}
		if err := CheckTimelineURL(u); err != nil {
			return err
		}
	}
	if c.Session.ScrollMinMS > c.Session.ScrollMaxMS {
		return fmt.Errorf("scroll_min_ms > scroll_max_ms")
	}
	if c.Session.MaxPosts <= 0 || c.Session.MaxMinutes <= 0 {
		return fmt.Errorf("max_posts and max_minutes must be > 0")
	}
	// Fail at startup rather than on every seed: a push loop that 401s once per
	// row looks like a backend outage in the log and is really a missing token.
	if c.Bank.Enabled {
		if c.Bank.Token == "" {
			return fmt.Errorf("bank.enabled is true but no token: put it in " +
				"config.local.yaml as bank.token, or export FEED_ENGINE_BANK_TOKEN")
		}
		if c.Bank.BaseURL == "" {
			return fmt.Errorf("bank.enabled is true but bank.base_url is empty")
		}
	}
	if c.Login.AutoLogin && (c.Login.Username == "" || c.Login.Password == "") {
		return fmt.Errorf("login.auto_login is true but the credentials are empty: " +
			"set login.username/login.password in config.local.yaml, or export " +
			"FEED_ENGINE_X_USERNAME/FEED_ENGINE_X_PASSWORD")
	}
	return nil
}

// CheckTimelineURL accepts the two things the scraper can actually scroll: an X
// List and the home timeline. A profile, a single status, or a search page
// either paginates differently or is not an endless timeline at all, and would
// scroll to nothing.
//
// Exported because the -list override has to clear the same bar as the config
// file, and it must fail before the browser starts, not after.
func CheckTimelineURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("unparseable URL %q: %w", raw, err)
	}
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	if host != "x.com" && host != "twitter.com" {
		return fmt.Errorf("not an x.com URL: %s", raw)
	}
	path := strings.TrimSuffix(u.Path, "/")
	if strings.Contains(path, "/lists/") || path == "/home" || path == "" {
		return nil
	}
	return fmt.Errorf("not a scrollable timeline, want /home or a /lists/ URL: %s", raw)
}

// EnsureDirs creates every directory the run writes into.
func (c *Config) EnsureDirs() error {
	dirs := []string{
		c.Browser.ProfileDir, c.Paths.Screenshots,
		filepath.Dir(c.Paths.DB), filepath.Dir(c.Paths.PostsJSON),
		filepath.Dir(c.Paths.SeenIDs), filepath.Dir(c.Paths.Log),
	}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}
