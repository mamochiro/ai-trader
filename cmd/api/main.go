// Package main exposes a REST API for the dashboard.
//
// It serves current positions, recent trades, indicator snapshots,
// and Claude sentiment scores read from TimescaleDB and Redis.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/mamochiro/ai-trader/internal/exchange"
)

//go:embed static
var staticFiles embed.FS

func main() {
	_ = godotenv.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	logger.Info("api starting", "service", "api")

	testnet := os.Getenv("BINANCE_TESTNET") != "false"
	ex := exchange.NewBinanceClient(
		os.Getenv("BINANCE_API_KEY"),
		os.Getenv("BINANCE_SECRET_KEY"),
		testnet,
	)

	portfolio := 10_000.0
	if v := os.Getenv("PORTFOLIO_VALUE"); v != "" {
		if p, err := strconv.ParseFloat(v, 64); err == nil && p > 0 {
			portfolio = p
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// --- TimescaleDB ------------------------------------------------
	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		logger.Error("DB_URL is required")
		os.Exit(1)
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		logger.Error("timescale connect failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	// --- Routes -----------------------------------------------------
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /api/balance", func(w http.ResponseWriter, r *http.Request) {
		asset := r.URL.Query().Get("asset")
		if asset == "" {
			asset = "USDT"
		}
		bal, err := ex.GetBalance(r.Context(), asset)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"asset": asset, "free": bal})
	})

	mux.HandleFunc("GET /api/candles", func(w http.ResponseWriter, r *http.Request) {
		symbol := queryDefault(r, "symbol", "BTCUSDT")
		interval := queryDefault(r, "interval", "15m")
		limit := queryInt(r, "limit", 100)
		candles, err := queryCandles(r.Context(), pool, symbol, interval, limit)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, candles)
	})

	mux.HandleFunc("GET /api/signals", func(w http.ResponseWriter, r *http.Request) {
		symbol := queryDefault(r, "symbol", "BTCUSDT")
		limit := queryInt(r, "limit", 50)
		signals, err := querySignals(r.Context(), pool, symbol, limit)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, signals)
	})

	mux.HandleFunc("GET /api/trades", func(w http.ResponseWriter, r *http.Request) {
		symbol := queryDefault(r, "symbol", "BTCUSDT")
		limit := queryInt(r, "limit", 50)
		trades, err := queryTrades(r.Context(), pool, symbol, limit)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, trades)
	})

	mux.HandleFunc("GET /api/stats", func(w http.ResponseWriter, r *http.Request) {
		symbol := queryDefault(r, "symbol", "BTCUSDT")
		stats, err := queryStats(r.Context(), pool, symbol)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, stats)
	})

	mux.HandleFunc("GET /api/position", func(w http.ResponseWriter, r *http.Request) {
		symbol := queryDefault(r, "symbol", "BTCUSDT")
		baseAsset := exchange.BaseAsset(symbol)

		// Try Binance API for live balance; fall back to DB price
		// so the dashboard still shows something useful when the
		// API key doesn't work from inside Docker.
		var base, usdt, price float64
		var balanceErr error

		base, balanceErr = ex.GetBalance(r.Context(), baseAsset)
		if balanceErr != nil {
			logger.Warn("binance balance unavailable, using DB fallback", "err", balanceErr)
			base = 0
		}

		price, err = ex.GetTickerPrice(r.Context(), symbol)
		if err != nil {
			// Fall back to the latest candle close price from DB.
			dbPrice, dbErr := queryLastPrice(r.Context(), pool, symbol)
			if dbErr != nil {
				writeErr(w, fmt.Errorf("binance: %w; db fallback: %w", err, dbErr))
				return
			}
			price = dbPrice
		}

		usdt, _ = ex.GetBalance(r.Context(), "USDT")
		// If balance calls failed, estimate from portfolio env.
		if balanceErr != nil {
			usdt = portfolio
		}

		writeJSON(w, map[string]any{
			"symbol":      symbol,
			"base_asset":  baseAsset,
			"base":        base,
			"base_value":  base * price,
			"usdt":        usdt,
			"total_usdt":  usdt + base*price,
			"price":       price,
			"live":        balanceErr == nil,
		})
	})

	// --- Static dashboard -------------------------------------------
	staticSub, _ := fs.Sub(staticFiles, "static")
	mux.Handle("GET /", http.FileServer(http.FS(staticSub)))

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("api listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("api server failed", "err", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	logger.Info("api stopped")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func queryDefault(r *http.Request, key, def string) string {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	return v
}

func queryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	if n > 1000 {
		return 1000
	}
	return n
}
