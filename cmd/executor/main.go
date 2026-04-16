// Package main runs the order execution service.
//
// It subscribes to strategy decisions on Redis, enforces risk rules,
// places orders on Binance, logs trades to TimescaleDB, and
// sends Telegram notifications.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"

	"github.com/mamochiro/ai-trader/internal/exchange"
	"github.com/mamochiro/ai-trader/internal/risk"
)

func main() {
	_ = godotenv.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// --- env --------------------------------------------------------
	dbURL := requireEnv(logger, "DB_URL")
	redisURL := requireEnv(logger, "REDIS_URL")
	apiKey := requireEnv(logger, "BINANCE_API_KEY")
	apiSecret := requireEnv(logger, "BINANCE_SECRET_KEY")
	testnet := os.Getenv("BINANCE_TESTNET") != "false"
	symbols := exchange.ParseSymbols(os.Getenv("SYMBOLS"), []string{"BTCUSDT"})

	// Portfolio value for risk manager — default 10 000 USDT.
	portfolio := 10_000.0
	if v := os.Getenv("PORTFOLIO_VALUE"); v != "" {
		if p, err := strconv.ParseFloat(v, 64); err == nil && p > 0 {
			portfolio = p
		}
	}

	// --- TimescaleDB ------------------------------------------------
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		logger.Error("timescale connect failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := ensureTradesSchema(ctx, pool); err != nil {
		logger.Error("trades schema migration failed", "err", err)
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

	// --- Binance ----------------------------------------------------
	ex := exchange.NewBinanceClient(apiKey, apiSecret, testnet)

	// --- Risk manager -----------------------------------------------
	rm := risk.NewRiskManager(portfolio)
	rm.MaxPositions = len(symbols)

	// --- Telegram (optional) ----------------------------------------
	var tg *TelegramNotifier
	if token := os.Getenv("TELEGRAM_BOT_TOKEN"); token != "" {
		chatID := os.Getenv("TELEGRAM_CHAT_ID")
		if chatID != "" {
			tg = NewTelegramNotifier(token, chatID)
		}
	}

	// --- Executors (one per symbol) ---------------------------------
	var wg sync.WaitGroup
	for _, sym := range symbols {
		executor := &Executor{
			logger:  logger,
			rdb:     rdb,
			db:      pool,
			ex:      ex,
			risk:    rm,
			tg:      tg,
			symbol:  sym,
			channel: "signals:" + sym,
		}

		logger.Info("executor starting",
			"symbol", executor.symbol,
			"channel", executor.channel,
			"testnet", testnet,
			"portfolio", portfolio,
			"telegram", tg != nil,
		)

		wg.Add(1)
		go func() {
			defer wg.Done()
			executor.Run(ctx)
		}()
	}
	wg.Wait()
	logger.Info("executor stopped")
}

func requireEnv(logger *slog.Logger, key string) string {
	v := os.Getenv(key)
	if v == "" {
		logger.Error("required env var is missing", "key", key)
		os.Exit(1)
	}
	return v
}
