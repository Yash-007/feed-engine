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
	HasMedia   bool      `json:"has_media"`  // photo, video, or link card present
	IsRepost   bool      `json:"is_repost"`  // surfaced via a retweet social-context row
	HasVisual  bool      `json:"has_visual"` // media + short text -> screenshotted
	ImagePath  string    `json:"image_path,omitempty"`
	ListURL    string    `json:"list_url"`
	ScrapedAt  time.Time `json:"scraped_at"`
}

// IdeaSeed is what Claude returns per keepable post, and what lands in SQLite.
type IdeaSeed struct {
	ID             string    `json:"id"`
	CreatedAt      time.Time `json:"created_at"`
	Category       string    `json:"category"`
	ThemeTags      []string  `json:"theme_tags"`
	Tension        string    `json:"tension"`
	AngleHint      string    `json:"angle_hint"`
	ShelfLife      string    `json:"shelf_life"` // evergreen | weeks | days | hours
	SourceType     string    `json:"source_type"`
	SourceAuthor   string    `json:"source_author"`
	SourcePostText string    `json:"source_post_text"`
	SourcePostURL  string    `json:"source_post_url"`
	SourcePostID   string    `json:"source_post_id"`
	Visual         bool      `json:"visual"`
	Status         string    `json:"status"`
}

// SeedResponse is the shape Claude is told to emit: one object per kept post.
// post_id ties it back to the Post it came from; everything else is the seed.
type SeedResponse struct {
	PostID    string   `json:"post_id"`
	Category  string   `json:"category"`
	ThemeTags []string `json:"theme_tags"`
	Tension   string   `json:"tension"`
	AngleHint string   `json:"angle_hint"`
	ShelfLife string   `json:"shelf_life"`
	Skip      bool     `json:"skip"` // an explicit "no substance here"; also just omit the post
}
