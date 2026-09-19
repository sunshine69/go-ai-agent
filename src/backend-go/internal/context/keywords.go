// Package context implements the knowledge-routing brain: domain-driven keyword
// extraction, search-term ordering, tool routing, and parallel MCP/RAG retrieval.
// It is a port of src/backend/app/context_builder.py plus src/backend/app/
// domain_config.py (minus the now-dropped skills tools, per project decision).
package context

import (
	"regexp"
	"sort"
	"strings"
)

// stopWords is the explicit stop-word set from context_builder.py::stop_words.
// It includes the deliberately-listed conversational fillers — they must not be
// dropped, since they affect keyword extraction parity.
var stopWords = map[string]bool{
	"this": true, "that": true, "these": true, "those": true, "the": true,
	"a": true, "an": true, "and": true, "or": true, "but": true, "if": true,
	"then": true, "so": true, "of": true, "to": true, "in": true, "on": true,
	"at": true, "for": true, "with": true, "by": true, "from": true,
	"about": true, "into": true, "over": true, "after": true, "is": true,
	"are": true, "was": true, "were": true, "be": true, "been": true,
	"being": true, "am": true, "do": true, "does": true, "did": true,
	"done": true, "have": true, "has": true, "had": true, "having": true,
	"will": true, "would": true, "should": true, "could": true, "can": true,
	"may": true, "might": true, "must": true, "shall": true,
	"i": true, "you": true, "he": true, "she": true, "it": true,
	"we": true, "they": true, "them": true, "his": true, "her": true,
	"its": true, "their": true, "our": true, "your": true, "my": true,
	"me": true, "him": true, "us": true, "myself": true, "himself": true,
	"herself": true, "itself": true, "themselves": true, "ourselves": true,
	"yourselves": true,
	// Conversational fillers explicitly present in the Python set.
	"please": true, "know": true, "wanted": true, "need": true,
	"wants": true, "help": true, "asking": true, "think": true,
	"sure": true, "actually": true, "basically": true, "simply": true,
	"literally": true, "really": true, "just": true, "quite": true,
	"pretty": true, "totally": true, "definitely": true,
}

var wordRe = regexp.MustCompile(`\b[a-z]+(?:\d+[a-z]*)?\b`)

// keywordSuffixes lists the suffixes examined during length-truncation. Each
// entry is {suffix, keepLen}: the truncated token keeps the first keepLen chars
// of the stem. Kept only if the resulting stem has >= 3 characters.
var keywordSuffixes = []struct {
	suffix  string
	keepLen int
}{
	{"ing", 3}, {"tion", 4}, {"ment", 4}, {"ness", 4}, {"ance", 4}, {"ence", 4},
}

// extractKeywordsFromMessage mirrors context_builder.py::extract_keywords_from_message.
// It lowercases, tokenises, drops stopwords, applies length truncation, sorts by
// length descending, and returns at most 5 keywords (deduplicated, preserving order).
func extractKeywordsFromMessage(message string) []string {
	message = strings.ToLower(message)
	tokens := wordRe.FindAllString(message, -1)

	keywords := make([]string, 0, len(tokens))
	seen := make(map[string]bool)

	for _, t := range tokens {
		t = strings.TrimSpace(t)
		if len(t) < 3 {
			continue
		}
		if strings.ContainsAny(t, "0123456789") {
			continue
		}
		if stopWords[t] {
			continue
		}
		if len(t) > 8 {
			for _, suf := range keywordSuffixes {
				if strings.HasSuffix(t, suf.suffix) {
					stem := t[:len(t)-len(suf.suffix)]
					if len(stem) >= suf.keepLen {
						t = stem
					}
				}
			}
		}
		if len(t) < 3 {
			continue
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		keywords = append(keywords, t)
	}

	// Stable sort by length descending, preserving insertion order for ties.
	sort.SliceStable(keywords, func(i, j int) bool {
		return len(keywords[i]) > len(keywords[j])
	})

	if len(keywords) > 5 {
		keywords = keywords[:5]
	}
	return keywords
}
