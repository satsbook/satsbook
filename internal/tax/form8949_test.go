package tax

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"
)

func TestWriteForm8949CSV(t *testing.T) {
	events := []TaxableEvent{
		{
			DisposedAt:   time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC),
			AcquiredAt:   time.Date(2023, 1, 10, 0, 0, 0, 0, time.UTC),
			AmountSat:    50_000_000, // 0.5 BTC
			CostBasisUSD: 10000.00,
			ProceedsUSD:  15000.00,
			GainLossUSD:  5000.00,
			IsLongTerm:   true,
		},
		{
			DisposedAt:   time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			AcquiredAt:   time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			AmountSat:    10_000_000, // 0.1 BTC
			CostBasisUSD: 4500.00,
			ProceedsUSD:  4200.00,
			GainLossUSD:  -300.00,
			IsLongTerm:   false,
		},
	}

	var buf bytes.Buffer
	err := WriteForm8949CSV(&buf, events)
	if err != nil {
		t.Fatal(err)
	}

	r := csv.NewReader(strings.NewReader(buf.String()))
	records, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}

	// Header + 2 data rows
	if len(records) != 3 {
		t.Fatalf("expected 3 rows (header + 2), got %d", len(records))
	}

	// Check header
	if records[0][0] != "Description of Property" {
		t.Errorf("header col 0: %q", records[0][0])
	}

	// Check row 1
	if records[1][0] != "0.50000000 BTC" {
		t.Errorf("row 1 description: %q", records[1][0])
	}
	if records[1][1] != "01/10/2023" {
		t.Errorf("row 1 acquired: %q", records[1][1])
	}
	if records[1][2] != "06/15/2024" {
		t.Errorf("row 1 sold: %q", records[1][2])
	}
	if records[1][3] != "15000.00" {
		t.Errorf("row 1 proceeds: %q", records[1][3])
	}
	if records[1][4] != "10000.00" {
		t.Errorf("row 1 cost: %q", records[1][4])
	}
	if records[1][5] != "5000.00" {
		t.Errorf("row 1 gain: %q", records[1][5])
	}
	if records[1][6] != "Long-term" {
		t.Errorf("row 1 period: %q", records[1][6])
	}

	// Check row 2
	if records[2][5] != "-300.00" {
		t.Errorf("row 2 loss: %q", records[2][5])
	}
	if records[2][6] != "Short-term" {
		t.Errorf("row 2 period: %q", records[2][6])
	}
}

func TestWriteForm8949CSV_Empty(t *testing.T) {
	var buf bytes.Buffer
	err := WriteForm8949CSV(&buf, nil)
	if err != nil {
		t.Fatal(err)
	}

	r := csv.NewReader(strings.NewReader(buf.String()))
	records, _ := r.ReadAll()
	if len(records) != 1 { // header only
		t.Errorf("expected 1 row (header only), got %d", len(records))
	}
}

func TestEventToForm8949Row(t *testing.T) {
	e := TaxableEvent{
		DisposedAt:   time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		AcquiredAt:   time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC),
		AmountSat:    100_000, // 0.001 BTC
		CostBasisUSD: 30.00,
		ProceedsUSD:  45.00,
		GainLossUSD:  15.00,
		IsLongTerm:   true,
	}

	row := EventToForm8949Row(e)
	if row.Description != "0.00100000 BTC" {
		t.Errorf("description: %q", row.Description)
	}
	if row.HoldingPeriod != "Long-term" {
		t.Errorf("period: %q", row.HoldingPeriod)
	}
}
