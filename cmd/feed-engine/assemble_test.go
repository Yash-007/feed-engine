package main

import (
	"testing"

	"github.com/Yash-007/feed-engine/internal/model"
)

func post(id, url string) model.Post {
	return model.Post{ID: id, URL: url, Author: "someone", Text: "the original post"}
}

func reply(id, category string, tags ...string) model.SeedResponse {
	return model.SeedResponse{
		ClientSeedID: model.SeedIDPrefix + id,
		Category:     category,
		ThemeTags:    tags,
		Tension:      "a generalized pattern",
		AngleHint:    "the insider layer",
	}
}

func TestAssembleKeepsRepostWithAPermalink(t *testing.T) {
	sent := []model.Post{post("1", "https://x.com/someone/status/1")}
	seeds, report := assemble([]model.SeedResponse{reply("1", "repost", "repost", "fintech")}, sent)

	if len(seeds) != 1 {
		t.Fatalf("got %d seeds, want 1", len(seeds))
	}
	s := seeds[0]
	if s.Category != model.CategoryRepost {
		t.Errorf("category = %q, want repost", s.Category)
	}
	if s.SourcePostURL != "https://x.com/someone/status/1" {
		t.Errorf("the permalink must travel with a repost seed, got %q", s.SourcePostURL)
	}
	if report.Demoted != 0 {
		t.Errorf("nothing should have been demoted: %+v", report)
	}
}

// A quote post with nothing to quote would reach the app as an action with no
// target, so the engine has to catch it rather than the UI.
func TestAssembleDemotesRepostWithNoPermalink(t *testing.T) {
	sent := []model.Post{post("2", "")}
	seeds, report := assemble([]model.SeedResponse{reply("2", "repost", "repost", "hiring")}, sent)

	if len(seeds) != 1 {
		t.Fatalf("the seed is still worth banking, got %d", len(seeds))
	}
	if seeds[0].Category != "take" {
		t.Errorf("category = %q, want take after demotion", seeds[0].Category)
	}
	if report.Demoted != 1 {
		t.Errorf("demotion should be reported: %+v", report)
	}
	// The stale category must not survive in the tags, or the app filters on a
	// word the seed no longer claims.
	if got := seeds[0].ThemeTags; len(got) == 0 || got[0] != "take" {
		t.Errorf("theme_tags must lead with the corrected category, got %v", got)
	}
	for _, tag := range seeds[0].ThemeTags {
		if tag == "repost" {
			t.Error("the demoted category is still in the tags")
		}
	}
}

func TestAssembleBlanksUnknownCategory(t *testing.T) {
	sent := []model.Post{post("3", "https://x.com/someone/status/3")}
	seeds, report := assemble(
		[]model.SeedResponse{reply("3", "hot_take", "hot_take", "payments")}, sent)

	if len(seeds) != 1 {
		t.Fatalf("got %d seeds, want 1", len(seeds))
	}
	if seeds[0].Category != "" {
		t.Errorf("an invented category must not be stored, got %q", seeds[0].Category)
	}
	if len(report.BadCategory) != 1 || report.BadCategory[0] != "hot_take" {
		t.Errorf("the bad value should be reported for the log: %+v", report.BadCategory)
	}
	if got := seeds[0].ThemeTags; len(got) != 1 || got[0] != "payments" {
		t.Errorf("tags should keep only the real topics, got %v", got)
	}
}

func TestAssembleSetsVisualFromMedia(t *testing.T) {
	// A post with media that went through the TEXT pipeline still needs the
	// flag: the reader has an image to go and look at either way, which is the
	// whole point of surfacing it.
	textWithImage := post("4", "https://x.com/someone/status/4")
	textWithImage.HasMedia = true
	textWithImage.HasVisual = false

	plain := post("5", "https://x.com/someone/status/5")

	seeds, _ := assemble([]model.SeedResponse{
		reply("4", "take", "take"),
		reply("5", "take", "take"),
	}, []model.Post{textWithImage, plain})

	if len(seeds) != 2 {
		t.Fatalf("got %d seeds, want 2", len(seeds))
	}
	byID := map[string]model.IdeaSeed{}
	for _, s := range seeds {
		byID[s.SourcePostID] = s
	}
	if !byID["4"].Visual {
		t.Error("a post with media must be marked visual even without a screenshot")
	}
	if byID["5"].Visual {
		t.Error("a post with no media must not be marked visual")
	}
}

func TestLeadWithCategory(t *testing.T) {
	cases := []struct {
		name     string
		tags     []string
		category string
		claimed  string
		want     []string
	}{
		{
			name:     "category already leads",
			tags:     []string{"take", "fintech", "latency"},
			category: "take",
			claimed:  "take",
			want:     []string{"take", "fintech", "latency"},
		},
		{
			name:     "category missing from the tags",
			tags:     []string{"fintech"},
			category: "repost",
			claimed:  "repost",
			want:     []string{"repost", "fintech"},
		},
		{
			name:     "a different category buried mid-list is dropped",
			tags:     []string{"fintech", "shitpost", "hiring"},
			category: "take",
			claimed:  "take",
			want:     []string{"take", "fintech", "hiring"},
		},
		{
			name:     "no category leaves only topics",
			tags:     []string{"trend", "markets"},
			category: "",
			claimed:  "trend",
			want:     []string{"markets"},
		},
		{
			name:     "blanks and whitespace go",
			tags:     []string{"", "  ", "fintech"},
			category: "take",
			claimed:  "take",
			want:     []string{"take", "fintech"},
		},
		{
			// The whole reason claimed is a separate argument: an invented
			// category is in nobody's known list, so only the raw value drops it.
			name:     "an invented category does not survive as a topic tag",
			tags:     []string{"hot_take", "payments"},
			category: "",
			claimed:  "hot_take",
			want:     []string{"payments"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := leadWithCategory(c.tags, c.category, c.claimed)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}

func TestNormalizeCategory(t *testing.T) {
	for _, in := range []string{"take", "REPOST", " war_story ", "trend"} {
		if _, ok := model.NormalizeCategory(in); !ok {
			t.Errorf("NormalizeCategory(%q) should be known", in)
		}
	}
	for _, in := range []string{"", "hot_take", "quote", "essay"} {
		if got, ok := model.NormalizeCategory(in); ok || got != "" {
			t.Errorf("NormalizeCategory(%q) = %q,%v; want \"\",false", in, got, ok)
		}
	}
	if got, _ := model.NormalizeCategory("  REPOST "); got != model.CategoryRepost {
		t.Errorf("normalization should lowercase and trim, got %q", got)
	}
}
