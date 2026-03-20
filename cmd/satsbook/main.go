package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/satsbook/satsbook/internal/config"
	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/lnd"
	"github.com/satsbook/satsbook/internal/syncer"
)

func main() {
	fmt.Println("satsbook - Bitcoin node analytics and accounting")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v\n", err)
		os.Exit(1)
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

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Printf("received signal %v, shutting down...", sig)
		cancel()
	}()

	// Run syncer (blocks until ctx is cancelled)
	s.Run(ctx)

	log.Println("shutdown complete")
}
