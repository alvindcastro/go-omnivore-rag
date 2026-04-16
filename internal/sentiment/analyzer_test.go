package sentiment

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAnalyzer_FrustratedMessages(t *testing.T) {
	a := NewAnalyzer(DefaultConfig())
	cases := []struct {
		input    string
		expected Sentiment
	}{
		{"WHY IS THIS NOT WORKING???", Frustrated},
		{"I've been waiting 3 days and nobody answers", Frustrated},
		{"This system is absolutely useless!!", Frustrated},
		{"I keep getting an error and this is ridiculous", Frustrated},
	}
	for _, c := range cases {
		result := a.Analyze(c.input)
		assert.Equal(t, Frustrated, result.Sentiment, "input: %q", c.input)
		assert.Greater(t, result.Score, 0.6, "input: %q", c.input)
	}
}

func TestAnalyzer_NeutralMessages(t *testing.T) {
	a := NewAnalyzer(DefaultConfig())
	for _, msg := range []string{
		"When is the add/drop deadline?",
		"How do I pay my fees?",
		"I need a transcript",
	} {
		result := a.Analyze(msg)
		assert.Equal(t, Neutral, result.Sentiment, "input: %q", msg)
	}
}

func TestAnalyzer_PositiveMessages(t *testing.T) {
	a := NewAnalyzer(DefaultConfig())
	assert.Equal(t, Positive, a.Analyze("Thank you that was really helpful!").Sentiment)
}

func TestAnalyzer_ScoreInRange(t *testing.T) {
	a := NewAnalyzer(DefaultConfig())
	for _, msg := range []string{"hello", "HELP ME NOW!!!", "thanks"} {
		r := a.Analyze(msg)
		assert.GreaterOrEqual(t, r.Score, 0.0)
		assert.LessOrEqual(t, r.Score, 1.0)
	}
}

func TestAnalyzer_CustomConfig(t *testing.T) {
	cfg := Config{
		FrustratedKeywords: []string{"banana"},
		PositiveKeywords:   []string{"mango"},
	}
	a := NewAnalyzer(cfg)
	assert.Equal(t, Frustrated, a.Analyze("this banana situation").Sentiment)
	assert.Equal(t, Positive, a.Analyze("I love mango").Sentiment)
}
