package intent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifier_KnownIntents(t *testing.T) {
	c := NewClassifier(DefaultIntentConfig())
	cases := []struct {
		input    string
		expected Intent
	}{
		{"What changed in Banner version 9.3.37?", BannerRelease},
		{"Show me the release notes for this upgrade", BannerRelease},
		{"What are the breaking changes in 9.4?", BannerRelease},
		{"What is the General Ledger structure in Banner?", BannerFinance},
		{"What is the budget balance for the grants fund?", BannerFinance},
		{"What are the steps to approve a Banner requisition?", SopQuery},
		{"How do I process a transcript request?", SopQuery},
		{"What Banner module parameters control FGAC access?", BannerAdmin},
		{"What is the weather today?", General},
		{"help", General},
	}
	for _, tc := range cases {
		result := c.Classify(tc.input)
		assert.Equal(t, tc.expected, result.Intent, "input: %q", tc.input)
	}
}

func TestClassifier_ConfidenceRange(t *testing.T) {
	c := NewClassifier(DefaultIntentConfig())
	for _, msg := range []string{"show me release notes", "what is the budget balance", "how do I run a report"} {
		r := c.Classify(msg)
		assert.GreaterOrEqual(t, r.Confidence, 0.0)
		assert.LessOrEqual(t, r.Confidence, 1.0)
	}
}

func TestClassifier_AmbiguousDefaultsToGeneral(t *testing.T) {
	c := NewClassifier(DefaultIntentConfig())
	r := c.Classify("I have a question")
	assert.Equal(t, General, r.Intent)
	assert.Less(t, r.Confidence, 0.3)
}

func TestClassifier_CustomConfig(t *testing.T) {
	cfg := IntentConfig{
		SopQuery: []string{"procedure"},
	}
	c := NewClassifier(cfg)
	assert.Equal(t, SopQuery, c.Classify("what is the procedure for this?").Intent)
}

func TestClassifier_BannerReleaseDetectsVersion(t *testing.T) {
	c := NewClassifier(DefaultIntentConfig())
	for _, msg := range []string{
		"What changed in 9.3.37?",
		"show me the breaking changes for Banner",
		"release notes for 9.4",
	} {
		assert.Equal(t, BannerRelease, c.Classify(msg).Intent, "input: %q", msg)
	}
}

func TestClassifier_BannerUsage(t *testing.T) {
	c := NewClassifier(DefaultIntentConfig())
	cases := []struct {
		input    string
		expected Intent
	}{
		{"How do I navigate the Banner main menu?", BannerUsage},
		{"Where do I find the journal entry form in Banner?", BannerUsage},
		{"How to use the Banner Finance module", BannerUsage},
		{"How to restart the Banner server", SopQuery},
		{"What changed in Banner Finance?", BannerRelease},
		{"What is Banner Access Management?", BannerUsage},
		{"What is SOAIDEN in Banner?", BannerUsage},
		{"What is the FGAJVCD form in Banner?", BannerUsage},
	}
	for _, tc := range cases {
		result := c.Classify(tc.input)
		assert.Equal(t, tc.expected, result.Intent, "input: %q", tc.input)
	}
}
