package storage

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

CREATE TABLE IF NOT EXISTS overlay_mutations (
    txid       BLOB PRIMARY KEY,
    phase      TEXT NOT NULL CHECK (phase IN ('active', 'applied', 'rollback_pending')),
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_overlay_mutations_phase ON overlay_mutations(phase, created_at);

CREATE TABLE IF NOT EXISTS mutation_outputs (
    mutation_txid   BLOB NOT NULL,
    outpoint        BLOB NOT NULL,
    txid            BLOB NOT NULL,
    satoshis        INTEGER,
    spend_txid      BLOB,
    score           REAL NOT NULL,
    deps            BLOB,
    inputs_consumed BLOB,
    consumed_by     BLOB,
    created_at      INTEGER NOT NULL,
    PRIMARY KEY (mutation_txid, outpoint)
);
CREATE INDEX IF NOT EXISTS idx_mutation_outputs_txid ON mutation_outputs(txid, mutation_txid);

CREATE TABLE IF NOT EXISTS mutation_events (
    mutation_txid BLOB NOT NULL,
    event         TEXT NOT NULL,
    outpoint      BLOB NOT NULL,
    score         REAL NOT NULL,
    PRIMARY KEY (mutation_txid, event, outpoint)
);

CREATE TABLE IF NOT EXISTS mutation_evictions (
    mutation_txid BLOB NOT NULL,
    outpoint      BLOB NOT NULL,
    PRIMARY KEY (mutation_txid, outpoint)
);

CREATE TABLE IF NOT EXISTS mutation_reservations (
    outpoint      BLOB PRIMARY KEY,
    mutation_txid BLOB NOT NULL
);

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

var _ ReversibleTopicStorage = (*SQLiteStorage)(nil)

// idleConnTimeout bounds how long an unused connection is kept open. Each live
// SQLite connection carries its own page cache allocated outside the Go heap,
// so across thousands of per-topic databases idle connections dominate RSS.
const idleConnTimeout = 30 * time.Second

// NewSQLiteStorage opens (or creates) a SQLite database at path with WAL mode
// and separate read/write connections.
func NewSQLiteStorage(path string) (*SQLiteStorage, error) {
	writer, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("open writer %s: %w", path, err)
	}
	writer.SetMaxOpenConns(1)
	writer.SetConnMaxIdleTime(idleConnTimeout)

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
	reader.SetConnMaxIdleTime(idleConnTimeout)

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

// BeginMutation atomically creates a replay guard, snapshots the complete
// retained ancestry of direct inputs, reserves it, and marks direct inputs as
// spent. No production engine path invokes it.
func (s *SQLiteStorage) BeginMutation(ctx context.Context, txid *chainhash.Hash, directInputs []*transaction.Outpoint) error {
	if txid == nil {
		return fmt.Errorf("begin mutation: transaction ID is nil")
	}
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO overlay_mutations (txid, phase, created_at) VALUES (?, 'active', ?)`,
		txid[:], time.Now().Unix(),
	); err != nil {
		return err
	}
	var phase string
	if err := tx.QueryRowContext(ctx, `SELECT phase FROM overlay_mutations WHERE txid = ?`, txid[:]).Scan(&phase); err != nil {
		return err
	}
	if MutationPhase(phase) != MutationPhaseActive {
		return fmt.Errorf("begin mutation: transaction %s is %s", txid, phase)
	}

	if err := sqliteSnapshotAncestry(ctx, tx, txid, directInputs); err != nil {
		return err
	}
	for _, op := range directInputs {
		if op == nil {
			return fmt.Errorf("begin mutation: direct input is nil")
		}
		result, err := tx.ExecContext(ctx,
			`UPDATE outputs SET spend_txid = ? WHERE outpoint = ? AND (spend_txid IS NULL OR spend_txid = ?)`,
			txid[:], op.Bytes(), txid[:],
		)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil {
			return err
		} else if affected != 1 {
			return fmt.Errorf("begin mutation: direct input %s is missing or already reserved", op)
		}
	}
	return tx.Commit()
}

func sqliteSnapshotAncestry(ctx context.Context, tx *sql.Tx, mutationTxid *chainhash.Hash, directInputs []*transaction.Outpoint) error {
	frontier := append([]*transaction.Outpoint(nil), directInputs...)
	seen := make(map[string]struct{}, len(frontier))
	direct := make(map[string]struct{}, len(directInputs))
	for _, op := range directInputs {
		if op == nil {
			return fmt.Errorf("begin mutation: direct input is nil")
		}
		direct[string(op.Bytes())] = struct{}{}
	}

	for len(frontier) > 0 {
		sort.Slice(frontier, func(i, j int) bool {
			return bytes.Compare(frontier[i].Bytes(), frontier[j].Bytes()) < 0
		})
		op := frontier[0]
		frontier = frontier[1:]
		key := string(op.Bytes())
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		var inputsConsumed []byte
		err := tx.QueryRowContext(ctx, `SELECT inputs_consumed FROM outputs WHERE outpoint = ?`, op.Bytes()).Scan(&inputsConsumed)
		if err == sql.ErrNoRows {
			if _, isDirect := direct[key]; isDirect {
				return fmt.Errorf("begin mutation: direct input %s was not found", op)
			}
			continue
		}
		if err != nil {
			return err
		}

		var owner []byte
		err = tx.QueryRowContext(ctx, `SELECT mutation_txid FROM mutation_reservations WHERE outpoint = ?`, op.Bytes()).Scan(&owner)
		switch {
		case err == nil && !bytes.Equal(owner, mutationTxid[:]):
			return fmt.Errorf("begin mutation: output %s is reserved by another transaction", op)
		case err != nil && err != sql.ErrNoRows:
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO mutation_reservations (outpoint, mutation_txid) VALUES (?, ?)`,
			op.Bytes(), mutationTxid[:],
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO mutation_outputs
				(mutation_txid, outpoint, txid, satoshis, spend_txid, score, deps, inputs_consumed, consumed_by, created_at)
			SELECT ?, outpoint, txid, satoshis, spend_txid, score, deps, inputs_consumed, consumed_by, created_at
			FROM outputs WHERE outpoint = ?`, mutationTxid[:], op.Bytes()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO mutation_events (mutation_txid, event, outpoint, score)
			SELECT ?, event, outpoint, score FROM events WHERE outpoint = ?`, mutationTxid[:], op.Bytes()); err != nil {
			return err
		}

		ancestors, err := decodeOutpoints(inputsConsumed)
		if err != nil {
			return fmt.Errorf("begin mutation: output %s: %w", op, err)
		}
		frontier = append(frontier, ancestors...)
	}
	return nil
}

func (s *SQLiteStorage) CommitMutation(ctx context.Context, txid *chainhash.Hash, score float64) error {
	if txid == nil {
		return fmt.Errorf("commit mutation: transaction ID is nil")
	}
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var phase string
	if err := tx.QueryRowContext(ctx, `SELECT phase FROM overlay_mutations WHERE txid = ?`, txid[:]).Scan(&phase); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("commit mutation: transaction %s has no active guard", txid)
		}
		return err
	}
	if MutationPhase(phase) == MutationPhaseRollbackPending {
		return fmt.Errorf("commit mutation: transaction %s is pending rollback finalization", txid)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO applied_txs (txid, score) VALUES (?, ?)`, txid[:], score,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE overlay_mutations SET phase = 'applied' WHERE txid = ?`, txid[:],
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM mutation_reservations WHERE mutation_txid = ?`, txid[:],
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStorage) GetMutation(ctx context.Context, txid *chainhash.Hash) (*MutationRecord, error) {
	if txid == nil {
		return nil, fmt.Errorf("get mutation: transaction ID is nil")
	}
	var txidBytes []byte
	var phase string
	var createdAt int64
	err := s.reader.QueryRowContext(ctx,
		`SELECT txid, phase, created_at FROM overlay_mutations WHERE txid = ?`, txid[:],
	).Scan(&txidBytes, &phase, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return newMutationRecord(txidBytes, phase, createdAt)
}

func (s *SQLiteStorage) ListMutations(ctx context.Context) ([]MutationRecord, error) {
	rows, err := s.reader.QueryContext(ctx,
		`SELECT txid, phase, created_at FROM overlay_mutations ORDER BY created_at, txid`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var mutations []MutationRecord
	for rows.Next() {
		var txidBytes []byte
		var phase string
		var createdAt int64
		if err := rows.Scan(&txidBytes, &phase, &createdAt); err != nil {
			return nil, err
		}
		record, err := newMutationRecord(txidBytes, phase, createdAt)
		if err != nil {
			return nil, err
		}
		mutations = append(mutations, *record)
	}
	return mutations, rows.Err()
}

func (s *SQLiteStorage) HasMutationGuard(ctx context.Context, txid *chainhash.Hash) (bool, error) {
	record, err := s.GetMutation(ctx, txid)
	return record != nil, err
}

func (s *SQLiteStorage) RollbackMutation(ctx context.Context, txid *chainhash.Hash) (*RollbackResult, error) {
	if txid == nil {
		return nil, fmt.Errorf("rollback mutation: transaction ID is nil")
	}
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var phase string
	if err := tx.QueryRowContext(ctx, `SELECT phase FROM overlay_mutations WHERE txid = ?`, txid[:]).Scan(&phase); err != nil {
		if err == sql.ErrNoRows {
			var applied bool
			if err := tx.QueryRowContext(ctx,
				`SELECT EXISTS(SELECT 1 FROM applied_txs WHERE txid = ?)`, txid[:],
			).Scan(&applied); err != nil {
				return nil, err
			}
			if applied {
				return nil, fmt.Errorf("rollback mutation: applied transaction %s has no rollback journal", txid)
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return &RollbackResult{}, nil
		}
		return nil, err
	}
	if MutationPhase(phase) != MutationPhaseRollbackPending {
		if err := sqliteRejectLiveDescendants(ctx, tx, txid); err != nil {
			return nil, fmt.Errorf("rollback mutation: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO mutation_evictions (mutation_txid, outpoint)
			SELECT ?, outpoint FROM outputs WHERE txid = ?`, txid[:], txid[:]); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM events WHERE outpoint IN (SELECT outpoint FROM outputs WHERE txid = ?)`, txid[:],
		); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM outputs WHERE txid = ?`, txid[:]); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM events WHERE outpoint IN
				(SELECT outpoint FROM mutation_outputs WHERE mutation_txid = ?)`, txid[:]); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR REPLACE INTO outputs
				(outpoint, txid, satoshis, spend_txid, score, deps, inputs_consumed, consumed_by, created_at)
			SELECT outpoint, txid, satoshis, spend_txid, score, deps, inputs_consumed, consumed_by, created_at
			FROM mutation_outputs WHERE mutation_txid = ?`, txid[:]); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR REPLACE INTO events (event, outpoint, score)
			SELECT event, outpoint, score FROM mutation_events WHERE mutation_txid = ?`, txid[:]); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM mutation_reservations WHERE mutation_txid = ?`, txid[:]); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE overlay_mutations SET phase = 'rollback_pending' WHERE txid = ?`, txid[:],
		); err != nil {
			return nil, err
		}
	}

	result, err := sqliteMutationResult(ctx, tx, txid)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func sqliteRejectLiveDescendants(ctx context.Context, tx *sql.Tx, txid *chainhash.Hash) error {
	var conflictingOutpoint []byte
	err := tx.QueryRowContext(ctx, `
		SELECT r.outpoint FROM mutation_reservations r
		WHERE r.mutation_txid != ? AND (
			r.outpoint IN (SELECT outpoint FROM outputs WHERE txid = ?)
			OR r.outpoint IN (SELECT outpoint FROM mutation_outputs WHERE mutation_txid = ?)
		) LIMIT 1`, txid[:], txid[:], txid[:]).Scan(&conflictingOutpoint)
	if err == nil {
		return fmt.Errorf("output %x is reserved by an active descendant", conflictingOutpoint)
	}
	if err != sql.ErrNoRows {
		return err
	}

	var descendantTxid []byte
	err = tx.QueryRowContext(ctx, `
		SELECT m.txid FROM overlay_mutations m
		JOIN mutation_outputs o ON o.mutation_txid = m.txid
		WHERE m.txid != ? AND m.phase IN ('active', 'applied') AND o.txid = ?
		ORDER BY m.created_at, m.txid LIMIT 1`, txid[:], txid[:]).Scan(&descendantTxid)
	if err == nil {
		return fmt.Errorf("live descendant %x must be rolled back first", descendantTxid)
	}
	if err != sql.ErrNoRows {
		return err
	}
	return nil
}

func sqliteMutationResult(ctx context.Context, tx *sql.Tx, txid *chainhash.Hash) (*RollbackResult, error) {
	result := &RollbackResult{}
	rows, err := tx.QueryContext(ctx,
		`SELECT outpoint FROM mutation_evictions WHERE mutation_txid = ? ORDER BY outpoint`, txid[:],
	)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		op := transaction.NewOutpointFromBytes(raw)
		if op == nil {
			rows.Close()
			return nil, fmt.Errorf("rollback mutation: invalid evicted outpoint %x", raw)
		}
		result.Evicted = append(result.Evicted, *op)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	rows, err = tx.QueryContext(ctx, `
		SELECT outpoint, txid, satoshis, spend_txid, score, deps, inputs_consumed, consumed_by, created_at
		FROM mutation_outputs WHERE mutation_txid = ? ORDER BY outpoint`, txid[:])
	if err != nil {
		return nil, err
	}
	result.Restored, err = scanOutputs(rows)
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	return result, err
}

func (s *SQLiteStorage) FinalizeRollback(ctx context.Context, txid *chainhash.Hash) error {
	if txid == nil {
		return fmt.Errorf("finalize rollback: transaction ID is nil")
	}
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var phase string
	err = tx.QueryRowContext(ctx, `SELECT phase FROM overlay_mutations WHERE txid = ?`, txid[:]).Scan(&phase)
	if err == sql.ErrNoRows {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if MutationPhase(phase) != MutationPhaseRollbackPending {
		return fmt.Errorf("finalize rollback: transaction %s is %s", txid, phase)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM applied_txs WHERE txid = ?`, txid[:]); err != nil {
		return err
	}
	if err := sqliteDeleteMutationJournal(ctx, tx, txid); err != nil {
		return err
	}
	return tx.Commit()
}

// PruneMutation discards rollback material for an applied mutation while
// preserving its applied transaction marker. The caller owns finality policy.
func (s *SQLiteStorage) PruneMutation(ctx context.Context, txid *chainhash.Hash) error {
	if txid == nil {
		return fmt.Errorf("prune mutation: transaction ID is nil")
	}
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var phase string
	err = tx.QueryRowContext(ctx, `SELECT phase FROM overlay_mutations WHERE txid = ?`, txid[:]).Scan(&phase)
	if err == sql.ErrNoRows {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if MutationPhase(phase) != MutationPhaseApplied {
		return fmt.Errorf("prune mutation: transaction %s is %s", txid, phase)
	}
	if err := sqliteRejectLiveAncestors(ctx, tx, txid); err != nil {
		return fmt.Errorf("prune mutation: %w", err)
	}
	if err := sqliteDeleteMutationJournal(ctx, tx, txid); err != nil {
		return err
	}
	return tx.Commit()
}

func sqliteRejectLiveAncestors(ctx context.Context, tx *sql.Tx, txid *chainhash.Hash) error {
	var ancestorTxid []byte
	err := tx.QueryRowContext(ctx, `
		SELECT parent.txid FROM overlay_mutations parent
		JOIN mutation_outputs child ON child.txid = parent.txid
		WHERE child.mutation_txid = ? AND parent.txid != ?
		ORDER BY parent.created_at, parent.txid LIMIT 1`, txid[:], txid[:]).Scan(&ancestorTxid)
	if err == nil {
		return fmt.Errorf("reversible ancestor %x must be pruned first", ancestorTxid)
	}
	if err != sql.ErrNoRows {
		return err
	}
	return nil
}

func sqliteDeleteMutationJournal(ctx context.Context, tx *sql.Tx, txid *chainhash.Hash) error {
	for _, query := range []string{
		`DELETE FROM mutation_events WHERE mutation_txid = ?`,
		`DELETE FROM mutation_outputs WHERE mutation_txid = ?`,
		`DELETE FROM mutation_evictions WHERE mutation_txid = ?`,
		`DELETE FROM mutation_reservations WHERE mutation_txid = ?`,
		`DELETE FROM overlay_mutations WHERE txid = ?`,
	} {
		if _, err := tx.ExecContext(ctx, query, txid[:]); err != nil {
			return err
		}
	}
	return nil
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
