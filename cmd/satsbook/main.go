package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/satsbook/satsbook/internal/config"
	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/lnd"
	"github.com/satsbook/satsbook/internal/price"
	"github.com/satsbook/satsbook/internal/syncer"
	"github.com/satsbook/satsbook/internal/wallet"
	"github.com/satsbook/satsbook/internal/web"
)

// version is set at build time via -ldflags "-X main.version=<tag>".
// Defaults to "dev" for local builds.
var version = "dev"

func main() {
	fmt.Printf("satsbook %s - Bitcoin node analytics and accounting\n", version)

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Log configuration (without sensitive data)
	log.Printf("Configuration loaded successfully")
	log.Printf("LND Host: %s:%d", cfg.LNDHost, cfg.LNDPort)
	log.Printf("Database: %s", cfg.DatabasePath)
	log.Printf("App Port: %d", cfg.AppPort)
	log.Printf("Log Level: %s", cfg.LogLevel)
	log.Printf("Sync Interval: %v", cfg.SyncInterval)
	log.Printf("Max History Days: %d", cfg.MaxHistoryDays)

	// Initialize database
	database, err := db.NewDB(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}
	defer database.Close()
	log.Printf("database initialized at %s", cfg.DatabasePath)

	// Initialize LND client
	lndClient, err := lnd.NewClient(cfg.LNDHost, cfg.LNDPort, cfg.LNDMacaroonPath, cfg.LNDTLSCertPath)
	if err != nil {
		log.Fatalf("failed to connect to LND: %v", err)
	}
	defer lndClient.Close()
	log.Printf("connected to LND at %s:%d", cfg.LNDHost, cfg.LNDPort)

	// Initialize syncer
	syncerLogger := log.New(os.Stdout, "[syncer] ", log.LstdFlags)
	s := syncer.New(lndClient, database, syncerLogger, cfg.SyncInterval, cfg.MaxHistoryDays)

	// Initialize price cache
	priceCache := price.NewCache(price.WithAPIURL(cfg.PriceAPIURL))

	// Enable portfolio snapshots after each sync
	s.SetSnapshotStore(database, priceCache)

	// Initialize HTTP server
	httpLogger := log.New(os.Stdout, "[http] ", log.LstdFlags)
	handler := web.NewHandler(database, lndClient, priceCache, database, httpLogger)

	// Wire up wallet tracking (wallet store is always available; scanner requires Electrum or Bitcoin RPC)
	handler.SetWalletStore(database)
	walletLogger := log.New(os.Stdout, "[wallet] ", log.LstdFlags)
	var walletScannerSet bool

	// Try Electrum first (preferred — fast per-address lookups)
	if cfg.ElectrumHost != "" {
		electrumClient, err := wallet.NewElectrumClient(context.Background(), cfg.ElectrumHost, cfg.ElectrumPort)
		if err != nil {
			walletLogger.Printf("Electrum not available (%s:%d): %v",
				cfg.ElectrumHost, cfg.ElectrumPort, err)
		} else {
			defer electrumClient.Close()
			walletLogger.Printf("connected to Electrum at %s:%d", cfg.ElectrumHost, cfg.ElectrumPort)
			scanner := wallet.NewScanner(electrumClient, wallet.WithLogger(walletLogger))
			handler.SetWalletScanner(wallet.NewScannerAdapter(scanner))
			walletScannerSet = true
		}
	}

	// Fall back to Bitcoin Core RPC (works on pruned nodes, but slower)
	if !walletScannerSet && cfg.BitcoinRPCConfigured() {
		opts := []wallet.BitcoinRPCOption{wallet.WithRPCLogger(walletLogger)}
		if cfg.BitcoinRPCCookiePath != "" {
			opts = append(opts, wallet.WithCookieAuth(cfg.BitcoinRPCCookiePath))
		} else {
			opts = append(opts, wallet.WithUserPassAuth(cfg.BitcoinRPCUser, cfg.BitcoinRPCPassword))
		}
		rpcScanner := wallet.NewBitcoinRPCScanner(cfg.BitcoinRPCHost, cfg.BitcoinRPCPort, opts...)
		handler.SetWalletScanner(rpcScanner)
		walletScannerSet = true
		walletLogger.Printf("using Bitcoin Core RPC at %s:%d for wallet scanning (scantxoutset)",
			cfg.BitcoinRPCHost, cfg.BitcoinRPCPort)
	}

	if !walletScannerSet {
		walletLogger.Printf("wallet scanning disabled (no Electrum or Bitcoin RPC configured)")
	}

	srv := web.NewServer(handler, cfg.AppPort, httpLogger)

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Printf("received signal %v, shutting down...", sig)
		cancel()

		// Give HTTP server 5 seconds to drain
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server shutdown error: %v", err)
		}
	}()

	// Start HTTP server in background
	go func() {
		if err := srv.Start(); err != nil {
			log.Printf("HTTP server error: %v", err)
			cancel()
		}
	}()

	// Run syncer (blocks until ctx is cancelled)
	s.Run(ctx)

	log.Println("shutdown complete")
}
