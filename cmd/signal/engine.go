package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/mamochiro/ai-trader/internal/exchange"
	"github.com/mamochiro/ai-trader/internal/indicators"
	"github.com/mamochiro/ai-trader/internal/sentiment"
	"github.com/mamochiro/ai-trader/internal/strategy"
)

// SignalEngine subscribes to candle updates and emits trade decisions.
type SignalEngine struct {
	logger       *slog.Logger
	db           *pgxpool.Pool
	rdb          *redis.Client
	sentiment    *sentiment.Analyzer
	symbol         string
	listenCh       string    // subscribe to candle updates
	publishCh      string    // publish trade decisions
	interval       string    // only act on this candle interval
	candleWindow   int       // how many candles to load for indicators
	lastCandleTime time.Time // dedup: skip already-processed candles
}

// Run subscribes to the candle channel and blocks until ctx is canceled.
func (e *SignalEngine) Run(ctx context.Context) {
	sub := e.rdb.Subscribe(ctx, e.listenCh)
	defer sub.Close()

	ch := sub.Channel()
	e.logger.Info("subscribed to candle channel", "channel", e.listenCh)

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			e.handleCandle(ctx, msg.Payload)
		}
	}
}

func (e *SignalEngine) handleCandle(ctx context.Context, payload string) {
	var candle exchange.Candle
	if err := json.Unmarshal([]byte(payload), &candle); err != nil {
		e.logger.Error("invalid candle payload", "err", err)
		return
	}

	// Only act on finalized candles of the configured interval.
	if !candle.IsFinal || candle.Interval != e.interval {
		return
	}

	// Skip candles already processed or older than the last one we saw
	// (e.g. reconnect backfill publishes older candles).
	if !candle.OpenTime.After(e.lastCandleTime) {
		return
	}
	e.lastCandleTime = candle.OpenTime

	e.logger.Debug("15m candle closed",
		"open_time", candle.OpenTime.Format(time.RFC3339),
		"close", candle.Close,
	)

	// 1. Load recent candle history from TimescaleDB.
	candles, err := loadCandles(ctx, e.db, e.symbol, e.interval, e.candleWindow)
	if err != nil {
		e.logger.Error("failed to load candles", "err", err)
		return
	}
	if len(candles) < 35 {
		e.logger.Warn("not enough candle history", "count", len(candles))
		return
	}

	// 2. Run technical indicators.
	summary := indicators.Analyze(candles)

	// 3. Run sentiment analysis (cached for 15 min).
	sentResult := e.sentiment.Analyze(ctx, exchange.BaseAsset(e.symbol))

	// 4. Strategy decision.
	decision := strategy.Decide(summary, sentResult)

	e.logger.Info("decision",
		"action", decision.Action,
		"score", decision.TotalScore,
		"reason", decision.Reason,
		"rsi", summary.RSI,
		"macd_hist", summary.MACD.Histogram,
		"sentiment", sentResult.Score,
	)

	// 5. Log to signals table (all decisions, including HOLD).
	// Truncate to 15m boundary so the unique constraint (symbol, time)
	// prevents duplicates regardless of exact candle timestamp.
	sig := Signal{
		Time:      candle.OpenTime.Truncate(15 * time.Minute),
		Symbol:    e.symbol,
		Action:    decision.Action,
		Score:     decision.TotalScore,
		Reason:    decision.Reason,
		RSI:       summary.RSI,
		MACD:      summary.MACD.Histogram,
		Sentiment: sentResult.Score,
	}
	inserted, err := insertSignal(ctx, e.db, sig)
	if err != nil {
		e.logger.Error("failed to log signal", "err", err)
		return
	}
	if !inserted {
		e.logger.Debug("duplicate signal skipped", "candle_time", candle.OpenTime)
		return
	}

	// 6. Publish actionable decisions only.
	if decision.Action == "HOLD" {
		return
	}

	data, err := json.Marshal(decision)
	if err != nil {
		e.logger.Error("marshal decision failed", "err", err)
		return
	}
	if err := e.rdb.Publish(ctx, e.publishCh, data).Err(); err != nil {
		e.logger.Error("failed to publish decision", "err", err)
		return
	}
	e.logger.Info("decision published",
		"channel", e.publishCh,
		"action", decision.Action,
	)
}
