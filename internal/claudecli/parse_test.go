package claudecli

import (
	"testing"

	"github.com/Yash-007/feed-engine/internal/model"
)

// The wire format the prompt asks for has to survive a round trip, or seeds land
// in the bank with empty fields.
func TestParseSeedsWireFormat(t *testing.T) {
	in := `[{"client_seed_id":"harvest-123","source":"harvest","platform":"x",
	  "post_author":"@someone","post_text":"the original post",
	  "category":"war_story","theme_tags":["war_story","fintech","latency"],
	  "tension":"the generalized pattern","angle_hint":"the insider take",
	  "shelf_life":"evergreen"}]`

	got, err := parseSeeds(in)
	if err != nil {
		t.Fatalf("parseSeeds: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d seeds, want 1", len(got))
	}
	s := got[0]
	if s.ClientSeedID != model.SeedIDPrefix+"123" {
		t.Errorf("client_seed_id = %q", s.ClientSeedID)
	}
	if s.Category != "war_story" || s.ShelfLife != "evergreen" {
		t.Errorf("category/shelf_life = %q/%q", s.Category, s.ShelfLife)
	}
	if s.PostAuthor != "@someone" || s.PostText != "the original post" {
		t.Errorf("post_author/post_text = %q/%q", s.PostAuthor, s.PostText)
	}
	if len(s.ThemeTags) != 3 || s.ThemeTags[0] != s.Category {
		t.Errorf("theme_tags must lead with the category, got %v", s.ThemeTags)
	}
}

// The CLI can wrap the array in a fence or prose; the salvage pass has to cope
// with every shape we've actually seen come back.
func TestParseSeeds(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  int
		fails bool
	}{
		{name: "bare array", in: `[{"client_seed_id":"harvest-1","category":"take"}]`, want: 1},
		{name: "empty array", in: "[]", want: 0},
		{name: "fenced json", in: "```json\n[{\"client_seed_id\":\"1\"}]\n```", want: 1},
		{name: "fenced no lang", in: "```\n[{\"client_seed_id\":\"1\"},{\"client_seed_id\":\"2\"}]\n```", want: 2},
		{name: "prose wrapped", in: "Here are the seeds:\n[{\"client_seed_id\":\"9\"}]\nHope that helps!", want: 1},
		{name: "leading whitespace", in: "\n\n  [{\"client_seed_id\":\"1\"}]  \n", want: 1},
		{name: "empty output", in: "   ", fails: true},
		{name: "no array at all", in: "I could not find anything worth keeping.", fails: true},
		{name: "object not array", in: `{"client_seed_id":"1"}`, fails: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseSeeds(c.in)
			if c.fails {
				if err == nil {
					t.Fatalf("expected a parse error, got %d seeds", len(got))
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSeeds: %v", err)
			}
			if len(got) != c.want {
				t.Fatalf("got %d seeds, want %d", len(got), c.want)
			}
		})
	}
}
