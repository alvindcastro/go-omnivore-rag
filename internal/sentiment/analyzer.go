package sentiment

import (
	"strings"
	"unicode"
)

// Sentiment represents the classified emotional tone of a message.
type Sentiment string

const (
	Positive   Sentiment = "Positive"
	Neutral    Sentiment = "Neutral"
	Frustrated Sentiment = "Frustrated"
)

// SentimentResult holds the classification and a normalized score (0–1).
type SentimentResult struct {
	Sentiment Sentiment
	Score     float64
}

// Config holds keyword lists used by the Analyzer.
// Use DefaultConfig() for production defaults or inject custom lists in tests.
type Config struct {
	FrustratedKeywords []string
	PositiveKeywords   []string
}

// DefaultConfig returns the production keyword configuration.
func DefaultConfig() Config {
	return Config{
		FrustratedKeywords: []string{
			"useless", "ridiculous", "not working", "nobody", "waiting too long",
			"broken", "error", "can't", "doesn't work", "terrible",
		},
		PositiveKeywords: []string{
			"thank", "thanks", "helpful", "great", "perfect", "works",
		},
	}
}

// Analyzer classifies messages using rule-based heuristics.
type Analyzer struct {
	cfg Config
}

// NewAnalyzer returns an Analyzer configured with the given Config.
func NewAnalyzer(cfg Config) *Analyzer {
	return &Analyzer{cfg: cfg}
}

// Analyze classifies message and returns a SentimentResult.
// Scoring combines:
//  1. All-caps ratio: ALL_CAPS words / total words
//  2. Punctuation density: (! + ? count) / message length
//  3. Keyword match weight: each frustrated keyword match += 0.25 (capped at 1.0)
//
// Frustration wins over Positive when both match.
func (a *Analyzer) Analyze(message string) SentimentResult {
	lower := strings.ToLower(message)

	frustratedScore := a.frustratedScore(message, lower)
	positiveHit := a.hasKeyword(lower, a.cfg.PositiveKeywords)

	switch {
	case frustratedScore > 0.6:
		return SentimentResult{Sentiment: Frustrated, Score: clamp(frustratedScore)}
	case positiveHit && frustratedScore <= 0.6:
		// Positive score: fixed 0.75 for a keyword hit
		return SentimentResult{Sentiment: Positive, Score: 0.75}
	default:
		// Neutral: return the frustrated score as a low indicator
		return SentimentResult{Sentiment: Neutral, Score: clamp(frustratedScore)}
	}
}

// frustratedScore combines caps ratio, punctuation density, and keyword hits.
// When any keyword matches, the floor is 0.7 so a single frustrated keyword
// always produces a score > 0.6 (the Frustrated classification threshold).
func (a *Analyzer) frustratedScore(original, lower string) float64 {
	words := strings.Fields(original)
	if len(words) == 0 {
		return 0
	}

	// 1. All-caps ratio
	capsCount := 0
	for _, w := range words {
		if isAllCaps(w) && len(w) > 1 {
			capsCount++
		}
	}
	capsRatio := float64(capsCount) / float64(len(words))

	// 2. Punctuation density
	punctCount := 0
	for _, ch := range original {
		if ch == '!' || ch == '?' {
			punctCount++
		}
	}
	msgLen := len([]rune(original))
	var punctDensity float64
	if msgLen > 0 {
		punctDensity = float64(punctCount) / float64(msgLen)
	}
	// Scale punctuation density; cap at 0.4 contribution
	punctScore := min(punctDensity*10, 0.4)

	// 3. Keyword match weight: each match += 0.25, capped at 1.0
	keywordScore := 0.0
	keywordHit := false
	for _, kw := range a.cfg.FrustratedKeywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			keywordScore += 0.25
			keywordHit = true
		}
	}
	keywordScore = min(keywordScore, 1.0)

	// Combine heuristics
	combined := clamp(capsRatio*0.3 + punctScore*0.2 + keywordScore*0.5)

	// A confirmed keyword hit guarantees the score reflects strong frustration signal
	if keywordHit && combined < 0.7 {
		combined = 0.7
	}

	return combined
}

// hasKeyword returns true if any keyword from list appears in lower.
func (a *Analyzer) hasKeyword(lower string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// isAllCaps returns true if every letter in s is uppercase.
func isAllCaps(s string) bool {
	hasLetter := false
	for _, ch := range s {
		if unicode.IsLetter(ch) {
			hasLetter = true
			if unicode.IsLower(ch) {
				return false
			}
		}
	}
	return hasLetter
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
