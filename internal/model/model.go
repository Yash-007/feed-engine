// Package model holds the two records that move through the pipeline:
// a scraped Post and the IdeaSeed Claude distills out of it.
package model

import "time"

// Post is one tweet as read off the DOM. Nothing here is derived from an API.
type Post struct {
	ID         string    `json:"id"`          // numeric status id, the dedupe key
	URL        string    `json:"url"`         // https://x.com/<handle>/status/<id>
	Author     string    `json:"author"`      // @handle, without the @
	AuthorName string    `json:"author_name"` // display name, best effort
	Text       string    `json:"text"`
	Timestamp  string    `json:"timestamp"` // ISO-8601 from the <time datetime> attr
	WordCount  int       `json:"word_count"`
	HasMedia   bool      `json:"has_media"`   // photo, video, or link card present
	IsRepost   bool      `json:"is_repost"`   // surfaced via a retweet social-context row
	IsPromoted bool      `json:"is_promoted"` // an ad injected into the home timeline
	HasVisual  bool      `json:"has_visual"`  // media + short text -> screenshotted
	ImagePath  string    `json:"image_path,omitempty"`
	ListURL    string    `json:"list_url"`
	ScrapedAt  time.Time `json:"scraped_at"`
}

// IdeaSeed is what Claude returns per keepable post, and what lands in SQLite.
// Field names mirror the idea bank's wire format so a row can be POSTed to the
// Feed Runner backend without reshaping.
type IdeaSeed struct {
	ID               string    `json:"client_seed_id"` // "harvest-<post id>", the idempotency key
	CreatedAt        time.Time `json:"-"`
	CapturedAtMillis int64     `json:"captured_at_millis"` // post timestamp, filled by the engine
	Source           string    `json:"source"`             // always "harvest" from this engine
	Platform         string    `json:"platform"`           // always "x"
	Category         string    `json:"category"`           // take|shitpost|banter|war_story|thought|trend
	ThemeTags        []string  `json:"theme_tags"`         // category first, then 2-4 topic tags
	Tension          string    `json:"tension"`
	AngleHint        string    `json:"angle_hint"`
	ShelfLife        string    `json:"shelf_life"` // evergreen | timely
	PostAuthor       string    `json:"post_author"`
	PostText         string    `json:"post_text"`
	SourcePostURL    string    `json:"source_post_url"`
	SourcePostID     string    `json:"source_post_id"`
	Visual           bool      `json:"visual"`
	Status           string    `json:"status"`
}

// SeedResponse is the shape Claude is told to emit: one object per kept post,
// already in the bank's wire format. ClientSeedID ties it back to the Post it
// came from — it is "harvest-" plus the post id.
type SeedResponse struct {
	ClientSeedID string   `json:"client_seed_id"`
	Source       string   `json:"source"`
	Platform     string   `json:"platform"`
	PostAuthor   string   `json:"post_author"`
	PostText     string   `json:"post_text"`
	Category     string   `json:"category"`
	ThemeTags    []string `json:"theme_tags"`
	Tension      string   `json:"tension"`
	AngleHint    string   `json:"angle_hint"`
	ShelfLife    string   `json:"shelf_life"`
}

// SeedIDPrefix is prepended to a post id to form client_seed_id.
const SeedIDPrefix = "harvest-"
