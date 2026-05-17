package licensedb

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const migration = `
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

type License struct {
	ID         int64
	LicenseKey string
	Email      string
	Tier       string
	Status     string
	ExpiresAt  *time.Time
	CreatedAt  time.Time
}

type Store struct {
	db *sql.DB
}

func Open(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if _, err := db.Exec(migration); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// Lookup finds a license by key. Returns nil if not found.
func (s *Store) Lookup(key string) (*License, error) {
	row := s.db.QueryRow(
		`SELECT id, license_key, email, tier, status, expires_at, created_at FROM licenses WHERE license_key = ?`,
		key,
	)
	var l License
	var expiresAt sql.NullTime
	err := row.Scan(&l.ID, &l.LicenseKey, &l.Email, &l.Tier, &l.Status, &expiresAt, &l.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lookup: %w", err)
	}
	if expiresAt.Valid {
		l.ExpiresAt = &expiresAt.Time
	}
	return &l, nil
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

// Create inserts a new license with a generated key.
func (s *Store) Create(email, tier string, expiresAt *time.Time) (*License, error) {
	key, err := generateKey()
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	var expVal any
	if expiresAt != nil {
		expVal = *expiresAt
	}

	res, err := s.db.Exec(
		`INSERT INTO licenses (license_key, email, tier, expires_at) VALUES (?, ?, ?, ?)`,
		key, email, tier, expVal,
	)
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}

	id, _ := res.LastInsertId()
	return &License{
		ID:         id,
		LicenseKey: key,
		Email:      email,
		Tier:       tier,
		Status:     "active",
		ExpiresAt:  expiresAt,
		CreatedAt:  time.Now(),
	}, nil
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
	rows, err := s.db.Query(
		`SELECT id, license_key, email, tier, status, expires_at, created_at FROM licenses ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	defer rows.Close()

	var licenses []License
	for rows.Next() {
		var l License
		var expiresAt sql.NullTime
		if err := rows.Scan(&l.ID, &l.LicenseKey, &l.Email, &l.Tier, &l.Status, &expiresAt, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		if expiresAt.Valid {
			l.ExpiresAt = &expiresAt.Time
		}
		licenses = append(licenses, l)
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
