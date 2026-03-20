package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/satsbook/satsbook/internal/config"
	"github.com/satsbook/satsbook/internal/lnd"
)

func main() {
	fmt.Println("LND Connection Checker")
	fmt.Println("======================")
	fmt.Println()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v\n", err)
	}

	fmt.Printf("Connecting to LND at %s:%d...\n", cfg.LNDHost, cfg.LNDPort)

	// Create LND client
	client, err := lnd.NewClient(cfg.LNDHost, cfg.LNDPort, cfg.LNDMacaroonPath, cfg.LNDTLSCertPath)
	if err != nil {
		log.Fatalf("Failed to create LND client: %v\n", err)
	}
	defer client.Close()

	fmt.Println("Connection established!")
	fmt.Println()

	// Get node info
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	info, err := client.GetInfo(ctx)
	if err != nil {
		log.Fatalf("Failed to get node info: %v\n", err)
	}

	// Print node information
	fmt.Println("Node Information:")
	fmt.Printf("  Alias:           %s\n", info.Alias)
	fmt.Printf("  Public Key:      %s\n", info.PubKey)
	fmt.Printf("  Version:         %s\n", info.Version)
	fmt.Printf("  Synced:          %v\n", info.Synced)
	fmt.Printf("  Block Height:    %d\n", info.BlockHeight)
	fmt.Printf("  Total Channels:  %d\n", info.NumChannels)
	fmt.Printf("  Active Channels: %d\n", info.NumActiveChannels)
	fmt.Printf("  Peers:           %d\n", info.NumPeers)
	fmt.Println()

	// Get wallet balance
	balance, err := client.WalletBalance(ctx)
	if err != nil {
		log.Printf("Warning: Failed to get wallet balance: %v\n", err)
	} else {
		fmt.Println("On-Chain Wallet Balance:")
		fmt.Printf("  Total:       %d sats\n", balance.TotalBalance)
		fmt.Printf("  Confirmed:   %d sats\n", balance.ConfirmedBalance)
		fmt.Printf("  Unconfirmed: %d sats\n", balance.UnconfirmedBalance)
		fmt.Println()
	}

	// Get channels
	channels, err := client.ListChannels(ctx)
	if err != nil {
		log.Printf("Warning: Failed to list channels: %v\n", err)
	} else {
		fmt.Printf("Channels (%d total):\n", len(channels))
		if len(channels) > 0 {
			for i, ch := range channels {
				if i >= 5 {
					fmt.Printf("  ... and %d more channels\n", len(channels)-5)
					break
				}
				status := "inactive"
				if ch.Active {
					status = "active"
				}
				fmt.Printf("  Channel %d: %d sats capacity, %d local, %s\n",
					ch.ChannelID, ch.Capacity, ch.LocalBalance, status)
			}
		} else {
			fmt.Println("  No channels found")
		}
		fmt.Println()
	}

	fmt.Println("Connection test successful!")
	os.Exit(0)
}
