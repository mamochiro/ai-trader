// Package sentiment fetches recent crypto news headlines from
// CryptoCompare (free, no API key) and scores them for
// bullish/bearish bias via the Claude API. Results are cached in
// Redis for 15 minutes.
package sentiment

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/redis/go-redis/v9"
)

// SentimentResult is the output of a single analysis run.
type SentimentResult struct {
	Score      int       `json:"score"`      // -5 to +5
	Direction  string    `json:"direction"`  // BULLISH | BEARISH | NEUTRAL
	Confidence float64   `json:"confidence"` // 0.0–1.0
	Reason     string    `json:"reason"`     // one sentence
	FetchedAt  time.Time `json:"fetched_at"`
}

// Analyzer orchestrates the news-fetch → Claude-score → cache flow.
type Analyzer struct {
	claude     anthropic.Client
	rdb        *redis.Client
	httpClient *http.Client
	logger     *slog.Logger
}

// NewAnalyzer constructs an Analyzer. The Anthropic SDK reads
// ANTHROPIC_API_KEY from the environment. News headlines come from
// CryptoCompare which requires no API key.
func NewAnalyzer(rdb *redis.Client, logger *slog.Logger) *Analyzer {
	return &Analyzer{
		claude:     anthropic.NewClient(),
		rdb:        rdb,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		logger:     logger,
	}
}

// Analyze returns the current sentiment for the given asset (e.g.
// "BTC"). It checks the Redis cache first; on a miss it fetches
// fresh headlines and calls Claude. If any step fails the fallback
// is a NEUTRAL result with score 0 — the caller never gets an error
// because a missing sentiment should not block trading decisions.
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

	// 3. Score via Claude.
	result, err := a.scoreSentiment(ctx, headlines)
	if err != nil {
		a.logger.Error("claude scoring failed, returning neutral",
			"asset", asset, "err", err)
		return neutralResult()
	}

	// 4. Cache the result.
	if err := a.setCache(ctx, asset, result); err != nil {
		a.logger.Warn("failed to cache sentiment", "asset", asset, "err", err)
	}

	a.logger.Info("sentiment scored",
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
