package logging

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const logsSchema = `
CREATE TABLE IF NOT EXISTS logs (
    id INTEGER PRIMARY KEY,
    time_ns INTEGER NOT NULL,
    level TEXT NOT NULL,
    component TEXT,
    msg TEXT NOT NULL,
    attrs TEXT
);
CREATE INDEX IF NOT EXISTS idx_logs_time ON logs(time_ns);
CREATE INDEX IF NOT EXISTS idx_logs_component ON logs(component);
CREATE INDEX IF NOT EXISTS idx_logs_level ON logs(level);
`

// SQLiteHandlerOptions configures the SQLiteHandler.
type SQLiteHandlerOptions struct {
	BatchSize     int           // flush after N records (default 100)
	FlushInterval time.Duration // flush after this duration (default 1s)
	Retention     time.Duration // delete records older than this (default 7 days)
	PruneInterval time.Duration // how often to run pruning (default 1h)
	BufferSize    int           // channel capacity (default 10000)
	Level         slog.Level    // minimum level to store (default LevelDebug)
}

func (o *SQLiteHandlerOptions) applyDefaults() {
	if o.BatchSize <= 0 {
		o.BatchSize = 100
	}
	if o.FlushInterval <= 0 {
		o.FlushInterval = time.Second
	}
	if o.Retention <= 0 {
		o.Retention = 7 * 24 * time.Hour
	}
	if o.PruneInterval <= 0 {
		o.PruneInterval = time.Hour
	}
	if o.BufferSize <= 0 {
		o.BufferSize = 10000
	}
}

// LogQuery specifies filters for querying stored log entries.
type LogQuery struct {
	Component string
	Level     string
	Since     time.Time
	Until     time.Time
	Search    string // LIKE %search% on msg
	Limit     int
	Offset    int
}

// LogEntry is a single stored log record returned from a query.
type LogEntry struct {
	TimeNs    int64          `json:"time_ns"`
	Time      string         `json:"time"`
	Level     string         `json:"level"`
	Component string         `json:"component"`
	Msg       string         `json:"msg"`
	Attrs     map[string]any `json:"attrs,omitempty"`
}

type logRecord struct {
	timeNs    int64
	level     string
	component string
	msg       string
	attrs     string // JSON-encoded
}

// SQLiteHandler is an slog.Handler that writes log records to a SQLite database.
type SQLiteHandler struct {
	opts      SQLiteHandlerOptions
	db        *sql.DB
	insertStmt *sql.Stmt
	ch        chan logRecord
	closeOnce sync.Once
	done      chan struct{}
	// pre-applied attrs from WithAttrs
	component string
	group     string
	preAttrs  []slog.Attr
}

// NewSQLiteHandler opens (or creates) a SQLite log database at dbPath and starts
// background writer and pruner goroutines.
func NewSQLiteHandler(dbPath string, opts *SQLiteHandlerOptions) (*SQLiteHandler, error) {
	var o SQLiteHandlerOptions
	if opts != nil {
		o = *opts
	}
	o.applyDefaults()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open log db %s: %w", dbPath, err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err := db.Exec(logsSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init log schema: %w", err)
	}

	insertStmt, err := db.Prepare(`INSERT INTO logs (time_ns, level, component, msg, attrs) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("prepare insert: %w", err)
	}

	h := &SQLiteHandler{
		opts:       o,
		db:         db,
		insertStmt: insertStmt,
		ch:         make(chan logRecord, o.BufferSize),
		done:       make(chan struct{}),
	}

	go h.writer()
	go h.pruner()

	return h, nil
}

// Enabled reports whether the handler handles records at the given level.
func (h *SQLiteHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.opts.Level
}

// Handle sends the record to the background writer via a non-blocking channel send.
func (h *SQLiteHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level < h.opts.Level {
		return nil
	}

	component := h.component
	attrsMap := make(map[string]any, len(h.preAttrs))

	// Apply pre-attrs
	for _, a := range h.preAttrs {
		if a.Key == ComponentKey {
			component = a.Value.String()
		} else {
			attrsMap[h.prefixKey(a.Key)] = a.Value.Any()
		}
	}

	// Apply record attrs
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == ComponentKey && h.group == "" {
			component = a.Value.String()
		} else {
			attrsMap[h.prefixKey(a.Key)] = a.Value.Any()
		}
		return true
	})

	var attrsJSON string
	if len(attrsMap) > 0 {
		b, _ := json.Marshal(attrsMap)
		attrsJSON = string(b)
	}

	rec := logRecord{
		timeNs:    r.Time.UnixNano(),
		level:     r.Level.String(),
		component: component,
		msg:       r.Message,
		attrs:     attrsJSON,
	}

	select {
	case h.ch <- rec:
	default:
		// channel full — drop record
	}
	return nil
}

// WithAttrs returns a new handler with the given attrs pre-applied.
// The "component" key is extracted into the component field.
func (h *SQLiteHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	nh := h.shallowCopy()
	for _, a := range attrs {
		if a.Key == ComponentKey && h.group == "" {
			nh.component = a.Value.String()
		} else {
			nh.preAttrs = append(nh.preAttrs, a)
		}
	}
	return nh
}

// WithGroup returns a new handler with the given group prefix for attr keys.
func (h *SQLiteHandler) WithGroup(name string) slog.Handler {
	nh := h.shallowCopy()
	if h.group != "" {
		nh.group = h.group + "." + name
	} else {
		nh.group = name
	}
	return nh
}

func (h *SQLiteHandler) prefixKey(key string) string {
	if h.group != "" {
		return h.group + "." + key
	}
	return key
}

func (h *SQLiteHandler) shallowCopy() *SQLiteHandler {
	preAttrs := make([]slog.Attr, len(h.preAttrs))
	copy(preAttrs, h.preAttrs)
	return &SQLiteHandler{
		opts:       h.opts,
		db:         h.db,
		insertStmt: h.insertStmt,
		ch:         h.ch,
		done:       h.done,
		component:  h.component,
		group:      h.group,
		preAttrs:   preAttrs,
	}
}

// Query retrieves log entries matching the given filters. Returns entries, total count, and error.
func (h *SQLiteHandler) Query(q LogQuery) ([]LogEntry, int64, error) {
	where, args := buildWhere(q)

	countQuery := "SELECT COUNT(*) FROM logs" + where
	var total int64
	if err := h.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count logs: %w", err)
	}

	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}

	selectQuery := "SELECT time_ns, level, component, msg, attrs FROM logs" + where +
		" ORDER BY time_ns DESC LIMIT ? OFFSET ?"
	rows, err := h.db.Query(selectQuery, append(args, limit, q.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close()

	entries := make([]LogEntry, 0, limit)
	for rows.Next() {
		var e LogEntry
		var component sql.NullString
		var attrsJSON sql.NullString
		if err := rows.Scan(&e.TimeNs, &e.Level, &component, &e.Msg, &attrsJSON); err != nil {
			return nil, 0, fmt.Errorf("scan log row: %w", err)
		}
		e.Component = component.String
		e.Time = time.Unix(0, e.TimeNs).UTC().Format(time.RFC3339Nano)
		if attrsJSON.Valid && attrsJSON.String != "" {
			if err := json.Unmarshal([]byte(attrsJSON.String), &e.Attrs); err != nil {
				e.Attrs = map[string]any{"_raw": attrsJSON.String}
			}
		}
		entries = append(entries, e)
	}
	return entries, total, rows.Err()
}

// Components returns distinct non-empty component names from stored logs.
func (h *SQLiteHandler) Components() ([]string, error) {
	rows, err := h.db.Query(`SELECT DISTINCT component FROM logs WHERE component IS NOT NULL AND component != '' ORDER BY component`)
	if err != nil {
		return nil, fmt.Errorf("query components: %w", err)
	}
	defer rows.Close()
	var components []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		components = append(components, c)
	}
	return components, rows.Err()
}

func buildWhere(q LogQuery) (string, []any) {
	var clauses []string
	var args []any

	if q.Component != "" {
		clauses = append(clauses, "component = ?")
		args = append(args, q.Component)
	}
	if q.Level != "" {
		clauses = append(clauses, "level = ?")
		args = append(args, q.Level)
	}
	if !q.Since.IsZero() {
		clauses = append(clauses, "time_ns >= ?")
		args = append(args, q.Since.UnixNano())
	}
	if !q.Until.IsZero() {
		clauses = append(clauses, "time_ns <= ?")
		args = append(args, q.Until.UnixNano())
	}
	if q.Search != "" {
		clauses = append(clauses, "msg LIKE ?")
		args = append(args, "%"+q.Search+"%")
	}

	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// Close shuts down the handler: stops accepting new records, drains remaining
// records, flushes to DB, and closes the database.
func (h *SQLiteHandler) Close() error {
	h.closeOnce.Do(func() {
		close(h.ch)
		<-h.done
		h.insertStmt.Close()
		h.db.Close()
	})
	return nil
}

func (h *SQLiteHandler) writer() {
	defer close(h.done)

	ticker := time.NewTicker(h.opts.FlushInterval)
	defer ticker.Stop()

	batch := make([]logRecord, 0, h.opts.BatchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		tx, err := h.db.Begin()
		if err != nil {
			batch = batch[:0]
			return
		}
		stmt := tx.Stmt(h.insertStmt)
		for _, rec := range batch {
			var component any
			if rec.component != "" {
				component = rec.component
			}
			var attrs any
			if rec.attrs != "" {
				attrs = rec.attrs
			}
			stmt.Exec(rec.timeNs, rec.level, component, rec.msg, attrs)
		}
		stmt.Close()
		tx.Commit()
		batch = batch[:0]
	}

	for {
		select {
		case rec, ok := <-h.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, rec)
			if len(batch) >= h.opts.BatchSize {
				flush()
				ticker.Reset(h.opts.FlushInterval)
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (h *SQLiteHandler) pruner() {
	ticker := time.NewTicker(h.opts.PruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-h.opts.Retention).UnixNano()
			h.db.Exec(`DELETE FROM logs WHERE time_ns < ?`, cutoff)
		case <-h.done:
			return
		}
	}
}
