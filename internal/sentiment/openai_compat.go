package sentiment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go"
)

// scoreWithOpenAI sends headlines to any OpenAI-compatible API
// (NVIDIA NIM, Groq, Together, OpenAI, etc.) and parses the response.
func (a *Analyzer) scoreWithOpenAI(ctx context.Context, headlines []string) (SentimentResult, error) {
	bulletList := "- " + strings.Join(headlines, "\n- ")
	userPrompt := fmt.Sprintf(userPromptTemplate, bulletList)

	resp, err := a.openaiClient.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:     a.openaiModel,
		MaxTokens: openai.Int(256),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemPrompt),
			openai.UserMessage(userPrompt),
		},
	})
	if err != nil {
		return SentimentResult{}, fmt.Errorf("openai-compat api: %w", err)
	}

	if len(resp.Choices) == 0 {
		return SentimentResult{}, fmt.Errorf("openai-compat returned no choices")
	}
	raw := resp.Choices[0].Message.Content
	if raw == "" {
		return SentimentResult{}, fmt.Errorf("openai-compat returned empty content")
	}

	raw = stripCodeFence(raw)

	var parsed struct {
		Score      int     `json:"score"`
		Direction  string  `json:"direction"`
		Confidence float64 `json:"confidence"`
		Reason     string  `json:"reason"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return SentimentResult{}, fmt.Errorf("parse openai-compat JSON: %w (body: %s)", err, truncate(raw, 200))
	}

	return SentimentResult{
		Score:      clamp(parsed.Score, -5, 5),
		Direction:  normalizeDirection(parsed.Direction),
		Confidence: clampf(parsed.Confidence, 0, 1),
		Reason:     parsed.Reason,
		FetchedAt:  time.Now(),
	}, nil
}
