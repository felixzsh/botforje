package engine

import (
	"strings"
	"unicode"

	"github.com/felixzsh/botflota"
)

// matchThreshold is the lowest trigger score that still transitions.
const matchThreshold = 0.7

// bestTriggerMatch returns the highest-scoring trigger of an edge and the
// matching layer that produced it, for the engine to record (ADR 0019).
func bestTriggerMatch(text string, triggers []string) (float64, botflota.RoutingMethod) {
	best := 0.0
	bestMethod := botflota.RoutingMethod("")
	for _, trigger := range triggers {
		score, method := matchTrigger(text, trigger)
		if score > best {
			best, bestMethod = score, method
		}
	}
	return best, bestMethod
}

// matchScore scores how well a trigger matches the user text.
func matchScore(text, trigger string) float64 {
	score, _ := matchTrigger(text, trigger)
	return score
}

// matchTrigger scores a trigger against the user text and names the layer that
// won (ADR 0019): an exact match, then the trigger appearing as contiguous
// words, then all of its words in any order, then a typo-tolerant similarity
// (the fuzzy path). A score below the threshold returns no layer.
func matchTrigger(text, trigger string) (float64, botflota.RoutingMethod) {
	a := normalize(text)
	b := normalize(trigger)
	if a == "" || b == "" {
		return 0, ""
	}
	if a == b {
		return 1, botflota.RoutingTriggerExact
	}
	aWords, bWords := strings.Fields(a), strings.Fields(b)
	if hasSequence(aWords, bWords) {
		return 0.9, botflota.RoutingTriggerSequence
	}
	if hasAll(aWords, bWords) {
		return 0.7, botflota.RoutingTriggerWords
	}
	if sim := similarity(a, b); sim >= 0.8 {
		return sim, botflota.RoutingTriggerFuzzy
	}
	return 0, ""
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
