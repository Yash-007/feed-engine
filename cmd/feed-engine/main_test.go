package main

import (
	"testing"

	"github.com/Yash-007/feed-engine/internal/model"
)

func seedOf(id, category string) model.IdeaSeed {
	return model.IdeaSeed{ID: id, Category: category}
}

func TestCapCategories(t *testing.T) {
	// What a real run looks like: the text batch respects the cap on its own,
	// then the vision batch pushes the same category over it.
	in := []model.IdeaSeed{
		seedOf("a", "take"), seedOf("b", "shitpost"), seedOf("c", "take"),
		seedOf("d", "take"), seedOf("e", "trend"), seedOf("f", "take"),
		seedOf("g", "take"), // the vision batch's fifth take
	}

	out, dropped := capCategories(in, 4)
	if len(out) != 6 {
		t.Fatalf("kept %d seeds, want 6: %+v", len(out), out)
	}
	if dropped["take"] != 1 || len(dropped) != 1 {
		t.Fatalf("dropped = %v, want exactly one take", dropped)
	}

	counts := map[string]int{}
	for _, s := range out {
		counts[s.Category]++
	}
	if counts["take"] != 4 {
		t.Errorf("take = %d, want 4", counts["take"])
	}
	if counts["shitpost"] != 1 || counts["trend"] != 1 {
		t.Errorf("other categories should be untouched: %v", counts)
	}
	// The survivors are the earliest ones, so a later weak seed cannot evict an
	// earlier strong one.
	if out[len(out)-1].ID != "f" {
		t.Errorf("last kept = %q, want the 4th take (f)", out[len(out)-1].ID)
	}
}

func TestCapCategoriesDisabled(t *testing.T) {
	in := []model.IdeaSeed{seedOf("a", "take"), seedOf("b", "take"), seedOf("c", "take")}
	out, dropped := capCategories(in, 0)
	if len(out) != 3 || len(dropped) != 0 {
		t.Fatalf("cap 0 must pass everything through, got %d kept %v", len(out), dropped)
	}
}

func TestTruncateDoesNotSplitRunes(t *testing.T) {
	// 300 bytes would cut this mid-character; 300 runes must not.
	s := ""
	for i := 0; i < 400; i++ {
		s += "é"
	}
	got := truncate(s, 300)
	if n := len([]rune(got)); n != 300 {
		t.Fatalf("got %d runes, want 300", n)
	}
	for _, r := range got {
		if r == '�' {
			t.Fatal("truncate produced a replacement character")
		}
	}
	if short := truncate("abc", 300); short != "abc" {
		t.Errorf("short strings must pass through unchanged, got %q", short)
	}
}
