package main

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/adshao/go-binance/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/mamochiro/ai-trader/internal/exchange"
)

// Feeder owns the lifecycle of all kline websocket subscriptions and
// fans updates out to TimescaleDB and Redis.
type Feeder struct {
	logger    *slog.Logger
	symbol    string
	intervals []string
	db        *pgxpool.Pool
	rdb       *redis.Client
	ex        *exchange.BinanceClient
	testnet   bool
	channel   string
}

// Run starts one stream per interval and blocks until ctx is canceled.
func (f *Feeder) Run(ctx context.Context) {
	// UseTestnet is a package-level toggle in go-binance; set it once
	// before opening any websocket so both intervals see the same URL.
	binance.UseTestnet = f.testnet

	var wg sync.WaitGroup
	for _, interval := range f.intervals {
		wg.Add(1)
		go func(iv string) {
			defer wg.Done()
			f.runStream(ctx, iv)
		}(interval)
	}
	wg.Wait()
}

// runStream maintains one kline websocket subscription, reconnecting
// with exponential backoff (capped) on any disconnect or error.
func (f *Feeder) runStream(ctx context.Context, interval string) {
	const (
		initialBackoff = time.Second
		maxBackoff     = time.Minute
	)
	backoff := initialBackoff

	for {
		if ctx.Err() != nil {
			return
		}

		handler := func(event *binance.WsKlineEvent) {
			f.handleEvent(ctx, event)
		}
		errHandler := func(err error) {
			f.logger.Warn("kline stream error", "interval", interval, "err", err)
		}

		doneC, stopC, err := binance.WsKlineServe(f.symbol, interval, handler, errHandler)
		if err != nil {
			f.logger.Error("kline stream connect failed",
				"interval", interval, "err", err, "backoff", backoff)
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}

		f.logger.Info("kline stream connected",
			"symbol", f.symbol, "interval", interval)

		// Backfill candles missed while disconnected so the signal
		// engine doesn't skip a beat.
		f.backfillOnReconnect(ctx, interval)

		backoff = initialBackoff

		select {
		case <-ctx.Done():
			close(stopC)
			<-doneC
			return
		case <-doneC:
			f.logger.Warn("kline stream disconnected, reconnecting",
				"interval", interval, "backoff", backoff)
		}

		if !sleepCtx(ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff, maxBackoff)
	}
}

// handleEvent parses a raw websocket event into a Candle, publishes
// every update to Redis, and persists only finalized candles.
func (f *Feeder) handleEvent(ctx context.Context, event *binance.WsKlineEvent) {
	c, err := toCandle(event)
	if err != nil {
		f.logger.Error("invalid kline event",
			"symbol", event.Symbol, "err", err)
		return
	}

	// Publish every tick so downstream can see in-progress candles.
	if err := publishCandle(ctx, f.rdb, f.channel, c); err != nil && ctx.Err() == nil {
		f.logger.Error("redis publish failed",
			"symbol", c.Symbol, "interval", c.Interval, "err", err)
	}

	// Persist only when the candle has closed.
	if !c.IsFinal {
		return
	}
	if err := insertCandle(ctx, f.db, c); err != nil {
		if ctx.Err() == nil {
			f.logger.Error("timescale insert failed",
				"symbol", c.Symbol, "interval", c.Interval, "err", err)
		}
		return
	}
	f.logger.Info("candle stored",
		"symbol", c.Symbol,
		"interval", c.Interval,
		"open_time", c.OpenTime.UTC().Format(time.RFC3339),
		"close", c.Close,
		"volume", c.Volume,
	)
}

// toCandle converts a go-binance kline event (all numeric fields are
// strings) into our normalized Candle type.
func toCandle(event *binance.WsKlineEvent) (exchange.Candle, error) {
	k := event.Kline
	open, err := strconv.ParseFloat(k.Open, 64)
	if err != nil {
		return exchange.Candle{}, err
	}
	high, err := strconv.ParseFloat(k.High, 64)
	if err != nil {
		return exchange.Candle{}, err
	}
	low, err := strconv.ParseFloat(k.Low, 64)
	if err != nil {
		return exchange.Candle{}, err
	}
	cls, err := strconv.ParseFloat(k.Close, 64)
	if err != nil {
		return exchange.Candle{}, err
	}
	vol, err := strconv.ParseFloat(k.Volume, 64)
	if err != nil {
		return exchange.Candle{}, err
	}
	return exchange.Candle{
		Symbol:   k.Symbol,
		Interval: k.Interval,
		OpenTime: time.UnixMilli(k.StartTime),
		Open:     open,
		High:     high,
		Low:      low,
		Close:    cls,
		Volume:   vol,
		IsFinal:  k.IsFinal,
	}, nil
}

// backfillOnReconnect fetches recent candles from the Binance REST API
// after a websocket reconnect. Missed candles are persisted to
// TimescaleDB and the latest finalized candle is published to Redis so
// the signal engine can process it.
func (f *Feeder) backfillOnReconnect(ctx context.Context, interval string) {
	if f.ex == nil {
		return
	}

	const catchUpLimit = 5
	klines, err := f.ex.GetKlines(ctx, f.symbol, interval, catchUpLimit)
	if err != nil {
		f.logger.Error("reconnect backfill fetch failed",
			"interval", interval, "err", err)
		return
	}

	inserted := 0
	var latest exchange.Candle
	for _, k := range klines {
		c := exchange.Candle{
			Symbol:   f.symbol,
			Interval: interval,
			OpenTime: time.UnixMilli(k.OpenTime),
			Open:     k.Open,
			High:     k.High,
			Low:      k.Low,
			Close:    k.Close,
			Volume:   k.Volume,
			IsFinal:  true,
		}
		if err := insertCandle(ctx, f.db, c); err != nil {
			f.logger.Error("reconnect backfill insert failed",
				"interval", interval, "err", err)
			break
		}
		inserted++
		latest = c
	}

	// Publish the latest candle to Redis so the signal engine triggers.
	if inserted > 0 {
		if err := publishCandle(ctx, f.rdb, f.channel, latest); err != nil {
			f.logger.Error("reconnect backfill publish failed",
				"interval", interval, "err", err)
		}
	}

	f.logger.Info("reconnect backfill complete",
		"interval", interval,
		"inserted", inserted,
	)
}

// sleepCtx sleeps for d or until ctx is canceled. Returns false if
// the sleep was cut short by cancellation.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func nextBackoff(current, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		return max
	}
	return next
}
