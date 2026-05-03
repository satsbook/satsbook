package db

import (
	"context"
	"database/sql"
	"time"
)

// CachedLicense represents the locally cached license validation result.
type CachedLicense struct {
	LicenseKey   string
	Tier         string
	SignedToken  string
	LastVerified time.Time
	ExpiresAt    time.Time
}

// GetCachedLicense reads the singleton license cache row.
func (d *DB) GetCachedLicense(ctx context.Context) (*CachedLicense, error) {
	var cl CachedLicense
	var lastVerified, expiresAt sql.NullTime
	var signedToken sql.NullString

	err := d.db.QueryRowContext(ctx,
		`SELECT license_key, tier, signed_token, last_verified, expires_at FROM license_cache WHERE id = 1`,
	).Scan(&cl.LicenseKey, &cl.Tier, &signedToken, &lastVerified, &expiresAt)

	if err == sql.ErrNoRows {
		return &CachedLicense{Tier: "free"}, nil
	}
	if err != nil {
		return nil, err
	}

	if signedToken.Valid {
		cl.SignedToken = signedToken.String
	}
	if lastVerified.Valid {
		cl.LastVerified = lastVerified.Time
	}
	if expiresAt.Valid {
		cl.ExpiresAt = expiresAt.Time
	}
	return &cl, nil
}

// UpdateCachedLicense writes the license validation result to the cache.
func (d *DB) UpdateCachedLicense(ctx context.Context, cl *CachedLicense) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE license_cache SET license_key = ?, tier = ?, signed_token = ?, last_verified = ?, expires_at = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1`,
		cl.LicenseKey, cl.Tier, cl.SignedToken, cl.LastVerified, cl.ExpiresAt,
	)
	return err
}
