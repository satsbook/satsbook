package monarch

import (
	"context"
	"fmt"
	"log"

	mm "github.com/eshaffer321/monarchmoney-go/pkg/monarch"
)

const (
	accountName   = "Bitcoin (Satsbook)"
	btcSecurityID = "90020945152078462"
)

// AccountClient is the subset of the Monarch Money client used by Syncer.
type AccountClient interface {
	List(ctx context.Context) ([]*mm.Account, error)
	Delete(ctx context.Context, accountID string) error
	CreateInvestmentsAccount(ctx context.Context, params *mm.CreateInvestmentsAccountParams) (*mm.Account, error)
	GetHoldings(ctx context.Context, accountID string) ([]*mm.Holding, error)
	DeleteHolding(ctx context.Context, holdingID string) error
	CreateHolding(ctx context.Context, params *mm.CreateHoldingParams) (*mm.Holding, error)
}

// Syncer pushes BTC balance to a Monarch Money manual account.
type Syncer struct {
	accounts  AccountClient
	accountID string
}

// NewSyncer creates a Monarch syncer using a raw auth token.
func NewSyncer(token, accountID string) (*Syncer, error) {
	client, err := mm.NewClientWithToken(token)
	if err != nil {
		return nil, fmt.Errorf("create monarch client: %w", err)
	}
	return &Syncer{accounts: client.Accounts, accountID: accountID}, nil
}

// NewSyncerWithLogin creates a Monarch syncer by logging in with email/password.
// Returns ErrOTPRequired if Monarch requires an email OTP code.
// When OTP is required, the PendingClient is returned so the same session can be reused.
func NewSyncerWithLogin(ctx context.Context, email, password, accountID string) (*Syncer, string, *PendingClient, error) {
	client, err := mm.NewClient(nil)
	if err != nil {
		return nil, "", nil, fmt.Errorf("create monarch client: %w", err)
	}
	if err := client.Auth.Login(ctx, email, password); err != nil {
		if err.Error() == "Email OTP required" || err.Error() == "MFA required" {
			return nil, "", &PendingClient{Client: client, AccountID: accountID}, fmt.Errorf("%w: %s", ErrOTPRequired, err.Error())
		}
		return nil, "", nil, fmt.Errorf("monarch login: %w", err)
	}
	sess, err := client.Auth.GetSession()
	if err != nil {
		return nil, "", nil, fmt.Errorf("get session: %w", err)
	}
	return &Syncer{accounts: client.Accounts, accountID: accountID}, sess.Token, nil, nil
}

// ErrOTPRequired indicates that a second factor is needed.
var ErrOTPRequired = fmt.Errorf("OTP required")

// PendingClient holds a partially-authenticated Monarch client awaiting OTP.
type PendingClient struct {
	Client    *mm.Client
	AccountID string
}

// CompleteOTP finishes login using an email OTP code on the existing client session.
func (p *PendingClient) CompleteOTP(ctx context.Context, email, password, otpCode string) (*Syncer, string, error) {
	if err := p.Client.Auth.LoginWithEmailOTP(ctx, email, password, otpCode); err != nil {
		return nil, "", fmt.Errorf("monarch OTP login: %w", err)
	}
	sess, err := p.Client.Auth.GetSession()
	if err != nil {
		return nil, "", fmt.Errorf("get session: %w", err)
	}
	return &Syncer{accounts: p.Client.Accounts, accountID: p.AccountID}, sess.Token, nil
}

// NewSyncerWithClient creates a Syncer with a provided AccountClient (for testing).
func NewSyncerWithClient(accounts AccountClient, accountID string) *Syncer {
	return &Syncer{accounts: accounts, accountID: accountID}
}

// findAccount looks for an existing satsbook account by name.
func (s *Syncer) findAccount(ctx context.Context) (string, error) {
	accounts, err := s.accounts.List(ctx)
	if err != nil {
		return "", fmt.Errorf("list accounts: %w", err)
	}
	for _, a := range accounts {
		if a.DisplayName == accountName {
			return a.ID, nil
		}
	}
	return "", nil
}

// ensureAccount finds or creates the satsbook investment account.
func (s *Syncer) ensureAccount(ctx context.Context, btcQuantity float64) (string, error) {
	if s.accountID != "" {
		return s.accountID, nil
	}

	id, err := s.findAccount(ctx)
	if err != nil {
		return "", err
	}
	if id != "" {
		s.accountID = id
		log.Printf("monarch: found existing account %s (%s)", accountName, id)
		return id, nil
	}

	// Create new investments account with BTC holding
	acct, err := s.accounts.CreateInvestmentsAccount(ctx, &mm.CreateInvestmentsAccountParams{
		Name:                            accountName,
		Subtype:                         "cryptocurrency",
		ManualInvestmentsTrackingMethod: "holdings",
		InitialHoldings: []mm.InitialHolding{
			{SecurityID: btcSecurityID, Quantity: btcQuantity},
		},
	})
	if err != nil {
		return "", fmt.Errorf("create investments account: %w", err)
	}

	s.accountID = acct.ID
	log.Printf("monarch: created investments account %s (%s)", accountName, acct.ID)
	return acct.ID, nil
}

// SyncHolding updates the BTC holding quantity in Monarch.
// On first call it creates the account with the holding. On subsequent calls
// it deletes the existing BTC holding and recreates it with the new quantity.
// If the holding update fails, it falls back to deleting and recreating the account.
func (s *Syncer) SyncHolding(ctx context.Context, btcQuantity float64) error {
	accountID, err := s.ensureAccount(ctx, btcQuantity)
	if err != nil {
		return err
	}

	// If we just created the account, the holding is already set
	// Check by seeing if GetHoldings returns our BTC holding with the right quantity
	holdings, err := s.accounts.GetHoldings(ctx, accountID)
	if err != nil {
		log.Printf("monarch: get holdings failed, recreating account: %v", err)
		return s.recreateAccount(ctx, btcQuantity)
	}

	for _, h := range holdings {
		if h.Symbol == "BTC" || h.Symbol == "BTC-USD" {
			if h.Quantity == btcQuantity {
				log.Printf("monarch: BTC holding already at %.8f BTC, no update needed", btcQuantity)
				return nil
			}
			// Delete old holding, create new one
			if err := s.accounts.DeleteHolding(ctx, h.ID); err != nil {
				log.Printf("monarch: delete holding failed, recreating account: %v", err)
				return s.recreateAccount(ctx, btcQuantity)
			}
			_, err := s.accounts.CreateHolding(ctx, &mm.CreateHoldingParams{
				AccountID:  accountID,
				SecurityID: btcSecurityID,
				Quantity:   btcQuantity,
			})
			if err != nil {
				log.Printf("monarch: create holding failed, recreating account: %v", err)
				return s.recreateAccount(ctx, btcQuantity)
			}
			log.Printf("monarch: updated BTC holding to %.8f BTC", btcQuantity)
			return nil
		}
	}

	// No BTC holding found (shouldn't happen but handle it)
	log.Printf("monarch: no BTC holding found, recreating account")
	return s.recreateAccount(ctx, btcQuantity)
}

// recreateAccount deletes the existing account and creates a fresh one.
func (s *Syncer) recreateAccount(ctx context.Context, btcQuantity float64) error {
	if s.accountID != "" {
		if err := s.accounts.Delete(ctx, s.accountID); err != nil {
			log.Printf("monarch: warning: failed to delete account %s: %v", s.accountID, err)
		}
		s.accountID = ""
	}

	acct, err := s.accounts.CreateInvestmentsAccount(ctx, &mm.CreateInvestmentsAccountParams{
		Name:                            accountName,
		Subtype:                         "cryptocurrency",
		ManualInvestmentsTrackingMethod: "holdings",
		InitialHoldings: []mm.InitialHolding{
			{SecurityID: btcSecurityID, Quantity: btcQuantity},
		},
	})
	if err != nil {
		return fmt.Errorf("recreate investments account: %w", err)
	}

	s.accountID = acct.ID
	log.Printf("monarch: recreated account with %.8f BTC (%s)", btcQuantity, acct.ID)
	return nil
}
