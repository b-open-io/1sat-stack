package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// PostgresStorage implements TopicStorage backed by a shared PostgreSQL database.
// Each instance is scoped to a single topic via topicID.
type PostgresStorage struct {
	db      *sql.DB
	topicID int
}

func (s *PostgresStorage) DB() *sql.DB {
	return s.db
}

func (s *PostgresStorage) TopicID() int {
	return s.topicID
}

func (s *PostgresStorage) Close() error {
	return nil // connection pool is owned by the factory
}

// --- Engine writes ---

func (s *PostgresStorage) InsertOutput(ctx context.Context, op *transaction.Outpoint, txid *chainhash.Hash, satoshis uint64, deps []byte, inputsConsumed []byte, score float64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO outputs (topic_id, outpoint, txid, satoshis, score, deps, inputs_consumed, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (topic_id, outpoint) DO UPDATE SET
			txid = EXCLUDED.txid, satoshis = EXCLUDED.satoshis, score = EXCLUDED.score,
			deps = EXCLUDED.deps, inputs_consumed = EXCLUDED.inputs_consumed, created_at = EXCLUDED.created_at`,
		s.topicID, op.Bytes(), txid[:], satoshis, score, deps, inputsConsumed, time.Now().Unix(),
	)
	return err
}

func (s *PostgresStorage) GetOutput(ctx context.Context, op *transaction.Outpoint) (*OutputRecord, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT outpoint, txid, satoshis, spend_txid, score, deps, inputs_consumed, consumed_by, created_at
		FROM outputs WHERE topic_id = $1 AND outpoint = $2`,
		s.topicID, op.Bytes(),
	)
	return scanOutput(row)
}

func (s *PostgresStorage) FindOutputs(ctx context.Context, outpoints []*transaction.Outpoint) ([]OutputRecord, error) {
	if len(outpoints) == 0 {
		return nil, nil
	}

	query := `SELECT outpoint, txid, satoshis, spend_txid, score, deps, inputs_consumed, consumed_by, created_at
		FROM outputs WHERE topic_id = $1 AND outpoint IN (`
	args := []any{s.topicID}
	for i, op := range outpoints {
		if i > 0 {
			query += ","
		}
		query += fmt.Sprintf("$%d", i+2)
		args = append(args, op.Bytes())
	}
	query += ")"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOutputs(rows)
}

func (s *PostgresStorage) FindOutputsForTransaction(ctx context.Context, txid *chainhash.Hash) ([]OutputRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT outpoint, txid, satoshis, spend_txid, score, deps, inputs_consumed, consumed_by, created_at
		FROM outputs WHERE topic_id = $1 AND txid = $2`,
		s.topicID, txid[:],
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOutputs(rows)
}

func (s *PostgresStorage) MarkSpent(ctx context.Context, op *transaction.Outpoint, spendTxid *chainhash.Hash) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE outputs SET spend_txid = $1 WHERE topic_id = $2 AND outpoint = $3`,
		spendTxid[:], s.topicID, op.Bytes(),
	)
	return err
}

func (s *PostgresStorage) UpdateConsumedBy(ctx context.Context, op *transaction.Outpoint, consumedBy []byte) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE outputs SET consumed_by = $1 WHERE topic_id = $2 AND outpoint = $3`,
		consumedBy, s.topicID, op.Bytes(),
	)
	return err
}

func (s *PostgresStorage) FindUTXOs(ctx context.Context, opts *QueryOpts) ([]OutputRecord, error) {
	query := `SELECT outpoint, txid, satoshis, spend_txid, score, deps, inputs_consumed, consumed_by, created_at
		FROM outputs WHERE topic_id = $1 AND spend_txid IS NULL`
	args := []any{s.topicID}
	paramIdx := 2

	if opts != nil && opts.Since > 0 {
		query += fmt.Sprintf(" AND score > $%d", paramIdx)
		args = append(args, opts.Since)
		paramIdx++
	}
	if opts != nil && opts.Reverse {
		query += " ORDER BY score DESC"
	} else {
		query += " ORDER BY score ASC"
	}
	if opts != nil && opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", paramIdx)
		args = append(args, opts.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOutputs(rows)
}

func (s *PostgresStorage) DeleteOutput(ctx context.Context, op *transaction.Outpoint) error {
	opBytes := op.Bytes()
	_, err := s.db.ExecContext(ctx, `DELETE FROM outputs WHERE topic_id = $1 AND outpoint = $2`, s.topicID, opBytes)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM events WHERE topic_id = $1 AND outpoint = $2`, s.topicID, opBytes)
	return err
}

func (s *PostgresStorage) Rollback(ctx context.Context, txid *chainhash.Hash) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM outputs WHERE topic_id = $1 AND txid = $2`, s.topicID, txid[:])
	return err
}

// --- Applied transactions ---

func (s *PostgresStorage) InsertAppliedTx(ctx context.Context, txid *chainhash.Hash, score float64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO applied_txs (topic_id, txid, score) VALUES ($1, $2, $3)
		ON CONFLICT (topic_id, txid) DO NOTHING`,
		s.topicID, txid[:], score,
	)
	return err
}

func (s *PostgresStorage) HasAppliedTx(ctx context.Context, txid *chainhash.Hash) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM applied_txs WHERE topic_id = $1 AND txid = $2)`,
		s.topicID, txid[:],
	).Scan(&exists)
	return exists, err
}

// --- GASP peer sync ---

func (s *PostgresStorage) UpdateLastInteraction(ctx context.Context, host string, since float64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO peer_interactions (topic_id, host, since) VALUES ($1, $2, $3)
		ON CONFLICT (topic_id, host) DO UPDATE SET since = EXCLUDED.since`,
		s.topicID, host, since,
	)
	return err
}

func (s *PostgresStorage) GetLastInteraction(ctx context.Context, host string) (float64, error) {
	var since float64
	err := s.db.QueryRowContext(ctx,
		`SELECT since FROM peer_interactions WHERE topic_id = $1 AND host = $2`,
		s.topicID, host,
	).Scan(&since)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return since, err
}

// --- Lookup service events ---

func (s *PostgresStorage) SaveEvent(ctx context.Context, event string, op *transaction.Outpoint, score float64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO events (topic_id, event, outpoint, score) VALUES ($1, $2, $3, $4)
		ON CONFLICT (topic_id, event, outpoint) DO UPDATE SET score = EXCLUDED.score`,
		s.topicID, event, op.Bytes(), score,
	)
	return err
}

func (s *PostgresStorage) DeleteEvent(ctx context.Context, event string, op *transaction.Outpoint) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM events WHERE topic_id = $1 AND event = $2 AND outpoint = $3`,
		s.topicID, event, op.Bytes(),
	)
	return err
}

func (s *PostgresStorage) FindByEvent(ctx context.Context, event string, opts *QueryOpts) ([]OutputRecord, error) {
	query := `SELECT o.outpoint, o.txid, o.satoshis, o.spend_txid, o.score, o.deps, o.inputs_consumed, o.consumed_by, o.created_at
		FROM events e JOIN outputs o ON e.topic_id = o.topic_id AND e.outpoint = o.outpoint
		WHERE e.topic_id = $1 AND e.event = $2`
	args := []any{s.topicID, event}
	paramIdx := 3

	if opts != nil && opts.Since > 0 {
		query += fmt.Sprintf(" AND e.score > $%d", paramIdx)
		args = append(args, opts.Since)
		paramIdx++
	}
	if opts != nil && opts.Reverse {
		query += " ORDER BY e.score DESC"
	} else {
		query += " ORDER BY e.score ASC"
	}
	if opts != nil && opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", paramIdx)
		args = append(args, opts.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOutputs(rows)
}

