// Package selectors is the ONLY place X/Twitter DOM selectors live.
//
// X ships DOM changes without notice. When the scraper starts returning zero
// posts or empty fields, fix it HERE and nowhere else. Every field is used by
// name in the extraction script, so a stale value shows up in the run log as
// "selector miss: <field>" rather than as a silent empty harvest.
//
// Verified working: 2026-08-30. Bump Version when you edit anything below.
package selectors

const Version = "2026-08-30c"

// S is the selector set. Values are plain CSS, evaluated in page context.
type Set struct {
	// Timeline structure
	Cell    string // the virtualized row wrapper each post sits in
	Tweet   string // the post itself; the unit we extract and screenshot
	Primary string // main column, used to confirm the page rendered

	// Fields inside a post
	Text          string // post body; absent on pure-image posts
	Time          string // <time datetime="..."> inside the permalink anchor
	PermalinkAnc  string // anchor whose href is /<handle>/status/<id>
	UserName      string // display-name + handle block
	SocialContext string // "X reposted" banner row

	// Media markers -> HasMedia
	Photo string
	Video string
	Card  string
	Poll  string

	// Ad marker -> IsPromoted. Only the home timeline injects these; a List
	// never does. Health cannot check this one: a feed with no ads and a broken
	// ad selector look identical from here.
	Promoted string

	// Page-state probes
	LoginLink  string // present when the profile is logged out
	LoginForm  string
	ErrorRetry string // "Something went wrong" retry button
	EmptyState string // list with nothing in it
}

// S is the live set. Everything reads from this.
var S = Set{
	Cell:    `div[data-testid="cellInnerDiv"]`,
	Tweet:   `article[data-testid="tweet"]`,
	Primary: `div[data-testid="primaryColumn"]`,

	Text:          `div[data-testid="tweetText"]`,
	Time:          `time[datetime]`,
	PermalinkAnc:  `a[href*="/status/"]`,
	UserName:      `div[data-testid="User-Name"]`,
	SocialContext: `div[data-testid="socialContext"]`,

	Photo: `div[data-testid="tweetPhoto"], div[data-testid="previewInterstitial"]`,
	Video: `div[data-testid="videoPlayer"], video`,
	Card:  `div[data-testid="card.wrapper"], div[data-testid="card.layoutLarge.media"]`,
	Poll:  `div[data-testid="cardPoll"]`,

	Promoted: `div[data-testid="placementTracking"], [data-testid="promotedIndicator"]`,

	// Verified against the live logged-out splash on 2026-08-30: x.com/home
	// redirects to x.com/ and renders a sign-in form with no "login" anchors at
	// all, so anchor-based probes silently pass and the run then dies 45s later
	// blaming the tweet selector. Match the form itself instead.
	LoginLink: `a[href="/login"], a[href^="/i/flow/login"], a[data-testid="loginButton"], ` +
		`div[data-testid="google_sign_in_container"]`,
	LoginForm: `input[name="username_or_email"], input[name="password"], ` +
		`input[name="text"][autocomplete^="username"]`,
	ErrorRetry: `button[role="button"]:has-text("Retry")`,
	EmptyState: `div[data-testid="empty_state_header_text"]`,
}

// TweetByID targets one post for an element screenshot.
func (s Set) TweetByID(id string) string {
	return s.Tweet + `:has(a[href$="/status/` + id + `"])`
}

// Health counts how often each field came back empty during a run. The scraper
// fills this in and logs it, so DOM drift is visible instead of mysterious.
type Health struct {
	Articles int
	Miss     map[string]int
}

func NewHealth() *Health { return &Health{Miss: map[string]int{}} }

func (h *Health) Note(field string, ok bool) {
	if !ok {
		h.Miss[field]++
	}
}

// Broken returns fields missing on more than ratio of the articles seen —
// the signal that a selector, not the content, is the problem.
func (h *Health) Broken(ratio float64) []string {
	if h.Articles == 0 {
		return nil
	}
	var out []string
	for f, n := range h.Miss {
		if float64(n)/float64(h.Articles) > ratio {
			out = append(out, f)
		}
	}
	return out
}
