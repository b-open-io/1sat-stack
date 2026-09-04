package storage

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"sort"
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

var _ ReversibleTopicStorage = (*PostgresStorage)(nil)

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

func (s *PostgresStorage) BeginMutation(ctx context.Context, txid *chainhash.Hash, directInputs []*transaction.Outpoint) error {
	if txid == nil {
		return fmt.Errorf("begin mutation: transaction ID is nil")
	}
	tx, err := s.beginLifecycleTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO overlay_mutations (topic_id, txid, phase, created_at)
		VALUES ($1, $2, 'active', $3) ON CONFLICT DO NOTHING`, s.topicID, txid[:], time.Now().Unix()); err != nil {
		return err
	}
	var phase string
	if err := tx.QueryRowContext(ctx,
		`SELECT phase FROM overlay_mutations WHERE topic_id = $1 AND txid = $2`, s.topicID, txid[:],
	).Scan(&phase); err != nil {
		return err
	}
	if MutationPhase(phase) != MutationPhaseActive {
		return fmt.Errorf("begin mutation: transaction %s is %s", txid, phase)
	}
	if err := s.postgresSnapshotAncestry(ctx, tx, txid, directInputs); err != nil {
		return err
	}
	for _, op := range directInputs {
		if op == nil {
			return fmt.Errorf("begin mutation: direct input is nil")
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE outputs SET spend_txid = $1
			WHERE topic_id = $2 AND outpoint = $3 AND (spend_txid IS NULL OR spend_txid = $1)`,
			txid[:], s.topicID, op.Bytes())
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

func (s *PostgresStorage) beginLifecycleTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(s.topicID)); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (s *PostgresStorage) postgresSnapshotAncestry(ctx context.Context, tx *sql.Tx, mutationTxid *chainhash.Hash, directInputs []*transaction.Outpoint) error {
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
		err := tx.QueryRowContext(ctx, `
			SELECT inputs_consumed FROM outputs
			WHERE topic_id = $1 AND outpoint = $2 FOR UPDATE`, s.topicID, op.Bytes()).Scan(&inputsConsumed)
		if err == sql.ErrNoRows {
			if _, isDirect := direct[key]; isDirect {
				return fmt.Errorf("begin mutation: direct input %s was not found", op)
			}
			continue
		}
		if err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mutation_reservations (topic_id, outpoint, mutation_txid)
			VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, s.topicID, op.Bytes(), mutationTxid[:]); err != nil {
			return err
		}
		var owner []byte
		if err := tx.QueryRowContext(ctx, `
			SELECT mutation_txid FROM mutation_reservations
			WHERE topic_id = $1 AND outpoint = $2`, s.topicID, op.Bytes()).Scan(&owner); err != nil {
			return err
		}
		if !bytes.Equal(owner, mutationTxid[:]) {
			return fmt.Errorf("begin mutation: output %s is reserved by another transaction", op)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mutation_outputs
				(topic_id, mutation_txid, outpoint, txid, satoshis, spend_txid, score, deps, inputs_consumed, consumed_by, created_at)
			SELECT topic_id, $1, outpoint, txid, satoshis, spend_txid, score, deps, inputs_consumed, consumed_by, created_at
			FROM outputs WHERE topic_id = $2 AND outpoint = $3
			ON CONFLICT DO NOTHING`, mutationTxid[:], s.topicID, op.Bytes()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mutation_events (topic_id, mutation_txid, event, outpoint, score)
			SELECT topic_id, $1, event, outpoint, score FROM events
			WHERE topic_id = $2 AND outpoint = $3 ON CONFLICT DO NOTHING`, mutationTxid[:], s.topicID, op.Bytes()); err != nil {
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

func (s *PostgresStorage) CommitMutation(ctx context.Context, txid *chainhash.Hash, score float64) error {
	if txid == nil {
		return fmt.Errorf("commit mutation: transaction ID is nil")
	}
	tx, err := s.beginLifecycleTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var phase string
	if err := tx.QueryRowContext(ctx,
		`SELECT phase FROM overlay_mutations WHERE topic_id = $1 AND txid = $2`, s.topicID, txid[:],
	).Scan(&phase); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("commit mutation: transaction %s has no active guard", txid)
		}
		return err
	}
	if MutationPhase(phase) == MutationPhaseRollbackPending {
		return fmt.Errorf("commit mutation: transaction %s is pending rollback finalization", txid)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO applied_txs (topic_id, txid, score) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, s.topicID, txid[:], score); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE overlay_mutations SET phase = 'applied' WHERE topic_id = $1 AND txid = $2`, s.topicID, txid[:]); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM mutation_reservations WHERE topic_id = $1 AND mutation_txid = $2`, s.topicID, txid[:]); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStorage) GetMutation(ctx context.Context, txid *chainhash.Hash) (*MutationRecord, error) {
	if txid == nil {
		return nil, fmt.Errorf("get mutation: transaction ID is nil")
	}
	var txidBytes []byte
	var phase string
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT txid, phase, created_at FROM overlay_mutations
		WHERE topic_id = $1 AND txid = $2`, s.topicID, txid[:],
	).Scan(&txidBytes, &phase, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return newMutationRecord(txidBytes, phase, createdAt)
}

func (s *PostgresStorage) ListMutations(ctx context.Context) ([]MutationRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT txid, phase, created_at FROM overlay_mutations
		WHERE topic_id = $1 ORDER BY created_at, txid`, s.topicID)
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

func (s *PostgresStorage) HasMutationGuard(ctx context.Context, txid *chainhash.Hash) (bool, error) {
	record, err := s.GetMutation(ctx, txid)
	return record != nil, err
}

func (s *PostgresStorage) RollbackMutation(ctx context.Context, txid *chainhash.Hash) (*RollbackResult, error) {
	if txid == nil {
		return nil, fmt.Errorf("rollback mutation: transaction ID is nil")
	}
	tx, err := s.beginLifecycleTx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var phase string
	if err := tx.QueryRowContext(ctx, `
		SELECT phase FROM overlay_mutations WHERE topic_id = $1 AND txid = $2`, s.topicID, txid[:],
	).Scan(&phase); err != nil {
		if err == sql.ErrNoRows {
			var applied bool
			if err := tx.QueryRowContext(ctx, `
				SELECT EXISTS(SELECT 1 FROM applied_txs WHERE topic_id = $1 AND txid = $2)`, s.topicID, txid[:],
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
		if err := s.postgresRejectLiveDescendants(ctx, tx, txid); err != nil {
			return nil, fmt.Errorf("rollback mutation: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mutation_evictions (topic_id, mutation_txid, outpoint)
			SELECT topic_id, $1, outpoint FROM outputs WHERE topic_id = $2 AND txid = $1
			ON CONFLICT DO NOTHING`, txid[:], s.topicID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM events WHERE topic_id = $1 AND outpoint IN
				(SELECT outpoint FROM outputs WHERE topic_id = $1 AND txid = $2)`, s.topicID, txid[:]); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM outputs WHERE topic_id = $1 AND txid = $2`, s.topicID, txid[:]); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM events WHERE topic_id = $1 AND outpoint IN
				(SELECT outpoint FROM mutation_outputs WHERE topic_id = $1 AND mutation_txid = $2)`, s.topicID, txid[:]); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO outputs
				(topic_id, outpoint, txid, satoshis, spend_txid, score, deps, inputs_consumed, consumed_by, created_at)
			SELECT topic_id, outpoint, txid, satoshis, spend_txid, score, deps, inputs_consumed, consumed_by, created_at
			FROM mutation_outputs WHERE topic_id = $1 AND mutation_txid = $2
			ON CONFLICT (topic_id, outpoint) DO UPDATE SET
				txid = EXCLUDED.txid, satoshis = EXCLUDED.satoshis, spend_txid = EXCLUDED.spend_txid,
				score = EXCLUDED.score, deps = EXCLUDED.deps, inputs_consumed = EXCLUDED.inputs_consumed,
				consumed_by = EXCLUDED.consumed_by, created_at = EXCLUDED.created_at`, s.topicID, txid[:]); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO events (topic_id, event, outpoint, score)
			SELECT topic_id, event, outpoint, score FROM mutation_events
			WHERE topic_id = $1 AND mutation_txid = $2
			ON CONFLICT (topic_id, event, outpoint) DO UPDATE SET score = EXCLUDED.score`, s.topicID, txid[:]); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM mutation_reservations WHERE topic_id = $1 AND mutation_txid = $2`, s.topicID, txid[:]); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE overlay_mutations SET phase = 'rollback_pending' WHERE topic_id = $1 AND txid = $2`, s.topicID, txid[:]); err != nil {
			return nil, err
		}
	}
	result, err := s.postgresMutationResult(ctx, tx, txid)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *PostgresStorage) postgresRejectLiveDescendants(ctx context.Context, tx *sql.Tx, txid *chainhash.Hash) error {
	var conflictingOutpoint []byte
	err := tx.QueryRowContext(ctx, `
		SELECT r.outpoint FROM mutation_reservations r
		WHERE r.topic_id = $1 AND r.mutation_txid != $2 AND (
			r.outpoint IN (SELECT outpoint FROM outputs WHERE topic_id = $1 AND txid = $2)
			OR r.outpoint IN (SELECT outpoint FROM mutation_outputs WHERE topic_id = $1 AND mutation_txid = $2)
		) LIMIT 1`, s.topicID, txid[:]).Scan(&conflictingOutpoint)
	if err == nil {
		return fmt.Errorf("output %x is reserved by an active descendant", conflictingOutpoint)
	}
	if err != sql.ErrNoRows {
		return err
	}

	var descendantTxid []byte
	err = tx.QueryRowContext(ctx, `
		SELECT m.txid FROM overlay_mutations m
		JOIN mutation_outputs o ON o.topic_id = m.topic_id AND o.mutation_txid = m.txid
		WHERE m.topic_id = $1 AND m.txid != $2
			AND m.phase IN ('active', 'applied') AND o.txid = $2
		ORDER BY m.created_at, m.txid LIMIT 1`, s.topicID, txid[:]).Scan(&descendantTxid)
	if err == nil {
		return fmt.Errorf("live descendant %x must be rolled back first", descendantTxid)
	}
	if err != sql.ErrNoRows {
		return err
	}
	return nil
}

func (s *PostgresStorage) postgresMutationResult(ctx context.Context, tx *sql.Tx, txid *chainhash.Hash) (*RollbackResult, error) {
	result := &RollbackResult{}
	rows, err := tx.QueryContext(ctx, `
		SELECT outpoint FROM mutation_evictions
		WHERE topic_id = $1 AND mutation_txid = $2 ORDER BY outpoint`, s.topicID, txid[:])
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
		FROM mutation_outputs WHERE topic_id = $1 AND mutation_txid = $2 ORDER BY outpoint`, s.topicID, txid[:])
	if err != nil {
		return nil, err
	}
	result.Restored, err = scanOutputs(rows)
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	return result, err
}

func (s *PostgresStorage) FinalizeRollback(ctx context.Context, txid *chainhash.Hash) error {
	if txid == nil {
		return fmt.Errorf("finalize rollback: transaction ID is nil")
	}
	tx, err := s.beginLifecycleTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var phase string
	err = tx.QueryRowContext(ctx, `
		SELECT phase FROM overlay_mutations WHERE topic_id = $1 AND txid = $2`, s.topicID, txid[:],
	).Scan(&phase)
	if err == sql.ErrNoRows {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if MutationPhase(phase) != MutationPhaseRollbackPending {
		return fmt.Errorf("finalize rollback: transaction %s is %s", txid, phase)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM applied_txs WHERE topic_id = $1 AND txid = $2`, s.topicID, txid[:],
	); err != nil {
		return err
	}
	if err := s.postgresDeleteMutationJournal(ctx, tx, txid); err != nil {
		return err
	}
	return tx.Commit()
}

// PruneMutation discards rollback material for an applied mutation while
// preserving its applied transaction marker. The caller owns finality policy.
func (s *PostgresStorage) PruneMutation(ctx context.Context, txid *chainhash.Hash) error {
	if txid == nil {
		return fmt.Errorf("prune mutation: transaction ID is nil")
	}
	tx, err := s.beginLifecycleTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var phase string
	err = tx.QueryRowContext(ctx, `
		SELECT phase FROM overlay_mutations WHERE topic_id = $1 AND txid = $2`, s.topicID, txid[:],
	).Scan(&phase)
	if err == sql.ErrNoRows {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if MutationPhase(phase) != MutationPhaseApplied {
		return fmt.Errorf("prune mutation: transaction %s is %s", txid, phase)
	}
	if err := s.postgresRejectLiveAncestors(ctx, tx, txid); err != nil {
		return fmt.Errorf("prune mutation: %w", err)
	}
	if err := s.postgresDeleteMutationJournal(ctx, tx, txid); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStorage) postgresRejectLiveAncestors(ctx context.Context, tx *sql.Tx, txid *chainhash.Hash) error {
	var ancestorTxid []byte
	err := tx.QueryRowContext(ctx, `
		SELECT parent.txid FROM overlay_mutations parent
		JOIN mutation_outputs child ON child.topic_id = parent.topic_id AND child.txid = parent.txid
		WHERE child.topic_id = $1 AND child.mutation_txid = $2 AND parent.txid != $2
		ORDER BY parent.created_at, parent.txid LIMIT 1`, s.topicID, txid[:]).Scan(&ancestorTxid)
	if err == nil {
		return fmt.Errorf("reversible ancestor %x must be pruned first", ancestorTxid)
	}
	if err != sql.ErrNoRows {
		return err
	}
	return nil
}

func (s *PostgresStorage) postgresDeleteMutationJournal(ctx context.Context, tx *sql.Tx, txid *chainhash.Hash) error {
	queries := []string{
		`DELETE FROM mutation_events WHERE topic_id = $1 AND mutation_txid = $2`,
		`DELETE FROM mutation_outputs WHERE topic_id = $1 AND mutation_txid = $2`,
		`DELETE FROM mutation_evictions WHERE topic_id = $1 AND mutation_txid = $2`,
		`DELETE FROM mutation_reservations WHERE topic_id = $1 AND mutation_txid = $2`,
		`DELETE FROM overlay_mutations WHERE topic_id = $1 AND txid = $2`,
	}
	for _, query := range queries {
		if _, err := tx.ExecContext(ctx, query, s.topicID, txid[:]); err != nil {
			return err
		}
	}
	return nil
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
