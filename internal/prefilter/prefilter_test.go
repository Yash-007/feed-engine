package prefilter

import (
	"testing"

	"github.com/Yash-007/feed-engine/internal/config"
	"github.com/Yash-007/feed-engine/internal/model"
)

type fakeSeen map[string]bool

func (f fakeSeen) Has(id string) bool { return f[id] }

func post(id, text string, words int, media, visual bool) model.Post {
	p := model.Post{ID: id, Text: text, WordCount: words, HasMedia: media, HasVisual: visual}
	if visual {
		p.ImagePath = "/tmp/" + id + ".png"
	}
	return p
}

func TestOwnPostsNeverSurvive(t *testing.T) {
	cfg := config.Prefilter{MinWords: 10, VisualMaxWords: 15, OwnHandle: "yashx_404"}
	f := New(cfg, fakeSeen{})

	mine := post("mine", "a genuinely substantial post of my own with plenty of words in it", 13, false, false)
	mine.Author = "yashx_404"
	upper := post("upper", "another substantial post of mine written with plenty of words here", 12, false, false)
	upper.Author = "YashX_404" // handle case must not matter
	atSign := post("at", "and one more substantial post of mine with plenty of words in it", 13, false, false)
	atSign.Author = "@yashx_404"
	other := post("other", "someone else made this point with plenty of words in the body", 12, false, false)
	other.Author = "someoneelse"

	text, _, st := f.Apply([]model.Post{mine, upper, atSign, other})
	if st.Own != 3 {
		t.Fatalf("own = %d, want 3: %+v", st.Own, st)
	}
	if len(text) != 1 || text[0].ID != "other" {
		t.Fatalf("only the other author should survive, got %+v", text)
	}

	// And they must never be screenshotted either, so no tokens or PNGs are spent.
	shot := model.Post{ID: "s", Author: "yashx_404", WordCount: 3, HasMedia: true}
	if f.WantShot(shot) {
		t.Error("WantShot must refuse the operator's own post")
	}

	// Empty handle disables the rule rather than dropping everything.
	off := New(config.Prefilter{MinWords: 10, VisualMaxWords: 15}, fakeSeen{})
	if _, _, s2 := off.Apply([]model.Post{mine}); s2.Own != 0 || s2.Kept != 1 {
		t.Errorf("empty own_handle must disable the rule, got %+v", s2)
	}
}

func TestApply(t *testing.T) {
	cfg := config.Prefilter{MinWords: 10, VisualMaxWords: 15}
	f := New(cfg, fakeSeen{"old": true})

	ad := post("ad", "our platform helps teams ship faster than ever, start your free trial", 13, false, false)
	ad.IsPromoted = true

	text, visual, st := f.Apply([]model.Post{
		post("long", "a post with clearly more than ten words in its body for sure", 13, false, false),
		post("short", "too short", 2, false, false),
		post("notext", "", 0, true, false),
		post("old", "already harvested on an earlier run with plenty of words here", 11, false, false),
		post("chart", "look at this", 3, true, true), // short but the image carries it
		ad, // long enough to pass the word floor, still an ad
	})

	if st.In != 6 || st.Kept != 2 {
		t.Fatalf("kept %d of %d, want 2: %+v", st.Kept, st.In, st)
	}
	if st.TooShort != 1 || st.NoText != 1 || st.Seen != 1 || st.Promoted != 1 {
		t.Fatalf("drop counts wrong: %+v", st)
	}
	if len(text) != 1 || text[0].ID != "long" {
		t.Fatalf("text batch wrong: %+v", text)
	}
	if len(visual) != 1 || visual[0].ID != "chart" {
		t.Fatalf("visual batch wrong: %+v", visual)
	}
}

func TestWantShot(t *testing.T) {
	f := New(config.Prefilter{MinWords: 10, VisualMaxWords: 15}, fakeSeen{"seen": true})

	cases := []struct {
		name string
		p    model.Post
		want bool
	}{
		{"media and short text", post("a", "chart go up", 3, true, false), true},
		{"media but wordy", post("b", "long caption", 40, true, false), false},
		{"no media", post("c", "short", 3, false, false), false},
		{"already seen", model.Post{ID: "seen", WordCount: 3, HasMedia: true}, false},
		{"promoted", model.Post{ID: "d", WordCount: 3, HasMedia: true, IsPromoted: true}, false},
	}
	for _, c := range cases {
		if got := f.WantShot(c.p); got != c.want {
			t.Errorf("%s: WantShot = %v, want %v", c.name, got, c.want)
		}
	}
}
