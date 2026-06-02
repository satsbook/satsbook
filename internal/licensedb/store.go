package licensedb

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const migrationV1 = `
CREATE TABLE IF NOT EXISTS licenses (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    license_key TEXT NOT NULL UNIQUE,
    email       TEXT,
    tier        TEXT NOT NULL DEFAULT 'free',
    status      TEXT NOT NULL DEFAULT 'active',
    expires_at  DATETIME,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

const migrationV2 = `
ALTER TABLE licenses ADD COLUMN stripe_customer_id TEXT;
ALTER TABLE licenses ADD COLUMN stripe_subscription_id TEXT;
CREATE INDEX IF NOT EXISTS idx_licenses_stripe_sub ON licenses(stripe_subscription_id);
`

type License struct {
	ID                   int64
	LicenseKey           string
	Email                string
	Tier                 string
	Status               string
	StripeCustomerID     string
	StripeSubscriptionID string
	ExpiresAt            *time.Time
	CreatedAt            time.Time
}

type Store struct {
	db *sql.DB
}

func Open(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if _, err := db.Exec(migrationV1); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate v1: %w", err)
	}
	if err := migrateV2(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate v2: %w", err)
	}
	return &Store{db: db}, nil
}

// migrateV2 adds Stripe columns if they don't exist yet.
func migrateV2(db *sql.DB) error {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('licenses') WHERE name = 'stripe_customer_id'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check schema: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(migrationV2); err != nil {
			return fmt.Errorf("apply v2: %w", err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

const licenseColumns = `id, license_key, email, tier, status, expires_at, created_at, COALESCE(stripe_customer_id,''), COALESCE(stripe_subscription_id,'')`

func scanLicense(row interface{ Scan(...any) error }) (*License, error) {
	var l License
	var expiresAt sql.NullTime
	err := row.Scan(&l.ID, &l.LicenseKey, &l.Email, &l.Tier, &l.Status, &expiresAt, &l.CreatedAt, &l.StripeCustomerID, &l.StripeSubscriptionID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if expiresAt.Valid {
		l.ExpiresAt = &expiresAt.Time
	}
	return &l, nil
}

// Lookup finds a license by key. Returns nil if not found.
func (s *Store) Lookup(key string) (*License, error) {
	l, err := scanLicense(s.db.QueryRow(`SELECT `+licenseColumns+` FROM licenses WHERE license_key = ?`, key))
	if err != nil {
		return nil, fmt.Errorf("lookup: %w", err)
	}
	return l, nil
}

// LookupByStripeSubscription finds a license by Stripe subscription ID.
func (s *Store) LookupByStripeSubscription(subID string) (*License, error) {
	l, err := scanLicense(s.db.QueryRow(`SELECT `+licenseColumns+` FROM licenses WHERE stripe_subscription_id = ?`, subID))
	if err != nil {
		return nil, fmt.Errorf("lookup by subscription: %w", err)
	}
	return l, nil
}

// IsValid returns true if the license exists, is active, and not expired.
func (l *License) IsValid() bool {
	if l == nil {
		return false
	}
	if l.Status != "active" {
		return false
	}
	if l.ExpiresAt != nil && l.ExpiresAt.Before(time.Now()) {
		return false
	}
	return true
}

// CreateParams holds optional parameters for license creation.
type CreateParams struct {
	ExpiresAt            *time.Time
	StripeCustomerID     string
	StripeSubscriptionID string
}

// Create inserts a new license with a generated key.
func (s *Store) Create(email, tier string, params *CreateParams) (*License, error) {
	key, err := generateKey()
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	var expVal, custVal, subVal any
	if params != nil {
		if params.ExpiresAt != nil {
			expVal = *params.ExpiresAt
		}
		if params.StripeCustomerID != "" {
			custVal = params.StripeCustomerID
		}
		if params.StripeSubscriptionID != "" {
			subVal = params.StripeSubscriptionID
		}
	}

	res, err := s.db.Exec(
		`INSERT INTO licenses (license_key, email, tier, expires_at, stripe_customer_id, stripe_subscription_id) VALUES (?, ?, ?, ?, ?, ?)`,
		key, email, tier, expVal, custVal, subVal,
	)
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}

	lic := &License{
		ID:         0,
		LicenseKey: key,
		Email:      email,
		Tier:       tier,
		Status:     "active",
		CreatedAt:  time.Now(),
	}
	lic.ID, _ = res.LastInsertId()
	if params != nil {
		lic.ExpiresAt = params.ExpiresAt
		lic.StripeCustomerID = params.StripeCustomerID
		lic.StripeSubscriptionID = params.StripeSubscriptionID
	}
	return lic, nil
}

// Revoke sets a license's status to "cancelled".
func (s *Store) Revoke(key string) error {
	res, err := s.db.Exec(`UPDATE licenses SET status = 'cancelled' WHERE license_key = ?`, key)
	if err != nil {
		return fmt.Errorf("revoke: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("license not found: %s", key)
	}
	return nil
}

// Upgrade changes a license's tier.
func (s *Store) Upgrade(key, tier string) error {
	res, err := s.db.Exec(`UPDATE licenses SET tier = ? WHERE license_key = ?`, tier, key)
	if err != nil {
		return fmt.Errorf("upgrade: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("license not found: %s", key)
	}
	return nil
}

// List returns all licenses.
func (s *Store) List() ([]License, error) {
	rows, err := s.db.Query(`SELECT ` + licenseColumns + ` FROM licenses ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	defer rows.Close()

	var licenses []License
	for rows.Next() {
		l, err := scanLicense(rows)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		licenses = append(licenses, *l)
	}
	return licenses, rows.Err()
}

func generateKey() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "sk_" + hex.EncodeToString(b), nil
}
