package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// BackupDB is the interface the backup/restore handlers need from the DB layer.
type BackupDB interface {
	Backup(ctx context.Context, destPath string) error
	Path() string
}

// HandleBackup serves GET /api/backup.
// It checkpoints the WAL, copies the database file, and streams it to the client.
func (h *Handler) HandleBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.backupDB == nil {
		h.writeError(w, http.StatusServiceUnavailable, "backup not available")
		return
	}

	// Write the backup to a temp file then stream it.
	tmp, err := os.CreateTemp("", "satsbook-backup-*.db")
	if err != nil {
		h.logger.Printf("backup: create temp file: %v", err)
		h.writeError(w, http.StatusInternalServerError, "failed to create backup")
		return
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err := h.backupDB.Backup(r.Context(), tmpPath); err != nil {
		h.logger.Printf("backup: %v", err)
		h.writeError(w, http.StatusInternalServerError, "failed to create backup: "+err.Error())
		return
	}

	f, err := os.Open(tmpPath)
	if err != nil {
		h.logger.Printf("backup: open temp: %v", err)
		h.writeError(w, http.StatusInternalServerError, "failed to read backup")
		return
	}
	defer f.Close()

	filename := fmt.Sprintf("satsbook-backup-%s.db", time.Now().UTC().Format("2006-01-02"))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Type", "application/octet-stream")

	if _, err := io.Copy(w, f); err != nil {
		h.logger.Printf("backup: stream: %v", err)
	}
}

// requiredTables lists the tables that must exist in an uploaded restore file.
var requiredTables = []string{
	"exchange_imports",
	"invoices",
	"payments",
	"channels",
	"forwarding_events",
}

// HandleRestore serves POST /api/restore.
// It accepts a multipart upload (field "database"), validates the SQLite file,
// then stages it for replacement. The running database is NOT hot-swapped;
// the user must restart satsbook after restore.
func (h *Handler) HandleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.backupDB == nil {
		h.writeError(w, http.StatusServiceUnavailable, "restore not available")
		return
	}

	// 500 MB max
	const maxSize = 500 << 20
	if err := r.ParseMultipartForm(maxSize); err != nil {
		h.logger.Printf("restore: parse multipart: %v", err)
		h.writeError(w, http.StatusBadRequest, "failed to parse upload: file may be too large (500MB max)")
		return
	}

	file, _, err := r.FormFile("database")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "missing field: database")
		return
	}
	defer file.Close()

	// Write uploaded file to a temp location.
	tmp, err := os.CreateTemp("", "satsbook-restore-*.db")
	if err != nil {
		h.logger.Printf("restore: create temp: %v", err)
		h.writeError(w, http.StatusInternalServerError, "failed to process upload")
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		h.logger.Printf("restore: write temp: %v", err)
		h.writeError(w, http.StatusInternalServerError, "failed to write upload")
		return
	}
	tmp.Close()

	// Validate: open with SQLite and check required tables.
	if err := validateSQLiteDB(tmpPath, requiredTables); err != nil {
		h.logger.Printf("restore: validation failed: %v", err)
		h.writeError(w, http.StatusBadRequest, "invalid database file: "+err.Error())
		return
	}

	// Stage: rename existing DB to .bak, move new DB into place.
	dbPath := h.backupDB.Path()
	if dbPath == "" || dbPath == ":memory:" {
		h.writeError(w, http.StatusServiceUnavailable, "restore not supported for in-memory databases")
		return
	}

	bakPath := dbPath + ".bak"
	newPath := dbPath + ".new"

	// Write validated file to <dbPath>.new
	if err := copyFile(tmpPath, newPath); err != nil {
		h.logger.Printf("restore: copy to new: %v", err)
		h.writeError(w, http.StatusInternalServerError, "failed to stage database")
		return
	}

	// Move current DB to .bak
	if err := os.Rename(dbPath, bakPath); err != nil {
		os.Remove(newPath)
		h.logger.Printf("restore: rename to bak: %v", err)
		h.writeError(w, http.StatusInternalServerError, "failed to back up current database")
		return
	}

	// Move new DB into place
	if err := os.Rename(newPath, dbPath); err != nil {
		// Try to restore original
		_ = os.Rename(bakPath, dbPath)
		h.logger.Printf("restore: rename new to db: %v", err)
		h.writeError(w, http.StatusInternalServerError, "failed to replace database")
		return
	}

	h.logger.Printf("restore: database replaced (previous backed up to %s); restart required", filepath.Base(bakPath))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"message": "Database replaced. Please restart satsbook.",
	})
}

// validateSQLiteDB opens the SQLite file at path and checks that all required
// tables exist. Returns an error if the file is not a valid SQLite database or
// any required table is missing.
func validateSQLiteDB(path string, tables []string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("not a valid SQLite file: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return fmt.Errorf("could not open as SQLite: %w", err)
	}

	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		return fmt.Errorf("could not query schema: %w", err)
	}
	defer rows.Close()

	found := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		found[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows: %w", err)
	}

	var missing []string
	for _, t := range tables {
		if !found[t] {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required tables: %s", strings.Join(missing, ", "))
	}
	return nil
}

// copyFile copies src to dst, creating or truncating dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
