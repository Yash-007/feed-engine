package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Yash-007/feed-engine/internal/model"
)

func openTemp(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "outbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func harvested(id string, tags ...string) model.IdeaSeed {
	return model.IdeaSeed{
		ID: id, CreatedAt: time.Now().UTC(), CapturedAtMillis: 1756598400000,
		Source: "harvest", Platform: "x", Category: model.CategoryRepost,
		ThemeTags: tags, Tension: "t", AngleHint: "a", ShelfLife: "evergreen",
		PostAuthor: "@someone", PostText: "the post",
		SourcePostURL: "https://x.com/someone/status/" + id,
		SourcePostID:  id, Visual: true, Status: "new",
	}
}

// A freshly banked seed is pending by definition: nothing has told the backend
// about it yet.
func TestUnpostedStartsWithEverythingBanked(t *testing.T) {
	db := openTemp(t)
	if _, err := db.Insert([]model.IdeaSeed{
		harvested("1", "repost", "fintech"),
		harvested("2", "take", "hiring"),
	}, 100, 0.6); err != nil {
		t.Fatal(err)
	}

	queued, err := db.Unposted(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 2 {
		t.Fatalf("got %d queued, want 2", len(queued))
	}
	if n, _ := db.PendingCount(); n != 2 {
		t.Errorf("PendingCount = %d, want 2", n)
	}

	// The row has to survive the round trip whole, or the backend gets a seed
	// with the link or the media flag silently missing.
	s := queued[0]
	if s.SourcePostURL == "" || !s.Visual || s.Category != model.CategoryRepost {
		t.Errorf("fields lost on read back: %+v", s)
	}
	if len(s.ThemeTags) == 0 {
		t.Error("theme tags lost on read back")
	}
}

func TestMarkPostedRemovesFromTheQueue(t *testing.T) {
	db := openTemp(t)
	if _, err := db.Insert([]model.IdeaSeed{
		harvested("1", "repost", "fintech"),
		harvested("2", "take", "hiring"),
	}, 100, 0.6); err != nil {
		t.Fatal(err)
	}

	if err := db.MarkPosted([]string{"1"}); err != nil {
		t.Fatal(err)
	}

	queued, err := db.Unposted(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 || queued[0].ID != "2" {
		t.Fatalf("only the unposted seed should remain, got %+v", queued)
	}
	if n, _ := db.PendingCount(); n != 1 {
		t.Errorf("PendingCount = %d, want 1", n)
	}

	// Idempotent: a re-push that answers duplicate stamps the same row again.
	if err := db.MarkPosted([]string{"1"}); err != nil {
		t.Errorf("re-stamping must not fail: %v", err)
	}
	if err := db.MarkPosted(nil); err != nil {
		t.Errorf("an empty batch must be a no-op: %v", err)
	}
}

func TestUnpostedIsOldestFirstAndCapped(t *testing.T) {
	db := openTemp(t)
	base := time.Now().UTC().Add(-10 * time.Hour)
	seeds := make([]model.IdeaSeed, 0, 5)
	for i, tag := range []string{"one", "two", "three", "four", "five"} {
		s := harvested(string(rune('a'+i)), tag)
		// Distinct times so the ordering is actually being tested rather than
		// falling out of insertion order.
		s.CreatedAt = base.Add(time.Duration(i) * time.Hour)
		seeds = append(seeds, s)
	}
	if _, err := db.Insert(seeds, 100, 0.6); err != nil {
		t.Fatal(err)
	}

	queued, err := db.Unposted(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 3 {
		t.Fatalf("got %d, want the cap of 3", len(queued))
	}
	// Oldest first, so a backlog fills the bank in the order things happened.
	if queued[0].ID != "a" || queued[2].ID != "c" {
		t.Errorf("wrong order: %s, %s, %s", queued[0].ID, queued[1].ID, queued[2].ID)
	}
}

/*
A bank created before the outbox existed has to open, migrate and work.

This is the case that actually shipped broken: the partial index over posted_at
lived in the schema block, which runs before the ALTER that adds the column, so
Open returned "no such column: posted_at" on every existing bank while passing
on every fresh one. Only a genuinely old file catches it.
*/
func TestOpenMigratesABankWithoutTheOutboxColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")

	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// The shape the bank had before the push queue existed: no posted_at.
	if _, err := old.Exec(`CREATE TABLE idea_seeds (
	  id TEXT PRIMARY KEY, created_at TEXT NOT NULL, captured_at_millis INTEGER DEFAULT 0,
	  source TEXT, platform TEXT, category TEXT, theme_tags TEXT, tension TEXT,
	  angle_hint TEXT, shelf_life TEXT, post_author TEXT, post_text TEXT,
	  source_post_url TEXT, source_post_id TEXT, visual INTEGER DEFAULT 0,
	  status TEXT DEFAULT 'new')`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(
		`INSERT INTO idea_seeds (id, created_at, tension, status) VALUES (?,?,?,?)`,
		"harvest-old", time.Now().UTC().Format(time.RFC3339), "banked before the outbox", "new",
	); err != nil {
		t.Fatal(err)
	}
	old.Close()

	db, err := Open(path)
	if err != nil {
		t.Fatalf("an existing bank must migrate rather than fail to open: %v", err)
	}
	defer db.Close()

	// The pre-existing seed is pending, which is right: nothing ever pushed it.
	queued, err := db.Unposted(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 || queued[0].ID != "harvest-old" {
		t.Fatalf("got %+v, want the pre-existing seed queued", queued)
	}
	if err := db.MarkPosted([]string{"harvest-old"}); err != nil {
		t.Fatalf("MarkPosted on a migrated bank: %v", err)
	}
	if n, _ := db.PendingCount(); n != 0 {
		t.Errorf("PendingCount = %d, want 0", n)
	}
}

// The outbox column is added by migration on banks that predate it, and those
// rows can hold NULL in text columns a fresh insert would fill with "".
func TestUnpostedSurvivesNullTextColumns(t *testing.T) {
	db := openTemp(t)
	if _, err := db.sql.Exec(
		`INSERT INTO idea_seeds (id, created_at, tension) VALUES (?, ?, ?)`,
		"legacy-1", time.Now().UTC().Format(time.RFC3339), "an old tension",
	); err != nil {
		t.Fatal(err)
	}

	queued, err := db.Unposted(50)
	if err != nil {
		t.Fatalf("a legacy row must not wedge the queue: %v", err)
	}
	if len(queued) != 1 {
		t.Fatalf("got %d, want the legacy row", len(queued))
	}
	if queued[0].Tension != "an old tension" {
		t.Errorf("tension = %q", queued[0].Tension)
	}
	// Defaults rather than empty, so the backend still sees a valid seed.
	if queued[0].Source != "harvest" || queued[0].Platform != "x" {
		t.Errorf("defaults not applied: %+v", queued[0])
	}
}
