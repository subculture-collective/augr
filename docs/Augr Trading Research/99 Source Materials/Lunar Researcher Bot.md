---
title: "I Built a Polymarket Bot With Claude Code in One Weekend. It's Up $11,400."
source: "https://x.com/LunarResearcher/status/2043690015675318360"
author:
  - "[[@LunarResearcher]]"
published: 2026-04-13
created: 2026-06-07
description: "Everyone talks about trading Polymarket. Nobody shows how the bot actually works.I'm going to show you exactly how I built mine. Every repo...."
tags:
  - "clippings"
---
![[003 Resources/Assets/11a627b2e801a5b9eaa592279323115f_MD5.jpg]]

Everyone talks about trading Polymarket. Nobody shows how the bot actually works.

I'm going to show you exactly how I built mine. Every repo. Every command. Every dollar.

Total cost: $25/month. Claude API + a VPS.

The bot runs 24/7. I haven't touched it in 19 days.

## Before you read:

This bot runs on 4 open-source repos, Claude Code, and a $5 VPS. Nothing is paywalled. Every link below is free.

If you want to skip the build and just copy the trades: -> Follow [@LunarResearcher](https://x.com/@LunarResearcher) and bookmark article -> Join my alpha channel with all info: [lunar alpha moves](https://t.me/+O4FFj3BmnJRiODhi) -> Copy here: [kreo.app/@lunar](https://kreo.app/@lunar) with my [bot](https://polymarket.com/@0x04283f2fef49d70d8c55ab240450d17a65bf85b?r=lunarlunar#uQuE4Xj)

## Step 0: The Data

You can't trade what you can't see.

Most people start with a strategy. I started with 86 million trades.

[github.com/warproxxx/poly\_data](https://github.com/warproxxx/poly_data) - 646 stars

Every trade ever made on Polymarket. Every wallet. Every entry. Every exit. Every timestamp.

I cloned it. Pointed Claude Code at the folder. One prompt:

```latex
> clone github.com/warproxxx/poly_data
> "analyze processed/trades.csv — find every wallet 
   with 100+ trades and win rate above 70%. 
   rank by profit. export top 50 to targets.json"
```

Claude scanned 14,000+ wallets in 4 minutes. Returned 47 targets.

```python
# what Claude generated
import polars as pl

df = pl.scan_csv("processed/trades.csv").collect(streaming=True)

wallets = (
    df.group_by("maker")
    .agg([
        pl.count().alias("trades"),
        (pl.col("profit") > 0).mean().alias("win_rate"),
        pl.col("profit").sum().alias("total_pnl"),
    ])
    .filter(
        (pl.col("trades") >= 100) & 
        (pl.col("win_rate") > 0.70)
    )
    .sort("total_pnl", descending=True)
    .head(50)
)
```

The top 20 wallets made more than the bottom 13,000 combined.

That's not a stat. That's a target list.

## Step 1: The Scanner

![[003 Resources/Assets/a7c98e79bd07ec64fe2a035e7f48ca8b_MD5.jpg]]

A trading bot without a scanner is just a random number generator.

[github.com/Polymarket/polymarket-cli](https://github.com/Polymarket/polymarket-cli) - Official CLI. Rust. Made for agents.

Three commands changed everything:

```bash
# pull every active market as JSON
polymarket markets list --limit 500 -o json

# check who's buying and selling
polymarket clob book $TOKEN_ID -o json

# get the exact midpoint price
polymarket clob midpoint $TOKEN_ID -o json
```

No API key needed for scanning. Read-only mode. Your bot can watch 500+ markets without a wallet.

I piped the output into Claude Code:

```latex
> "read the JSON output from polymarket-cli.
   for each market, score it on three factors:
   1. gap between market price and your probability estimate
   2. order book depth — is there $500+ on both sides?
   3. hours until resolution — sweet spot is 4-48h.
   kill everything below threshold. save survivors to queue.json"
```

Claude built the scoring function:

```python
def score_market(market, claude_estimate):
    price = market["midpoint"]
    gap = abs(claude_estimate - price)
    depth = min(market["bids_depth"], market["asks_depth"])
    hours_left = market["hours_to_resolution"]
    
    if gap < 0.07: return None          # edge too thin
    if depth < 500: return None         # can't fill
    if hours_left < 4: return None      # too late
    if hours_left > 168: return None    # too slow
    
    return {
        "market": market["question"],
        "gap": round(gap, 3),
        "depth": depth,
        "hours": hours_left,
        "ev": round(gap * depth * 0.001, 2)
    }
```

93% of markets get killed at this stage. That's the point.

## Step 2: The Brain

![[003 Resources/Assets/ba7a0b985d6d3b72a9c9c568fce4e20d_MD5.jpg]]

The scanner finds opportunities. The brain decides whether to take them.

[github.com/Polymarket/agents](https://github.com/Polymarket/agents) - Official agent framework. Python. MIT license.

This repo gives you the skeleton: market data fetching, LLM integration, trade execution. I ripped out the default logic and replaced it with Claude's analysis loop.

```latex
> "for every market in queue.json, run 4 checks:
   1. base rate — what does historical data say?
   2. news — has anything changed in last 6h?
   3. whale check — are any of the 47 targets in this market?
   4. disposition — is the crowd making a cognitive error?
   
   if 3/4 agree → generate thesis
   if thesis confidence > 75% → size with kelly
   if kelly says overbet → cut to quarter kelly"
```

Claude generated the Kelly sizing:

```python
def kelly_size(p_win, market_price, bankroll, max_fraction=0.25):
    """
    f* = (p * b - q) / b
    p = estimated probability
    b = payout ratio (1/price - 1)  
    q = 1 - p
    """
    b = (1 / market_price) - 1
    q = 1 - p_win
    f_star = (p_win * b - q) / b
    
    if f_star <= 0:
        return 0  # negative EV — kill trade
    
    # cap at quarter kelly
    f_capped = min(f_star, max_fraction)
    
    return round(bankroll * f_capped, 2)

# example: 
# claude says 82% chance, market price 0.65, bankroll $800
# kelly_size(0.82, 0.65, 800) → $114.28 position
```

If f\* < 0, the trade is negative EV. Kill it. If f\* > 0.25, you're overbetting. Cap at quarter Kelly. Sweet spot: f\* between 0.05 and 0.15. That's where the Sharpe lives.

## Step 3: The Execution

You have the data. You have the brain. Now you need hands.

[github.com/dylanpersonguy/Polymarket-Trading-Bot](https://github.com/dylanpersonguy/Polymarket-Trading-Bot) - 53,000 lines of TypeScript. 7 strategies.

I pulled three modules:

```latex
> "read the Polymarket-Trading-Bot codebase.
   extract three strategy modules:
   1. arbitrage — catches price gaps between related markets
   2. convergence — enters when price moves toward estimate
   3. whale_copy — mirrors the 47 target wallets with 60s delay
   
   run each as a separate agent. shared wallet, no shared memory.
   consensus logic: 
   - 2 agents agree → full position
   - 1 agent only → half position  
   - agents disagree → no trade"
```

The execution layer:

```python
async def execute_consensus(agents, market, wallet):
    votes = [agent.evaluate(market) for agent in agents]
    
    buy_votes = sum(1 for v in votes if v["action"] == "BUY")
    
    if buy_votes >= 2:
        size = kelly_size(
            p_win=avg([v["confidence"] for v in votes if v["action"] == "BUY"]),
            market_price=market["midpoint"],
            bankroll=wallet.balance
        )
        await place_order(market, size, side="BUY")
        
    elif buy_votes == 1:
        size = kelly_size(...) * 0.5  # half position
        await place_order(market, size, side="BUY")
```

Consensus filter alone killed 40% of losing trades.

## Step 4: The Exit

This is where most bots die. They know when to enter. They never know when to leave.

Three exit triggers:

```python
# 1. target hit — take profit at 85% of expected move
if current_price >= entry_price + (expected_gap * 0.85):
    exit("TARGET_HIT")

# 2. volume spike — 3x normal = smart money leaving  
if volume_10min > avg_volume_10min * 3:
    exit("VOLUME_EXIT")

# 3. time decay — thesis is stale after 24h
if hours_since_entry > 24 and abs(price_change) < 0.02:
    exit("STALE_THESIS")
```

Exit trigger #2 is the one nobody talks about. The top wallets don't hold to settlement. They buy at 40c, sell at 65c, and move on. The last 35 cents of profit isn't worth the risk.

I coded this directly from the poly\_data analysis. Claude found the pattern:

```latex
> "analyze exit behavior of the 47 target wallets.
   what % hold to settlement vs exit early?
   what triggers their exits?"

Claude: "91% of exits happen before resolution.
average exit: 73% of max potential profit captured.
primary trigger: volume spike within 10 minutes of exit.
secondary: price target hit at ~85% of estimated gap."
```

## The Stack

```latex
| Tool                   | Cost   | What it does                     |
| ---------------------- | ------ | -------------------------------- |
| poly_data              | Free   | 86M trades, every wallet         |
| polymarket-cli         | Free   | Market scanning, order placement |
| Polymarket/agents      | Free   | Agent framework, LLM integration |
| Polymarket-Trading-Bot | Free   | 7 strategies, execution engine   |
| Claude API             | $20/mo | The brain                        |
| VPS (Hetzner)          | $5/mo  | Runs 24/7                        |
| Total                  | $25/mo |                                  |
```

## The Startup Script

Every morning at 06:00 UTC my VPS runs:

```bash
#!/bin/bash
# update data
cd ~/poly_data && uv run python -c \
  "from update_utils.process_live import process_live; process_live()"

# refresh scan queue  
polymarket markets list --limit 500 -o json > ~/bot/markets.json

# launch agents
cd ~/bot
python scanner.py &          # scores markets → queue.json
python brain.py &            # evaluates queue → thesis.json  
python executor.py &         # consensus + kelly → trades
python exit_monitor.py &     # volume + target + decay triggers

echo "$(date) — 4 agents live" >> ~/bot/log.txt
```

Four processes. One screen session. $5/month.

![[003 Resources/Assets/5f8b5ee129946247bc32c2993cbba7c3_MD5.jpg]]

## Results: Days 1-19

Day 1: +$0. Spent the whole day debugging API auth. Day 2: +$340. First live trades. 4 positions. 3 winners. Day 3: +$180. Whale copy agent caught a senator resignation market. Day 5: +$890. Convergence engine started printing on crypto markets. Day 7: +$1,400 cumulative. Win rate: 69%.

Turned off the convergence engine on sports markets. Win rate jumped to 74%.

Day 10: +$4,100 cumulative. Day 14: +$7,600 cumulative. Added category rotation - crypto for two weeks, then politics, then macro. Day 19: +$11,400 cumulative. 214 trades. 74% win rate. Sharpe: 2.31.

The bot runs on a $5 VPS in Germany. I check it once a day on my phone. Most days I don't even do that.

## What Didn't Work

1. Sports markets. Data is priced in faster than Claude can analyze. Win rate: 52%. Killed it.
2. Markets under $10K volume. Slippage ate the edge. Minimum: .
3. Holding to settlement. Gave back 15-30% of unrealized profit every time. Volume exit fixed it.
4. All 7 strategies at once. Three focused agents > seven unfocused ones.

## The Part Nobody Tells You

The repos are free. Claude is $20/month. The VPS is $5/month.

But the edge isn't in the tools. It's in the combination.

poly\_data tells you WHO is winning. polymarket-cli tells you WHAT is mispriced. Polymarket/agents gives you HOW to act on it. Polymarket-Trading-Bot gives you WHEN to enter and exit.

Claude is the glue. It reads all four codebases, cross-references the data, and makes decisions faster than you can open a browser tab.

Nobody needs permission to do this. The repos are public. The CLI is official. The data is free.

The only question is whether you'll build it this weekend or read about someone else who did.

**Copy the trades:** [kreo.app/@lunar](https://kreo.app/@lunar) and wallet for copy: [wallet](https://polymarket.com/@0x04283f2fef49d70d8c55ab240450d17a65bf85b?r=lunarlunar#uQuE4Xj)

**The repos:**

1. [github.com/warproxxx/poly\_data](https://github.com/warproxxx/poly_data)
2. [github.com/Polymarket/polymarket-cli](https://github.com/Polymarket/polymarket-cli)
3. [github.com/Polymarket/agents](https://github.com/Polymarket/agents)
4. [github.com/dylanpersonguy/Polymarket-Trading-Bot](https://github.com/dylanpersonguy/Polymarket-Trading-Bot)