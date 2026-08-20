package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/satsbook/satsbook/internal/config"
	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/exchange"
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

	handler.SetVersion(version)

	// Wire up settings store, transaction store, and optional Monarch sync
	handler.SetSettingsStore(database)
	handler.SetTransactionStore(database)
	handler.SetMonarchTxStore(database)
	handler.SetTaxStore(database)
	handler.SetLicenseChecker(lc)
	// Derive checkout base URL from the validation URL (strip /v1/license/validate)
	checkoutBase := strings.TrimSuffix(cfg.LicenseValidationURL, "/v1/license/validate")
	handler.SetCheckoutBaseURL(checkoutBase)
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

	// Wire up Strike API auto-sync (goroutine started after ctx is created below)
	strikeAPIKey := cfg.StrikeAPIKey
	if strikeAPIKey == "" {
		if dbKey, _ := database.GetSetting(context.Background(), "strike_api_key"); dbKey != "" {
			strikeAPIKey = dbKey
		}
	}
	strikeLogger := log.New(os.Stdout, "[strike] ", log.LstdFlags)

	// Wire up Coinbase CDP API auto-sync
	coinbaseKeyID := cfg.CoinbaseAPIKeyID
	coinbaseSecret := cfg.CoinbaseAPISecret
	if coinbaseKeyID == "" {
		if dbKey, _ := database.GetSetting(context.Background(), "coinbase_api_key_id"); dbKey != "" {
			coinbaseKeyID = dbKey
		}
	}
	if coinbaseSecret == "" {
		if dbSec, _ := database.GetSetting(context.Background(), "coinbase_api_secret"); dbSec != "" {
			coinbaseSecret = dbSec
		}
	}
	coinbaseLogger := log.New(os.Stdout, "[coinbase] ", log.LstdFlags)

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

	// Start Strike API sync now that ctx is available
	if strikeAPIKey != "" {
		strikeClient := exchange.NewStrikeAPIClient(strikeAPIKey)
		if s != nil {
			s.SetStrikeClient(strikeClient, database)
			log.Printf("strike: API sync enabled (via LND syncer)")
		} else {
			go runStrikeSync(ctx, strikeClient, database, 15*time.Minute, strikeLogger)
			log.Printf("strike: API sync enabled (background goroutine)")
		}
	}
	// Hot-swap when the user saves/clears the key from settings
	handler.OnStrikeKeyChange(func(apiKey string) {
		if apiKey == "" {
			if s != nil {
				s.SetStrikeClient(nil, nil)
			}
			strikeLogger.Printf("API key removed — sync disabled")
			return
		}
		newClient := exchange.NewStrikeAPIClient(apiKey)
		if s != nil {
			s.SetStrikeClient(newClient, database)
			strikeLogger.Printf("API key updated — syncing via LND syncer")
		} else {
			go runStrikeSync(ctx, newClient, database, 15*time.Minute, strikeLogger)
			strikeLogger.Printf("API key updated — background goroutine started")
		}
	})

	// Start Coinbase CDP API sync
	if coinbaseKeyID != "" && coinbaseSecret != "" {
		if cbClient, err := exchange.NewCoinbaseAPIClient(coinbaseKeyID, coinbaseSecret); err != nil {
			coinbaseLogger.Printf("invalid credentials: %v", err)
		} else if s != nil {
			s.SetCoinbaseClient(cbClient, database)
			coinbaseLogger.Printf("API sync enabled (via LND syncer)")
		} else {
			go runCoinbaseSync(ctx, cbClient, database, 15*time.Minute, coinbaseLogger)
			coinbaseLogger.Printf("API sync enabled (background goroutine)")
		}
	}
	handler.OnCoinbaseKeyChange(func(keyID, secret string) {
		if keyID == "" || secret == "" {
			if s != nil {
				s.SetCoinbaseClient(nil, nil)
			}
			coinbaseLogger.Printf("credentials removed — sync disabled")
			return
		}
		newClient, err := exchange.NewCoinbaseAPIClient(keyID, secret)
		if err != nil {
			coinbaseLogger.Printf("invalid credentials: %v", err)
			return
		}
		if s != nil {
			s.SetCoinbaseClient(newClient, database)
			coinbaseLogger.Printf("credentials updated — syncing via LND syncer")
		} else {
			go runCoinbaseSync(ctx, newClient, database, 15*time.Minute, coinbaseLogger)
			coinbaseLogger.Printf("credentials updated — background goroutine started")
		}
	})

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

// runStrikeSync runs a Strike API sync immediately and then on the given interval.
// Used in dashboard-only mode (no LND syncer). The syncer handles this automatically
// when LND is configured.
func runStrikeSync(ctx context.Context, client *exchange.StrikeAPIClient, store interface {
	ImportStrikeCSV(ctx context.Context, rows []exchange.StrikeRow) (*db.ImportSummary, error)
	SetSetting(ctx context.Context, key, value string) error
}, interval time.Duration, logger *log.Logger) {
	doSync := func() {
		if sats, err := client.FetchBalance(ctx); err != nil {
			logger.Printf("fetch balance: %v", err)
		} else if err := store.SetSetting(ctx, "strike_live_balance_sats", strconv.FormatInt(sats, 10)); err != nil {
			logger.Printf("store balance: %v", err)
		}

		rows, err := client.FetchRows(ctx)
		if err != nil {
			logger.Printf("fetch rows: %v", err)
			return
		}
		if len(rows) == 0 {
			return
		}
		summary, err := store.ImportStrikeCSV(ctx, rows)
		if err != nil {
			logger.Printf("import: %v", err)
			return
		}
		if summary.NewPurchases > 0 || summary.Updated > 0 {
			logger.Printf("sync: %d new, %d updated, %d duplicates", summary.NewPurchases, summary.Updated, summary.Duplicates)
		}
	}

	doSync()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			doSync()
		}
	}
}

// runCoinbaseSync runs a Coinbase API sync immediately and then on the given interval.
// Used in dashboard-only mode (no LND syncer).
func runCoinbaseSync(ctx context.Context, client *exchange.CoinbaseAPIClient, store interface {
	ImportCoinbaseCSV(ctx context.Context, rows []exchange.CoinbaseRow) (*db.ImportSummary, error)
	SetSetting(ctx context.Context, key, value string) error
}, interval time.Duration, logger *log.Logger) {
	doSync := func() {
		if sats, err := client.FetchBalance(ctx); err != nil {
			logger.Printf("fetch balance: %v", err)
		} else if err := store.SetSetting(ctx, "coinbase_live_balance_sats", strconv.FormatInt(sats, 10)); err != nil {
			logger.Printf("store balance: %v", err)
		}

		rows, err := client.FetchRows(ctx)
		if err != nil {
			logger.Printf("fetch rows: %v", err)
			return
		}
		if len(rows) == 0 {
			return
		}
		summary, err := store.ImportCoinbaseCSV(ctx, rows)
		if err != nil {
			logger.Printf("import: %v", err)
			return
		}
		if summary.NewPurchases > 0 || summary.Updated > 0 {
			logger.Printf("sync: %d new, %d updated, %d duplicates", summary.NewPurchases, summary.Updated, summary.Duplicates)
		}
	}

	doSync()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			doSync()
		}
	}
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
