package logging

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteHandler_BatchInsert(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-logs.db")
	h, err := NewSQLiteHandler(dbPath, &SQLiteHandlerOptions{
		BatchSize:     5,
		FlushInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	logger := slog.New(h)
	for i := 0; i < 10; i++ {
		logger.Info("test message", "i", i)
	}

	time.Sleep(100 * time.Millisecond)

	entries, total, err := h.Query(LogQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if total != 10 {
		t.Errorf("expected 10 entries, got %d", total)
	}
	if len(entries) != 10 {
		t.Errorf("expected 10 returned entries, got %d", len(entries))
	}
}

func TestSQLiteHandler_FlushOnClose(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-logs.db")
	h, err := NewSQLiteHandler(dbPath, &SQLiteHandlerOptions{
		BatchSize:     1000,
		FlushInterval: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.New(h)
	logger.Info("before close")
	h.Close()

	h2, err := NewSQLiteHandler(dbPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close()

	_, total, err := h2.Query(LogQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("expected 1 flushed entry, got %d", total)
	}
}

func TestSQLiteHandler_QueryFilters(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-logs.db")
	h, err := NewSQLiteHandler(dbPath, &SQLiteHandlerOptions{
		BatchSize:     100,
		FlushInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	infoLogger := slog.New(h).With("component", "indexer")
	debugLogger := slog.New(h).With("component", "overlay")

	infoLogger.Info("indexer started")
	infoLogger.Error("indexer failed", "error", "timeout")
	debugLogger.Info("overlay sync")

	time.Sleep(100 * time.Millisecond)

	entries, total, err := h.Query(LogQuery{Component: "indexer", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("expected 2 indexer entries, got %d", total)
	}

	entries, total, err = h.Query(LogQuery{Level: "ERROR", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("expected 1 error entry, got %d", total)
	}
	_ = entries
}

func TestSQLiteHandler_Pruning(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-logs.db")
	h, err := NewSQLiteHandler(dbPath, &SQLiteHandlerOptions{
		BatchSize:     100,
		FlushInterval: 50 * time.Millisecond,
		Retention:     1 * time.Millisecond,
		PruneInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	logger := slog.New(h)
	logger.Info("old message")

	time.Sleep(200 * time.Millisecond)

	_, total, err := h.Query(LogQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Errorf("expected 0 entries after pruning, got %d", total)
	}
}

// TestSQLiteHandler_HandleDoesNotReFilter verifies that calling Handle
// directly (e.g. via a LeveledHandler wrapper) writes the record even when
// r.Level is below opts.Level. The slog contract puts the level gate in
// Enabled; wrapping handlers (like LeveledHandler in NewComponentLogger)
// rely on Handle being unconditional once the caller has authorized it.
func TestSQLiteHandler_HandleDoesNotReFilter(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-logs.db")
	h, err := NewSQLiteHandler(dbPath, &SQLiteHandlerOptions{
		BatchSize:     1,
		FlushInterval: 10 * time.Millisecond,
		Level:         slog.LevelInfo, // restrictive opts.Level
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	// Sanity: Enabled correctly reports DEBUG as gated.
	if h.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatalf("Enabled should return false for DEBUG when opts.Level=Info")
	}

	// But Handle, when invoked directly (the path a wrapping LeveledHandler
	// takes), must accept the record. Otherwise per-component log_level
	// overrides built on top of LeveledHandler are silently neutralized.
	r := slog.NewRecord(time.Now(), slog.LevelDebug, "debug record via Handle", 0)
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	entries, total, err := h.Query(LogQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("expected 1 entry persisted via Handle bypass, got %d", total)
	}
	if entries[0].Level != "DEBUG" {
		t.Errorf("expected DEBUG record persisted, got %q", entries[0].Level)
	}
}

// TestSQLiteHandler_LeveledHandlerOverride is the integration form of the
// above: a LeveledHandler(SQLiteHandler, LevelDebug) wraps a SQLiteHandler
// whose opts.Level is Info. Logging at DEBUG through the wrapped logger
// should land DEBUG records in SQLite.
func TestSQLiteHandler_LeveledHandlerOverride(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-logs.db")
	sqliteH, err := NewSQLiteHandler(dbPath, &SQLiteHandlerOptions{
		BatchSize:     1,
		FlushInterval: 10 * time.Millisecond,
		Level:         slog.LevelInfo,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sqliteH.Close()

	wrapped := NewLeveledHandler(sqliteH, slog.LevelDebug)
	logger := slog.New(wrapped)
	logger.Debug("debug via leveled wrapper")

	time.Sleep(50 * time.Millisecond)
	_, total, err := sqliteH.Query(LogQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("expected 1 DEBUG record under LeveledHandler override, got %d", total)
	}
}

func TestSQLiteHandler_DropWhenFull(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-logs.db")
	h, err := NewSQLiteHandler(dbPath, &SQLiteHandlerOptions{
		BatchSize:     1000,
		FlushInterval: 10 * time.Second,
		BufferSize:    5,
	})
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.New(h)
	for i := 0; i < 100; i++ {
		logger.Info("flood", "i", i)
	}

	h.Close()

	h2, err := NewSQLiteHandler(dbPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close()

	_, total, err := h2.Query(LogQuery{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if total >= 100 {
		t.Errorf("expected some dropped records, but got all %d", total)
	}
	if total == 0 {
		t.Error("expected at least some records to be written")
	}
}

func autoVacuumMode(t *testing.T, h *SQLiteHandler) int {
	t.Helper()
	var mode int
	if err := h.db.QueryRow("PRAGMA auto_vacuum").Scan(&mode); err != nil {
		t.Fatalf("read auto_vacuum: %v", err)
	}
	return mode
}

// TestSQLiteHandler_AutoVacuumOnFreshDB verifies a newly created log database is
// opened with incremental auto-vacuum (mode 2) — the prerequisite for the
// pruner's incremental_vacuum to return freed pages to the OS.
func TestSQLiteHandler_AutoVacuumOnFreshDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-logs.db")
	h, err := NewSQLiteHandler(dbPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	if mode := autoVacuumMode(t, h); mode != 2 {
		t.Errorf("expected auto_vacuum=INCREMENTAL (2) on fresh db, got %d", mode)
	}
}

// TestSQLiteHandler_LegacyNONEDBLeftIntact verifies that opening a pre-existing
// database created with auto_vacuum=NONE neither errors nor loses data. The
// handler intentionally does not convert it in place — the operator deletes the
// file to start fresh with incremental auto-vacuum — so the mode stays NONE.
func TestSQLiteHandler_LegacyNONEDBLeftIntact(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-logs.db")

	legacy, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	legacy.SetMaxOpenConns(1)
	if _, err := legacy.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(logsSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(
		`INSERT INTO logs (time_ns, level, msg) VALUES (?, 'INFO', 'legacy row')`,
		time.Now().UnixNano(),
	); err != nil {
		t.Fatal(err)
	}
	var legacyMode int
	if err := legacy.QueryRow("PRAGMA auto_vacuum").Scan(&legacyMode); err != nil {
		t.Fatal(err)
	}
	if legacyMode != 0 {
		t.Fatalf("precondition: expected legacy db auto_vacuum=NONE (0), got %d", legacyMode)
	}
	legacy.Close()

	h, err := NewSQLiteHandler(dbPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	if mode := autoVacuumMode(t, h); mode != 0 {
		t.Errorf("expected legacy db left at auto_vacuum=NONE (0), got %d", mode)
	}

	_, total, err := h.Query(LogQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("expected legacy row preserved, got %d", total)
	}
}
