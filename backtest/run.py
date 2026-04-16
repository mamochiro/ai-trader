"""
Backtest for the ai-trader RSI + MACD + Bollinger strategy.

Fetches 6 months of BTC/USDT 15m candles from Binance (public API,
no key needed), computes the same indicator scoring as the Go
aggregator (internal/indicators/aggregator.go), and runs a vectorbt
simulation.

The Go indicators are the source of truth. If this file's numbers
diverge meaningfully, fix this file, not the Go code.

Usage:
    cd backtest
    pip install -r requirements.txt
    python run.py
"""

from __future__ import annotations

from datetime import datetime, timedelta, timezone
from pathlib import Path

import ccxt
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np
import pandas as pd
import vectorbt as vbt


# ── Data ─────────────────────────────────────────────────────────────

def fetch_ohlcv(
    symbol: str = "BTC/USDT",
    timeframe: str = "15m",
    months: int = 6,
) -> pd.DataFrame:
    """Fetch historical OHLCV from Binance public API (no key)."""
    exchange = ccxt.binance({"enableRateLimit": True})
    since = datetime.now(timezone.utc) - timedelta(days=months * 30)
    since_ms = int(since.timestamp() * 1000)

    all_candles: list[list] = []
    while True:
        batch = exchange.fetch_ohlcv(
            symbol, timeframe, since=since_ms, limit=1000,
        )
        if not batch:
            break
        all_candles.extend(batch)
        since_ms = batch[-1][0] + 1  # 1 ms after last candle
        if len(batch) < 1000:
            break

    df = pd.DataFrame(
        all_candles,
        columns=["timestamp", "open", "high", "low", "close", "volume"],
    )
    df["timestamp"] = pd.to_datetime(df["timestamp"], unit="ms", utc=True)
    df = df.set_index("timestamp")
    df = df[~df.index.duplicated(keep="first")]
    return df


# ── Indicators ───────────────────────────────────────────────────────

def compute_signals(
    df: pd.DataFrame,
    rsi_period: int = 14,
    macd_fast: int = 12,
    macd_slow: int = 26,
    macd_signal: int = 9,
    bb_period: int = 20,
    bb_std: float = 2.0,
) -> tuple[pd.Series, pd.Series, pd.Series]:
    """Compute indicator scores per bar.

    Scoring rules (matching internal/indicators/aggregator.go):
        RSI < 30  → +1,   RSI > 70  → −1
        MACD hist > 0 → +1,  hist < 0  → −1
        price < BB lower → +1,  price > BB upper → −1

    Returns (total_score, rsi, macd_histogram).
    """
    close = df["close"]

    # ── RSI (Wilder smoothing: alpha = 1/period) ────────────────────
    delta = close.diff()
    gain = delta.clip(lower=0)
    loss = (-delta).clip(lower=0)
    avg_gain = gain.ewm(
        alpha=1 / rsi_period, min_periods=rsi_period, adjust=False,
    ).mean()
    avg_loss = loss.ewm(
        alpha=1 / rsi_period, min_periods=rsi_period, adjust=False,
    ).mean()
    rs = avg_gain / avg_loss
    rsi = 100 - (100 / (1 + rs))

    rsi_score = pd.Series(0, index=df.index, dtype=int)
    rsi_score[rsi < 30] = 1
    rsi_score[rsi > 70] = -1

    # ── MACD (EMA with span → k = 2/(span+1)) ────────────���─────────
    ema_fast = close.ewm(span=macd_fast, adjust=False).mean()
    ema_slow = close.ewm(span=macd_slow, adjust=False).mean()
    macd_line = ema_fast - ema_slow
    signal_line = macd_line.ewm(span=macd_signal, adjust=False).mean()
    histogram = macd_line - signal_line

    macd_score = pd.Series(0, index=df.index, dtype=int)
    macd_score[histogram > 0] = 1
    macd_score[histogram < 0] = -1

    # ── Bollinger Bands (population stddev, ddof=0) ─────────────��───
    sma = close.rolling(bb_period).mean()
    std = close.rolling(bb_period).std(ddof=0)
    bb_upper = sma + bb_std * std
    bb_lower = sma - bb_std * std

    bb_score = pd.Series(0, index=df.index, dtype=int)
    bb_score[close < bb_lower] = 1
    bb_score[close > bb_upper] = -1

    total_score = rsi_score + macd_score + bb_score
    return total_score, rsi, histogram


# ── Signals ──────────────────────────────────────────────────────────

def generate_signals(
    score: pd.Series,
    buy_threshold: int = 2,
    sell_threshold: int = -2,
) -> tuple[pd.Series, pd.Series]:
    """Convert score into boolean entry/exit signals.

    The live strategy uses weighted scoring with sentiment (see
    internal/strategy/engine.go).  In backtesting we have no
    sentiment, so we use the technical score alone with a
    threshold of ±2 (at least 2 of 3 indicators agree).
    """
    entries = (score >= buy_threshold).fillna(False)
    exits = (score <= sell_threshold).fillna(False)
    return entries, exits


# ── Backtest ─────────────────────────────────────────────────────────

def run_backtest(
    df: pd.DataFrame,
    entries: pd.Series,
    exits: pd.Series,
    init_cash: float = 64,
    sl_pct: float = 0.025,
    tp_pct: float = 0.05,
) -> vbt.Portfolio:
    """Run a vectorbt long-only portfolio simulation with SL/TP.

    Matches Go risk manager defaults:
      init_cash  = $64 (your portfolio)
      sl_pct     = 2.5% stop-loss
      tp_pct     = 5% take-profit
      fees       = 0.1% Binance taker fee
    """
    return vbt.Portfolio.from_signals(
        df["close"],
        entries=entries,
        exits=exits,
        init_cash=init_cash,
        fees=0.001,      # 0.1% Binance taker fee
        sl_stop=sl_pct,
        tp_stop=tp_pct,
        freq="15min",
    )


# ── Reporting ────────────────────────────────────────────────────────

def print_report(pf: vbt.Portfolio) -> None:
    total_return = pf.total_return() * 100
    sharpe = pf.sharpe_ratio()
    max_dd = pf.max_drawdown() * 100
    trades = pf.trades
    n_trades = trades.count()
    win_rate = trades.win_rate() * 100 if n_trades > 0 else 0.0

    print()
    print("=" * 50)
    print("  AI-TRADER BACKTEST RESULTS")
    print("=" * 50)
    print(f"  Total Return:   {total_return:>10.2f}%")
    print(f"  Sharpe Ratio:   {sharpe:>10.4f}")
    print(f"  Max Drawdown:   {max_dd:>10.2f}%")
    print(f"  Win Rate:       {win_rate:>10.2f}%")
    print(f"  Total Trades:   {n_trades:>10}")
    print("=" * 50)
    print()


def save_equity_curve(pf: vbt.Portfolio, path: str | Path) -> None:
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)

    fig, ax = plt.subplots(figsize=(14, 6))
    equity = pf.value()
    ax.plot(equity.index, equity.values, linewidth=0.8, color="#2563eb")
    ax.fill_between(
        equity.index, equity.values, equity.values[0],
        alpha=0.08, color="#2563eb",
    )
    ax.axhline(equity.values[0], color="gray", linestyle="--", linewidth=0.5)
    ax.set_title("AI-Trader Equity Curve — BTC/USDT 15m", fontsize=13)
    ax.set_xlabel("Date")
    ax.set_ylabel("Portfolio Value (USDT)")
    ax.grid(True, alpha=0.2)
    fig.tight_layout()
    fig.savefig(path, dpi=150)
    plt.close(fig)
    print(f"Equity curve saved to {path}")


# ── Main ─────────────────────────────────────────────────────────────

def main() -> None:
    print("[backtest] Fetching 6 months of BTC/USDT 15m candles ...")
    df = fetch_ohlcv("BTC/USDT", "15m", months=6)
    print(f"[backtest] {len(df)} candles: {df.index[0]} → {df.index[-1]}")

    print("[backtest] Computing indicators ...")
    score, _, _ = compute_signals(df)
    entries, exits = generate_signals(score)
    print(f"[backtest] Entries={entries.sum()}  Exits={exits.sum()}")

    print("[backtest] Running simulation ...")
    pf = run_backtest(df, entries, exits)

    print_report(pf)
    save_equity_curve(pf, "results/equity_curve.png")


if __name__ == "__main__":
    main()
