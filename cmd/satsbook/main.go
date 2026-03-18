package main

import (
	"fmt"
	"log"
	"os"

	"github.com/satsbook/satsbook/internal/config"
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

	// TODO: Implement main application logic
}
