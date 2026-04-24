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

	mux.HandleFunc("GET /api/balances", func(w http.ResponseWriter, r *http.Request) {
		balances, err := ex.GetAllBalances(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, balances)
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
		stats, err := queryStats(r.Context(), pool, symbol, portfolio)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, stats)
	})

	mux.HandleFunc("GET /api/open-position", func(w http.ResponseWriter, r *http.Request) {
		symbol := queryDefault(r, "symbol", "BTCUSDT")
		pos, err := queryOpenPosition(r.Context(), pool, symbol)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, pos)
	})

	mux.HandleFunc("GET /api/price", func(w http.ResponseWriter, r *http.Request) {
		symbol := queryDefault(r, "symbol", "BTCUSDT")
		now, prev, err := queryPriceChange(r.Context(), pool, symbol)
		if err != nil {
			writeErr(w, err)
			return
		}
		change := 0.0
		if prev > 0 {
			change = ((now - prev) / prev) * 100
		}
		writeJSON(w, map[string]any{
			"price":      now,
			"prev_24h":   prev,
			"change_pct": change,
		})
	})

	mux.HandleFunc("GET /api/signals/chart", func(w http.ResponseWriter, r *http.Request) {
		symbol := queryDefault(r, "symbol", "BTCUSDT")
		limit := queryInt(r, "limit", 100)
		signals, err := querySignalsForChart(r.Context(), pool, symbol, limit)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, signals)
	})

	mux.HandleFunc("GET /api/position", func(w http.ResponseWriter, r *http.Request) {
		symbol := queryDefault(r, "symbol", "BTCUSDT")
		baseAsset := exchange.BaseAsset(symbol)

		// Read free + locked so the dashboard reflects the real portfolio,
		// including funds locked in open OCO brackets.
		var baseFree, baseLocked, usdtFree, usdtLocked, price float64
		var balanceErr error

		balances, balanceErr := ex.GetAllBalances(r.Context())
		if balanceErr != nil {
			logger.Warn("binance balance unavailable, using DB fallback", "err", balanceErr)
		} else {
			for _, b := range balances {
				switch b.Asset {
				case baseAsset:
					baseFree, baseLocked = b.Free, b.Locked
				case "USDT":
					usdtFree, usdtLocked = b.Free, b.Locked
				}
			}
		}

		price, err = ex.GetTickerPrice(r.Context(), symbol)
		if err != nil {
			dbPrice, dbErr := queryLastPrice(r.Context(), pool, symbol)
			if dbErr != nil {
				writeErr(w, fmt.Errorf("binance: %w; db fallback: %w", err, dbErr))
				return
			}
			price = dbPrice
		}

		if balanceErr != nil {
			usdtFree = portfolio
		}

		baseTotal := baseFree + baseLocked
		usdtTotal := usdtFree + usdtLocked
		lockedValueUSDT := baseLocked*price + usdtLocked

		writeJSON(w, map[string]any{
			"symbol":            symbol,
			"base_asset":        baseAsset,
			"base":              baseTotal,
			"base_free":         baseFree,
			"base_locked":       baseLocked,
			"base_value":        baseTotal * price,
			"usdt":              usdtTotal,
			"usdt_free":         usdtFree,
			"usdt_locked":       usdtLocked,
			"total_usdt":        usdtTotal + baseTotal*price,
			"locked_value_usdt": lockedValueUSDT,
			"price":             price,
			"live":              balanceErr == nil,
		})
	})

	// --- Static dashboard -------------------------------------------
	staticSub, _ := fs.Sub(staticFiles, "static")
	mux.Handle("GET /", http.FileServer(http.FS(staticSub)))

	addr := ":8080"
	if v := os.Getenv("API_PORT"); v != "" {
		addr = ":" + v
	}
	srv := &http.Server{
		Addr:              addr,
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
