package alerts

import (
	"context"
	"time"

	"github.com/satsbook/satsbook/internal/db"
)

// DBStore adapts *db.DB to satisfy the Store interface.
type DBStore struct {
	db *db.DB
}

// NewDBStore wraps a *db.DB as an alerts.Store.
func NewDBStore(d *db.DB) *DBStore {
	return &DBStore{db: d}
}

func (s *DBStore) ChannelsWithClosingTx(ctx context.Context) ([]Channel, error) {
	rows, err := s.db.ChannelsWithClosingTx(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Channel, len(rows))
	for i, r := range rows {
		out[i] = Channel{
			ChanID:        r.ChanID,
			RemotePubKey:  r.RemotePubKey,
			Capacity:      r.Capacity,
			LocalBalance:  r.LocalBalance,
			ClosingTxHash: r.ClosingTxHash,
		}
	}
	return out, nil
}

func (s *DBStore) ChannelsBelowBalancePct(ctx context.Context, pct float64) ([]Channel, error) {
	rows, err := s.db.ChannelsBelowBalancePct(ctx, pct)
	if err != nil {
		return nil, err
	}
	out := make([]Channel, len(rows))
	for i, r := range rows {
		out[i] = Channel{
			ChanID:       r.ChanID,
			RemotePubKey: r.RemotePubKey,
			Capacity:     r.Capacity,
			LocalBalance: r.LocalBalance,
		}
	}
	return out, nil
}

func (s *DBStore) FeesMsatSince(ctx context.Context, since time.Time) (int64, error) {
	return s.db.FeesMsatSince(ctx, since)
}

func (s *DBStore) HasAlertedRecently(ctx context.Context, alertType, externalID string, since time.Time) (bool, error) {
	return s.db.HasAlertedRecently(ctx, alertType, externalID, since)
}

func (s *DBStore) RecordAlert(ctx context.Context, alertType, externalID, message string) error {
	return s.db.RecordAlert(ctx, alertType, externalID, message)
}
