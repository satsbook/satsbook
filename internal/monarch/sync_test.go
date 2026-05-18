package monarch

import (
	"context"
	"errors"
	"testing"

	mm "github.com/eshaffer321/monarchmoney-go/pkg/monarch"
)

type mockAccountClient struct {
	accounts     []*mm.Account
	holdings     []*mm.Holding
	listErr      error
	deleteErr    error
	createInvErr error
	getHoldErr   error
	delHoldErr   error
	createHoldErr error

	createdInvAccount *mm.CreateInvestmentsAccountParams
	deletedAccountID  string
	deletedHoldingID  string
	createdHolding    *mm.CreateHoldingParams
	createInvCount    int
}

func (m *mockAccountClient) List(ctx context.Context) ([]*mm.Account, error) {
	return m.accounts, m.listErr
}

func (m *mockAccountClient) Create(ctx context.Context, params *mm.CreateAccountParams) (*mm.Account, error) {
	return &mm.Account{ID: "new-manual-id", DisplayName: params.AccountName}, nil
}

func (m *mockAccountClient) Delete(ctx context.Context, accountID string) error {
	m.deletedAccountID = accountID
	return m.deleteErr
}

func (m *mockAccountClient) CreateInvestmentsAccount(ctx context.Context, params *mm.CreateInvestmentsAccountParams) (*mm.Account, error) {
	m.createdInvAccount = params
	m.createInvCount++
	if m.createInvErr != nil {
		return nil, m.createInvErr
	}
	return &mm.Account{ID: "new-inv-id", DisplayName: params.Name}, nil
}

func (m *mockAccountClient) GetHoldings(ctx context.Context, accountID string) ([]*mm.Holding, error) {
	if m.getHoldErr != nil {
		return nil, m.getHoldErr
	}
	return m.holdings, nil
}

func (m *mockAccountClient) DeleteHolding(ctx context.Context, holdingID string) error {
	m.deletedHoldingID = holdingID
	return m.delHoldErr
}

func (m *mockAccountClient) CreateHolding(ctx context.Context, params *mm.CreateHoldingParams) (*mm.Holding, error) {
	m.createdHolding = params
	if m.createHoldErr != nil {
		return nil, m.createHoldErr
	}
	return &mm.Holding{ID: "new-hold-id", AccountID: params.AccountID, Symbol: "BTC", Quantity: params.Quantity}, nil
}

func TestSyncHolding(t *testing.T) {
	tests := []struct {
		name    string
		mock    *mockAccountClient
		initID  string
		btcQty  float64
		wantErr bool
		check   func(t *testing.T, m *mockAccountClient)
	}{
		{
			name: "first sync creates account with holding",
			mock: &mockAccountClient{
				accounts: []*mm.Account{},
				holdings: []*mm.Holding{{ID: "h1", Symbol: "BTC", Quantity: 1.5}},
			},
			btcQty: 1.5,
			check: func(t *testing.T, m *mockAccountClient) {
				if m.createInvCount != 1 {
					t.Fatalf("expected 1 account creation, got %d", m.createInvCount)
				}
				if m.createdInvAccount.InitialHoldings[0].Quantity != 1.5 {
					t.Errorf("expected initial quantity 1.5, got %f", m.createdInvAccount.InitialHoldings[0].Quantity)
				}
				// Quantity matches, so no holding update needed
				if m.deletedHoldingID != "" {
					t.Error("should not delete holding when quantity matches")
				}
			},
		},
		{
			name:   "subsequent sync updates holding in place",
			initID: "existing-id",
			mock: &mockAccountClient{
				holdings: []*mm.Holding{{ID: "h1", Symbol: "BTC", Quantity: 1.0}},
			},
			btcQty: 2.5,
			check: func(t *testing.T, m *mockAccountClient) {
				if m.createInvCount != 0 {
					t.Error("should not recreate account")
				}
				if m.deletedHoldingID != "h1" {
					t.Errorf("expected delete holding h1, got %s", m.deletedHoldingID)
				}
				if m.createdHolding == nil {
					t.Fatal("expected holding recreation")
				}
				if m.createdHolding.Quantity != 2.5 {
					t.Errorf("expected quantity 2.5, got %f", m.createdHolding.Quantity)
				}
			},
		},
		{
			name:   "skips update when quantity unchanged",
			initID: "existing-id",
			mock: &mockAccountClient{
				holdings: []*mm.Holding{{ID: "h1", Symbol: "BTC", Quantity: 1.5}},
			},
			btcQty: 1.5,
			check: func(t *testing.T, m *mockAccountClient) {
				if m.deletedHoldingID != "" {
					t.Error("should not delete holding when quantity matches")
				}
				if m.createdHolding != nil {
					t.Error("should not create holding when quantity matches")
				}
			},
		},
		{
			name:   "falls back to recreate on holding delete failure",
			initID: "existing-id",
			mock: &mockAccountClient{
				holdings:   []*mm.Holding{{ID: "h1", Symbol: "BTC", Quantity: 1.0}},
				delHoldErr: errors.New("delete failed"),
			},
			btcQty: 2.0,
			check: func(t *testing.T, m *mockAccountClient) {
				if m.deletedAccountID != "existing-id" {
					t.Error("expected account deletion on fallback")
				}
				if m.createInvCount != 1 {
					t.Errorf("expected 1 account recreation, got %d", m.createInvCount)
				}
			},
		},
		{
			name:   "falls back to recreate on create holding failure",
			initID: "existing-id",
			mock: &mockAccountClient{
				holdings:      []*mm.Holding{{ID: "h1", Symbol: "BTC", Quantity: 1.0}},
				createHoldErr: errors.New("create failed"),
			},
			btcQty: 2.0,
			check: func(t *testing.T, m *mockAccountClient) {
				if m.deletedAccountID != "existing-id" {
					t.Error("expected account deletion on fallback")
				}
				if m.createInvCount != 1 {
					t.Errorf("expected 1 account recreation, got %d", m.createInvCount)
				}
			},
		},
		{
			name: "list error propagates",
			mock: &mockAccountClient{
				listErr: errors.New("network error"),
			},
			btcQty:  1.0,
			wantErr: true,
		},
		{
			name: "create account error propagates",
			mock: &mockAccountClient{
				accounts:     []*mm.Account{},
				createInvErr: errors.New("create failed"),
			},
			btcQty:  1.0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSyncerWithClient(tt.mock, tt.initID)
			err := s.SyncHolding(context.Background(), tt.btcQty)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, tt.mock)
			}
		})
	}
}
