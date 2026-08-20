package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// --- mockBackupDB ---

type mockBackupDB struct {
	path     string
	backupFn func(ctx context.Context, destPath string) error
}

func (m *mockBackupDB) Path() string { return m.path }
func (m *mockBackupDB) Backup(ctx context.Context, destPath string) error {
	if m.backupFn != nil {
		return m.backupFn(ctx, destPath)
	}
	if m.path == "" {
		// Produce a minimal valid-looking file (empty) for streaming tests
		f, err := os.Create(destPath)
		if err != nil {
			return err
		}
		return f.Close()
	}
	return copyFile(m.path, destPath)
}

// newBackupHandler creates a test Handler with only the backup/restore dependencies set.
func newBackupHandler(bdb BackupDB) *Handler {
	h := NewHandler(&mockStore{}, nil, &mockPrice{}, &mockImportStore{}, log.New(os.Stderr, "[test] ", 0))
	h.SetBackupDB(bdb)
	return h
}

// createValidSQLiteFile creates a SQLite file with all required tables at the given path.
func createValidSQLiteFile(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("createValidSQLiteFile: open: %v", err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS exchange_imports (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS invoices (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS payments (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS channels (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS forwarding_events (id INTEGER PRIMARY KEY)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("createValidSQLiteFile: exec %q: %v", s, err)
		}
	}
}

// createInvalidFile writes non-SQLite bytes to a file.
func createInvalidFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("this is not a sqlite database"), 0600); err != nil {
		t.Fatal(err)
	}
}

// createSQLiteFileWithoutTables creates a minimal valid SQLite file with no application tables.
func createSQLiteFileWithoutTables(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("createSQLiteFileWithoutTables: open: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Fatalf("createSQLiteFileWithoutTables: ping: %v", err)
	}
}

// makeMultipartFile builds a multipart/form-data body from a file on disk.
func makeMultipartFile(t *testing.T, field, filename, filePath string) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file %s: %v", filePath, err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}
	mw.Close()
	return &buf, mw.FormDataContentType()
}

// --- HandleBackup tests ---

func TestHandleBackup_Success(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "satsbook.db")
	createValidSQLiteFile(t, dbPath)

	mock := &mockBackupDB{path: dbPath}
	h := newBackupHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/api/backup", nil)
	w := httptest.NewRecorder()
	h.HandleBackup(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") {
		t.Errorf("expected attachment Content-Disposition, got: %q", cd)
	}
	if !strings.Contains(cd, "satsbook-backup-") {
		t.Errorf("expected filename with satsbook-backup- prefix, got: %q", cd)
	}
	if !strings.Contains(cd, ".db") {
		t.Errorf("expected .db extension in Content-Disposition, got: %q", cd)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/octet-stream" {
		t.Errorf("expected application/octet-stream, got %q", ct)
	}
	if w.Body.Len() == 0 {
		t.Error("expected non-empty body (SQLite file bytes)")
	}
}

func TestHandleBackup_WrongMethod(t *testing.T) {
	h := newBackupHandler(&mockBackupDB{path: "/tmp/test.db"})
	req := httptest.NewRequest(http.MethodPost, "/api/backup", nil)
	w := httptest.NewRecorder()
	h.HandleBackup(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleBackup_NoDB(t *testing.T) {
	h := newBackupHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/backup", nil)
	w := httptest.NewRecorder()
	h.HandleBackup(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestHandleBackup_BackupError(t *testing.T) {
	mock := &mockBackupDB{
		path: "/tmp/fake.db",
		backupFn: func(_ context.Context, _ string) error {
			return fmt.Errorf("disk full")
		},
	}
	h := newBackupHandler(mock)
	req := httptest.NewRequest(http.MethodGet, "/api/backup", nil)
	w := httptest.NewRecorder()
	h.HandleBackup(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// --- HandleRestore tests ---

func TestHandleRestore_ValidFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "satsbook.db")
	createValidSQLiteFile(t, dbPath)

	mock := &mockBackupDB{path: dbPath}
	h := newBackupHandler(mock)

	uploadPath := filepath.Join(dir, "upload.db")
	createValidSQLiteFile(t, uploadPath)

	body, contentType := makeMultipartFile(t, "database", "upload.db", uploadPath)
	req := httptest.NewRequest(http.MethodPost, "/api/restore", body)
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	h.HandleRestore(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}
	msg, _ := resp["message"].(string)
	if !strings.Contains(msg, "restart") {
		t.Errorf("expected restart message, got %q", msg)
	}
	// Backup file should exist
	if _, err := os.Stat(dbPath + ".bak"); err != nil {
		t.Errorf("expected .bak file to exist: %v", err)
	}
}

func TestHandleRestore_InvalidFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "satsbook.db")
	createValidSQLiteFile(t, dbPath)

	mock := &mockBackupDB{path: dbPath}
	h := newBackupHandler(mock)

	invalidPath := filepath.Join(dir, "invalid.db")
	createInvalidFile(t, invalidPath)

	body, contentType := makeMultipartFile(t, "database", "invalid.db", invalidPath)
	req := httptest.NewRequest(http.MethodPost, "/api/restore", body)
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	h.HandleRestore(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleRestore_MissingTables(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "satsbook.db")
	createValidSQLiteFile(t, dbPath)

	mock := &mockBackupDB{path: dbPath}
	h := newBackupHandler(mock)

	emptyDB := filepath.Join(dir, "empty.db")
	createSQLiteFileWithoutTables(t, emptyDB)

	body, contentType := makeMultipartFile(t, "database", "empty.db", emptyDB)
	req := httptest.NewRequest(http.MethodPost, "/api/restore", body)
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	h.HandleRestore(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "missing required tables") {
		t.Errorf("expected 'missing required tables' in response, got: %s", w.Body.String())
	}
}

func TestHandleRestore_WrongMethod(t *testing.T) {
	h := newBackupHandler(&mockBackupDB{path: "/tmp/test.db"})
	req := httptest.NewRequest(http.MethodGet, "/api/restore", nil)
	w := httptest.NewRecorder()
	h.HandleRestore(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleRestore_MissingField(t *testing.T) {
	h := newBackupHandler(&mockBackupDB{path: "/tmp/test.db"})

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/restore", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.HandleRestore(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleRestore_NoDB(t *testing.T) {
	h := newBackupHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/restore", nil)
	w := httptest.NewRecorder()
	h.HandleRestore(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

// --- validateSQLiteDB unit tests ---

func TestValidateSQLiteDB_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "valid.db")
	createValidSQLiteFile(t, path)

	if err := validateSQLiteDB(path, requiredTables); err != nil {
		t.Errorf("expected no error for valid file, got: %v", err)
	}
}

func TestValidateSQLiteDB_InvalidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invalid.db")
	createInvalidFile(t, path)

	if err := validateSQLiteDB(path, requiredTables); err == nil {
		t.Error("expected error for invalid file, got nil")
	}
}

func TestValidateSQLiteDB_MissingTables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.db")
	createSQLiteFileWithoutTables(t, path)

	err := validateSQLiteDB(path, requiredTables)
	if err == nil {
		t.Error("expected error for missing tables, got nil")
	}
	if !strings.Contains(err.Error(), "missing required tables") {
		t.Errorf("expected 'missing required tables', got: %v", err)
	}
}
