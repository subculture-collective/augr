---
title: "I Mass-Analyzed 14,000 Polymarket Wallets With Claude. Here's Guide How to Print Money."
source: "https://x.com/LunarResearcher/status/2038622884642398503"
author:
  - "[[@LunarResearcher]]"
published: 2026-03-30
created: 2026-06-07
description: "92% of Polymarket traders lose money. The top 0.1% extracted $3.7 billion. I reverse-engineered their playbook using Claude API, on-chain da..."
tags:
  - "clippings"
---
![Image](https://pbs.twimg.com/media/HEqjspbWoAA1w-9?format=jpg&name=large)

92% of Polymarket traders lose money. The top 0.1% extracted $3.7 billion. I reverse-engineered their playbook using Claude API, on-chain data, and 12 open-source tools. Every formula. Every tool. Every pattern they don't want you to see.

Bookmark this. You'll need it when you actually open terminal.

Here's the full breakdown:

Bookmark this before you start

Follow for daily alpha on Polymarket and AI trading

Join in my alpha channel with all bots: [lunar alpha moves](https://t.me/+O4FFj3BmnJRiODhi)

Copytrade bots with me: [kreo.app/@lunar](https://t.co/N2byLbLHH9)

**The Numbers That Should Make You Uncomfortable**

Polymarket hit $7.94B volume in February 2026 alone. Weekly volume broke $2.1B in March - new all-time high.

Meanwhile:

- 87% of wallets are in the red
- 14,000+ wallets traded last month
- The top 20 wallets captured more profit than the bottom 13,000 combined

This isn't a casino. It's a math exam. And most people showed up without a calculator.

**The Wallets I Studied (Copy These Profiles)**

Before the formulas - real wallets. Real numbers. Verified on-chain.

🐋 **HorizonSplendidView** - +$4,016,108 total PnL Trades crypto and macro markets. High-frequency, small edges, massive volume. Profile: [https://polymarket.com/@HorizonSplendidView](https://polymarket.com/@HorizonSplendidView?modal=signup&mt=1&via=lunarlunar)

🐋 **beachboy4** - $6.12M profit in a single day One day. Mostly sports - Tottenham and Sunderland matches netted [$1M+](https://x.com/search?q=%241M%2B&src=cashtag_click) each. Was deep in the red before this. One session changed everything. Profile: [https://polymarket.com/@beachboy4](https://polymarket.com/@beachboy4?modal=signup&mt=1&via=lunarlunar)

🐋 **majorexploiter** - +$2,416,975 in March 2026 Geopolitics and elections only. Doesn't touch crypto. Doesn't touch sports. Laser focus.

🐋 **CemeterySun** - $36.6M volume traded Tiny edge per trade. Thousands of trades. Market making on steroids.

What do they all share? Not insider info. Not luck. **Mathematical edge + automation.**

**Part I: The 3 Formulas That Separate Winners From Liquidation**

**Formula 1 - Expected Value (The Only Filter That Matters)**

EV = P\_true × (1 - P\_market) - (1 - P\_true) × P\_market

Market says 40%. You believe 60%. Your edge per dollar:

```latex
EV = 0.60 × 0.60 - 0.40 × 0.40 = $0.20
```

20 cents of edge per dollar. In traditional finance, careers are built on 2% edges. On Polymarket, 20% edges exist daily - if you can find them.

**Rule: EV < 5% - SKIP. No exceptions.** This single filter eliminates 90% of losing trades.

**Formula 2 - Kelly Criterion (How Much to Bet Without Blowing Up)**

f\* = (p × b - q) / b

Where b = (1 - P\_market) / P\_market, p = true probability, q = 1 - p.

Full Kelly says bet 33% of your bankroll. **Never do this.** 50 years of real trading proves Full Kelly destroys you emotionally before the math pays off.

**Use Quarter Kelly.** Always. With $1,000 bankroll: bet $83. Not exciting. Won't make you rich tomorrow. Won't blow you up either.

**Formula 3 - Bayesian Updating (Change Your Mind Correctly)**

P(H|E) = P(E|H) × P(H) / P(E)

Inflation data drops. Your prior on a Fed rate cut was 55%. After the data:

```latex
posterior = (0.80 × 0.55) / 0.50 = 0.88
```

55% - 88%. Not because you panicked. Because the math updated.

Most traders form an opinion and defend it to the death. **Certainty is a bug, not a feature.**

**Part II: The $0 Toolkit - 12 Open-Source Weapons**

Every tool is free. Tested personally. Sorted by what to install first.

**Layer 1: Data (Without This, Everything Is Guessing)**

1. **poly\_data** (warproxxx) - 646★ Every trade ever made on Polymarket. 86M+ trades. Every wallet. Every entry price. Download the snapshot first - saves 2+ days. 🔗 [github.com/warproxxx/poly\_data](https://github.com/warproxxx/poly_data)
2. **py-clob-client** - 947★ | Official SDK Made by Polymarket. Read prices, place orders, WebSocket streams. The foundation. 🔗 [github.com/Polymarket/py-clob-client](https://github.com/Polymarket/py-clob-client)
3. **pmxt** - The CCXT for prediction markets One library for Polymarket + Kalshi + Limitless. Unified API. pip install pmxt 🔗 [github.com/pmxt-dev/pmxt](https://github.com/pmxt-dev/pmxt)
4. **prediction-market-analysis** (Jon-Becker) Framework for collecting and analyzing Polymarket + Kalshi data into reusable research outputs. 🔗 [github.com/Jon-Becker/prediction-market-analysis](https://github.com/Jon-Becker/prediction-market-analysis)

**Layer 2: Intelligence (Finding Edge Before Everyone Else)**

1. **polyterm** (NYTEMODEONLY) - 32★ (criminally underrated) 73 terminal screens. Whale tracking. Insider detection. Cross-platform arb scanning vs Kalshi. Wash trade detection. **Never touches your private keys**. 🔗 [github.com/NYTEMODEONLY/polyterm](https://github.com/NYTEMODEONLY/polyterm)

```latex
polyterm wallets --type whales
polyterm wallets --type smart         # >70% WR
polyterm alerts --type insider
polyterm alerts --type arbitrage      # vs Kalshi
```

1. **insider-tracker** (pselamy) - 63★ ML + heuristics. Monitors fresh wallets, unusual position sizes, entries into low-liquidity markets. January 2026: flagged 5 alerts on a wallet that turned $35K into $442K before the event. 🔗 [github.com/pselamy/polymarket-insider-tracker](https://github.com/pselamy/polymarket-insider-tracker)
2. **MiroShark** - Multi-agent simulation engine Fork of MiroFish (33K★). Simulates thousands of AI personas to model market outcomes. Hit 285 stars in its first week. 🔗 [github.com/aaronjmars/MiroShark](https://github.com/aaronjmars/MiroShark)

**Layer 3: Execution (Actually Making Money)**

1. **poly-maker** (warproxxx) - 963★ Market making bot. Both sides of the book. Collect the spread. Don't predict direction. Config through Google Sheets. Includes gas optimization. 🔗 [github.com/warproxxx/poly-maker](https://github.com/warproxxx/poly-maker)
2. **Polymarket/agents** - 2,600★ | Official LLM-powered trading agents. RAG support, news sourcing, prompt engineering tools. Expect 2-4 hours debugging setup. 🔗 [github.com/Polymarket/agents](https://github.com/Polymarket/agents)
3. **polymarket-copy-trading-bot** (RaphaelKrutLandau) Low-latency copy trading. Mirror top wallets with configurable position sizing. 🔗 [github.com/RaphaelKrutLandau/polymarket-copy-trading-bot](https://github.com/RaphaelKrutLandau/polymarket-copy-trading-bot)

**Layer 4: Infrastructure**

1. **Polysights / Insider Finder** 24,000 users. $2M funding round. $25K grant from Polymarket itself. Tracks insider activity and turns it into trading signals.
2. **pmxt Data Archive** Free hourly Parquet snapshots of orderbook and trade data. Backtest anything. 🔗 [archive.pmxt.dev](https://archive.pmxt.dev/)

**Part III: The 20-Line Claude Brain That Replaces 4,000 Lines of Rules**

```python
import anthropic, json

def claude_probability(market_question, market_price):
    client = anthropic.Anthropic(api_key="sk-ant-...")
    
    response = client.messages.create(
        model="claude-sonnet-4-20250514",
        max_tokens=500,
        messages=[{"role": "user", "content": f"""
You are a calibrated prediction market analyst.

Market: {market_question}
Current price: {market_price}

Estimate the TRUE probability (0.00-1.00).
Consider base rates. Penalize extreme confidence.
If you say 70%, ~7 out of 10 such calls should resolve YES.

Return JSON only:
{{"probability": 0.XX, "confidence": "high/medium/low"}}
"""}]
    )
    return json.loads(response.content[0].text)
```

Pipe poly\_data - Claude scores wallets. Pipe insider-tracker - Claude cross-references with news. Pipe polyterm whale data - Claude decides. py-clob-client executes.

**That's 50 lines of custom code. Everything else is open source.**

![Image](https://pbs.twimg.com/media/HEqj4IrawAAb9ow?format=jpg&name=large)

**Part IV: The 5 Mental Bugs That Cost More Than Bad Code**

1. **Base Rate Neglect** - A 99% accurate test on a 0.1% event gives a 9% true positive. "Looks likely" ≠ "is likely."
2. **Sunk Cost Fallacy** - You bought at 70¢. Dropped to 40¢. New info says NO. The only question: would you buy at 40¢ right now with cash?
3. **Survivorship Bias** - 87% of wallets are in the red. You never see their screenshots. When someone posts +$50K, ask where the other 13,000 wallets went.
4. **Copying Without Filtering** - A wallet has 91% win rate on crypto and 15% on politics. Copying everything = net negative. Filter by category. Copy only dominance.
5. **Overfitting** - "Every time X happens, market goes up." Based on 3 examples. That's noise, not signal.

**Part V: The Security Warning Nobody Wants to Read**

In December 2025, a GitHub repo called polymarket-copy-trading-bot contained malware. Professional README. Working code. Real API connections. Hidden inside a dependency: code that read your .env, extracted your private key, and sent it to a remote server.

**The bot worked. Your money disappeared.**

Rules:

- NEVER use your main wallet. Dedicated wallet, minimal funds
- Audit every dependency. pip list. Google suspicious packages
- Repo created after Feb 2026 with 500+ stars? Likely star-farmed
- Use [Revoke.cash](https://revoke.cash/) to limit USDC approvals. Never unlimited
- Start with $100-300. If it works for 2 weeks, scale gradually

664 malicious repos are on GitHub right now. 14,285 people downloaded malware before anyone noticed.

**Where to Start Tonight**

Don't install 12 tools. Pick one path:

**"I want data first"** - poly\_data + polyterm. 15 minutes to install. Feed to Claude. Find 47 wallets with Sharpe > 2.0 in 4 minutes.

**"I want to copy smart wallets"** - polyterm --type smart + copy-trading-bot. Filter by category. Quarter Kelly sizing.

**"I want to build a bot"** - py-clob-client + Claude API + the 20-line brain above. Paper trade for 1 week minimum. 200+ trades before going live.

**"I want market making"** - poly-maker. Both sides. Collect the spread. $0.02-0.05 per fill. Thousands per day.

![Image](https://pbs.twimg.com/media/HEqj-PlXwAAFUvI?format=jpg&name=large)

Polymarket did $7.94B in February. $2.1B in a single week in March. The window where a $5/month VPS can compete with institutional desks is closing.

The edge isn't in knowing the tools exist. It's in actually opening terminal.

95% of people who read this will bookmark it and never install anything.

Be the other 5%.

Follow for Part 2 - I'll show how I built a live dashboard that tracks all 12 tools in one Telegram bot.

RT if this saved you 100+ hours of research.

Bookmark this. The tools don't change - but your understanding of how to combine them gets sharper every time.

- Copytrade my bot on Polymarket with me: [https://t.me/KreoPolyBot](https://t.me/KreoPolyBot?start=ref-btcsheet)
- Profile bot: [https://polymarket.com/@googoogaga23](https://polymarket.com/@googoogaga23?modal=signup&mt=1&via=lunarlunar)