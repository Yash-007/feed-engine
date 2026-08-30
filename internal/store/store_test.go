package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Yash-007/feed-engine/internal/model"
)

func TestJaccard(t *testing.T) {
	cases := []struct {
		a, b []string
		want float64
	}{
		{[]string{"pricing", "saas"}, []string{"pricing", "saas"}, 1},
		{[]string{"pricing", "saas"}, []string{"pricing", "hiring"}, 1.0 / 3.0},
		{[]string{"pricing"}, []string{"hiring"}, 0},
		{nil, []string{"pricing"}, 0},
	}
	for _, c := range cases {
		if got := jaccard(c.a, c.b); got < c.want-0.001 || got > c.want+0.001 {
			t.Errorf("jaccard(%v,%v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func seed(id string, tags ...string) model.IdeaSeed {
	return model.IdeaSeed{
		ID: id, CreatedAt: time.Now().UTC(), Category: "insight", ThemeTags: tags,
		Tension: "t", AngleHint: "a", ShelfLife: "evergreen",
		SourceType: "engine", SourceAuthor: "someone", SourcePostID: "post-" + id,
		Status: "new",
	}
}

func TestInsertDedupesByThemeAndPost(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	res, err := db.Insert([]model.IdeaSeed{seed("a", "pricing", "saas")}, 100, 0.6)
	if err != nil || res.Inserted != 1 {
		t.Fatalf("first insert: %+v err=%v", res, err)
	}

	// Same themes as a banked seed -> dropped on theme overlap.
	res, err = db.Insert([]model.IdeaSeed{seed("b", "pricing", "saas")}, 100, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	if res.Inserted != 0 || res.DupTheme != 1 {
		t.Fatalf("expected a theme dupe, got %+v", res)
	}

	// Different themes go in.
	res, err = db.Insert([]model.IdeaSeed{seed("c", "hiring", "remote")}, 100, 0.6)
	if err != nil || res.Inserted != 1 {
		t.Fatalf("distinct themes should insert: %+v err=%v", res, err)
	}

	// Two seeds with the same themes inside one batch: only the first survives.
	res, err = db.Insert([]model.IdeaSeed{
		seed("d", "churn", "onboarding"),
		seed("e", "churn", "onboarding"),
	}, 100, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	if res.Inserted != 1 || res.DupTheme != 1 {
		t.Fatalf("intra-batch dupe not caught: %+v", res)
	}

	// Same source post twice -> unique index catches it, whatever the themes.
	dup := seed("f", "totally", "unrelated")
	dup.SourcePostID = "post-a"
	res, err = db.Insert([]model.IdeaSeed{dup}, 100, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	if res.Inserted != 0 || res.DupPost != 1 {
		t.Fatalf("expected a post dupe, got %+v", res)
	}

	if n, err := db.Count("new"); err != nil || n != 3 {
		t.Fatalf("bank should hold 3 new seeds, got %d err=%v", n, err)
	}
}
