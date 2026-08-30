// Package prefilter throws away posts that are not worth a Claude call:
// too short, no text at all, or already harvested on an earlier run.
package prefilter

import (
	"strings"

	"github.com/Yash-007/feed-engine/internal/config"
	"github.com/Yash-007/feed-engine/internal/model"
)

type Stats struct {
	In        int
	TooShort  int
	NoText    int
	Seen      int
	Kept      int
	KeptText  int
	KeptShots int
}

// Seen is the subset of the seen-id store this package needs.
type Seen interface{ Has(id string) bool }

type Filter struct {
	cfg  config.Prefilter
	seen Seen
}

func New(cfg config.Prefilter, s Seen) *Filter { return &Filter{cfg: cfg, seen: s} }

// WantShot is the screenshot policy, handed to the scraper: a post carries a
// visual AND says little in words, so the meaning lives in the image.
// Already-seen posts never get shot — no point writing a PNG we won't send.
func (f *Filter) WantShot(p model.Post) bool {
	if !p.HasMedia || f.seen.Has(p.ID) {
		return false
	}
	return p.WordCount < f.cfg.VisualMaxWords
}

// Apply splits scraped posts into what goes to the text call, what goes to the
// vision call, and what gets dropped.
func (f *Filter) Apply(posts []model.Post) (text, visual []model.Post, st Stats) {
	st.In = len(posts)
	for _, p := range posts {
		body := strings.TrimSpace(p.Text)

		switch {
		case f.seen.Has(p.ID):
			st.Seen++
			continue
		// A visual post is allowed to be short — the image carries it. Everything
		// else has to clear the word floor.
		case p.HasVisual:
			// keep
		case body == "":
			st.NoText++
			continue
		case p.WordCount < f.cfg.MinWords:
			st.TooShort++
			continue
		}

		st.Kept++
		if p.HasVisual && p.ImagePath != "" {
			st.KeptShots++
			visual = append(visual, p)
		} else {
			st.KeptText++
			text = append(text, p)
		}
	}
	return text, visual, st
}
