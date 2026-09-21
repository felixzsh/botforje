package engine

import "testing"

// Every shape of match must clear the routing threshold and unrelated input
// must not; the exact scores stay free so tuning does not break the test.
func TestMatchScore(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		trigger string
		match   bool
	}{
		{"exact", "hours", "hours", true},
		{"exact with punctuation", "1.", "1", true},
		{"contiguous words", "what are your hours", "hours", true},
		{"all words any order", "menu please now", "menu now", true},
		{"typo", "hourse", "hours", true},
		{"similar but under the fuzzy floor", "housx", "hours", false},
		{"unrelated", "zzz", "hours", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			score := matchScore(tc.text, tc.trigger)
			if got := score >= matchThreshold; got != tc.match {
				t.Fatalf("matchScore(%q, %q) = %v, want match=%v",
					tc.text, tc.trigger, score, tc.match)
			}
		})
	}

	// The best score wins the transition, so a stronger shape of match must
	// keep outranking a weaker one.
	exact := matchScore("hours", "hours")
	contiguous := matchScore("what are your hours", "hours")
	all := matchScore("menu please now", "menu now")
	if exact <= contiguous || contiguous <= all {
		t.Fatalf("layers out of order: exact=%v contiguous=%v all=%v", exact, contiguous, all)
	}
}
