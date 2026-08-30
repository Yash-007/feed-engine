package claudecli

import "testing"

// The CLI can wrap the array in a fence or prose; the salvage pass has to cope
// with every shape we've actually seen come back.
func TestParseSeeds(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  int
		fails bool
	}{
		{name: "bare array", in: `[{"post_id":"1","category":"insight"}]`, want: 1},
		{name: "empty array", in: "[]", want: 0},
		{name: "fenced json", in: "```json\n[{\"post_id\":\"1\"}]\n```", want: 1},
		{name: "fenced no lang", in: "```\n[{\"post_id\":\"1\"},{\"post_id\":\"2\"}]\n```", want: 2},
		{name: "prose wrapped", in: "Here are the seeds:\n[{\"post_id\":\"9\"}]\nHope that helps!", want: 1},
		{name: "leading whitespace", in: "\n\n  [{\"post_id\":\"1\"}]  \n", want: 1},
		{name: "empty output", in: "   ", fails: true},
		{name: "no array at all", in: "I could not find anything worth keeping.", fails: true},
		{name: "object not array", in: `{"post_id":"1"}`, fails: true},
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
