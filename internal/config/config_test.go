package config

import "testing"

func TestTimelineURL(t *testing.T) {
	ok := []string{
		"https://x.com/home",
		"https://x.com/home/",
		"https://www.x.com/home",
		"https://twitter.com/home",
		"https://x.com",
		"https://x.com/i/lists/1234567890",
		"https://twitter.com/i/lists/1234567890",
	}
	for _, u := range ok {
		if err := timelineURL(u); err != nil {
			t.Errorf("timelineURL(%q) = %v, want nil", u, err)
		}
	}

	bad := []string{
		"https://x.com/someone",            // a profile, not an endless timeline
		"https://x.com/someone/status/123", // a single post
		"https://x.com/search?q=go",        // search paginates differently
		"https://example.com/home",         // not X at all
		"https://x.com/i/bookmarks",        // not a list or the home feed
	}
	for _, u := range bad {
		if err := timelineURL(u); err == nil {
			t.Errorf("timelineURL(%q) = nil, want an error", u)
		}
	}
}
