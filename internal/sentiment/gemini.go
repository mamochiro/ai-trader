package sentiment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"google.golang.org/genai"
)

// scoreWithGemini sends the headlines to Google Gemini and parses the
// structured JSON response into a SentimentResult.
func (a *Analyzer) scoreWithGemini(ctx context.Context, headlines []string) (SentimentResult, error) {
	bulletList := "- " + strings.Join(headlines, "\n- ")
	userPrompt := fmt.Sprintf(userPromptTemplate, bulletList)

	result, err := a.gemini.Models.GenerateContent(ctx, a.geminiModel, genai.Text(systemPrompt+"\n\n"+userPrompt), &genai.GenerateContentConfig{
		MaxOutputTokens: 256,
		Temperature:     genai.Ptr(float32(0.2)),
	})
	if err != nil {
		return SentimentResult{}, fmt.Errorf("gemini api: %w", err)
	}

	raw := result.Text()
	if raw == "" {
		return SentimentResult{}, fmt.Errorf("gemini returned no text content")
	}

	raw = stripCodeFence(raw)

	var parsed struct {
		Score      int     `json:"score"`
		Direction  string  `json:"direction"`
		Confidence float64 `json:"confidence"`
		Reason     string  `json:"reason"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return SentimentResult{}, fmt.Errorf("parse gemini JSON: %w (body: %s)", err, truncate(raw, 200))
	}

	return SentimentResult{
		Score:      clamp(parsed.Score, -5, 5),
		Direction:  normalizeDirection(parsed.Direction),
		Confidence: clampf(parsed.Confidence, 0, 1),
		Reason:     parsed.Reason,
		FetchedAt:  time.Now(),
	}, nil
}
