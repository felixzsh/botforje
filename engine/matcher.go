package engine

import (
	"strings"
	"unicode"
)

// matchThreshold is the lowest trigger score that still transitions.
const matchThreshold = 0.7

func bestScoreFor(text string, triggers []string) float64 {
	best := 0.0
	for _, trigger := range triggers {
		if score := matchScore(text, trigger); score > best {
			best = score
		}
	}
	return best
}

// matchScore scores how well a trigger matches the user text: an exact match
// wins, then the trigger appearing as contiguous words, then all of its words
// in any order, then a typo-tolerant similarity (the fuzzy path).
func matchScore(text, trigger string) float64 {
	a := normalize(text)
	b := normalize(trigger)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	aWords, bWords := strings.Fields(a), strings.Fields(b)
	if hasSequence(aWords, bWords) {
		return 0.9
	}
	if hasAll(aWords, bWords) {
		return 0.7
	}
	if similarity := similarity(a, b); similarity >= 0.8 {
		return similarity
	}
	return 0
}

func normalize(text string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(text) {
		if isWordRune(r) {
			out.WriteRune(r)
		} else {
			out.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func hasSequence(words, sequence []string) bool {
	if len(sequence) > len(words) {
		return false
	}
	for start := 0; start+len(sequence) <= len(words); start++ {
		matched := true
		for i, want := range sequence {
			if words[start+i] != want {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func hasAll(words, wanted []string) bool {
	present := make(map[string]bool, len(words))
	for _, word := range words {
		present[word] = true
	}
	for _, word := range wanted {
		if !present[word] {
			return false
		}
	}
	return true
}

func similarity(a, b string) float64 {
	left, right := []rune(a), []rune(b)
	longest := len(left)
	if len(right) > longest {
		longest = len(right)
	}
	if longest == 0 {
		return 0
	}
	distance := levenshtein(left, right)
	return 1 - float64(distance)/float64(longest)
}

func levenshtein(a, b []rune) int {
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous = current
	}
	return previous[len(b)]
}
