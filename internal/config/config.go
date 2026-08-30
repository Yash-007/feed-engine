// Package config loads feed-engine's YAML config and normalizes paths.
package config

import (
	"fmt"
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
	MinWords          int `yaml:"min_words"`
	VisualMaxWords    int `yaml:"visual_max_words"`
	SeenRetentionDays int `yaml:"seen_retention_days"`
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
	return c, c.validate()
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
		Claude: Claude{
			Bin: "claude", TextBatchSize: 25, VisualBatchSize: 4, TimeoutSec: 300,
			TextPrompt: "./prompts/filter_and_seed.md", VisualPrompt: "./prompts/filter_and_seed_visual.md",
		},
		Dedupe: Dedupe{RecentSeeds: 200, ThemeOverlap: 0.6},
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
		if !strings.Contains(u, "/lists/") {
			return fmt.Errorf("not an X list URL: %s", u)
		}
	}
	if c.Session.ScrollMinMS > c.Session.ScrollMaxMS {
		return fmt.Errorf("scroll_min_ms > scroll_max_ms")
	}
	if c.Session.MaxPosts <= 0 || c.Session.MaxMinutes <= 0 {
		return fmt.Errorf("max_posts and max_minutes must be > 0")
	}
	return nil
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
