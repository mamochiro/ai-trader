package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mamochiro/ai-trader/internal/exchange"
)

// backfillCandles fetches the most recent historical candles from
// Binance REST API and inserts them into TimescaleDB. This lets the
// signal engine start producing decisions immediately instead of
// waiting hours for the websocket to accumulate enough data.
//
// Existing candles are skipped via ON CONFLICT DO NOTHING (requires
// a unique index). If no unique index exists, duplicates are harmless
// — the signal engine only reads the latest N candles.
func backfillCandles(ctx context.Context, logger *slog.Logger, ex *exchange.BinanceClient, pool *pgxpool.Pool, symbol string, intervals []string, limit int) {
	for _, interval := range intervals {
		logger.Info("backfilling candles",
			"symbol", symbol,
			"interval", interval,
			"limit", limit,
		)

		klines, err := ex.GetKlines(ctx, symbol, interval, limit)
		if err != nil {
			logger.Error("backfill failed",
				"interval", interval, "err", err)
			continue
		}

		inserted := 0
		for _, k := range klines {
			c := exchange.Candle{
				Symbol:   symbol,
				Interval: interval,
				OpenTime: time.UnixMilli(k.OpenTime),
				Open:     k.Open,
				High:     k.High,
				Low:      k.Low,
				Close:    k.Close,
				Volume:   k.Volume,
				IsFinal:  true,
			}
			if err := insertCandle(ctx, pool, c); err != nil {
				logger.Error("backfill insert failed",
					"interval", interval, "err", err)
				break
			}
			inserted++
		}

		logger.Info("backfill complete",
			"interval", interval,
			"inserted", inserted,
		)
	}
}
