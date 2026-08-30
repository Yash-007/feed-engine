// Package store owns the SQLite idea bank. Pure-Go driver, so no cgo and no
// system sqlite needed.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Yash-007/feed-engine/internal/model"
)

const schema = `
CREATE TABLE IF NOT EXISTS idea_seeds (
  id               TEXT PRIMARY KEY,
  created_at       TEXT NOT NULL,
  category         TEXT,
  theme_tags       TEXT,          -- JSON array
  tension          TEXT,
  angle_hint       TEXT,
  shelf_life       TEXT,
  source_type      TEXT NOT NULL DEFAULT 'engine',
  source_author    TEXT,
  source_post_text TEXT,
  source_post_url  TEXT,
  source_post_id   TEXT,
  visual           INTEGER NOT NULL DEFAULT 0,
  status           TEXT NOT NULL DEFAULT 'new'
);
CREATE INDEX IF NOT EXISTS idx_seeds_created ON idea_seeds(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_seeds_status  ON idea_seeds(status);
CREATE UNIQUE INDEX IF NOT EXISTS idx_seeds_post ON idea_seeds(source_post_id)
  WHERE source_post_id IS NOT NULL AND source_post_id <> '';
`

type DB struct{ sql *sql.DB }

func Open(path string) (*DB, error) {
	// _time_format=sqlite keeps timestamps readable if you poke at the file by hand.
	h, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open db %s: %w", path, err)
	}
	if _, err := h.Exec(schema); err != nil {
		h.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &DB{sql: h}, nil
}

func (d *DB) Close() error { return d.sql.Close() }

// InsertResult reports what happened to a batch of seeds.
type InsertResult struct {
	Inserted   int
	DupTheme   int // too close to a recent seed's theme set
	DupPost    int // that post already produced a seed
	Skipped    int // model flagged it, or it was unusable
	InsertedID []string
}

// Insert writes seeds, dropping ones that duplicate a recent seed by theme.
func (d *DB) Insert(seeds []model.IdeaSeed, recentN int, overlap float64) (InsertResult, error) {
	var res InsertResult
	if len(seeds) == 0 {
		return res, nil
	}

	recent, err := d.recentThemes(recentN)
	if err != nil {
		return res, err
	}

	tx, err := d.sql.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO idea_seeds
	 (id, created_at, category, theme_tags, tension, angle_hint, shelf_life,
	  source_type, source_author, source_post_text, source_post_url, source_post_id, visual, status)
	 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return res, err
	}
	defer stmt.Close()

	for _, s := range seeds {
		tags := normTags(s.ThemeTags)
		if dupTheme(tags, recent, overlap) {
			res.DupTheme++
			continue
		}

		raw, _ := json.Marshal(tags)
		out, err := stmt.Exec(
			s.ID, s.CreatedAt.UTC().Format(time.RFC3339), s.Category, string(raw),
			s.Tension, s.AngleHint, s.ShelfLife, s.SourceType, s.SourceAuthor,
			s.SourcePostText, s.SourcePostURL, s.SourcePostID, boolInt(s.Visual), s.Status,
		)
		if err != nil {
			return res, fmt.Errorf("insert seed %s: %w", s.ID, err)
		}
		if n, _ := out.RowsAffected(); n == 0 {
			res.DupPost++ // unique index on source_post_id caught it
			continue
		}

		res.Inserted++
		res.InsertedID = append(res.InsertedID, s.ID)
		recent = append(recent, tags) // guard against dupes inside this same batch
	}

	return res, tx.Commit()
}

func (d *DB) recentThemes(n int) ([][]string, error) {
	if n <= 0 {
		return nil, nil
	}
	rows, err := d.sql.Query(
		`SELECT theme_tags FROM idea_seeds ORDER BY created_at DESC LIMIT ?`, n)
	if err != nil {
		return nil, fmt.Errorf("load recent themes: %w", err)
	}
	defer rows.Close()

	var out [][]string
	for rows.Next() {
		var raw sql.NullString
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var tags []string
		if raw.Valid && raw.String != "" {
			_ = json.Unmarshal([]byte(raw.String), &tags)
		}
		if len(tags) > 0 {
			out = append(out, normTags(tags))
		}
	}
	return out, rows.Err()
}

// Count returns how many seeds sit at a given status.
func (d *DB) Count(status string) (int, error) {
	var n int
	err := d.sql.QueryRow(`SELECT COUNT(*) FROM idea_seeds WHERE status = ?`, status).Scan(&n)
	return n, err
}

func normTags(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// dupTheme is jaccard overlap on the tag sets: same themes, already banked.
func dupTheme(tags []string, recent [][]string, threshold float64) bool {
	if len(tags) == 0 || threshold <= 0 {
		return false
	}
	for _, r := range recent {
		if jaccard(tags, r) >= threshold {
			return true
		}
	}
	return false
}

func jaccard(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := make(map[string]bool, len(a))
	for _, x := range a {
		set[x] = true
	}
	inter := 0
	for _, y := range b {
		if set[y] {
			inter++
		}
	}
	union := len(set)
	for _, y := range b {
		if !set[y] {
			union++
		}
	}
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
