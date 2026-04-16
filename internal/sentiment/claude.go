package sentiment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

const (
	systemPrompt = "You are a crypto market sentiment analyzer. Analyze news headlines and return ONLY a JSON object."

	userPromptTemplate = `Analyze these BTC news headlines and return sentiment score:
Headlines:
%s

Return ONLY this JSON (no explanation):
{
  "score": <integer -5 to +5>,
  "direction": <"BULLISH" | "BEARISH" | "NEUTRAL">,
  "confidence": <float 0.0-1.0>,
  "reason": <one sentence max>
}`
)

// scoreSentiment sends the headlines to Claude and parses the
// structured JSON response into a SentimentResult.
func (a *Analyzer) scoreSentiment(ctx context.Context, headlines []string) (SentimentResult, error) {
	bulletList := "- " + strings.Join(headlines, "\n- ")
	userPrompt := fmt.Sprintf(userPromptTemplate, bulletList)

	msg, err := a.claude.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeHaiku4_5,
		MaxTokens: 256,
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(userPrompt)),
		},
	})
	if err != nil {
		return SentimentResult{}, fmt.Errorf("claude api: %w", err)
	}

	// Extract the first text block from the response.
	var raw string
	for _, block := range msg.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			raw = tb.Text
			break
		}
	}
	if raw == "" {
		return SentimentResult{}, fmt.Errorf("claude returned no text content")
	}

	// Claude should return bare JSON but may wrap it in markdown fences.
	raw = stripCodeFence(raw)

	var parsed struct {
		Score      int     `json:"score"`
		Direction  string  `json:"direction"`
		Confidence float64 `json:"confidence"`
		Reason     string  `json:"reason"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return SentimentResult{}, fmt.Errorf("parse claude JSON: %w (body: %s)", err, truncate(raw, 200))
	}

	return SentimentResult{
		Score:      clamp(parsed.Score, -5, 5),
		Direction:  normalizeDirection(parsed.Direction),
		Confidence: clampf(parsed.Confidence, 0, 1),
		Reason:     parsed.Reason,
		FetchedAt:  time.Now(),
	}, nil
}

// stripCodeFence removes ```json ... ``` wrapping if present.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Skip the opening fence line (```json or just ```).
	if i := strings.Index(s[3:], "\n"); i >= 0 {
		s = s[3+i+1:]
	}
	if j := strings.LastIndex(s, "```"); j >= 0 {
		s = s[:j]
	}
	return strings.TrimSpace(s)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampf(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func normalizeDirection(d string) string {
	switch strings.ToUpper(strings.TrimSpace(d)) {
	case "BULLISH":
		return "BULLISH"
	case "BEARISH":
		return "BEARISH"
	default:
		return "NEUTRAL"
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
