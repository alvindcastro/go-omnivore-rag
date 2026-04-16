package intent

import (
	"strings"
)

// Intent represents one of the 6 classified routing intents.
type Intent string

const (
	BannerRelease Intent = "BannerRelease"
	BannerFinance Intent = "BannerFinance"
	SopQuery      Intent = "SopQuery"
	BannerAdmin   Intent = "BannerAdmin"
	BannerUsage   Intent = "BannerUsage"
	General       Intent = "General"
)

// IntentResult holds the classified intent and a normalized confidence (0–1).
type IntentResult struct {
	Intent     Intent
	Confidence float64
}

// IntentConfig maps each intent to its keyword list.
// Leave a slice nil or empty to use no keywords for that intent (General is always the fallback).
type IntentConfig struct {
	BannerRelease []string
	BannerFinance []string
	SopQuery      []string
	BannerAdmin   []string
	BannerUsage   []string
}

// DefaultIntentConfig returns the production keyword configuration.
func DefaultIntentConfig() IntentConfig {
	return IntentConfig{
		BannerRelease: []string{
			"what changed", "breaking changes", "release notes",
			"release", "version", "upgrade", "patch", "9.", "compatibility",
		},
		BannerFinance: []string{
			"general ledger", "accounts receivable", "journal entry", "fund code",
			"budget", "grant", "fiscal", "encumbrance", "finance",
		},
		SopQuery: []string{
			"how to", "how do i", "steps to", "step by step",
			"procedure", "sop", "process", "guide",
		},
		BannerAdmin: []string{
			"configure", "configuration", "security role", "parameter",
			"permission", "module", "install", "setup",
		},
		BannerUsage: []string{
			"how to use", "how do i navigate", "navigate banner", "banner navigation",
			"banner form", "banner screen", "banner menu",
			"user guide", "where do i", "look up a",
			"what is the banner", "how do i find",
			"what is banner", "in banner",
		},
	}
}

// Classifier classifies messages into one of the 6 intents.
type Classifier struct {
	cfg IntentConfig
}

// NewClassifier returns a Classifier configured with the given IntentConfig.
func NewClassifier(cfg IntentConfig) *Classifier {
	return &Classifier{cfg: cfg}
}

// Classify returns the IntentResult for message.
// Scoring: each matched keyword contributes weight proportional to phrase length
// (longer phrases are more specific and score higher). The winning intent must
// score >= 0.3 to be selected; otherwise General is returned with low confidence.
func (c *Classifier) Classify(message string) IntentResult {
	lower := strings.ToLower(message)

	type candidate struct {
		intent Intent
		words  []string
	}
	candidates := []candidate{
		{BannerRelease, c.cfg.BannerRelease},
		{BannerFinance, c.cfg.BannerFinance},
		{SopQuery, c.cfg.SopQuery},
		{BannerAdmin, c.cfg.BannerAdmin},
		{BannerUsage, c.cfg.BannerUsage},
	}

	scores := make(map[Intent]float64, len(candidates))
	for _, cand := range candidates {
		scores[cand.intent] = scoreKeywords(lower, cand.words)
	}

	best := General
	bestScore := 0.0
	for _, cand := range candidates {
		s := scores[cand.intent]
		if s > bestScore {
			bestScore = s
			best = cand.intent
		}
	}

	if bestScore < 0.3 {
		return IntentResult{Intent: General, Confidence: bestScore}
	}

	// Normalize: cap at 1.0
	confidence := bestScore
	if confidence > 1.0 {
		confidence = 1.0
	}
	return IntentResult{Intent: best, Confidence: confidence}
}

// scoreKeywords sums weights for matched keywords in lower.
// Weight per keyword = word count of the keyword phrase * 0.3 (so multi-word phrases score higher).
// A single-word match scores exactly 0.3, which meets the 0.3 selection threshold.
func scoreKeywords(lower string, keywords []string) float64 {
	score := 0.0
	for _, kw := range keywords {
		kl := strings.ToLower(kw)
		if strings.Contains(lower, kl) {
			wordCount := len(strings.Fields(kl))
			score += float64(wordCount) * 0.3
		}
	}
	return score
}
