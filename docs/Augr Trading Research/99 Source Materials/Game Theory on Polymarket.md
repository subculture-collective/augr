---
title: "Game Theory on Polymarket: The 5 Formulas tested on 72 million trades"
source: "https://x.com/0xMovez/status/2037499562064073209"
author:
  - "[[@0xMovez]]"
published: 2026-01-18
created: 2026-06-07
description: "Slot machines on the Vegas Strip return 0.93¢ on the dollar. On Polymarket, traders voluntarily accept returns as low as 0.43¢ on the 1$ - b..."
tags:
  - "clippings"
---
![[003 Resources/Assets/38a60214b09c21197a7ae6a0ed13027d_MD5.jpg]]

Slot machines on the Vegas Strip return 0.93¢ on the dollar. On Polymarket, traders voluntarily accept returns as low as 0.43¢ on the 1$ - betting on longshots with worse odds than a casino.

That's not a metaphor. That's real data from **72.1 million trades** and **$18.26 billion** in volume, analyzed by researcher Jonathan Becker across every resolved market on Kalshi.

> Jan 18
> 
> 0/ i analyzed every single trade on kalshi from 2021 to 2025. i found a systematic wealth transfer where "takers" pay a massive premium for affirmative outcomes, and "makers" harvest the edge without needing to predict the future. here is the data

The patterns he found apply directly to Polymarket - same mechanics, same biases, same opportunities.

> **Here's what the data shows:** 87% of prediction market wallets lose money. But the top 13% aren't lucky - they're using a specific set of mathematical frameworks that most traders don't even know exist.

This article breaks down the **5 game theory formulas** that separate winners from losers in prediction markets. Each one comes with the math, the real-world example, and Python code you can run today.

> **Example of traders using these formulas in their strategies:**

- RN - Polymarket algo bot that made +$6M PnL on Sports using the models from this article. Profile: [https://polymarket.com/@rn1?r=following#EL2kxhb](https://polymarket.com/@rn1?r=following#EL2kxhb)

![[003 Resources/Assets/85e41bf8d76dd5519c921fc379d5d50c_MD5.jpg]]

- Distinct-baguette turned 560$ → $812K market-making UP/DOWN markets. Profile: [https://polymarket.com/profile/%40distinct-baguette?r=following#N8G8PGV](https://polymarket.com/profile/%40distinct-baguette?r=following#N8G8PGV)

![[003 Resources/Assets/ad23875711358a450a7d8237aac0fc19_MD5.jpg]]

Many other traders and bots use these formulas daily to shift their approach from gambling to a precise, math-driven strategy.

## 1) Expected Value - the main formula

Every decision you make on Polymarket is an expected value calculation. Most traders do it with their gut. The top 13% do it with math.

EV tells you whether a bet is worth taking, regardless of the outcome of any single trade. It's the average return you'd get if you made the same bet a thousand times.

![[003 Resources/Assets/504b01d8fae268ac31be7cde47b5a145_MD5.png]]

> **Real example on Polymarket:**

A contract asks: "Will Bitcoin hit $150K by June 2026?" The YES price is 12¢. That implies the market thinks there's a 12% chance.

But you've done your research - on-chain data, halving cycle analysis, ETF flows - and you estimate the real probability is 20%. Should you buy?

<video preload="none" tabindex="-1" playsinline="" aria-label="Embedded video" poster="https://pbs.twimg.com/amplify_video_thumb/2037439157883039744/img/8qU8v_7l7rVyTSm6.jpg" style="width: 100%; height: 100%; position: absolute; background-color: black; top: 0%; left: 0%; transform: rotate(0deg) scale(1.005);"><source type="video/mp4" src="blob:https://x.com/94cc13df-b3cb-4c0a-8428-04d8dc11d6ad"></video>

![[003 Resources/Assets/04d5872500384346f7caa40f333e92bf_MD5.jpg]]

> **Run the EV calculation:**

```java
EV = (0.20 × $0.88) + (0.80 × −$0.12) = $0.176 − $0.096 = +$0.08 per contract
```

Positive EV ⭢ Every contract you buy at 12¢ earns you 8¢ on average. Buy 100 contracts = $8 expected profit on a $12 investment. That's +66.7% ER.

But here's what the research found: **most traders on prediction markets don't calculate EV at all.** They bet because "Bitcoin always pumps" or "my gut says YES." That's why the average taker loses 1.12% per trade across 72 million trades

> **Python: EV Calculator for Polymarket**

```python
# Expected Value Calculator for Polymarket

def calculate_ev(market_price, your_probability):
    """
    market_price: current YES price (0.01 to 0.99)
    your_probability: your estimated true probability
    Returns: expected value per $1 risked
    """
    cost = market_price
    payout = 1.0 - market_price  # profit if YES wins
    
    ev = (your_probability * payout) - ((1 - your_probability) * cost)
    roi = ev / cost * 100
    
    return {
        "ev_per_contract": round(ev, 4),
        "roi_percent": round(roi, 2),
        "verdict": "BUY ✅" if ev > 0 else "SKIP ❌"
    }

# Example: BTC $150K contract at 12¢, you think 20%
result = calculate_ev(0.12, 0.20)
print(f"EV per contract: ${result['ev_per_contract']}")
print(f"ROI: {result['roi_percent']}%")
print(f"Verdict: {result['verdict']}")

# Output:
# EV per contract: $0.08
# ROI: 66.67%
# Verdict: BUY ✅
```

**The key insight from 72M trades:** Takers (people who market-buy) lose an average of -1.12% per trade. Makers (people who set limit orders) gain +1.12%. The difference isn't information - it's patience. Makers wait for positive EV. Takers act on impulse.

## 2) Mispricing formula - сheap contracts trap

The longshot bias is the most expensive mistake in prediction markets. Traders systematically overpay for low-probability outcomes.

- A contract priced at 5 cents should win 5% of the time. On Kalshi, it wins only **4.18%** - that's a **\-16.36% mispricing**.
- At the extreme: 1¢ contracts should win 1% of the time. For takers, they win only **0.43%**. That's a -57% mispricing.

<video preload="none" tabindex="-1" playsinline="" aria-label="Embedded video" poster="https://pbs.twimg.com/amplify_video_thumb/2037246486736666624/img/Ay2-1c59d2q0Zbid.jpg" style="width: 100%; height: 100%; position: absolute; background-color: black; top: 0%; left: 0%; transform: rotate(0deg) scale(1.005);"><source type="video/mp4" src="blob:https://x.com/d9d9be87-1afd-4c5e-869e-bd333f999928"></video>

![[003 Resources/Assets/8a6a9322a8c7e4662b6f0ebbed71a0bf_MD5.jpg]]

The chart above shows the calibration curve. Green dashed line is "perfect efficiency" - where actual win rate equals implied probability. The blue line is reality. Below 20¢, the blue line dips below the green: contracts win less than they should. Above 80¢, it rises above: contracts win more than they should.

The market is remarkably well-calibrated in the middle (30-70¢). The inefficiency concentrates at the tails - exactly where emotional bettors congregate.

## Two Formulas That Reveal Everything

> **Formula 1: Mispricing (δ):**

Mispricing measures how far a contract's actual win rate deviates from its implied probability.

![[003 Resources/Assets/f495ab392b2f22db2b867fe5b833c54a_MD5.png]]

- **Example - 5¢ contracts:**

```plaintext
100,000 trades at 5¢ across all resolved markets
4,180 of them resolved YES (won)

Actual win rate = 4,180 / 100,000 = 4.18%
Implied probability = 5 / 100 = 5.00%

δ = 4.18% − 5.00% = −0.82 percentage points
Relative mispricing = −0.82 / 5.00 = −16.36%
```

You're overpaying by 16.36% on every 5¢ contract.

> **Formula 2: Gross Excess Return (rᵢ)**

While mispricing shows the aggregate bias, gross excess return shows what happens on each individual trade.

![[003 Resources/Assets/ebd8dc74a20db96c9f7106652421aa1f_MD5.png]]

This is where the psychology becomes visible. Let's look at what happens when you buy a 5¢ contract:

- **Scenario A - contract wins:**

rᵢ = (100 × 1 − 5) / 5 = 95 / 5 = **+1,900% return** ( х20 returns )

- **Scenario B - contract loses:**

rᵢ = (100 × 0 − 5) / 5 = −5 / 5 = **−100% return** ( 5¢ is gone )

This is exactly why longshots are addictive. **When they hit, the return is enormous.** +1,900%. Your brain remembers that. It tells stories about that. It tweets about that.

But they hit less often than the price implies. And the asymmetry between "lose everything" and "win big" - averaged over thousands of trades -produces a negative expected value. You're buying lottery tickets that are priced above their fair value.

> **How "Mispricing" looks like across every price level:**

![[003 Resources/Assets/73f2c4e1eba512f5f58925bf316ca47f_MD5.jpg]]

Read the "Return on $1" column. For every dollar you invest in 1¢ contracts as a taker, you get back 43¢. For every dollar in 90¢ contracts, you get back $1.02. The pattern is monotonic - the cheaper the contract, the worse the deal.

<video preload="none" tabindex="-1" playsinline="" aria-label="Embedded video" poster="https://pbs.twimg.com/amplify_video_thumb/2037244721861378048/img/92ysHq-hDGNSd3hD.jpg" style="width: 100%; height: 100%; position: absolute; background-color: black; top: 0%; left: 0%; transform: rotate(0deg) scale(1.005);"><source type="video/mp4" src="blob:https://x.com/3d88a15c-395e-470c-9b83-1807626dda91"></video>

![[003 Resources/Assets/999d8792c954bfe0a1c8a8375ca0f387_MD5.jpg]]

The chart above separates the data by role.

- The red line (Takers) dives to -57% at the left edge.
- The green line (Makers) mirrors it at +57%.
- The purple line (Combined) shows the aggregate market mispricing.

Makers are literally the mirror image of takers - every cent a taker loses, a maker gains.

> **Python: Detect Mispriced Markets**

```python
# Scan Polymarket for longshot bias opportunities
import requests

def scan_mispriced_markets():
    """Find markets where longshot bias creates edge"""
    url = "https://gamma-api.polymarket.com/markets"
    params = {"active": "true", "limit": 50,
              "order": "volume24hr", "ascending": "false"}
    
    markets = requests.get(url, params=params).json()
    opportunities = []
    
    for m in markets:
        price = float(m.get("bestAsk", 0))
        
        # Flag longshots (under 10¢) — historically overpriced
        if 0.01 < price < 0.10:
            expected_mispricing = -16 * (0.10 - price) / 0.10
            opportunities.append({
                "market": m["question"][:60],
                "price": price,
                "estimated_mispricing": f"{expected_mispricing:.1f}%",
                "action": "SELL YES (or BUY NO)"
            })
        
        # Flag near-certainties (over 90¢) — historically underpriced
        elif price > 0.90:
            opportunities.append({
                "market": m["question"][:60],
                "price": price,
                "estimated_mispricing": "+underpriced",
                "action": "BUY YES (near-certainty edge)"
            })
    
    return opportunities

for opp in scan_mispriced_markets():
    print(opp)
```

**The game theory takeaway:** Low-probability contracts are systematically overpriced. High-probability contracts are systematically underpriced. The smart money sells longshots and buys near-certainties.

## 3) Kelly Criterion - how much to bet

You found a positive EV trade on Polymarket. You're 70% confident the market is mispriced. Your bankroll is $5,000. How much do you bet?

If you bet too much, a single loss wipes out weeks of gains. If you bet too little, your edge compounds so slowly it's barely worth the effort. Somewhere between "everything" and "nothing" is a mathematically optimal amount.

![[003 Resources/Assets/0ec294d166ba17e01688c602c1ede0f1_MD5.png]]

**That amount has a name.** It's called the **Kelly Criterion**, and it was invented in 1956 by John Kelly Jr. at Bell Labs. Originally designed to optimize long-distance telephone signal noise, it turned out to be the most powerful position sizing formula ever discovered for gambling, trading, and - as it turns out - prediction markets.

Every professional poker player, every serious sports bettor, every quant fund on Wall Street uses some version of Kelly.

> **Kelly Criterion for Prediction Markets**

On prediction markets, the mechanics are slightly different because contracts are binary (pay $1 or $0) and prices directly represent probabilities.

![[003 Resources/Assets/c2e8bbcd37d4d50aa0d53d81486cb7de_MD5.png]]

Let's unpack the **{ b }** term: On Polymarket, if you buy a YES contract at 30¢, you risk 30¢ to potentially win 70¢ (the contract pays $1 if YES, so your profit is $1 − $0.30 = $0.70). **Your net odds are:**

> at 30¢: b = 0.70 / 0.30 = **2.33** (win $2.33 per $1 risked) at 50¢: b = 0.50 / 0.50 = **1.00** (win $1.00 per $1 risked) at 10¢: b = 0.90 / 0.10 = **9.00** (win $9.00 per $1 risked) at 80¢: b = 0.20 / 0.80 = **0.25** (win $0.25 per $1 risked)

The higher the odds, the more Kelly tells you to bet - if you have edge.

## { critical rule } - Never use Full Kelly

Full Kelly maximizes the long-run growth rate of your bankroll. Mathematically, it's optimal. In practice, it's a disaster. Full Kelly produces **drawdowns of 50% or more** regularly. Over 1,000 bets with genuine edge, full Kelly will eventually make you the most money - but along the way, you'll experience stomach-churning swings that make most humans abandon the strategy entirely.

<video preload="none" tabindex="-1" playsinline="" aria-label="Embedded video" poster="https://pbs.twimg.com/amplify_video_thumb/2037256940733292547/img/tbBum0McEgSRCiNk.jpg" style="width: 100%; height: 100%; position: absolute; background-color: black; top: 0%; left: 0%; transform: rotate(0deg) scale(1.005);"><source type="video/mp4" src="blob:https://x.com/e64e5c19-7ce5-43a3-b710-fe943a89c2cd"></video>

![[003 Resources/Assets/6674a4929bdf71e2fa6f929609f5fd46_MD5.jpg]]

The chart above simulates 1,000 bets with a consistent 55% win rate at even odds.

- **Full Kelly (blue)** - produces the highest ending bankroll but swings wildly
- **Quarter Kelly (green)** \- grows steadily with manageable drawdowns
- **Half Kelly (orange)** \- sits in between.

> **Kelly Bet Size - lookup table:**

Use this table to quickly estimate your quarter-Kelly bet size without doing the math. Find your probability estimate on the left, the market price on the top, and read the fraction of bankroll.

![[003 Resources/Assets/35b944a6fb1fa7d49c4f8142894d8ee1_MD5.jpg]]

> **Production-Ready Kelly Calculator:**

```python
# Kelly Criterion for Polymarket — Production Version

class KellyCalculator:
    def __init__(self, bankroll, kelly_fraction=0.25,
                 max_bet_pct=0.05):
        """
        bankroll: total capital
        kelly_fraction: 0.25 = quarter-Kelly (default)
        max_bet_pct: hard cap per position (5% default)
        """
        self.bankroll = bankroll
        self.fraction = kelly_fraction
        self.max_bet_pct = max_bet_pct
    
    def calculate(self, price, your_prob, correlated=False):
        """Calculate optimal bet for a YES contract"""
        b = (1 - price) / price  # net odds
        q = 1 - your_prob
        
        full_kelly = (your_prob * b - q) / b
        
        if full_kelly <= 0:
            # Check NO side
            no_price = 1 - price
            no_prob = 1 - your_prob
            no_b = (1 - no_price) / no_price
            no_kelly = (no_prob * no_b - your_prob) / no_b
            
            if no_kelly > 0:
                return self._build_result(
                    no_kelly, no_price, "NO", correlated
                )
            return {"action": "NO BET",
                    "reason": "No edge on either side"}
        
        return self._build_result(
            full_kelly, price, "YES", correlated
        )
    
    def _build_result(self, fk, price, side, correlated):
        adj = fk * self.fraction
        if correlated:
            adj *= 0.5  # halve for correlated positions
        
        # Hard cap
        adj = min(adj, self.max_bet_pct)
        
        bet = round(self.bankroll * adj, 2)
        contracts = int(bet / price)
        max_profit = round(contracts * (1 - price), 2)
        
        return {
            "side": side,
            "full_kelly": f"{fk*100:.1f}%",
            "adjusted": f"{adj*100:.1f}%",
            "bet": bet,
            "contracts": contracts,
            "max_profit": max_profit,
            "max_loss": bet,
            "risk_reward": f"{max_profit/bet:.1f}x"
        }

# Usage
k = KellyCalculator(bankroll=5000)

print("=== Fed Rate Cut: 30c, you think 45% ===")
print(k.calculate(0.30, 0.45))

print("\n=== BTC $200K: 5c, you think 12% ===")
print(k.calculate(0.05, 0.12))

print("\n=== No edge: 8c, you think 6% ===")
print(k.calculate(0.08, 0.06))

print("\n=== Correlated crypto bet ===")
print(k.calculate(0.30, 0.45, correlated=True))
```

Calculate your edge (your probability minus the market's implied probability). If edge is positive, Kelly tells you how much to bet.

## 4) Bayesian Updating - change mind like pro

Prediction markets move because new information arrives. The question isn't whether your original estimate was right - it's **how you update when the evidence changes**.

Most traders either ignore new evidence entirely (stubbornness) or overcorrect wildly (panic). Bayesian updating gives you the mathematically correct amount to adjust.

![[003 Resources/Assets/da3f81a1d24876077b8142f0fef29c24_MD5.png]]

> **simply:** your new belief = how well the evidence fits your theory × your old belief ÷ how common the evidence is in general.

the denominator P(E) is usually expanded using the law of total probability, which gives us the practical version:

![[003 Resources/Assets/1b89b8d1cfd602ad047f942d60c04b59_MD5.png]]

> **Example: Fed Rate Cut on Polymarket**

![[003 Resources/Assets/d09c16e77b6aaf1e86cafdebf1f776de_MD5.jpg]]

You hold a contract: "Will the Fed cut rates at the June meeting?" The market price is 35 cents, and you agree - your prior is 35%.

Then the monthly jobs report drops. It's much weaker than expected: 120K jobs added vs 200K expected. Unemployment ticks up. Wage growth slows.

1. **If the Fed IS going to cut, how likely is a weak jobs report?** Pretty likely. A weak economy is exactly why the Fed would cut. Your estimate: **70%**.
2. **If the Fed is NOT going to cut, how likely is a weak jobs report?** Less likely, but possible - weak reports happen even in strong economies. Your estimate: **25%**.
- **Bayesian Update Calculation:**

```plaintext
P(cut | weak jobs) = P(weak | cut) × P(cut) / [P(weak | cut) × P(cut) + P(weak | no cut) × P(no cut)]

= 0.70 × 0.35 / [(0.70 × 0.35) + (0.25 × 0.65)]

= 0.245 / [0.245 + 0.1625]

= 0.245 / 0.4075

= 0.601 = 60.1%
One data point: 35% → 60.1%. A shift of +25.1 percentage points.
```

## Likelihood Ratio - Bayes without formula

You don't need to compute the full formula every time. There's a shortcut that professional forecasters use: the **likelihood ratio**.

![[003 Resources/Assets/9f543234feb564ba578dceac3dc9270a_MD5.png]]

You don't need to compute the full formula every time. There's a shortcut that professional forecasters use: the **likelihood ratio**.

- **LR Reference Table for Common Prediction Market Scenarios**

![[003 Resources/Assets/b5155a4883dbda82b350da28ad646248_MD5.jpg]]

The chart below shows that the same evidence (LR = 3) has different effects depending on your prior. Starting at 10%, it moves you to 25%. Starting at 50%, it moves you to 75%. Starting at 90%, it barely moves you to 96%. **Evidence matters most when you're uncertain.**

<video preload="none" tabindex="-1" playsinline="" aria-label="Embedded video" poster="https://pbs.twimg.com/amplify_video_thumb/2037446487609597952/img/tgCy3bGWKoNPdYvu.jpg" style="width: 100%; height: 100%; position: absolute; background-color: black; top: 0%; left: 0%; transform: rotate(0deg) scale(1.005);"><source type="video/mp4" src="blob:https://x.com/5021c148-8c91-402e-9497-4c5024eabafd"></video>

![[003 Resources/Assets/a0ea53bedfa0165fc51e5462429d1505_MD5.jpg]]

> **Production Bayesian updater for Polymarket:**

```python
class BayesianTracker:
    def __init__(self, prior, market_name="Unnamed"):
        self.prior = prior
        self.current = prior
        self.market = market_name
        self.history = [{"event": "Initial prior",
                         "posterior": prior, "shift": 0}]
    
    def update(self, p_if_true, p_if_false, evidence_name=""):
        """Single Bayesian update"""
        num = p_if_true * self.current
        den = num + (p_if_false * (1 - self.current))
        posterior = num / den
        shift = posterior - self.current
        
        lr = p_if_true / p_if_false
        
        self.history.append({
            "event": evidence_name,
            "prior": round(self.current * 100, 1),
            "posterior": round(posterior * 100, 1),
            "shift": round(shift * 100, 1),
            "LR": round(lr, 2)
        })
        
        self.current = posterior
        return self
    
    def edge_vs_market(self, market_price):
        """Compare your posterior to market price"""
        diff = self.current - market_price
        if abs(diff) < 0.03:
            return "No edge (within 3pp of market)"
        side = "YES" if diff > 0 else "NO"
        return f"Edge on {side}: your {self.current*100:.0f}% vs market {market_price*100:.0f}%"
    
    def summary(self):
        print(f"\n=== {self.market} ===")
        for h in self.history:
            if "prior" in h:
                direction = "+" if h["shift"] > 0 else ""
                print(f"  {h['event']}: {h['prior']}% -> {h['posterior']}% ({direction}{h['shift']} pp, LR={h['LR']})")
            else:
                print(f"  {h['event']}: {h['posterior']*100:.0f}%")

# Usage: Fed rate cut example
fed = BayesianTracker(0.35, "Fed Rate Cut June")
fed.update(0.70, 0.25, "Weak jobs report")
fed.update(0.60, 0.30, "Dovish Fed speech")
fed.update(0.20, 0.50, "Hot CPI print")
fed.summary()
print(fed.edge_vs_market(0.45))
```

The traders who beat prediction markets aren't the ones who are right most often. They're the ones who **update fastest when the evidence changes**. Bayes gives you the exact speed.

## 5) Nash Equilibrium - poker formula that predicts who wins on Polymarket

In poker, a bluff isn't a guess. It's a calculation. There's a mathematically optimal frequency at which you should bluff - and if you deviate from it, a skilled opponent will exploit you.

The same math applies to prediction markets. Except on Polymarket, the "bluff" is a **contrarian trade** - going against the crowd when the market is mispriced. And "folding" is being a passive taker who pays the optimism tax.

> **How Bluff Frequency works in Poker**

In No-Limit Hold'em, when you bet, your opponent faces a decision: call or fold. Your bet gives them specific **pot odds** - the ratio of what they can win to what it costs to call.

If you bet $100 into a $200 pot, your opponent must call $100 to win $300 total. Their pot odds are 100/300 = 33%. They need to win at least 33% of the time to break even on a call.

Here's where Nash Equilibrium enters: **your optimal bluff frequency must make your opponent indifferent between calling and folding.**

If you bluff too often, they always call and profit. If you never bluff, they always fold and you never get paid on your value bets.

![[003 Resources/Assets/42293b9535538d601e172638da7d3dd0_MD5.png]]

- **Poker example:**

```plaintext
Pot = $200. You bet $100.

Bluff% = 100 / (100 + 200) = 100 / 300 = 33.3%

For every 2 value bets, you should make 1 bluff.
Your opponent can't exploit this — calling and folding both yield the same EV.

This is Nash Equilibrium: the strategy that can't be beaten by any counter-strategy.
```

## From Poker Bluffs to Contrarian Trades

On a prediction market, the two "players" are **Makers** (who provide liquidity with limit orders) and **Takers** (who consume liquidity with market orders). **The parallel to poker is direct:**

- Poker: Bluff ( weak hand bet ) = PM: Contrarian trade (against crowd bet)
- Poker: Value bet (strong hand bet) = PM: Conviction trade (follow market)

The adapted formula for prediction markets:

![[003 Resources/Assets/269ac5dc786624bd2158c861cce02e67_MD5.png]]

But the more useful formulation comes from the **indifference principle**. At Nash Equilibrium, the market should make a marginal trader indifferent between being a maker and a taker. This gives us:

![[003 Resources/Assets/18d8072240cbf99bb6625fdfd5f5f4ca_MD5.png]]

> **The Optimal Ratio Changes by Category**

Just like a poker player adjusts their bluff frequency against different opponents, your maker-taker ratio should change based on which category you're trading. The data shows dramatically different optimal frequencies.

![[003 Resources/Assets/d6942aae5bcf7d2df3e053e6503c81dd_MD5.jpg]]

**The poker parallel is exact:** against a tight, rational opponent (Finance), you bluff less - they'll catch you.

Against a loose, emotional opponent (Entertainment, Sports), you bluff more - they overpay for hope and you exploit it by providing liquidity.

> **How the Equilibrium shifted over time on Prediction markets**

One of the most fascinating findings from Becker's research: the Nash Equilibrium of the market has shifted dramatically. In the early days (2021-2023), takers were the winning population. The equilibrium strategy was the opposite of today.

<video preload="none" tabindex="-1" playsinline="" aria-label="Embedded video" poster="https://pbs.twimg.com/amplify_video_thumb/2037462265453035521/img/Y6NTRWSWQZxYjtJf.jpg" style="width: 100%; height: 100%; position: absolute; background-color: black; top: 0%; left: 0%; transform: rotate(0deg) scale(1.005);"><source type="video/mp4" src="blob:https://x.com/8300fdc0-70a0-4750-9d89-63a595174e85"></video>

![[003 Resources/Assets/fdf0fd58dafcbbda247b3270e933d1cd_MD5.jpg]]

Before October 2024, the optimal strategy was **60%+ taker** - amateur makers were the losing population, and takers captured value from their poorly-priced limit orders. After the volume explosion (Q4 2024), professional market makers entered, and the equilibrium flipped. Now the optimal strategy is **65-70% maker**.

This is exactly what game theory predicts. As the player pool changes, the equilibrium shifts. A strategy that was optimal against amateurs becomes suboptimal against professionals, and vice versa. **The meta evolves.**

## Conclusion

87% of prediction market wallets lose money. Not because prediction markets are rigged - because those traders never run the math. They buy longshots at prices worse than slot machines, size positions on vibes, ignore new evidence, and pay the optimism tax on every market order.

The 13% who win use these 5 formulas as one system, one workflow - and every single one is backed by 72.1 million trades of real data.

The window won't stay open forever. Professional market makers are already compressing the spreads - the taker edge that was +2.0% in 2022 is now -1.12%. The meta is evolving. The only question is whether you evolve with it, or keep buying lottery tickets at 43 cents on the dollar.