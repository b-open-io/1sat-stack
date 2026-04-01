package storage

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	_ "github.com/mattn/go-sqlite3"
)

const schema = `
CREATE TABLE IF NOT EXISTS outputs (
    outpoint          BLOB PRIMARY KEY,
    txid              BLOB NOT NULL,
    satoshis          INTEGER,
    spend_txid        BLOB,
    score             REAL NOT NULL,
    deps              BLOB,
    inputs_consumed   BLOB,
    consumed_by       BLOB,
    created_at        INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_outputs_txid ON outputs(txid);
CREATE INDEX IF NOT EXISTS idx_outputs_unspent ON outputs(spend_txid) WHERE spend_txid IS NULL;

CREATE TABLE IF NOT EXISTS applied_txs (
    txid    BLOB PRIMARY KEY,
    score   REAL NOT NULL
);

CREATE TABLE IF NOT EXISTS events (
    event       TEXT NOT NULL,
    outpoint    BLOB NOT NULL,
    score       REAL NOT NULL,
    PRIMARY KEY (event, outpoint)
);
CREATE INDEX IF NOT EXISTS idx_events_score ON events(event, score);

CREATE TABLE IF NOT EXISTS peer_interactions (
    host  TEXT PRIMARY KEY,
    since REAL NOT NULL
);
`

// SQLiteStorage implements TopicStorage backed by a single SQLite database.
type SQLiteStorage struct {
	writer *sql.DB
	reader *sql.DB
}

// NewSQLiteStorage opens (or creates) a SQLite database at path with WAL mode
// and separate read/write connections.
func NewSQLiteStorage(path string) (*SQLiteStorage, error) {
	writer, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("open writer %s: %w", path, err)
	}
	writer.SetMaxOpenConns(1)

	if _, err := writer.Exec(schema); err != nil {
		writer.Close()
		return nil, fmt.Errorf("init schema %s: %w", path, err)
	}

	reader, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000&mode=ro")
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("open reader %s: %w", path, err)
	}
	reader.SetMaxOpenConns(4)

	return &SQLiteStorage{writer: writer, reader: reader}, nil
}

// SQLiteFactory manages per-topic SQLite databases and the cross-topic txid→topics index.
type SQLiteFactory struct {
	basePath     string
	stores       sync.Map
	txTopicIndex *TxTopicIndex
}

// NewSQLiteFactory creates a factory that manages per-topic SQLite databases
// under basePath, plus a singleton txid→topics index database.
func NewSQLiteFactory(basePath string) (*SQLiteFactory, error) {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("create overlay storage dir %s: %w", basePath, err)
	}
	idx, err := NewTxTopicIndex(filepath.Join(basePath, "tx_topics.db"))
	if err != nil {
		return nil, fmt.Errorf("failed to create tx topic index: %w", err)
	}
	return &SQLiteFactory{
		basePath:     basePath,
		txTopicIndex: idx,
	}, nil
}

// Topic returns the TopicStorage for a given topic, creating it if needed.
func (f *SQLiteFactory) Topic(topic string) (TopicStorage, error) {
	if s, ok := f.stores.Load(topic); ok {
		return s.(*SQLiteStorage), nil
	}
	path := filepath.Join(f.basePath, topic+".db")
	slog.Info("SQLiteFactory.Topic", "basePath", f.basePath, "topic", topic, "path", path)
	s, err := NewSQLiteStorage(path)
	if err != nil {
		return nil, err
	}
	actual, loaded := f.stores.LoadOrStore(topic, s)
	if loaded {
		s.Close()
		return actual.(*SQLiteStorage), nil
	}
	return s, nil
}

// Factory returns a Factory function for callers that need the func signature.
func (f *SQLiteFactory) Factory() Factory {
	return f.Topic
}

// TxTopicIndex returns the cross-topic txid→topics index.
func (f *SQLiteFactory) TxTopicIndex() *TxTopicIndex {
	return f.txTopicIndex
}

// Close closes the txid→topics index and all cached topic databases.
func (f *SQLiteFactory) Close() error {
	f.stores.Range(func(key, value any) bool {
		value.(*SQLiteStorage).Close()
		return true
	})
	return f.txTopicIndex.Close()
}

func (s *SQLiteStorage) DB() *sql.DB {
	return s.writer
}

func (s *SQLiteStorage) TopicID() int {
	return 0
}

func (s *SQLiteStorage) Close() error {
	s.reader.Close()
	return s.writer.Close()
}

// --- Engine writes ---

func (s *SQLiteStorage) InsertOutput(ctx context.Context, op *transaction.Outpoint, txid *chainhash.Hash, satoshis uint64, deps []byte, inputsConsumed []byte, score float64) error {
	_, err := s.writer.ExecContext(ctx,
		`INSERT OR REPLACE INTO outputs (outpoint, txid, satoshis, score, deps, inputs_consumed, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		op.Bytes(), txid[:], satoshis, score, deps, inputsConsumed, time.Now().Unix(),
	)
	return err
}

func (s *SQLiteStorage) GetOutput(ctx context.Context, op *transaction.Outpoint) (*OutputRecord, error) {
	row := s.reader.QueryRowContext(ctx,
		`SELECT outpoint, txid, satoshis, spend_txid, score, deps, inputs_consumed, consumed_by, created_at FROM outputs WHERE outpoint = ?`,
		op.Bytes(),
	)
	return scanOutput(row)
}

func (s *SQLiteStorage) FindOutputs(ctx context.Context, outpoints []*transaction.Outpoint) ([]OutputRecord, error) {
	if len(outpoints) == 0 {
		return nil, nil
	}

	query := `SELECT outpoint, txid, satoshis, spend_txid, score, deps, inputs_consumed, consumed_by, created_at FROM outputs WHERE outpoint IN (`
	args := make([]any, len(outpoints))
	for i, op := range outpoints {
		if i > 0 {
			query += ","
		}
		query += "?"
		args[i] = op.Bytes()
	}
	query += ")"

	rows, err := s.reader.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOutputs(rows)
}

func (s *SQLiteStorage) FindOutputsForTransaction(ctx context.Context, txid *chainhash.Hash) ([]OutputRecord, error) {
	rows, err := s.reader.QueryContext(ctx,
		`SELECT outpoint, txid, satoshis, spend_txid, score, deps, inputs_consumed, consumed_by, created_at FROM outputs WHERE txid = ?`,
		txid[:],
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOutputs(rows)
}

func (s *SQLiteStorage) MarkSpent(ctx context.Context, op *transaction.Outpoint, spendTxid *chainhash.Hash) error {
	_, err := s.writer.ExecContext(ctx,
		`UPDATE outputs SET spend_txid = ? WHERE outpoint = ?`,
		spendTxid[:], op.Bytes(),
	)
	return err
}

func (s *SQLiteStorage) UpdateConsumedBy(ctx context.Context, op *transaction.Outpoint, consumedBy []byte) error {
	_, err := s.writer.ExecContext(ctx,
		`UPDATE outputs SET consumed_by = ? WHERE outpoint = ?`,
		consumedBy, op.Bytes(),
	)
	return err
}

func (s *SQLiteStorage) FindUTXOs(ctx context.Context, opts *QueryOpts) ([]OutputRecord, error) {
	query := `SELECT outpoint, txid, satoshis, spend_txid, score, deps, inputs_consumed, consumed_by, created_at FROM outputs WHERE spend_txid IS NULL`
	var args []any

	if opts != nil && opts.Since > 0 {
		query += " AND score > ?"
		args = append(args, opts.Since)
	}
	if opts != nil && opts.Reverse {
		query += " ORDER BY score DESC"
	} else {
		query += " ORDER BY score ASC"
	}
	if opts != nil && opts.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, opts.Limit)
	}

	rows, err := s.reader.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOutputs(rows)
}

func (s *SQLiteStorage) DeleteOutput(ctx context.Context, op *transaction.Outpoint) error {
	opBytes := op.Bytes()
	_, err := s.writer.ExecContext(ctx, `DELETE FROM outputs WHERE outpoint = ?`, opBytes)
	if err != nil {
		return err
	}
	_, err = s.writer.ExecContext(ctx, `DELETE FROM events WHERE outpoint = ?`, opBytes)
	return err
}

func (s *SQLiteStorage) Rollback(ctx context.Context, txid *chainhash.Hash) error {
	_, err := s.writer.ExecContext(ctx, `DELETE FROM outputs WHERE txid = ?`, txid[:])
	return err
}

// --- Applied transactions ---

func (s *SQLiteStorage) InsertAppliedTx(ctx context.Context, txid *chainhash.Hash, score float64) error {
	_, err := s.writer.ExecContext(ctx,
		`INSERT OR IGNORE INTO applied_txs (txid, score) VALUES (?, ?)`,
		txid[:], score,
	)
	return err
}

func (s *SQLiteStorage) HasAppliedTx(ctx context.Context, txid *chainhash.Hash) (bool, error) {
	var exists bool
	err := s.reader.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM applied_txs WHERE txid = ?)`,
		txid[:],
	).Scan(&exists)
	return exists, err
}

// --- GASP peer sync ---

func (s *SQLiteStorage) UpdateLastInteraction(ctx context.Context, host string, since float64) error {
	_, err := s.writer.ExecContext(ctx,
		`INSERT OR REPLACE INTO peer_interactions (host, since) VALUES (?, ?)`,
		host, since,
	)
	return err
}

func (s *SQLiteStorage) GetLastInteraction(ctx context.Context, host string) (float64, error) {
	var since float64
	err := s.reader.QueryRowContext(ctx,
		`SELECT since FROM peer_interactions WHERE host = ?`,
		host,
	).Scan(&since)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return since, err
}

// --- Lookup service events ---

func (s *SQLiteStorage) SaveEvent(ctx context.Context, event string, op *transaction.Outpoint, score float64) error {
	_, err := s.writer.ExecContext(ctx,
		`INSERT OR REPLACE INTO events (event, outpoint, score) VALUES (?, ?, ?)`,
		event, op.Bytes(), score,
	)
	return err
}

func (s *SQLiteStorage) DeleteEvent(ctx context.Context, event string, op *transaction.Outpoint) error {
	_, err := s.writer.ExecContext(ctx,
		`DELETE FROM events WHERE event = ? AND outpoint = ?`,
		event, op.Bytes(),
	)
	return err
}

func (s *SQLiteStorage) FindByEvent(ctx context.Context, event string, opts *QueryOpts) ([]OutputRecord, error) {
	query := `SELECT o.outpoint, o.txid, o.satoshis, o.spend_txid, o.score, o.deps, o.inputs_consumed, o.consumed_by, o.created_at
		FROM events e JOIN outputs o ON e.outpoint = o.outpoint
		WHERE e.event = ?`
	args := []any{event}

	if opts != nil && opts.Since > 0 {
		query += " AND e.score > ?"
		args = append(args, opts.Since)
	}
	if opts != nil && opts.Reverse {
		query += " ORDER BY e.score DESC"
	} else {
		query += " ORDER BY e.score ASC"
	}
	if opts != nil && opts.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, opts.Limit)
	}

	rows, err := s.reader.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOutputs(rows)
}

// --- scan helpers ---

func scanOutput(row *sql.Row) (*OutputRecord, error) {
	var rec OutputRecord
	var opBytes, txidBytes, spendBytes []byte
	err := row.Scan(&opBytes, &txidBytes, &rec.Satoshis, &spendBytes, &rec.Score, &rec.Deps, &rec.InputsConsumed, &rec.ConsumedBy, &rec.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := hydrateRecord(&rec, opBytes, txidBytes, spendBytes); err != nil {
		return nil, err
	}
	return &rec, nil
}

func scanOutputs(rows *sql.Rows) ([]OutputRecord, error) {
	var results []OutputRecord
	for rows.Next() {
		var rec OutputRecord
		var opBytes, txidBytes, spendBytes []byte
		if err := rows.Scan(&opBytes, &txidBytes, &rec.Satoshis, &spendBytes, &rec.Score, &rec.Deps, &rec.InputsConsumed, &rec.ConsumedBy, &rec.CreatedAt); err != nil {
			return nil, err
		}
		if err := hydrateRecord(&rec, opBytes, txidBytes, spendBytes); err != nil {
			return nil, err
		}
		results = append(results, rec)
	}
	return results, rows.Err()
}

func hydrateRecord(rec *OutputRecord, opBytes, txidBytes, spendBytes []byte) error {
	op := transaction.NewOutpointFromBytes(opBytes)
	if op == nil {
		return fmt.Errorf("invalid outpoint bytes: %x", opBytes)
	}
	rec.Outpoint = *op
	if len(txidBytes) == 32 {
		copy(rec.Txid[:], txidBytes)
	}
	if len(spendBytes) == 32 {
		rec.SpendTxid = &chainhash.Hash{}
		copy(rec.SpendTxid[:], spendBytes)
	}
	return nil
}
