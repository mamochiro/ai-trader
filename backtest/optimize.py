"""
Parameter optimization for the ai-trader strategy.

Grid search over:
  - RSI period
  - MACD (fast, slow, signal)
  - Stop-loss %
  - Take-profit %

Ranked by Sharpe ratio. Matches Go risk manager config.

Usage:
    cd backtest
    pip install -r requirements.txt
    python optimize.py
"""

from __future__ import annotations

import itertools
from pathlib import Path

import pandas as pd

from run import compute_signals, fetch_ohlcv, generate_signals, run_backtest

# ── Search grid ──────────────────────────────────────────────────────

RSI_PERIODS = [10, 14, 21]
MACD_FAST = [8, 12, 16]
MACD_SLOW = [21, 26, 30]
MACD_SIGNAL = [7, 9, 12]
SL_PCTS = [0.015, 0.025, 0.035]    # 1.5%, 2.5%, 3.5%
TP_PCTS = [0.03, 0.05, 0.07]       # 3%, 5%, 7%


def main() -> None:
    print("[optimize] Fetching 6 months of BTC/USDT 15m candles ...")
    df = fetch_ohlcv("BTC/USDT", "15m", months=6)
    print(f"[optimize] {len(df)} candles loaded")

    # Pre-compute indicator scores for each RSI+MACD combo (expensive part)
    indicator_combos = [
        (rsi_p, fast, slow, sig)
        for rsi_p, fast, slow, sig in itertools.product(
            RSI_PERIODS, MACD_FAST, MACD_SLOW, MACD_SIGNAL,
        )
        if fast < slow
    ]

    # Full grid: indicators × SL × TP
    total = len(indicator_combos) * len(SL_PCTS) * len(TP_PCTS)
    print(f"[optimize] {total} parameter combinations to test\n")

    results: list[dict] = []
    count = 0

    for rsi_p, fast, slow, sig in indicator_combos:
        # Compute indicators once per combo
        score, _, _ = compute_signals(
            df,
            rsi_period=rsi_p,
            macd_fast=fast,
            macd_slow=slow,
            macd_signal=sig,
        )
        entries, exits = generate_signals(score)

        if entries.sum() == 0:
            count += len(SL_PCTS) * len(TP_PCTS)
            continue

        # Test each SL/TP combo against same signals
        for sl, tp in itertools.product(SL_PCTS, TP_PCTS):
            count += 1
            label = f"RSI={rsi_p:>2} MACD({fast},{slow},{sig}) SL={sl:.1%} TP={tp:.1%}"
            print(f"\r  [{count:>4}/{total}] {label}", end="", flush=True)

            pf = run_backtest(df, entries, exits, sl_pct=sl, tp_pct=tp)
            trades = pf.trades
            n_trades = trades.count()

            if n_trades == 0:
                continue

            results.append({
                "rsi_period": rsi_p,
                "macd_fast": fast,
                "macd_slow": slow,
                "macd_signal": sig,
                "stop_loss_pct": sl,
                "take_profit_pct": tp,
                "reward_risk": round(tp / sl, 1),
                "total_return_pct": round(pf.total_return() * 100, 2),
                "sharpe_ratio": round(pf.sharpe_ratio(), 4),
                "max_drawdown_pct": round(pf.max_drawdown() * 100, 2),
                "win_rate_pct": round(trades.win_rate() * 100, 2),
                "total_trades": n_trades,
            })

    print("\n")

    if not results:
        print("[optimize] No valid results — every combination produced zero trades.")
        return

    results_df = pd.DataFrame(results).sort_values(
        "sharpe_ratio", ascending=False,
    )

    out_path = Path("results/optimization.csv")
    out_path.parent.mkdir(parents=True, exist_ok=True)
    results_df.to_csv(out_path, index=False)
    print(f"[optimize] Full results saved to {out_path}")

    # ── Top 10 ───────────────────────────────────────────────────────
    print()
    print("  TOP 10 BY SHARPE RATIO")
    print("  " + "─" * 100)
    header = (
        f"  {'RSI':>3}  {'MACD':>12}  {'SL%':>5}  {'TP%':>5}  {'R:R':>4}  "
        f"{'Sharpe':>8}  {'Return%':>8}  {'MaxDD%':>8}  {'WR%':>6}  {'Trades':>6}"
    )
    print(header)
    print("  " + "─" * 100)
    for _, r in results_df.head(10).iterrows():
        macd_str = f"({int(r.macd_fast)},{int(r.macd_slow)},{int(r.macd_signal)})"
        print(
            f"  {int(r.rsi_period):>3}  {macd_str:>12}  "
            f"{r.stop_loss_pct:>4.1%}  {r.take_profit_pct:>4.1%}  "
            f"{r.reward_risk:>4.1f}  "
            f"{r.sharpe_ratio:>8.4f}  {r.total_return_pct:>7.2f}%  "
            f"{r.max_drawdown_pct:>7.2f}%  {r.win_rate_pct:>5.1f}%  "
            f"{int(r.total_trades):>6}"
        )

    # ── Current config comparison ────────────────────────────────────
    current = results_df[
        (results_df.rsi_period == 14) &
        (results_df.macd_fast == 12) &
        (results_df.macd_slow == 26) &
        (results_df.macd_signal == 9) &
        (results_df.stop_loss_pct == 0.025) &
        (results_df.take_profit_pct == 0.05)
    ]
    if not current.empty:
        r = current.iloc[0]
        rank = (results_df.sharpe_ratio > r.sharpe_ratio).sum() + 1
        print(f"\n  YOUR CURRENT CONFIG: rank #{rank}/{len(results_df)}")
        print(f"  Sharpe={r.sharpe_ratio:.4f}  Return={r.total_return_pct:.2f}%  "
              f"MaxDD={r.max_drawdown_pct:.2f}%  WR={r.win_rate_pct:.1f}%  "
              f"Trades={int(r.total_trades)}")
    print()


if __name__ == "__main__":
    main()
