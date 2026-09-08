package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type stubEconomicAccountReader struct {
	accounts []domain.Account
	flows    []domain.CapitalFlow
	summary  *domain.AccountCapitalSummary
	err      error
	gotID    uuid.UUID
	limit    int
	offset   int
}

func (s *stubEconomicAccountReader) List(_ context.Context, limit, offset int) ([]domain.Account, error) {
	s.limit, s.offset = limit, offset
	return s.accounts, s.err
}

func (s *stubEconomicAccountReader) GetByID(_ context.Context, id uuid.UUID) (*domain.Account, error) {
	s.gotID = id
	if s.err != nil {
		return nil, s.err
	}
	return &s.accounts[0], nil
}

func (s *stubEconomicAccountReader) ListCapitalFlows(_ context.Context, id uuid.UUID, limit, offset int) ([]domain.CapitalFlow, error) {
	s.gotID, s.limit, s.offset = id, limit, offset
	return s.flows, s.err
}

func (s *stubEconomicAccountReader) GetCapitalSummary(_ context.Context, id uuid.UUID) (*domain.AccountCapitalSummary, error) {
	s.gotID = id
	return s.summary, s.err
}

type stubEconomicLedgerReader struct {
	transaction *ledger.Transaction
	err         error
	gotID       uuid.UUID
}

func (s *stubEconomicLedgerReader) GetByID(_ context.Context, id uuid.UUID) (*ledger.Transaction, error) {
	s.gotID = id
	return s.transaction, s.err
}

func TestEconomicReadRoutesAreDisabledByDefault(t *testing.T) {
	id := uuid.NewString()
	for _, path := range []string{
		"/api/v1/accounts/" + testAPIAccountID.String() + "/economic/account",
		"/api/v1/accounts/" + testAPIAccountID.String() + "/economic/capital-flows",
		"/api/v1/accounts/" + testAPIAccountID.String() + "/economic/capital-summary",
		"/api/v1/accounts/" + testAPIAccountID.String() + "/economic/ledger-transactions/" + id,
	} {
		rr := doRequest(t, newTestServer(t), http.MethodGet, path, nil)
		if rr.Code != http.StatusNotImplemented {
			t.Fatalf("GET %s status = %d; body: %s", path, rr.Code, rr.Body.String())
		}
	}
}

func TestCurrentUserAccountsReturnsOnlyConfiguredAccount(t *testing.T) {
	deps := testDeps()
	deps.EconomicAccounts = &stubEconomicAccountReader{accounts: []domain.Account{{ID: testAPIAccountID, Name: "canonical"}}}
	srv := newTestServerWithDeps(t, deps)
	rr := doRequest(t, srv, http.MethodGet, "/api/v1/me/accounts", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	accounts := decodeJSON[[]domain.Account](t, rr)
	if len(accounts) != 1 || accounts[0].ID != testAPIAccountID {
		t.Fatalf("accounts=%+v", accounts)
	}
}

func TestEconomicReadRoutesInspectAccountsAndLedger(t *testing.T) {
	accountID := testAPIAccountID
	transactionID := uuid.New()
	account := domain.Account{ID: accountID, Name: "scored-500", StartingCapital: decimal.NewFromInt(500)}
	flow := domain.CapitalFlow{ID: uuid.New(), AccountID: accountID, Amount: decimal.NewFromInt(500)}
	summary := &domain.AccountCapitalSummary{AccountID: accountID, StartingCapital: decimal.NewFromInt(500), NetCapital: decimal.NewFromInt(500)}
	transaction := &ledger.Transaction{ID: transactionID, AccountID: accountID}
	accounts := &stubEconomicAccountReader{accounts: []domain.Account{account}, flows: []domain.CapitalFlow{flow}, summary: summary}
	ledgerReader := &stubEconomicLedgerReader{transaction: transaction}
	deps := testDeps()
	deps.EconomicAccounts, deps.EconomicLedger = accounts, ledgerReader
	srv := newTestServerWithDeps(t, deps)

	base := "/api/v1/accounts/" + accountID.String() + "/economic"
	rr := doRequest(t, srv, http.MethodGet, base+"/account", nil)
	if rr.Code != http.StatusOK || accounts.gotID != accountID {
		t.Fatalf("account status=%d id=%s body=%s", rr.Code, accounts.gotID, rr.Body.String())
	}
	for _, path := range []string{
		base + "/capital-flows?limit=3&offset=1",
		base + "/capital-summary",
	} {
		rr = doRequest(t, srv, http.MethodGet, path, nil)
		if rr.Code != http.StatusOK || accounts.gotID != accountID {
			t.Fatalf("GET %s status=%d id=%s body=%s", path, rr.Code, accounts.gotID, rr.Body.String())
		}
	}
	rr = doRequest(t, srv, http.MethodGet, base+"/ledger-transactions/"+transactionID.String(), nil)
	if rr.Code != http.StatusOK || ledgerReader.gotID != transactionID {
		t.Fatalf("ledger status=%d id=%s body=%s", rr.Code, ledgerReader.gotID, rr.Body.String())
	}
}

func TestEconomicReadRoutesRejectInvalidAndMissingIdentity(t *testing.T) {
	deps := testDeps()
	deps.EconomicAccounts = &stubEconomicAccountReader{err: repository.ErrNotFound}
	deps.EconomicLedger = &stubEconomicLedgerReader{err: repository.ErrNotFound}
	srv := newTestServerWithDeps(t, deps)
	for _, path := range []string{
		"/api/v1/accounts/" + testAPIAccountID.String() + "/economic/ledger-transactions/not-a-uuid",
	} {
		if rr := doRequest(t, srv, http.MethodGet, path, nil); rr.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status=%d body=%s", path, rr.Code, rr.Body.String())
		}
	}
	id := uuid.NewString()
	base := "/api/v1/accounts/" + testAPIAccountID.String() + "/economic"
	for _, path := range []string{
		base + "/account",
		base + "/capital-flows",
		base + "/capital-summary",
		base + "/ledger-transactions/" + id,
	} {
		if rr := doRequest(t, srv, http.MethodGet, path, nil); rr.Code != http.StatusNotFound {
			t.Fatalf("GET %s status=%d body=%s", path, rr.Code, rr.Body.String())
		}
	}
}
