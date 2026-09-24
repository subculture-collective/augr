package agent

import (
	"context"
	"strings"
	"testing"
)

func TestAccountContextSurvivesStateBoundaries(t *testing.T) {
	account := &AccountContext{Cash: 100, Positions: []AccountPosition{{Ticker: "SPY", Quantity: 3}}}
	state := &PipelineState{Ticker: "SPY"}
	applyInitialStateSeed(state, InitialStateSeed{Account: account})
	account.Positions[0].Quantity = 99
	for _, got := range []*AccountContext{tradingInputFromState(state).Account, riskJudgeInputFromState(state).Account, PipelineStateFromView(snapshotState(state)).Account} {
		if got == nil || got.Cash != 100 || got.Positions[0].Quantity != 3 {
			t.Fatalf("lost snapshot: %+v", got)
		}
		got.Positions[0].Quantity = 25
	}
	if state.Account.Positions[0].Quantity != 3 {
		t.Fatal("snapshot mutation leaked into state")
	}
}

func TestOptionsHoldingsAreNotDirectUnderlyingPosition(t *testing.T) {
	account := &AccountContext{Positions: []AccountPosition{{Ticker: "SPY260925C00600000", UnderlyingTicker: "SPY", Quantity: 1, AssetClass: "option"}}}
	prompt := AccountContextPrompt(account, "SPY")
	if !strings.Contains(prompt, "no direct position in SPY") || !strings.Contains(prompt, "SPY260925C00600000") {
		t.Fatal(prompt)
	}
}

func TestRunnerPassesAccountToTraderAndRiskManager(t *testing.T) {
	calls := 0
	check := func(account *AccountContext) {
		t.Helper()
		calls++
		if account == nil || account.Cash != 1250 || len(account.Positions) != 0 {
			t.Fatalf("account not passed: %+v", account)
		}
	}
	runner := NewRunner(Definition{
		Trader: stubTradeAgent{name: "trader", role: AgentRoleTrader, fn: func(_ context.Context, input TradingInput) (TradingOutput, error) {
			check(input.Account)
			input.Account.Cash = 0 // Nodes must not mutate another node's snapshot.
			return TradingOutput{Plan: TradingPlan{Ticker: input.Ticker, Action: PipelineSignalHold}, StoredOutput: "hold"}, nil
		}},
		Risk: RiskDebateStage{
			Debaters: []DebateAgent{stubDebateAgent{name: "risk", role: AgentRoleNeutralAnalyst, fn: func(context.Context, DebateInput) (DebateOutput, error) {
				return DebateOutput{Contribution: "assess"}, nil
			}}},
			Judge: stubRiskJudge{name: "manager", role: AgentRoleRiskManager, fn: func(_ context.Context, input RiskJudgeInput) (RiskJudgeOutput, error) {
				check(input.Account)
				return RiskJudgeOutput{FinalSignal: FinalSignal{Signal: PipelineSignalHold}, TradingPlan: input.TradingPlan, StoredSignal: "hold"}, nil
			}},
		},
	}, Dependencies{Persister: newRunnerSpyPersister()})
	prepared, err := runner.Prepare(strategyWithDebateRounds(t, "SPY", 1), GlobalSettings{})
	if err != nil {
		t.Fatal(err)
	}
	prepared.InitialState.Account = &AccountContext{Cash: 1250, Positions: []AccountPosition{}}
	prepared.Runtime.SkipPhases = map[Phase]bool{PhaseAnalysis: true, PhaseResearchDebate: true, PhaseExecutionGate: true}
	result, err := runner.Run(context.Background(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || result.State.Account == nil || result.State.Account.Cash != 1250 {
		t.Fatalf("calls=%d state=%+v", calls, result.State.Account)
	}
}
