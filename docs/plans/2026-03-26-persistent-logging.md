# Persistent SQLite Logging Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add persistent SQLite-backed logging with batched writes, admin API for querying (filter by level, component, time range), admin UI log viewer, and consistent module tagging across all services.

**Architecture:** A custom `slog.Handler` fans out log records to both the existing JSON/stdout handler and a new SQLite handler. The SQLite handler buffers records in a channel, drains them in batches via a background goroutine, and flushes on shutdown (deferred before all service teardown so module shutdown logs are captured). The admin API exposes query endpoints; the admin UI adds a Logs section to the settings sidebar.

**Tech Stack:** Go `log/slog`, `database/sql` + `modernc.org/sqlite`, React 19, Tailwind CSS, Radix UI

---

## File Structure

### New Files
| File | Responsibility |
|------|---------------|
| `pkg/logging/multi_handler.go` | `slog.Handler` that fans out to N child handlers |
| `pkg/logging/sqlite_handler.go` | `slog.Handler` that buffers records and batch-inserts into SQLite |
| `pkg/logging/sqlite_handler_test.go` | Tests for SQLite handler (batch insert, flush, pruning, query) |
| `pkg/logging/multi_handler_test.go` | Tests for multi-handler fan-out |
| `admin/log_routes.go` | Admin API endpoints for log queries |
| `admin/ui/src/sections/Logs.tsx` | Log viewer UI component |

### Modified Files
| File | Change |
|------|--------|
| `pkg/logging/logging.go` | Add `WithSQLite` option, `Closer` interface, update `NewLogger` |
| `cmd/server/main.go` | Wire SQLite handler, defer flush before service close |
| `cmd/server/config.go` | Pass component loggers to all modules in `Initialize()` |
| `admin/config.go` | Accept log query store in deps |
| `admin/routes.go` | Register log query routes, add log store to `Routes` struct |
| `admin/ui/src/api.ts` | Add log query API functions |
| `admin/ui/src/pages/SettingsPage.tsx` | Add "Logs" section to sidebar nav and render area |

---

## Task 1: Multi-Handler

**Files:**
- Create: `pkg/logging/multi_handler.go`
- Create: `pkg/logging/multi_handler_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/logging/multi_handler_test.go
package logging

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
)

func TestMultiHandler_FansOut(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	h1 := slog.NewJSONHandler(&buf1, nil)
	h2 := slog.NewJSONHandler(&buf2, nil)

	multi := NewMultiHandler(h1, h2)
	logger := slog.New(multi)
	logger.Info("test message", "key", "value")

	if buf1.Len() == 0 {
		t.Error("handler 1 received no output")
	}
	if buf2.Len() == 0 {
		t.Error("handler 2 received no output")
	}
}

func TestMultiHandler_Enabled(t *testing.T) {
	h1 := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelWarn})
	h2 := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})

	multi := NewMultiHandler(h1, h2)
	// Should be enabled if ANY child is enabled
	if !multi.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("multi should be enabled at debug when one child accepts debug")
	}
}

func TestMultiHandler_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, nil)
	multi := NewMultiHandler(h)

	withAttrs := multi.WithAttrs([]slog.Attr{slog.String("component", "test")})
	logger := slog.New(withAttrs)
	logger.Info("tagged")

	if !bytes.Contains(buf.Bytes(), []byte(`"component"`)) {
		t.Error("attrs not propagated")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/davidcase/Source/1sat/1sat-stack && go test ./pkg/logging/ -run TestMultiHandler -v`
Expected: FAIL — `NewMultiHandler` not defined

- [ ] **Step 3: Implement MultiHandler**

```go
// pkg/logging/multi_handler.go
package logging

import (
	"context"
	"log/slog"
)

// MultiHandler fans out log records to multiple slog.Handlers.
type MultiHandler struct {
	handlers []slog.Handler
}

// NewMultiHandler creates a handler that writes to all provided handlers.
func NewMultiHandler(handlers ...slog.Handler) *MultiHandler {
	return &MultiHandler{handlers: handlers}
}

func (m *MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *MultiHandler) Handle(ctx context.Context, r slog.Record) error {
	var firstErr error
	for _, h := range m.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (m *MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithAttrs(attrs)
	}
	return &MultiHandler{handlers: handlers}
}

func (m *MultiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithGroup(name)
	}
	return &MultiHandler{handlers: handlers}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/davidcase/Source/1sat/1sat-stack && go test ./pkg/logging/ -run TestMultiHandler -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/logging/multi_handler.go pkg/logging/multi_handler_test.go
git commit -m "feat(logging): add MultiHandler for fan-out to multiple slog handlers"
```

---

## Task 2: SQLite Handler

**Files:**
- Create: `pkg/logging/sqlite_handler.go`
- Create: `pkg/logging/sqlite_handler_test.go`

- [ ] **Step 1: Write failing tests**

```go
// pkg/logging/sqlite_handler_test.go
package logging

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteHandler_BatchInsert(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-logs.db")
	h, err := NewSQLiteHandler(dbPath, &SQLiteHandlerOptions{
		BatchSize:    5,
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

	// Wait for flush
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
		BatchSize:    1000, // large batch so timer flush won't fire
		FlushInterval: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.New(h)
	logger.Info("before close")
	h.Close()

	// Reopen and query to verify the record was flushed
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
		BatchSize:    100,
		FlushInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	// Log with different levels and components
	infoLogger := slog.New(h).With("component", "indexer")
	debugLogger := slog.New(h).With("component", "overlay")

	infoLogger.Info("indexer started")
	infoLogger.Error("indexer failed", "error", "timeout")
	debugLogger.Info("overlay sync")

	time.Sleep(100 * time.Millisecond)

	// Filter by component
	entries, total, err := h.Query(LogQuery{Component: "indexer", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("expected 2 indexer entries, got %d", total)
	}

	// Filter by level
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
		BatchSize:    100,
		FlushInterval: 50 * time.Millisecond,
		Retention:    1 * time.Millisecond, // expire immediately
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
```

Also add a test for channel-full (non-blocking drop) behavior:

```go
func TestSQLiteHandler_DropWhenFull(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-logs.db")
	h, err := NewSQLiteHandler(dbPath, &SQLiteHandlerOptions{
		BatchSize:     1000,       // never auto-flush by batch
		FlushInterval: 10 * time.Second, // never auto-flush by timer
		BufferSize:    5,          // tiny channel
	})
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.New(h)
	// Write more records than the buffer can hold — should not block
	for i := 0; i < 100; i++ {
		logger.Info("flood", "i", i)
	}

	h.Close()

	// Some records should have been dropped
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/davidcase/Source/1sat/1sat-stack && go test ./pkg/logging/ -run TestSQLiteHandler -v`
Expected: FAIL — `NewSQLiteHandler` not defined

- [ ] **Step 3: Implement SQLiteHandler**

Key design points:
- `NewSQLiteHandler(dbPath string, opts *SQLiteHandlerOptions)` opens/creates the DB, prepares the INSERT statement, starts the writer goroutine and pruner goroutine
- `Handle()` sends a `logRecord` struct into a buffered channel (non-blocking via `select`/`default`; drops if full to avoid backpressure on the logging path). Channel capacity is configurable via `BufferSize` (default 10000)
- Writer goroutine: drains channel, accumulates batch, flushes when batch hits `BatchSize` or `FlushInterval` timer fires
- `Close()` closes the channel, waits for the writer to drain remaining records, flushes final batch, closes DB
- `Query(LogQuery)` executes a parameterized SELECT with optional WHERE clauses for component, level, time range, message text search. **Important:** The `Search` field uses `WHERE msg LIKE ?` with `"%" + search + "%"` as the bound parameter — never string concatenation
- `WithAttrs` / `WithGroup` propagate pre-applied attributes (component gets extracted specially)
- Schema: `CREATE TABLE IF NOT EXISTS logs (id INTEGER PRIMARY KEY, time_ns INTEGER NOT NULL, level TEXT NOT NULL, component TEXT, msg TEXT NOT NULL, attrs TEXT)`
- Indexes on `time_ns`, `component`, `level`
- Uses `modernc.org/sqlite` (pure Go, no CGO) — already a transitive dependency via the config store

The `LogQuery` struct:
```go
type LogQuery struct {
	Component string
	Level     string
	Since     time.Time
	Until     time.Time
	Search    string // LIKE %search% on msg
	Limit     int
	Offset    int
}

type LogEntry struct {
	TimeNs    int64             `json:"time_ns"`
	Time      string            `json:"time"`
	Level     string            `json:"level"`
	Component string            `json:"component"`
	Msg       string            `json:"msg"`
	Attrs     map[string]any    `json:"attrs,omitempty"`
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/davidcase/Source/1sat/1sat-stack && go test ./pkg/logging/ -run TestSQLiteHandler -v`
Expected: PASS

- [ ] **Step 5: Tidy modules and commit**

`modernc.org/sqlite` is a transitive dependency (via config store) — importing it directly in the logging package will promote it from `// indirect`. Run `go mod tidy` to update.

```bash
cd /Users/davidcase/Source/1sat/1sat-stack && go mod tidy
git add pkg/logging/sqlite_handler.go pkg/logging/sqlite_handler_test.go go.mod go.sum
git commit -m "feat(logging): add SQLite handler with batched writes, pruning, and query"
```

---

## Task 3: Wire Logger in Main

**Files:**
- Modify: `pkg/logging/logging.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Fix NewComponentLogger level-override to preserve handler chain**

Currently, `NewComponentLogger` with a non-empty `levelOverride` creates a brand new `slog.NewJSONHandler(os.Stdout, ...)`, which bypasses the MultiHandler entirely — those module logs would only go to stdout, not SQLite. Fix it to wrap the parent's handler with `LeveledHandler` instead:

In `pkg/logging/logging.go`, replace the `NewComponentLogger` function:

```go
func NewComponentLogger(parent *slog.Logger, component string, levelOverride string) *slog.Logger {
	if levelOverride == "" {
		return parent.With(ComponentKey, component)
	}
	// Wrap the parent's handler chain (preserving MultiHandler) with a level filter
	return slog.New(NewLeveledHandler(parent.Handler(), ParseLevel(levelOverride))).With(ComponentKey, component)
}
```

The existing `LeveledHandler` (already in logging.go) handles this — it wraps any handler with a level gate while preserving the underlying handler chain (stdout + SQLite).

- [ ] **Step 2: Add Closer interface and NewLoggerWithSQLite**

Add to `pkg/logging/logging.go`:

```go
// LogCloser flushes and closes persistent log handlers.
type LogCloser interface {
	Close() error
}

// LoggerResult holds a logger, its closer, and the query-able log store.
type LoggerResult struct {
	Logger   *slog.Logger
	Closer   LogCloser
	LogStore *SQLiteHandler // exposed for admin API to query
}

// NewLoggerWithSQLite creates a logger that writes to both stdout and SQLite.
// The returned LogCloser must be called on shutdown to flush remaining logs.
func NewLoggerWithSQLite(level string, dbPath string, opts *SQLiteHandlerOptions) (*LoggerResult, error) {
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: ParseLevel(level),
	})

	sqliteHandler, err := NewSQLiteHandler(dbPath, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create sqlite log handler: %w", err)
	}

	multi := NewMultiHandler(jsonHandler, sqliteHandler)
	return &LoggerResult{
		Logger:   slog.New(multi),
		Closer:   sqliteHandler,
		LogStore: sqliteHandler,
	}, nil
}
```

- [ ] **Step 3: Update main.go — wire SQLite handler and defer flush**

In `cmd/server/main.go`, replace `log := cfg.CreateLogger(*logLevel)` (line 118) and `slog.SetDefault(log)` (line 119). Note: `CreateLogger` handles log-level override from the `--log-level` CLI flag — preserve that logic:

```go
// Resolve effective log level (CLI override > config)
logLevel := cfg.Logging.Level
if *logLevelFlag != "" {
	logLevel = *logLevelFlag
}
cfg.Logging.Level = logLevel
cfg.Logging.SetDefaults()

// Create logger with persistent SQLite storage
logsDBPath := filepath.Join(resolvedDataDir, "logs.db")
logResult, err := logging.NewLoggerWithSQLite(
	logLevel,
	logsDBPath,
	&logging.SQLiteHandlerOptions{
		BatchSize:     100,
		FlushInterval: 1 * time.Second,
		Retention:     7 * 24 * time.Hour,
		PruneInterval: 1 * time.Hour,
		BufferSize:    10000, // channel capacity; drops if full
	},
)
if err != nil {
	slog.Error("failed to create logger", "error", err)
	os.Exit(1)
}
// Flush logs LAST — deferred FIRST (LIFO) so it runs AFTER svc.Close()
defer logResult.Closer.Close()

log := logResult.Logger
slog.SetDefault(log)
```

- [ ] **Step 4: Thread LogStore to admin via Config struct**

In `cmd/server/config.go`, add a field to the `Config` struct (not `Services` — it needs to be set before `Initialize`):
```go
// LogStore is set by main before Initialize, passed to admin for log queries
LogStore *logging.SQLiteHandler `mapstructure:"-"`
```

In `main.go`, after creating the logger:
```go
cfg.LogStore = logResult.LogStore
```

In `config.go`'s `Initialize()`, where admin deps are built (~line 1276):
```go
adminDeps.LogStore = c.LogStore
```

- [ ] **Step 5: Verify the server starts and logs to both stdout and SQLite**

Run: `cd /Users/davidcase/Source/1sat/1sat-stack && go run ./cmd/server --data-dir /tmp/1sat-test --log-level debug 2>&1 | head -5`

Check that `/tmp/1sat-test/logs.db` exists and has the `logs` table.

- [ ] **Step 6: Commit**

```bash
git add pkg/logging/logging.go cmd/server/main.go cmd/server/config.go
git commit -m "feat(logging): wire SQLite persistent logging with flush-on-shutdown"
```

---

## Task 4: Module Tagging

**Files:**
- Modify: `cmd/server/config.go` (the `Initialize` method, lines 811+)

Every module's `Initialize()` call currently receives the raw root `logger`. Wrap each with `logging.NewComponentLogger()` before passing it in.

- [ ] **Step 1: Add component loggers for all modules in Initialize()**

The component IDs use flat names matching the package name. In `config.go`'s `Initialize()` method, before each module's `Initialize()` call, create a tagged logger:

| Module | Component ID | Line (approx) |
|--------|-------------|----------------|
| store | `store` | 821 |
| pubsub | `pubsub` | 848 |
| beef | `beef` | 900 |
| txo | `txo` | 944 |
| overlay | `overlay` | 974 |
| bsv21 | `bsv21` | 992 |
| bap | `bap` | 1017 |
| bsocial | `bsocial` | 1040 |
| opns | `opns` | 1062 |
| ordlock | `ordlock` | 1088 |
| spends | `spends` | 1113 |
| ordfs | `ordfs` | 1135 |
| indexer | `indexer` | 1160 (already done — verify) |
| owner | `owner` | 1239 (already done — verify) |
| admin | `admin` | 1305 |
| sweep | `sweep` | 1315 |
| landing | `landing` | 1323 |
| wallet | `wallet` | 1345 |
| faucet | `faucet` | 1390 |
| messagebox | `messagebox` | 1399 |
| paymail | `paymail` | ~1407 |
| merkle | `merkle` | (wherever it's initialized) |

Pattern for each (example for store):
```go
storeSvc, err := c.Store.Initialize(ctx, logging.NewComponentLogger(logger, "store", ""))
```

For modules that already use `NewComponentLogger` internally (indexer, owner, bsv21-sync, wallet), verify they aren't double-tagging — if the module's own `Initialize()` already wraps with `NewComponentLogger`, pass the raw logger and let the module handle it. Otherwise, wrap at the call site.

- [ ] **Step 2: Verify logs include component tags**

Run: `cd /Users/davidcase/Source/1sat/1sat-stack && go run ./cmd/server --data-dir /tmp/1sat-test 2>&1 | head -20 | jq -r '.component // "NONE"'`

Every line should show a component name, not "NONE".

- [ ] **Step 3: Commit**

```bash
git add cmd/server/config.go
git commit -m "feat(logging): add component tags to all module loggers"
```

---

## Task 5: Admin API — Log Query Endpoints

**Files:**
- Create: `admin/log_routes.go`
- Modify: `admin/config.go`
- Modify: `admin/routes.go`

- [ ] **Step 1: Add LogStore to admin dependencies**

In `admin/config.go`, add to `InitializeDeps`:
```go
LogStore interface {
    Query(logging.LogQuery) ([]logging.LogEntry, int64, error)
}
```

In `admin/routes.go`, add to `Routes` struct:
```go
logStore interface {
    Query(logging.LogQuery) ([]logging.LogEntry, int64, error)
}
```

Wire it in `NewRoutes()` from deps.

- [ ] **Step 2: Create log_routes.go with query handler**

```go
// admin/log_routes.go
package admin

import (
	"strconv"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/logging"
	"github.com/gofiber/fiber/v2"
)

// handleQueryLogs queries persistent logs with filters.
// @Summary Query logs
// @Description Query system logs with optional filtering by level, component, time range
// @Tags admin
// @Produce json
// @Param component query string false "Component filter"
// @Param level query string false "Log level filter (DEBUG, INFO, WARN, ERROR)"
// @Param since query string false "Start time (RFC3339)"
// @Param until query string false "End time (RFC3339)"
// @Param search query string false "Text search in message"
// @Param limit query int false "Results per page (default 100, max 1000)"
// @Param offset query int false "Pagination offset"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/logs [get]
func (r *Routes) handleQueryLogs(c *fiber.Ctx) error {
	if r.logStore == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "log store not available",
		})
	}

	q := logging.LogQuery{
		Component: c.Query("component"),
		Level:     c.Query("level"),
		Search:    c.Query("search"),
		Limit:     100,
	}

	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "limit must be 1-1000"})
		}
		q.Limit = n
	}
	if v := c.Query("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid offset"})
		}
		q.Offset = n
	}
	if v := c.Query("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid since time"})
		}
		q.Since = t
	}
	if v := c.Query("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid until time"})
		}
		q.Until = t
	}

	entries, total, err := r.logStore.Query(q)
	if err != nil {
		r.logger.Error("log query failed", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "query failed"})
	}

	return c.JSON(fiber.Map{
		"total":   total,
		"entries": entries,
	})
}
```

- [ ] **Step 3: Register the route**

In `admin/routes.go`, in the `Register()` method, add:
```go
guardedGroup.Get("/logs", r.handleQueryLogs)
```

- [ ] **Step 4: Wire LogStore in config.go Initialize and main.go**

In `cmd/server/config.go` where admin is initialized (~line 1276), add:
```go
adminDeps.LogStore = cfg.LogStore // the SQLiteHandler from main.go
```

- [ ] **Step 5: Test the endpoint**

Run the server, then:
```bash
curl -H "X-Api-Key: <key>" "http://localhost:8080/1sat/admin/api/logs?limit=5&level=INFO" | jq .
```

Expected: JSON with `total` and `entries` array.

- [ ] **Step 6: Commit**

```bash
git add admin/log_routes.go admin/config.go admin/routes.go cmd/server/config.go
git commit -m "feat(admin): add log query API endpoint with filtering"
```

---

## Task 6: Admin UI — Log Viewer

**Files:**
- Create: `admin/ui/src/sections/Logs.tsx`
- Modify: `admin/ui/src/api.ts`
- Modify: `admin/ui/src/pages/SettingsPage.tsx`

- [ ] **Step 1: Add API functions to api.ts**

Append to `admin/ui/src/api.ts`:

```typescript
export interface LogEntry {
  time_ns: number;
  time: string;
  level: string;
  component: string;
  msg: string;
  attrs?: Record<string, any>;
}

export interface LogQueryResponse {
  total: number;
  entries: LogEntry[];
}

export async function queryLogs(params: {
  component?: string;
  level?: string;
  since?: string;
  until?: string;
  search?: string;
  limit?: number;
  offset?: number;
}): Promise<LogQueryResponse> {
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") qs.append(k, String(v));
  }
  const res = await apiFetch(`/logs?${qs}`);
  if (!res.ok) throw new Error((await res.json()).error || res.statusText);
  return res.json();
}
```

- [ ] **Step 2: Create Logs.tsx component**

`admin/ui/src/sections/Logs.tsx` — follows the existing section pattern (Card, CardHeader, CardContent, ScrollArea, Badge, Button). Contains:
- Filter bar: text input for search, dropdown for level (All/Debug/Info/Warn/Error), text input for component, date range inputs for since/until
- Log list: each entry shows timestamp, level badge (color-coded), component tag, message, expandable attrs
- Pagination: Previous/Next with total count
- Refresh button
- Auto-refresh toggle (polls every 5 seconds when enabled)

Level badge colors matching existing Badge usage:
- ERROR: `bg-destructive/20 text-destructive`
- WARN: `bg-yellow-500/20 text-yellow-600 dark:text-yellow-400`
- INFO: `bg-blue-500/20 text-blue-600 dark:text-blue-400`
- DEBUG: `bg-muted text-muted-foreground`

- [ ] **Step 3: Add Logs section to SettingsPage**

In `admin/ui/src/pages/SettingsPage.tsx`:

1. Add `"logs"` to `SectionId` type union
2. Add nav item: `{ type: "item", id: "logs", label: "Logs", icon: ScrollText }`
3. Import `ScrollText` from `lucide-react`
4. Import `Logs` from `@/sections/Logs`
5. Add render case: `{activeSection === "logs" && <Logs />}`

- [ ] **Step 4: Build and verify**

```bash
cd /Users/davidcase/Source/1sat/1sat-stack/admin/ui && bun run build
```

- [ ] **Step 5: Commit**

```bash
git add admin/ui/src/sections/Logs.tsx admin/ui/src/api.ts admin/ui/src/pages/SettingsPage.tsx
git commit -m "feat(admin): add log viewer UI with filtering and pagination"
```

---

## Task 7: Update Standards Doc

**Files:**
- Modify: `docs/standards/PER_MODULE_LOGGING.md`

- [ ] **Step 1: Update the doc**

Update the table of modules to reflect that all modules now have component tagging. Add a section on the SQLite persistent logging and how to query logs via admin. Remove the jq/grep filtering examples (superseded by admin UI).

- [ ] **Step 2: Commit**

```bash
git add docs/standards/PER_MODULE_LOGGING.md
git commit -m "docs: update logging standards with SQLite persistence and full module list"
```
