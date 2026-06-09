---
title: Glossary
created: 2026-06-08
tags: [reference, glossary, obsidian]
status: packaged
links:
  - "[[Automated Trading Synthesis]]"
  - "[[Core Theory Frameworks]]"
  - "[[Polymarket Flow]]"
  - "[[Stocks and Options Flow]]"
---

# Glossary

## B

### [[Bayesian updating]]
A probability update method that combines a prior belief with new evidence. Useful for event markets, macro signals, weather updates, and news-driven repricing.

### [[Bregman projection]]
A projection method referenced in advanced prediction-market arbitrage material. It can represent the distance between current prices and an arbitrage-free set. Later-stage research topic for Augr.

## C

### [[CLOB]]
Central limit order book. A venue structure where buy and sell orders rest at price levels and match by price/time priority.

### [[Copy trading]]
Mirroring another wallet or trader. Useful for research and discovery, but dangerous if copied trades arrive after the original edge has already been captured.

## D

### [[Delta]]
An option Greek measuring price sensitivity to a one-unit move in the underlying.

## E

### [[Expected value]]
The average expected result of a trade if repeated many times. A positive EV trade is not guaranteed to win, but should be profitable over a large sample if assumptions hold.

## F

### [[Frank-Wolfe]]
An iterative optimization algorithm referenced for large combinatorial arbitrage problems. Useful for later-stage prediction-market constraint solving.

## G

### [[Gamma]]
An option Greek measuring how quickly delta changes as the underlying moves.

### [[Greeks]]
Option risk sensitivities: delta, gamma, vega, theta, and related measures.

## K

### [[Kelly criterion]]
A formula for optimal bet sizing under known edge and odds. Production systems should use fractional Kelly due to model uncertainty and drawdowns.

## L

### [[Limit order]]
An order with a specified price. Usually preferable for small-edge strategies because it controls execution price.

### [[Longshot bias]]
The tendency for low-probability lottery-like outcomes to be overpriced relative to realized probability.

## M

### [[Maker]]
A trader whose order rests on the book and supplies liquidity.

### [[Markov chain]]
A state-transition model where the next state depends on the current state. Used in the source materials for short-horizon crypto market persistence.

### [[Monte Carlo]]
A simulation method that estimates probabilities by sampling many possible future paths.

## N

### [[Near-resolution bot]]
A bot that trades when an outcome is almost decided but market price has not fully converged to final payout.

## P

### [[pUSD]]
Polymarket collateral terminology in current official docs. Verify at implementation time.

### [[Post-only]]
An order type that only posts liquidity and will not immediately take liquidity.

## S

### [[Sweeper bot]]
A queue-sensitive near-resolution bot that tries to absorb winning shares sold below final payout.

## T

### [[Taker]]
A trader whose order immediately matches against resting liquidity.

### [[Theta]]
Option time decay; the daily cost or benefit from time passing.

## V

### [[Vega]]
Option sensitivity to implied-volatility changes.

## W

### [[Wallet intelligence]]
Analysis of successful wallets to infer market selection, timing, sizing, and category specialization.
