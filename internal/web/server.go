package web

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/satsbook/satsbook/internal/license"
)

// Server is the HTTP server for the dashboard API.
type Server struct {
	httpServer *http.Server
	logger     *log.Logger
}

// NewServer creates a new HTTP server with routing and middleware.
func NewServer(handler *Handler, port int, logger *log.Logger, checker license.Checker) *Server {
	mux := http.NewServeMux()

	// HTML routes
	mux.HandleFunc("/", handler.HandleDashboard)
	mux.HandleFunc("/lightning", handler.HandleLightningPage)
	mux.HandleFunc("/import", handler.HandleImportPage)
	mux.HandleFunc("/pl", handler.HandlePLPage)
	mux.HandleFunc("/wallets", handler.HandleWalletsPage)
	mux.HandleFunc("/exchange/", handler.HandleExchangeDetail)
	mux.HandleFunc("/wallets/", handler.HandleWalletDetail)
	mux.HandleFunc("/partials/forwarding", handler.HandleForwardingPartial)
	mux.HandleFunc("/partials/portfolio-chart", handler.HandlePortfolioChartPartial)
	mux.HandleFunc("/settings", handler.HandleSettingsPage)
	mux.HandleFunc("/transactions", handler.HandleTransactionsPage)

	// Static assets (embedded)
	staticSub, _ := fs.Sub(staticFS, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// JSON API routes
	mux.HandleFunc("/api/summary", handler.HandleSummary)
	mux.HandleFunc("/api/channels", handler.HandleChannels)
	mux.HandleFunc("/api/forwarding", handler.HandleForwarding)
	mux.HandleFunc("/api/node", handler.HandleNode)
	mux.HandleFunc("/api/import/strike", handler.HandleStrikeImport)
	mux.HandleFunc("/api/import/river", handler.HandleRiverImport)
	mux.HandleFunc("/api/import/coinbase", handler.HandleCoinbaseImport)
	mux.HandleFunc("/api/import/swan", handler.HandleSwanImport)
	mux.HandleFunc("/api/wallets", handler.HandleAddWallet)
	mux.HandleFunc("/api/wallets/delete", handler.HandleRemoveWallet)
	mux.HandleFunc("/api/wallets/refresh", handler.HandleRefreshWallet)
	mux.HandleFunc("/api/wallets/refresh-all", handler.HandleRefreshAll)
	mux.HandleFunc("/api/portfolio/backfill", handler.HandlePortfolioBackfill)
	mux.HandleFunc("/api/transactions/note", handler.HandleTransactionNoteSave)
	mux.HandleFunc("/api/transactions/note/edit", handler.HandleTransactionNoteEdit)
	mux.Handle("/api/import/strike/clear", handler.HandleClearImport("strike"))
	mux.Handle("/api/import/river/clear", handler.HandleClearImport("river"))
	mux.Handle("/api/import/coinbase/clear", handler.HandleClearImport("coinbase"))
	mux.Handle("/api/import/swan/clear", handler.HandleClearImport("swan"))
	monarchGate := requireTier(license.TierPower, handler.renderer)
	mux.HandleFunc("/api/monarch/save", monarchGate(handler.HandleMonarchSave))
	mux.HandleFunc("/api/monarch/token", monarchGate(handler.HandleMonarchToken))
	mux.HandleFunc("/api/monarch/disconnect", monarchGate(handler.HandleMonarchDisconnect))
	mux.HandleFunc("/api/monarch/sync", monarchGate(handler.HandleMonarchSync))
	mux.HandleFunc("/api/monarch/sync-types", monarchGate(handler.HandleMonarchSyncTypes))
	mux.HandleFunc("/api/monarch/tx-sync", monarchGate(handler.HandleMonarchTxSync))

	return &Server{
		httpServer: &http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			Handler:      tierMiddleware(checker, requestLogger(mux, logger)),
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 5 * time.Minute,
			IdleTimeout:  60 * time.Second,
		},
		logger: logger,
	}
}

// Start begins listening for HTTP requests. It blocks until the server is shut down.
func (s *Server) Start() error {
	s.logger.Printf("HTTP server listening on %s", s.httpServer.Addr)
	err := s.httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// requestLogger is middleware that logs each HTTP request.
func requestLogger(next http.Handler, logger *log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)
		logger.Printf("%s %s %d %s", r.Method, r.URL.Path, rw.statusCode, time.Since(start).Round(time.Millisecond))
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
