---
title: "Markov Chains: nuclear algorithm used by Quants to crush 87% of Polymarket traders"
source: "https://x.com/0xMovez/status/2041842410112639172"
author:
  - "[[@0xMovez]]"
published: 2026-01-18
created: 2026-06-07
description: "How the invention of the nuclear bomb, the creation of Google, and LLM models are connected to building a successful trading strategy on Pol..."
tags:
  - "clippings"
---
![[003 Resources/Assets/eee406123b4db642a35bf73034c03039_MD5.jpg]]

How the invention of the nuclear bomb, the creation of Google, and LLM models are connected to building a successful trading strategy on Polymarket.

In 1906, a Russian mathematician named Andrey Markov was so furious at a colleague that he analyzed 20,000 letters of a poem - by hand - just to prove him wrong.

That act of mathematical spite created a framework that would go on to help build the nuclear bomb, power Google's $2 trillion search engine, and predict the next word in every AI chatbot you've ever used.

It's called a **Markov chain**. And if you trade on Polymarket without understanding it, you're leaving money on the table.

> **Best Polymarket algo bots that use Markov Chains in their trading:**

• RN - Polymarket algo bot that made +$7M PnL on Sports. Profile: [https://polymarket.com/@rn1?via=following](https://polymarket.com/@rn1?via=following)

![[003 Resources/Assets/80b1db01ad192cbfb79c86b8a6b4120a_MD5.jpg]]

• ColdMath - best weather trading algo turned $1.2K → $105K using the same math. Profile:[https://polymarket.com/@coldmath?via=following](https://polymarket.com/@coldmath?via=following)

![[003 Resources/Assets/0b4bf62c33e5f9588914ffaab035cddd_MD5.jpg]]

• Sharky6999 - Quant bot with a 99% win rate and $807K PnL made in 27K trades. Profile: [https://polymarket.com/@sharky6999?via=following](https://polymarket.com/@sharky6999?via=following)

![[003 Resources/Assets/250caf523efe857c7c19a194f1a8ed09_MD5.jpg]]

This is the story of one equation, five breakthroughs, and one prediction market strategy that ties them all together.

## Part (1): The Russian math feud (1906)

In 1905, Russia was tearing itself apart. Socialists demanded the Tsar step down. Tsarists wanted to keep the monarchy. The division was so bad it crept into every institution - including the math department.

On the Tsar's side was **Pavel Nekrasov**, the "Tsar of Probability." A deeply religious man, Nekrasov believed mathematics could prove the existence of free will.

His argument was elegant. He pointed to social statistics - marriage rates, crime rates, birth rates - and noticed they followed the **Law of Large Numbers**.

![[003 Resources/Assets/b7b33488ef63e68162c0d951f0aceab9_MD5.jpg]]

Number of Belgian Marriages from 1841 to 1845, showing the Law of Large Numbers

The averages converged year after year. Since Bernoulli had proved the law only works for independent events, Nekrasov concluded that human decisions must be independent. And if decisions are independent, they must be acts of free will.

## The law of large numbers - a coin flip away from philosophy

To understand what Nekrasov was claiming, consider a coin flip.

Flip a coin 10 times and you might get 6 heads and 4 tails - obviously not the 50/50 you'd expect. But keep flipping. After a large number of flips, the ratio slowly settles down and approaches 50/50.

<video preload="none" tabindex="-1" playsinline="" aria-label="Embedded video" poster="https://pbs.twimg.com/amplify_video_thumb/2041805570567532544/img/XGpN9e-MWVWkVUE0.jpg" style="width: 100%; height: 100%; position: absolute; background-color: black; top: 0%; left: 0%; transform: rotate(0deg) scale(1.005);"><source type="video/mp4" src="blob:https://x.com/e211ba11-7f69-4693-bd00-c5b8c228afa1"></video>

![[003 Resources/Assets/c18623c1c2f31f54dac088a7f303f41b_MD5.jpg]]

> **The Law of Large Numbers.** At first the head/tail ratio jumps wildly (orange line). After enough flips, it converges toward 0.50 (red dashed line).

Bernoulli proved this in 1713 - but only for **independent events**, like fair coin flips. Nekrasov exploited that loophole: if marriage statistics converge, then marriage decisions must be independent. Math proves free will. Math proves God.

> **Why it matters for Polymarket:** each trade is noisy individually, but over thousands of trades, price converges to true probability. That's why prediction markets work at all.

But the key question - the one Markov asked - is whether this still holds for **dependent events**. Can you model price movements that depend on previous prices?

## The poem that сhanged Probability

On the other side stood Andrey Markov - an atheist mathematician nicknamed "Andrey the Furious" - who thought Nekrasov was delusional. Math had nothing to do with God or free will.

But disproving Nekrasov required showing that dependent events could also follow the Law of Large Numbers - and for that, Markov needed a clear real-world example.**He chose Russian poetry.**

Markov took "Eugene Onegin" by Alexander Pushkin - the most celebrated poem in Russian literature - stripped out all punctuation and spaces, and pushed 20,000 letters into one long string.

![[003 Resources/Assets/2f55c63e1ddb517826d7540569a006c0_MD5.png]]

The letters were clearly dependent - a vowel makes the next letter much more likely to be a consonant, and vice versa. But when Markov ran his prediction machine - two states (V and C), transition probabilities based on the data - the ratio of vowels to consonants still converged to 43/57.

**The Law of Large Numbers still worked, even with dependent events.**

Markov ended his paper with a devastating one-liner: "Thus, free will is not necessary to do probability."

## Part (2) - the Nuclear Bomb & Solitaire (1946)

On July 16, 1945, the United States detonated the world's first nuclear bomb. A 6-kilogram plutonium core created an explosion equivalent to 25,000 tons of TNT.

![[003 Resources/Assets/f4950c5143192f9154649b609a4d2f85_MD5.jpg]]

But even after the war, a key question remained: **how much uranium do you actually need to build a bomb?**

That depended on how neutrons behave inside a nuclear core - a system with trillions of particles, each interacting with its surroundings.

Computing this directly was impossible. The number of possible outcomes was astronomical.

Then mathematician **Stanislaw Ulam** got sick. Encephalitis. Months in bed. To pass the time, he played Solitaire. Game after game, he kept wondering: what are the odds that a random shuffle can be won?

52 cards. 52 factorial possible arrangements. That's 8 × 10⁶⁷ - more than the number of atoms in the observable universe. Solving it analytically? Hopeless.

But then Ulam had a flash of insight: **what if I just play hundreds of games and count how many I win?** That gives a statistical approximation - no exact calculation needed.

![[003 Resources/Assets/843c4c525ebfb2b717d1818c381cfb06_MD5.jpg]]

When he returned to Los Alamos, he applied the same idea to neutrons. Instead of computing every possible interaction, they **simulated** random neutron paths through the core using a Markov chain:

run this thousands of times. Count how many neutrons are produced per run (the multiplication factor **k**). If k > 1, the reaction grows exponentially. You have a bomb.

<video preload="none" tabindex="-1" playsinline="" aria-label="Embedded video" poster="https://pbs.twimg.com/amplify_video_thumb/2041806304461910018/img/2Ih_XP1m96qy_HkF.jpg" style="width: 100%; height: 100%; position: absolute; background-color: black; top: 0%; left: 0%; transform: rotate(0deg) scale(1.005);"><source type="video/mp4" src="blob:https://x.com/2a177189-3ef2-46b0-9f72-4ea76448a591"></video>

![[003 Resources/Assets/a38c3d516c13568e35075b9b41e1e675_MD5.jpg]]

Ulam named the method after his uncle's favorite casino. **The Monte Carlo method was born.**

> **Key insight for Polymarket:** Monte Carlo simulation - running thousands of random scenarios to approximate the answer - is exactly how you should model prediction market outcomes.

You don't need to compute every possible future. You simulate thousands of them and count the wins.

## Part (3) - Google and the $2 Trillion Markov Chain (1998)

By the mid-1990s, the internet was exploding. Thousands of new pages every day. Search engines like Yahoo ranked pages by keyword frequency - how often a search term appeared on the page. The problem? Easy to game. Just repeat keywords in white text on a white background.

![[003 Resources/Assets/da5b1e8df9e4f2a6451dd4f12e2b3bad_MD5.jpg]]

Two Stanford PhD students, **Sergey Brin** and **Larry Page**, realized that the web itself contained a signal that keyword frequency missed: **links**. Each link to a page is an endorsement. A page with many links from other important pages is probably important too.

They modeled the entire web as a Markov chain. Each webpage is a state. Each link is a transition.

> A "random surfer" follows links with 85% probability and jumps to a random page 15% of the time (the damping factor - to avoid getting stuck in loops)

Run this chain long enough, and the fraction of time spent on each page converges to a steady state - **PageRank**. The pages you visit most often in the simulation are the most important.

![[003 Resources/Assets/eb1950bae7ffd67e828b13bcd8199749_MD5.png]]

One equation. One Markov chain. And it turned into a $2 trillion company. The core insight: **you don't need to analyze every page. Just model the transitions between them, and the importance emerges from the structure.**

## Part ( 4 ) - How Quants Use Markov Chains to Trade Polymarket

87% of wallets on prediction markets lose money. The top 13% aren't luckier - they model the market as a **Markov chain**, run thousands of simulations, and bet only when the math gives them edge.

This article is the practical playbook. No history, no theory - just the **exact framework** quants use to extract value from Polymarket. Every formula has a Python implementation. Every claim is backed by 72.1 million trades.

## 1) Model: market as a state machine

A Polymarket contract has a price between 0¢ and 100¢. At any given moment, the price is in one of these states. The next state depends on the current state - not the entire history. This is a **Markov chain**.

- The quant approach: discretize the price into 10 states (0-10¢, 10-20¢, ..., 90-100¢), count how often the price moves from each state to every other state, and build a **transition matrix**.

![[003 Resources/Assets/f1576e6889e0b01bac82681ce75fec10_MD5.png]]

```python
import numpy as np

def build_transition_matrix(prices, n_states=10):
    """Convert price history → transition matrix."""
    states = np.clip(
        (np.array(prices) * n_states).astype(int),
        0, n_states - 1
    )
    T = np.zeros((n_states, n_states))
    for i in range(len(states) - 1):
        T[states[i], states[i+1]] += 1
    
    # Normalize rows → probabilities
    row_sums = T.sum(axis=1, keepdims=True)
    row_sums[row_sums == 0] = 1
    return T / row_sums

# Example: 60 days of price history
prices = [0.45, 0.47, 0.43, 0.48, 0.52, ...]
T = build_transition_matrix(prices)
# T[4] = [0, 0, 0.05, 0.15, 0.40, 0.25, 0.10, 0.05, 0, 0]
# From state 4 (40-50¢): 40% stay, 25% → up, 15% → down
```

The matrix row T\[4\] tells you: from state 4 (price 40-50¢), there's a 40% chance of staying, 25% of going up one state, 15% of going down, etc. Every row sums to 1.0.

> **Key insight:** Markets near 50¢ have wider transition distributions (can move in either direction).

Markets near 5¢ or 95¢ have narrower distributions - they tend to stay near the extremes. This asymmetry is the foundation of the longshot bias.

## 2) Simulation: Monte Carlo - 10,000 futures in 0.1 seconds

Once you have the transition matrix, you simulate. Start at the current price state. Walk forward through the matrix for N days. Repeat 10,000 times. Count outcomes.

![[003 Resources/Assets/24defb052c66468a05d06f323890e221_MD5.png]]

If your model says 55% and the market says 45%, that's a 10¢ edge per contract. On 100 contracts ($45 invested), that's $10 expected profit.

<video preload="none" tabindex="-1" playsinline="" aria-label="Embedded video" poster="https://pbs.twimg.com/amplify_video_thumb/2041820750739566592/img/kI69e3mVWWoti-Ts.jpg" style="width: 100%; height: 100%; position: absolute; background-color: black; top: 0%; left: 0%; transform: rotate(0deg) scale(1.005);"><source type="video/mp4" src="blob:https://x.com/7c0663c7-9b42-45d0-b018-5975f8d25706"></video>

![[003 Resources/Assets/865ce48a8a6d15a13e68ae5c12584e8e_MD5.jpg]]

Each line is one simulated future. Some paths go to 90¢ (YES resolves). Some crash to 5¢ (NO resolves).

The **orange line** is the median. The red zone (<20¢) is where longshot bias is strongest. The green zone (>80¢) is where near-certainties are underpriced.

> **Python code for Monte Carlo simulation**

```python
def monte_carlo(T, start_state, days=30, n_sims=10000):
    """Simulate N price paths through transition matrix."""
    n_states = len(T)
    finals = []
    for _ in range(n_sims):
        state = start_state
        for _ in range(days):
            state = np.random.choice(n_states, p=T[state])
        finals.append(state)

    finals = np.array(finals)
    p_yes = (finals >= n_states // 2).mean()
    return p_yes

# Market at 45¢, run 10K sims for 30 days
p = monte_carlo(T, start_state=4)
print(f"Model says: {p:.1%} | Market says: 45%")
print(f"Edge: {(p - 0.45)*100:+.1f}¢ per contract")
```

## 3) Data: what 72 million trades reveal

In 2026, researcher Jonathan Becker analyzed **72.1 million trades** across **$18.26 billion** in volume. Every resolved market - politics, sports, crypto, weather. The largest empirical study of prediction market microstructure ever published.

> Jan 18
> 
> 0/ i analyzed every single trade on kalshi from 2021 to 2025. i found a systematic wealth transfer where "takers" pay a massive premium for affirmative outcomes, and "makers" harvest the edge without needing to predict the future. here is the data

4 findings that change how you trade:

> **Finding 1: The Longshot Bias**

A contract priced at 5¢ should win 5% of the time. It wins 4.18%. A contract at 1¢ should win 1% - it wins 0.43%. **Cheap contracts are systematically overpriced.**

![[003 Resources/Assets/bc17ec8b1bf99cdc6bf68ece20f2a2cd_MD5.jpg]]

For every dollar you put into 1¢ contracts as a taker, you get back **43 cents**. Worse than a slot machine (93¢). This bias means your Monte Carlo simulation will overestimate longshot probabilities unless you calibrate against it.

> **Finding 2: Maker-Taker Wealth Transfer**

**Makers** (limit orders) earn +1.12% per trade. **Takers** (market orders) lose −1.12%. The gap is 2.24 percentage points, statistically bulletproof across 72.1 million trades.

![[003 Resources/Assets/e20369362261a0714318b699b6f46caa_MD5.jpg]]

Makers don't win because they predict better - makers buying YES earn +0.77%, makers buying NO earn +1.25%. **They win because they provide liquidity to people who systematically overpay.**

**Implication for your bot:** Every market order you place pays the Optimism Tax. Use limit orders. Always. Your Monte Carlo might say "buy" - but how you buy determines whether you're a maker (+1.12%) or a taker (−1.12%).

> **Finding 3: Category Edge**

Not all markets are equally inefficient. The gap between makers and takers varies 40× across categories:

![[003 Resources/Assets/ba0255aecedbe519736a65313324fa58_MD5.jpg]]

**Finance** (0.17 pp) is nearly efficient -options traders and macro analysts who think in probabilities.

**Entertainment/World Events** (4.79-7.32 pp) attract fans and narrative bettors. If you're building a maker bot, target Sports/Crypto/Entertainment where the Optimism Tax is highest.

> **Finding 4: The Optimism Tax**

At 1¢, YES returns −41% while NO returns +23%. A **64 percentage point gap**. Takers disproportionately buy YES - betting on their team, their candidate, their bags. NO outperforms YES at **69 of 99 price levels**.

**Rule:** If you must trade as a taker below 30¢, buy NO instead of YES. You're not fighting the bias - you're riding it.

## The 5-Step Quant System

> **1.** **Build the Markov model**

Take 30-60 days of price history. Discretize into 10 states. Compute the transition matrix. This captures how the market actually moves - not how you think it moves.

> **2\. Run Monte Carlo**

Start from the current state. Run 10,000 random walks through the matrix. Count how many end above 50¢.

That fraction = your estimated true probability.

> **3\. Calibrate against the bias**

If your model says a 5¢ contract has a 5% chance - adjust down. Becker's data says it's really 4.18%.

Apply the mispricing table as a correction factor to your Monte Carlo output.

> **4\. Size with Kelly**

Kelly Criterion tells you how much to bet. Use quarter-Kelly (0.25×) - full Kelly is mathematically optimal but emotionally destructive.

> **5\. Execute via limit orders**

Place limit orders, not market orders. Maker = +1.12%. Taker = −1.12%. The execution method alone is a 2.24 pp swing on your returns.

## Conclusion:

One mathematical lineage stretching from a Russian math feud in 1906 to nuclear bomb simulations in 1946 to Google's PageRank in 1998 - all the way to Polymarket in 2026.

The 87% who lose trade on intuition. The 13% who win trade on transition matrices. The math has been free for 120 years. The only question is whether you'll use it.