// Package sentiment fetches recent crypto news headlines and scores
// them for bullish/bearish bias via an LLM (Claude or Gemini).
// Set LLM_PROVIDER=gemini to use Gemini, otherwise Claude is used.
// Results are cached in Redis for 15 minutes.
package sentiment

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/redis/go-redis/v9"
	"google.golang.org/genai"
)

// SentimentResult is the output of a single analysis run.
type SentimentResult struct {
	Score      int       `json:"score"`      // -5 to +5
	Direction  string    `json:"direction"`  // BULLISH | BEARISH | NEUTRAL
	Confidence float64   `json:"confidence"` // 0.0–1.0
	Reason     string    `json:"reason"`     // one sentence
	FetchedAt  time.Time `json:"fetched_at"`
}

// Analyzer orchestrates the news-fetch → LLM-score → cache flow.
type Analyzer struct {
	claude       anthropic.Client
	gemini       *genai.Client
	geminiModel  string
	openaiClient openai.Client
	openaiModel  string
	provider     string // "claude", "gemini", or "nvidia"
	rdb          *redis.Client
	httpClient   *http.Client
	logger       *slog.Logger
}

// NewAnalyzer constructs an Analyzer. Set LLM_PROVIDER=gemini and
// GEMINI_API_KEY to use Gemini; otherwise falls back to Claude
// (ANTHROPIC_API_KEY).
func NewAnalyzer(rdb *redis.Client, logger *slog.Logger) *Analyzer {
	a := &Analyzer{
		rdb:        rdb,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		logger:     logger,
		provider:   "claude",
	}

	provider := strings.ToLower(os.Getenv("LLM_PROVIDER"))
	if provider == "gemini" {
		apiKey := os.Getenv("GEMINI_API_KEY")
		if apiKey != "" {
			client, err := genai.NewClient(ctx(), &genai.ClientConfig{
				APIKey:  apiKey,
				Backend: genai.BackendGeminiAPI,
			})
			if err == nil {
				a.gemini = client
				a.geminiModel = "gemini-2.0-flash"
				if m := os.Getenv("GEMINI_MODEL"); m != "" {
					a.geminiModel = m
				}
				a.provider = "gemini"
				logger.Info("sentiment provider: gemini", "model", a.geminiModel)
			} else {
				logger.Error("gemini client init failed, falling back to claude", "err", err)
			}
		} else {
			logger.Warn("LLM_PROVIDER=gemini but GEMINI_API_KEY is empty, falling back to claude")
		}
	}

	if provider == "nvidia" {
		apiKey := os.Getenv("NVIDIA_API_KEY")
		if apiKey != "" {
			baseURL := os.Getenv("NVIDIA_BASE_URL")
			if baseURL == "" {
				baseURL = "https://integrate.api.nvidia.com/v1"
			}
			a.openaiClient = openai.NewClient(
				option.WithAPIKey(apiKey),
				option.WithBaseURL(baseURL),
			)
			a.openaiModel = "meta/llama-3.1-8b-instruct"
			if m := os.Getenv("NVIDIA_MODEL"); m != "" {
				a.openaiModel = m
			}
			a.provider = "nvidia"
			logger.Info("sentiment provider: nvidia", "model", a.openaiModel, "base_url", baseURL)
		} else {
			logger.Warn("LLM_PROVIDER=nvidia but NVIDIA_API_KEY is empty, falling back to claude")
		}
	}

	if a.provider == "claude" {
		a.claude = anthropic.NewClient()
		logger.Info("sentiment provider: claude")
	}

	return a
}

func ctx() context.Context {
	return context.Background()
}

// Analyze returns the current sentiment for the given asset (e.g.
// "BTC"). It checks the Redis cache first; on a miss it fetches
// fresh headlines and scores via the configured LLM. If any step
// fails the fallback is a NEUTRAL result with score 0.
func (a *Analyzer) Analyze(ctx context.Context, asset string) SentimentResult {
	// 1. Cache hit → return immediately.
	if cached, ok := a.getCached(ctx, asset); ok {
		a.logger.Debug("sentiment cache hit", "asset", asset)
		return cached
	}

	// 2. Fetch headlines.
	headlines, err := a.fetchHeadlines(ctx, asset)
	if err != nil {
		a.logger.Error("headline fetch failed, returning neutral",
			"asset", asset, "err", err)
		return neutralResult()
	}
	if len(headlines) == 0 {
		a.logger.Warn("no headlines found, returning neutral", "asset", asset)
		return neutralResult()
	}

	// 3. Score via LLM.
	var result SentimentResult
	switch a.provider {
	case "gemini":
		result, err = a.scoreWithGemini(ctx, headlines)
	case "nvidia":
		result, err = a.scoreWithOpenAI(ctx, headlines)
	default:
		result, err = a.scoreSentiment(ctx, headlines)
	}
	if err != nil {
		a.logger.Error("llm scoring failed, returning neutral",
			"provider", a.provider, "asset", asset, "err", err)
		return neutralResult()
	}

	// 4. Cache the result.
	if err := a.setCache(ctx, asset, result); err != nil {
		a.logger.Warn("failed to cache sentiment", "asset", asset, "err", err)
	}

	a.logger.Info("sentiment scored",
		"provider", a.provider,
		"asset", asset,
		"score", result.Score,
		"direction", result.Direction,
		"confidence", result.Confidence,
	)
	return result
}

func neutralResult() SentimentResult {
	return SentimentResult{
		Score:     0,
		Direction: "NEUTRAL",
		Reason:    "fallback: analysis unavailable",
		FetchedAt: time.Now(),
	}
}
