package web

import (
	"context"
	"log"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/license"
)

func TestNewServer_RoutesRegistered(t *testing.T) {
	store := &mockStore{
		feeSummaryFn:    func(_ context.Context, _ time.Time) (int64, int64, error) { return 0, 0, nil },
		activeChannelFn: func(_ context.Context) (int, error) { return 0, nil },
		latestWalletFn:  func(_ context.Context) (*db.WalletBalanceSnapshot, error) { return nil, nil },
	}
	price := &mockPrice{err: nil}
	logger := log.New(os.Stderr, "[test] ", 0)
	h := NewHandler(store, nil, price, &mockImportStore{}, logger)
	srv := NewServer(h, 0, logger, license.FreeChecker{})

	if srv.httpServer == nil {
		t.Fatal("expected httpServer to be set")
	}
	if srv.httpServer.ReadTimeout != 10*time.Second {
		t.Errorf("expected ReadTimeout 10s, got %v", srv.httpServer.ReadTimeout)
	}
}

func TestServer_StartAndShutdown(t *testing.T) {
	store := &mockStore{
		feeSummaryFn:    func(_ context.Context, _ time.Time) (int64, int64, error) { return 0, 0, nil },
		activeChannelFn: func(_ context.Context) (int, error) { return 0, nil },
		latestWalletFn:  func(_ context.Context) (*db.WalletBalanceSnapshot, error) { return nil, nil },
	}
	logger := log.New(os.Stderr, "[test] ", 0)
	h := NewHandler(store, nil, &mockPrice{}, &mockImportStore{}, logger)
	// Use port 0 to let the OS pick a free port
	srv := NewServer(h, 0, logger, license.FreeChecker{})

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	// Give server a moment to start
	time.Sleep(50 * time.Millisecond)

	// Shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}

	// Start should return nil (ErrServerClosed is swallowed)
	if err := <-errCh; err != nil {
		t.Errorf("expected nil from Start after shutdown, got: %v", err)
	}
}

func TestResponseWriter_CapturesStatusCode(t *testing.T) {
	recorder := &responseWriter{
		ResponseWriter: &noopResponseWriter{},
		statusCode:     http.StatusOK,
	}
	recorder.WriteHeader(http.StatusNotFound)
	if recorder.statusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", recorder.statusCode)
	}
}

// noopResponseWriter is a minimal ResponseWriter for unit testing.
type noopResponseWriter struct{}

func (n *noopResponseWriter) Header() http.Header        { return http.Header{} }
func (n *noopResponseWriter) Write(b []byte) (int, error) { return len(b), nil }
func (n *noopResponseWriter) WriteHeader(int)             {}
