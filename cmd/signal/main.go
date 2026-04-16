// Package main runs the signal engine.
//
// It subscribes to candle updates on Redis, loads recent history from
// TimescaleDB, computes technical indicators + Claude sentiment, runs
// the strategy engine, and publishes BUY/SELL decisions for the
// executor to consume.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"

	"github.com/mamochiro/ai-trader/internal/exchange"
	"github.com/mamochiro/ai-trader/internal/sentiment"
)

func main() {
	_ = godotenv.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// --- env --------------------------------------------------------
	dbURL := requireEnv(logger, "DB_URL")
	redisURL := requireEnv(logger, "REDIS_URL")
	symbols := exchange.ParseSymbols(os.Getenv("SYMBOLS"), []string{"BTCUSDT"})

	// --- TimescaleDB ------------------------------------------------
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		logger.Error("timescale connect failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := ensureSignalsSchema(ctx, pool); err != nil {
		logger.Error("signals schema migration failed", "err", err)
		os.Exit(1)
	}

	// --- Redis ------------------------------------------------------
	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		logger.Error("redis url parse failed", "err", err)
		os.Exit(1)
	}
	rdb := redis.NewClient(redisOpts)
	defer rdb.Close()

	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.Error("redis ping failed", "err", err)
		os.Exit(1)
	}

	// --- Sentiment analyzer -----------------------------------------
	analyzer := sentiment.NewAnalyzer(rdb, logger)

	// --- Signal engines (one per symbol) ----------------------------
	var wg sync.WaitGroup
	for _, sym := range symbols {
		eng := &SignalEngine{
			logger:       logger,
			db:           pool,
			rdb:          rdb,
			sentiment:    analyzer,
			symbol:       sym,
			listenCh:     "candles:" + sym,
			publishCh:    "signals:" + sym,
			interval:     "15m",
			candleWindow: 100,
		}

		logger.Info("signal engine starting",
			"symbol", eng.symbol,
			"listen", eng.listenCh,
			"publish", eng.publishCh,
			"interval", eng.interval,
		)

		wg.Add(1)
		go func() {
			defer wg.Done()
			eng.Run(ctx)
		}()
	}
	wg.Wait()
	logger.Info("signal engine stopped")
}

func requireEnv(logger *slog.Logger, key string) string {
	v := os.Getenv(key)
	if v == "" {
		logger.Error("required env var is missing", "key", key)
		os.Exit(1)
	}
	return v
}
