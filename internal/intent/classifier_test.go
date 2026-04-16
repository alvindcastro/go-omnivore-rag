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
		{"When is the add/drop deadline?", RegistrationBanner},
		{"How do I register for next semester?", RegistrationBanner},
		{"When are my tuition fees due?", FinanceBanner},
		{"How do I request an official transcript?", TranscriptSop},
		{"There is a financial hold on my account", HoldsSop},
		{"What changed in Banner General 9.3.37?", ReleaseSummary},
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
	for _, msg := range []string{"register for COMP 1234", "pay my fees", "transcript request"} {
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
		RegistrationBanner: []string{"enroll"},
	}
	c := NewClassifier(cfg)
	assert.Equal(t, RegistrationBanner, c.Classify("how do I enroll?").Intent)
}

func TestClassifier_ReleaseSummaryDetectsVersion(t *testing.T) {
	c := NewClassifier(DefaultIntentConfig())
	for _, msg := range []string{
		"What changed in 9.3.37?",
		"show me the breaking changes for Banner",
		"release notes for Student 9.4",
	} {
		assert.Equal(t, ReleaseSummary, c.Classify(msg).Intent, "input: %q", msg)
	}
}
