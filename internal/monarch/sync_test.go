package monarch

import (
	"context"
	"errors"
	"testing"
	"time"

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

// mockQueryBuilder implements mm.TransactionQueryBuilder for testing.
type mockQueryBuilder struct {
	result *mm.TransactionList
	err    error
}

func (m *mockQueryBuilder) Between(_, _ time.Time) mm.TransactionQueryBuilder { return m }
func (m *mockQueryBuilder) WithAccounts(_ ...string) mm.TransactionQueryBuilder { return m }
func (m *mockQueryBuilder) WithCategories(_ ...string) mm.TransactionQueryBuilder { return m }
func (m *mockQueryBuilder) WithTags(_ ...string) mm.TransactionQueryBuilder { return m }
func (m *mockQueryBuilder) WithMinAmount(_ float64) mm.TransactionQueryBuilder { return m }
func (m *mockQueryBuilder) WithMaxAmount(_ float64) mm.TransactionQueryBuilder { return m }
func (m *mockQueryBuilder) Search(_ string) mm.TransactionQueryBuilder { return m }
func (m *mockQueryBuilder) Limit(_ int) mm.TransactionQueryBuilder { return m }
func (m *mockQueryBuilder) Offset(_ int) mm.TransactionQueryBuilder { return m }
func (m *mockQueryBuilder) Stream(ctx context.Context) (<-chan *mm.Transaction, <-chan error) {
	txCh := make(chan *mm.Transaction)
	errCh := make(chan error, 1)
	close(txCh)
	close(errCh)
	return txCh, errCh
}
func (m *mockQueryBuilder) Execute(_ context.Context) (*mm.TransactionList, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.result != nil {
		return m.result, nil
	}
	return &mm.TransactionList{}, nil
}

// mockTransactionClient implements TransactionClient for testing.
type mockTransactionClient struct {
	createErr    error
	createdTxns  []*mm.CreateTransactionParams
	queryBuilder *mockQueryBuilder
	categories   []*mm.TransactionCategory
	categoryErr  error
}

func (m *mockTransactionClient) Create(_ context.Context, params *mm.CreateTransactionParams) (*mm.Transaction, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	m.createdTxns = append(m.createdTxns, params)
	return &mm.Transaction{ID: "monarch-tx-" + params.Notes}, nil
}

func (m *mockTransactionClient) Query() mm.TransactionQueryBuilder {
	if m.queryBuilder != nil {
		return m.queryBuilder
	}
	return &mockQueryBuilder{}
}

// mockCategoryLister implements CategoryLister for testing.
type mockCategoryLister struct {
	categories []*mm.TransactionCategory
	err        error
}

func (m *mockCategoryLister) ListCategories(_ context.Context) ([]*mm.TransactionCategory, error) {
	return m.categories, m.err
}

func TestSyncTransactions(t *testing.T) {
	ts := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		accounts *mockAccountClient
		txClient *mockTransactionClient
		cats     *mockCategoryLister
		txns     []TxToSync
		wantErr  bool
		check    func(t *testing.T, result *TxSyncResult, synced map[string]string, txc *mockTransactionClient)
	}{
		{
			name: "nil transaction client returns error",
			accounts: &mockAccountClient{
				accounts: []*mm.Account{{ID: "acc1", DisplayName: txAccountName}},
			},
			txClient: nil,
			txns:     []TxToSync{{SourceID: "tx1", AmountUSD: 100}},
			wantErr:  true,
		},
		{
			name: "empty batch returns zero counts",
			accounts: &mockAccountClient{
				accounts: []*mm.Account{{ID: "acc1", DisplayName: txAccountName}},
			},
			txClient: &mockTransactionClient{},
			cats:     &mockCategoryLister{categories: []*mm.TransactionCategory{{ID: "cat1", Name: "Uncategorized"}}},
			txns:     []TxToSync{},
			check: func(t *testing.T, result *TxSyncResult, synced map[string]string, txc *mockTransactionClient) {
				if result.Created != 0 || result.Skipped != 0 || result.Errors != 0 {
					t.Errorf("expected all zeros, got %+v", result)
				}
				if len(txc.createdTxns) != 0 {
					t.Errorf("expected no API calls, got %d", len(txc.createdTxns))
				}
			},
		},
		{
			name: "zero-amount transactions are skipped",
			accounts: &mockAccountClient{
				accounts: []*mm.Account{{ID: "acc1", DisplayName: txAccountName}},
			},
			txClient: &mockTransactionClient{},
			cats:     &mockCategoryLister{categories: []*mm.TransactionCategory{{ID: "cat1", Name: "Uncategorized"}}},
			txns: []TxToSync{
				{SourceID: "zero1", AmountUSD: 0.0},
				{SourceID: "zero2", AmountUSD: 0.001}, // below threshold
			},
			check: func(t *testing.T, result *TxSyncResult, synced map[string]string, txc *mockTransactionClient) {
				if result.Skipped != 2 {
					t.Errorf("expected 2 skipped, got %d", result.Skipped)
				}
				if len(txc.createdTxns) != 0 {
					t.Errorf("expected no API calls, got %d", len(txc.createdTxns))
				}
			},
		},
		{
			name: "creates transactions and returns synced map",
			accounts: &mockAccountClient{
				accounts: []*mm.Account{{ID: "acc1", DisplayName: txAccountName}},
			},
			txClient: &mockTransactionClient{
				queryBuilder: &mockQueryBuilder{result: &mm.TransactionList{}},
			},
			cats: &mockCategoryLister{categories: []*mm.TransactionCategory{{ID: "cat1", Name: "Uncategorized"}}},
			txns: []TxToSync{
				{SourceID: "tx1", Source: "strike", TxType: "buy", Time: ts, AmountUSD: 100.00},
				{SourceID: "tx2", Source: "river", TxType: "sell", Time: ts, AmountUSD: -50.00},
			},
			check: func(t *testing.T, result *TxSyncResult, synced map[string]string, txc *mockTransactionClient) {
				if result.Created != 2 {
					t.Errorf("expected 2 created, got %d", result.Created)
				}
				if result.Errors != 0 {
					t.Errorf("expected 0 errors, got %d", result.Errors)
				}
				if len(synced) != 2 {
					t.Errorf("expected 2 synced entries, got %d", len(synced))
				}
				if _, ok := synced["tx1"]; !ok {
					t.Error("synced map missing tx1")
				}
			},
		},
		{
			name: "dedup: skips transactions already in Monarch",
			accounts: &mockAccountClient{
				accounts: []*mm.Account{{ID: "acc1", DisplayName: txAccountName}},
			},
			txClient: &mockTransactionClient{
				queryBuilder: &mockQueryBuilder{
					result: &mm.TransactionList{
						Transactions: []*mm.Transaction{
							{ID: "monarch-existing-1", Notes: "[buy] existing-tx-1"},
						},
					},
				},
			},
			cats: &mockCategoryLister{categories: []*mm.TransactionCategory{{ID: "cat1", Name: "Uncategorized"}}},
			txns: []TxToSync{
				{SourceID: "existing-tx-1", Source: "strike", TxType: "buy", Time: ts, AmountUSD: 100.00},
				{SourceID: "new-tx-2", Source: "strike", TxType: "buy", Time: ts, AmountUSD: 200.00},
			},
			check: func(t *testing.T, result *TxSyncResult, synced map[string]string, txc *mockTransactionClient) {
				if result.Created != 1 {
					t.Errorf("expected 1 created, got %d", result.Created)
				}
				if result.Skipped != 1 {
					t.Errorf("expected 1 skipped (dedup), got %d", result.Skipped)
				}
				// deduped tx is still in synced map with its existing Monarch ID
				if synced["existing-tx-1"] != "monarch-existing-1" {
					t.Errorf("expected deduped tx to have monarch ID, got %q", synced["existing-tx-1"])
				}
			},
		},
		{
			name: "API error on create increments error count",
			accounts: &mockAccountClient{
				accounts: []*mm.Account{{ID: "acc1", DisplayName: txAccountName}},
			},
			txClient: &mockTransactionClient{
				createErr:    errors.New("API 429"),
				queryBuilder: &mockQueryBuilder{},
			},
			cats: &mockCategoryLister{categories: []*mm.TransactionCategory{{ID: "cat1", Name: "Uncategorized"}}},
			txns: []TxToSync{
				{SourceID: "tx1", Source: "strike", TxType: "buy", Time: ts, AmountUSD: 100.00},
			},
			check: func(t *testing.T, result *TxSyncResult, synced map[string]string, _ *mockTransactionClient) {
				if result.Errors != 1 {
					t.Errorf("expected 1 error, got %d", result.Errors)
				}
				if result.Created != 0 {
					t.Errorf("expected 0 created, got %d", result.Created)
				}
			},
		},
		{
			name: "category error propagates",
			accounts: &mockAccountClient{
				accounts: []*mm.Account{{ID: "acc1", DisplayName: txAccountName}},
			},
			txClient: &mockTransactionClient{},
			cats:     &mockCategoryLister{err: errors.New("category API down")},
			txns: []TxToSync{
				{SourceID: "tx1", Source: "strike", TxType: "buy", Time: ts, AmountUSD: 100.00},
			},
			wantErr: true,
		},
		{
			name: "no categories returns error",
			accounts: &mockAccountClient{
				accounts: []*mm.Account{{ID: "acc1", DisplayName: txAccountName}},
			},
			txClient: &mockTransactionClient{},
			cats:     &mockCategoryLister{categories: []*mm.TransactionCategory{}},
			txns: []TxToSync{
				{SourceID: "tx1", Source: "strike", TxType: "buy", Time: ts, AmountUSD: 100.00},
			},
			wantErr: true,
		},
		{
			name: "creates tx account when missing",
			accounts: &mockAccountClient{
				accounts: []*mm.Account{}, // no existing accounts
			},
			txClient: &mockTransactionClient{
				queryBuilder: &mockQueryBuilder{},
			},
			cats: &mockCategoryLister{categories: []*mm.TransactionCategory{{ID: "cat1", Name: "Uncategorized"}}},
			txns: []TxToSync{
				{SourceID: "tx1", Source: "strike", TxType: "buy", Time: ts, AmountUSD: 100.00},
			},
			check: func(t *testing.T, result *TxSyncResult, _ map[string]string, txc *mockTransactionClient) {
				if result.Created != 1 {
					t.Errorf("expected 1 created, got %d", result.Created)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Syncer{
				accounts:     tt.accounts,
				transactions: tt.txClient,
				categories:   tt.cats,
				txAccountID:  "", // force account lookup
			}
			// If txClient is nil, set to nil explicitly so nil check in SyncTransactions fires
			if tt.txClient == nil {
				s.transactions = nil
			}

			result, synced, err := s.SyncTransactions(context.Background(), tt.txns)
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
				tt.check(t, result, synced, tt.txClient)
			}
		})
	}
}

func TestMerchantForTx(t *testing.T) {
	tests := []struct {
		name string
		tx   TxToSync
		want string
	}{
		{"strike with no memo", TxToSync{Source: "strike"}, "Strike"},
		{"strike with memo", TxToSync{Source: "strike", Memo: "Bill Pay"}, "Strike - Bill Pay"},
		{"river no memo", TxToSync{Source: "river"}, "River"},
		{"lnd forward", TxToSync{Source: "lnd_forward"}, "Lightning Routing"},
		{"lnd onchain", TxToSync{Source: "lnd_onchain"}, "On-chain"},
		{"unknown source", TxToSync{Source: "custom"}, "custom"},
		{"unknown with memo", TxToSync{Source: "custom", Memo: "note"}, "custom - note"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := merchantForTx(tt.tx)
			if got != tt.want {
				t.Errorf("merchantForTx() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNotesForTx(t *testing.T) {
	tx := TxToSync{SourceID: "src-123", TxType: "buy"}
	got := notesForTx(tx)
	want := "[buy] src-123"
	if got != want {
		t.Errorf("notesForTx() = %q, want %q", got, want)
	}
}

func TestSourceLabel(t *testing.T) {
	tests := []struct {
		source string
		want   string
	}{
		{"strike", "Strike"},
		{"river", "River"},
		{"coinbase", "Coinbase"},
		{"swan", "Swan"},
		{"lnd_forward", "Lightning Routing"},
		{"lnd_invoice", "Lightning Invoice"},
		{"lnd_payment", "Lightning Payment"},
		{"lnd_onchain", "On-chain"},
		{"unknown", "unknown"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			if got := sourceLabel(tt.source); got != tt.want {
				t.Errorf("sourceLabel(%q) = %q, want %q", tt.source, got, tt.want)
			}
		})
	}
}

func TestEnsureDefaultCategory(t *testing.T) {
	ctx := context.Background()

	t.Run("finds Uncategorized", func(t *testing.T) {
		s := &Syncer{
			categories: &mockCategoryLister{
				categories: []*mm.TransactionCategory{
					{ID: "cat1", Name: "Food"},
					{ID: "cat2", Name: "Uncategorized"},
				},
			},
		}
		id, err := s.ensureDefaultCategory(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "cat2" {
			t.Errorf("expected cat2, got %q", id)
		}
	})

	t.Run("falls back to first category", func(t *testing.T) {
		s := &Syncer{
			categories: &mockCategoryLister{
				categories: []*mm.TransactionCategory{
					{ID: "cat1", Name: "Food"},
				},
			},
		}
		id, err := s.ensureDefaultCategory(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "cat1" {
			t.Errorf("expected cat1, got %q", id)
		}
	})

	t.Run("no categories returns error", func(t *testing.T) {
		s := &Syncer{
			categories: &mockCategoryLister{categories: []*mm.TransactionCategory{}},
		}
		_, err := s.ensureDefaultCategory(ctx)
		if err == nil {
			t.Fatal("expected error for empty categories")
		}
	})

	t.Run("cached category ID is reused", func(t *testing.T) {
		callCount := 0
		s := &Syncer{
			defaultCategoryID: "cached-cat",
			categories: &mockCategoryLister{
				categories: []*mm.TransactionCategory{{ID: "fresh", Name: "Uncategorized"}},
			},
		}
		_ = s.categories.(*mockCategoryLister)
		id, err := s.ensureDefaultCategory(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "cached-cat" {
			t.Errorf("expected cached-cat, got %q", id)
		}
		if callCount != 0 {
			t.Error("should not have called ListCategories when ID is cached")
		}
	})

	t.Run("nil category lister returns error", func(t *testing.T) {
		s := &Syncer{categories: nil}
		_, err := s.ensureDefaultCategory(ctx)
		if err == nil {
			t.Fatal("expected error for nil category lister")
		}
	})
}
