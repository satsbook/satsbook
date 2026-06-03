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
	"github.com/satsbook/satsbook/internal/license"
	"github.com/satsbook/satsbook/internal/lnd"
	"github.com/satsbook/satsbook/internal/monarch"
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

	// Initialize license checker — always use DefaultChecker so keys can be
	// activated at runtime from the settings page without restarting.
	licenseKey := cfg.LicenseKey
	if licenseKey == "" {
		// Fall back to license key stored in the settings DB.
		if dbKey, _ := database.GetSetting(context.Background(), "license_key"); dbKey != "" {
			licenseKey = dbKey
			log.Printf("License key loaded from database settings")
		}
	}
	licenseLogger := log.New(os.Stdout, "[license] ", log.LstdFlags)
	lc := license.NewChecker(
		&licenseStoreAdapter{db: database},
		licenseKey,
		license.WithValidationURL(cfg.LicenseValidationURL),
		license.WithLogger(licenseLogger),
	)
	if licenseKey != "" {
		if err := lc.Verify(context.Background()); err != nil {
			log.Printf("WARNING: license verification failed: %v — defaulting to free tier", err)
		} else {
			log.Printf("License tier: %s", lc.CurrentTier())
		}
	} else {
		log.Printf("No license key configured — running free tier")
	}
	var licenseChecker license.Checker = lc

	// Initialize price cache
	priceCache := price.NewCache(price.WithAPIURL(cfg.PriceAPIURL))

	// Initialize LND client (optional — app works without it for dashboard-only mode)
	var lndClient *lnd.Client
	var s *syncer.Syncer
	if cfg.LNDConfigured() {
		lndClient, err = lnd.NewClient(cfg.LNDHost, cfg.LNDPort, cfg.LNDMacaroonPath, cfg.LNDTLSCertPath)
		if err != nil {
			log.Printf("WARNING: failed to connect to LND: %v — running without node sync", err)
		} else {
			defer lndClient.Close()
			log.Printf("connected to LND at %s:%d", cfg.LNDHost, cfg.LNDPort)

			syncerLogger := log.New(os.Stdout, "[syncer] ", log.LstdFlags)
			s = syncer.New(lndClient, database, syncerLogger, cfg.SyncInterval, cfg.MaxHistoryDays)
			s.SetSnapshotStore(database, priceCache)
		}
	} else {
		log.Printf("LND not configured — running in dashboard-only mode")
	}

	// Initialize HTTP server
	httpLogger := log.New(os.Stdout, "[http] ", log.LstdFlags)
	var nodeProvider web.NodeInfoProvider
	if lndClient != nil {
		nodeProvider = lndClient
	}
	handler := web.NewHandler(database, nodeProvider, priceCache, database, httpLogger)

	// Wire up settings store, transaction store, and optional Monarch sync
	handler.SetSettingsStore(database)
	handler.SetTransactionStore(database)
	handler.SetMonarchTxStore(database)
	handler.SetTaxStore(database)
	handler.SetLicenseChecker(lc)
	monarchToken, _ := database.GetSetting(context.Background(), "monarch_token")
	if monarchToken == "" {
		monarchToken = cfg.MonarchToken // fall back to env var
	}
	// When Monarch is connected at runtime via the settings page, propagate to the background syncer
	if s != nil {
		handler.OnMonarchChange(func(ms web.MonarchSyncer) {
			s.SetMonarchSyncer(ms)
			s.SetMonarchTxSync(database, database)
		})
	}
	if monarchToken != "" {
		monarchSyncer, err := monarch.NewSyncer(monarchToken, cfg.MonarchAccountID)
		if err != nil {
			log.Printf("monarch: failed to create syncer: %v", err)
		} else {
			handler.SetMonarchSyncer(monarchSyncer)
			if s != nil {
				s.SetMonarchTxSync(database, database)
			}
			log.Printf("monarch: sync enabled")
		}
	}

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

	srv := web.NewServer(handler, cfg.AppPort, httpLogger, licenseChecker)

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

	// Run syncer (blocks until ctx is cancelled) or just wait for signal
	if s != nil {
		s.Run(ctx)
	} else {
		<-ctx.Done()
	}

	log.Println("shutdown complete")
}

// licenseStoreAdapter bridges db.DB to the license.LicenseStore interface,
// converting between db.CachedLicense and license.CachedLicense to avoid
// circular imports.
type licenseStoreAdapter struct {
	db interface {
		GetCachedLicense(ctx context.Context) (*db.CachedLicense, error)
		UpdateCachedLicense(ctx context.Context, cl *db.CachedLicense) error
	}
}

func (a *licenseStoreAdapter) GetCachedLicense(ctx context.Context) (*license.CachedLicense, error) {
	cl, err := a.db.GetCachedLicense(ctx)
	if err != nil {
		return nil, err
	}
	return &license.CachedLicense{
		LicenseKey:   cl.LicenseKey,
		Tier:         cl.Tier,
		SignedToken:  cl.SignedToken,
		LastVerified: cl.LastVerified,
		ExpiresAt:    cl.ExpiresAt,
	}, nil
}

func (a *licenseStoreAdapter) UpdateCachedLicense(ctx context.Context, cl *license.CachedLicense) error {
	return a.db.UpdateCachedLicense(ctx, &db.CachedLicense{
		LicenseKey:   cl.LicenseKey,
		Tier:         cl.Tier,
		SignedToken:  cl.SignedToken,
		LastVerified: cl.LastVerified,
		ExpiresAt:    cl.ExpiresAt,
	})
}
