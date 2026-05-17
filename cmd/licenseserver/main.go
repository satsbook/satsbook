package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/satsbook/satsbook/internal/license"
	"github.com/satsbook/satsbook/internal/licensedb"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		cmdServe(os.Args[2:])
	case "create":
		cmdCreate(os.Args[2:])
	case "revoke":
		cmdRevoke(os.Args[2:])
	case "upgrade":
		cmdUpgrade(os.Args[2:])
	case "list":
		cmdList(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: licenseserver <command> [flags]

Commands:
  serve     Start the license validation HTTP server
  create    Create a new license key
  revoke    Revoke a license key
  upgrade   Change a license's tier
  list      List all licenses
`)
}

func getDBPath() string {
	p := os.Getenv("SATSBOOK_LICENSE_DB_PATH")
	if p == "" {
		p = "licenses.db"
	}
	return p
}

func openStore() *licensedb.Store {
	s, err := licensedb.Open(getDBPath())
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	return s
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.String("port", "", "listen port (default: $PORT or 8080)")
	fs.Parse(args)

	p := *port
	if p == "" {
		p = os.Getenv("PORT")
	}
	if p == "" {
		p = "8080"
	}

	privKeyHex := os.Getenv("SATSBOOK_LICENSE_SIGNING_KEY")
	if privKeyHex == "" {
		log.Fatal("SATSBOOK_LICENSE_SIGNING_KEY env var is required")
	}
	privBytes, err := hex.DecodeString(privKeyHex)
	if err != nil || len(privBytes) != ed25519.PrivateKeySize {
		log.Fatalf("invalid SATSBOOK_LICENSE_SIGNING_KEY (expected %d hex-encoded bytes)", ed25519.PrivateKeySize)
	}
	priv := ed25519.PrivateKey(privBytes)

	store := openStore()
	defer store.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/license/validate", validateHandler(store, priv))

	addr := ":" + p
	log.Printf("license server listening on %s (db=%s)", addr, getDBPath())
	log.Fatal(http.ListenAndServe(addr, mux))
}

type validateRequest struct {
	Key string `json:"key"`
}

type validateResponse struct {
	Valid bool   `json:"valid"`
	Tier  string `json:"tier"`
	Token string `json:"token"`
}

func validateHandler(store *licensedb.Store, priv ed25519.PrivateKey) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req validateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(validateResponse{Valid: false})
			return
		}

		lic, err := store.Lookup(req.Key)
		if err != nil {
			log.Printf("lookup error: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		if !lic.IsValid() {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(validateResponse{Valid: false})
			return
		}

		token, err := license.SignToken(license.TokenPayload{
			Key:       req.Key,
			Tier:      lic.Tier,
			ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Unix(),
		}, priv)
		if err != nil {
			log.Printf("sign error: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(validateResponse{
			Valid: true,
			Tier:  lic.Tier,
			Token: token,
		})
	}
}

func cmdCreate(args []string) {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	email := fs.String("email", "", "customer email")
	tier := fs.String("tier", "pro", "license tier (free, pro, power)")
	days := fs.Int("days", 0, "days until expiry (0 = no expiry)")
	fs.Parse(args)

	var expiresAt *time.Time
	if *days > 0 {
		t := time.Now().Add(time.Duration(*days) * 24 * time.Hour)
		expiresAt = &t
	}

	store := openStore()
	defer store.Close()

	lic, err := store.Create(*email, *tier, expiresAt)
	if err != nil {
		log.Fatalf("create: %v", err)
	}

	fmt.Printf("Created license:\n")
	fmt.Printf("  Key:     %s\n", lic.LicenseKey)
	fmt.Printf("  Email:   %s\n", lic.Email)
	fmt.Printf("  Tier:    %s\n", lic.Tier)
	if lic.ExpiresAt != nil {
		fmt.Printf("  Expires: %s\n", lic.ExpiresAt.Format(time.RFC3339))
	} else {
		fmt.Printf("  Expires: never\n")
	}
}

func cmdRevoke(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: licenseserver revoke <license_key>\n")
		os.Exit(1)
	}

	store := openStore()
	defer store.Close()

	if err := store.Revoke(args[0]); err != nil {
		log.Fatalf("revoke: %v", err)
	}
	fmt.Printf("Revoked: %s\n", args[0])
}

func cmdUpgrade(args []string) {
	fs := flag.NewFlagSet("upgrade", flag.ExitOnError)
	tier := fs.String("tier", "", "new tier (pro, power)")
	fs.Parse(args)

	if fs.NArg() < 1 || *tier == "" {
		fmt.Fprintf(os.Stderr, "usage: licenseserver upgrade -tier <tier> <license_key>\n")
		os.Exit(1)
	}

	store := openStore()
	defer store.Close()

	if err := store.Upgrade(fs.Arg(0), *tier); err != nil {
		log.Fatalf("upgrade: %v", err)
	}
	fmt.Printf("Upgraded %s to %s\n", fs.Arg(0), *tier)
}

func cmdList(args []string) {
	store := openStore()
	defer store.Close()

	licenses, err := store.List()
	if err != nil {
		log.Fatalf("list: %v", err)
	}

	if len(licenses) == 0 {
		fmt.Println("No licenses found.")
		return
	}

	fmt.Printf("%-30s %-25s %-8s %-10s %s\n", "KEY", "EMAIL", "TIER", "STATUS", "EXPIRES")
	for _, l := range licenses {
		exp := "never"
		if l.ExpiresAt != nil {
			exp = l.ExpiresAt.Format("2006-01-02")
		}
		fmt.Printf("%-30s %-25s %-8s %-10s %s\n", l.LicenseKey, l.Email, l.Tier, l.Status, exp)
	}
}
