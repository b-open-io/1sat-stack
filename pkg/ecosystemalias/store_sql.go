package ecosystemalias

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"sync"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

const zeroBlockIndex = "00000000000000000000"

// SQLStore implements ClaimStore using database/sql for SQLite or PostgreSQL.
// A positive topicID selects the shared PostgreSQL schema and scopes every
// statement to that topic. Zero selects the single-topic SQLite schema.
type SQLStore struct {
	db      *sql.DB
	topicID int
	once    sync.Once
	initErr error
}

// NewSQLStore creates a claim store over db. The caller retains ownership of
// the database handle.
func NewSQLStore(db *sql.DB, topicID int) *SQLStore {
	return &SQLStore{db: db, topicID: topicID}
}

type claimQB struct {
	topicID int
	args    []any
	n       int
}

func (s *SQLStore) newQB() *claimQB {
	q := &claimQB{topicID: s.topicID}
	if s.topicID > 0 {
		q.args = append(q.args, s.topicID)
		q.n = 1
	}
	return q
}

func (q *claimQB) ph(value any) string {
	q.n++
	q.args = append(q.args, value)
	return fmt.Sprintf("$%d", q.n)
}

func (q *claimQB) topicWhere() string {
	if q.topicID > 0 {
		return "topic_id = $1 AND "
	}
	return ""
}

func (q *claimQB) topicCols() string {
	if q.topicID > 0 {
		return "topic_id, "
	}
	return ""
}

func (q *claimQB) topicVals() string {
	if q.topicID > 0 {
		return "$1, "
	}
	return ""
}

func (s *SQLStore) ensureSchema() error {
	s.once.Do(func() {
		schema := sqliteClaimSchema
		if s.topicID > 0 {
			schema = postgresClaimSchema
		}
		_, s.initErr = s.db.Exec(schema)
	})
	return s.initErr
}

// UpsertClaim inserts a claim or refreshes its decoded and placement fields.
// spending_txid is deliberately absent from the conflict update: replaying an
// already-spent output must not make it queryable again.
func (s *SQLStore) UpsertClaim(ctx context.Context, claim *StoredClaim) error {
	if claim == nil {
		return fmt.Errorf("claim must not be nil")
	}
	if err := ValidateTokenAlias(claim.Alias); err != nil {
		return err
	}
	if err := ValidateTokenDomain(claim.Domain); err != nil {
		return err
	}
	if err := s.ensureSchema(); err != nil {
		return fmt.Errorf("failed to ensure ecosystem-alias schema: %w", err)
	}

	q := s.newQB()
	conflictTarget := "(txid, vout)"
	if s.topicID > 0 {
		conflictTarget = "(topic_id, txid, vout)"
	}
	var spendingTxID any
	if claim.SpendingTxID != nil {
		spendingTxID = claim.SpendingTxID.String()
	}
	query := fmt.Sprintf(
		`INSERT INTO ecosystem_alias_claims
		(%stxid, vout, alias, domain, confirmed, block_height, block_index, spending_txid)
		VALUES (%s%s, %s, %s, %s, %s, %s, %s, %s)
		ON CONFLICT %s DO UPDATE SET
			alias = excluded.alias,
			domain = excluded.domain,
			confirmed = excluded.confirmed,
			block_height = excluded.block_height,
			block_index = excluded.block_index`,
		q.topicCols(), q.topicVals(),
		q.ph(claim.Outpoint.Txid.String()), q.ph(claim.Outpoint.Index),
		q.ph(claim.Alias), q.ph(claim.Domain), q.ph(claim.Confirmed),
		q.ph(claim.BlockHeight), q.ph(formatBlockIndex(claim.BlockIndex)),
		q.ph(spendingTxID), conflictTarget,
	)
	if _, err := s.db.ExecContext(ctx, query, q.args...); err != nil {
		return fmt.Errorf("failed to upsert ecosystem-alias claim %s: %w", claim.Outpoint.String(), err)
	}
	return nil
}

// QueryClaims returns unspent claims in the contract's deterministic order.
func (s *SQLStore) QueryClaims(ctx context.Context, query Query, cursor *Cursor, limit int) ([]StoredClaim, error) {
	if err := s.ensureSchema(); err != nil {
		return nil, fmt.Errorf("failed to ensure ecosystem-alias schema: %w", err)
	}
	mode := query.Mode()
	if mode == ModeNone {
		return nil, fail(CodeInvalidCombination, "query must have exactly one of alias, domain, or findAll:true")
	}
	if limit <= 0 || limit > int(MaxLimit) {
		return nil, fmt.Errorf("claim query limit must be between 1 and %d", MaxLimit)
	}

	if cursor == nil && query.Cursor != nil {
		bound, err := BindCursor(*query.Cursor, query)
		if err != nil {
			return nil, err
		}
		cursor = &bound
	}
	if cursor != nil {
		bound, err := validateClaimCursor(*cursor, query)
		if err != nil {
			return nil, err
		}
		cursor = &bound
	}

	q := s.newQB()
	statement := `SELECT txid, vout, alias, domain, confirmed, block_height, block_index, spending_txid
		FROM ecosystem_alias_claims WHERE ` + q.topicWhere() + `spending_txid IS NULL`

	switch mode {
	case ModeAlias:
		alias, err := NormalizeAliasQuery(*query.Alias)
		if err != nil {
			return nil, err
		}
		statement += " AND alias = " + q.ph(alias)
	case ModeDomain:
		domain, err := NormalizeDomainQuery(*query.Domain)
		if err != nil {
			return nil, err
		}
		statement += " AND domain = " + q.ph(domain)
	case ModeFindAll:
	}

	if cursor != nil {
		placement, err := s.placementForCursor(ctx, *cursor)
		if err != nil {
			return nil, err
		}
		if placement == nil {
			return nil, fail(CodeMalformedCursor, "cursor outpoint is not present")
		}
		if mode == ModeFindAll {
			statement += outpointAfter(q, placement.Txid, placement.Vout)
		} else {
			statement += lookupAfter(q, *placement)
		}
	}

	if mode == ModeFindAll {
		statement += " ORDER BY txid ASC, vout ASC"
	} else {
		statement += ` ORDER BY confirmed DESC,
			CASE WHEN confirmed THEN block_height ELSE 0 END ASC,
			CASE WHEN confirmed THEN block_index ELSE '` + zeroBlockIndex + `' END ASC,
			txid ASC, vout ASC`
	}
	statement += " LIMIT " + q.ph(limit)

	rows, err := s.db.QueryContext(ctx, statement, q.args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query ecosystem-alias claims: %w", err)
	}
	defer rows.Close()

	claims := make([]StoredClaim, 0)
	for rows.Next() {
		claim, err := scanStoredClaim(rows)
		if err != nil {
			return nil, err
		}
		claims = append(claims, claim)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed while reading ecosystem-alias claims: %w", err)
	}
	return claims, nil
}

func validateClaimCursor(cursor Cursor, query Query) (Cursor, error) {
	if cursor.Encoded != "" {
		return BindCursor(cursor.Encoded, query)
	}
	fingerprint, err := QueryFingerprint(query)
	if err != nil {
		return Cursor{}, err
	}
	if cursor.Mode != query.Mode() || cursor.Fingerprint != fingerprint {
		return Cursor{}, fail(CodeCursorMismatch, "cursor belongs to a different query")
	}
	if _, err := normalizeTxid(cursor.Txid); err != nil {
		return Cursor{}, fail(CodeMalformedCursor, "cursor txid must be 64 lowercase hex characters")
	}
	return cursor, nil
}

func (s *SQLStore) placementForCursor(ctx context.Context, cursor Cursor) (*Placement, error) {
	txid, err := hashFromCanonicalText(cursor.Txid)
	if err != nil {
		return nil, fail(CodeMalformedCursor, "cursor txid must be 64 lowercase hex characters")
	}
	return s.PlacementForOutpoint(ctx, &transaction.Outpoint{Txid: *txid, Index: cursor.Vout})
}

func outpointAfter(q *claimQB, txid string, vout uint32) string {
	return fmt.Sprintf(" AND (txid > %s OR (txid = %s AND vout > %s))",
		q.ph(txid), q.ph(txid), q.ph(vout))
}

func lookupAfter(q *claimQB, placement Placement) string {
	if !placement.Confirmed {
		return fmt.Sprintf(" AND confirmed = %s%s",
			q.ph(false), outpointAfter(q, placement.Txid, placement.Vout))
	}
	return fmt.Sprintf(` AND (
		confirmed = %s OR
		(confirmed = %s AND (
			block_height > %s OR
			(block_height = %s AND block_index > %s) OR
			(block_height = %s AND block_index = %s AND txid > %s) OR
			(block_height = %s AND block_index = %s AND txid = %s AND vout > %s)
		)))`,
		q.ph(false), q.ph(true),
		q.ph(placement.BlockHeight),
		q.ph(placement.BlockHeight), q.ph(formatBlockIndex(placement.BlockIndex)),
		q.ph(placement.BlockHeight), q.ph(formatBlockIndex(placement.BlockIndex)), q.ph(placement.Txid),
		q.ph(placement.BlockHeight), q.ph(formatBlockIndex(placement.BlockIndex)), q.ph(placement.Txid), q.ph(placement.Vout),
	)
}

// PlacementForOutpoint resolves the stored ordering key for an outpoint. It
// returns nil, nil when the outpoint has never been stored or was evicted.
func (s *SQLStore) PlacementForOutpoint(ctx context.Context, outpoint *transaction.Outpoint) (*Placement, error) {
	if outpoint == nil {
		return nil, fmt.Errorf("outpoint must not be nil")
	}
	if err := s.ensureSchema(); err != nil {
		return nil, fmt.Errorf("failed to ensure ecosystem-alias schema: %w", err)
	}
	q := s.newQB()
	statement := fmt.Sprintf(
		`SELECT confirmed, block_height, block_index, txid, vout
		FROM ecosystem_alias_claims WHERE %stxid = %s AND vout = %s`,
		q.topicWhere(), q.ph(outpoint.Txid.String()), q.ph(outpoint.Index),
	)
	var (
		placement  Placement
		blockIndex any
	)
	err := s.db.QueryRowContext(ctx, statement, q.args...).Scan(
		&placement.Confirmed, &placement.BlockHeight, &blockIndex,
		&placement.Txid, &placement.Vout,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to resolve ecosystem-alias placement %s: %w", outpoint.String(), err)
	}
	placement.BlockIndex, err = parseBlockIndex(blockIndex)
	if err != nil {
		return nil, fmt.Errorf("failed to decode ecosystem-alias block index: %w", err)
	}
	return &placement, nil
}

// MarkSpent excludes an outpoint from all subsequent claim queries.
func (s *SQLStore) MarkSpent(ctx context.Context, outpoint *transaction.Outpoint, spendingTxID *chainhash.Hash) error {
	if outpoint == nil || spendingTxID == nil {
		return fmt.Errorf("outpoint and spending transaction ID must not be nil")
	}
	if err := s.ensureSchema(); err != nil {
		return fmt.Errorf("failed to ensure ecosystem-alias schema: %w", err)
	}
	q := s.newQB()
	statement := fmt.Sprintf(
		`UPDATE ecosystem_alias_claims SET spending_txid = %s
		WHERE %stxid = %s AND vout = %s`,
		q.ph(spendingTxID.String()), q.topicWhere(),
		q.ph(outpoint.Txid.String()), q.ph(outpoint.Index),
	)
	if _, err := s.db.ExecContext(ctx, statement, q.args...); err != nil {
		return fmt.Errorf("failed to mark ecosystem-alias claim %s spent: %w", outpoint.String(), err)
	}
	return nil
}

// UpdatePlacementByTxid applies an explicit confirmation state and ordering
// coordinates to every claim output created by txid.
func (s *SQLStore) UpdatePlacementByTxid(ctx context.Context, txid *chainhash.Hash, confirmed bool, height uint32, blockIndex uint64) error {
	if txid == nil {
		return fmt.Errorf("transaction ID must not be nil")
	}
	if err := s.ensureSchema(); err != nil {
		return fmt.Errorf("failed to ensure ecosystem-alias schema: %w", err)
	}
	q := s.newQB()
	statement := fmt.Sprintf(
		`UPDATE ecosystem_alias_claims
		SET confirmed = %s, block_height = %s, block_index = %s
		WHERE %stxid = %s`,
		q.ph(confirmed), q.ph(height), q.ph(formatBlockIndex(blockIndex)),
		q.topicWhere(), q.ph(txid.String()),
	)
	if _, err := s.db.ExecContext(ctx, statement, q.args...); err != nil {
		return fmt.Errorf("failed to update ecosystem-alias placement for %s: %w", txid.String(), err)
	}
	return nil
}

// DeleteOutpoint permanently evicts a claim output.
func (s *SQLStore) DeleteOutpoint(ctx context.Context, outpoint *transaction.Outpoint) error {
	if outpoint == nil {
		return fmt.Errorf("outpoint must not be nil")
	}
	if err := s.ensureSchema(); err != nil {
		return fmt.Errorf("failed to ensure ecosystem-alias schema: %w", err)
	}
	q := s.newQB()
	statement := fmt.Sprintf(
		`DELETE FROM ecosystem_alias_claims WHERE %stxid = %s AND vout = %s`,
		q.topicWhere(), q.ph(outpoint.Txid.String()), q.ph(outpoint.Index),
	)
	if _, err := s.db.ExecContext(ctx, statement, q.args...); err != nil {
		return fmt.Errorf("failed to delete ecosystem-alias claim %s: %w", outpoint.String(), err)
	}
	return nil
}

// RollbackTransaction removes claims created by txid and makes claims spent by
// txid queryable again. Both changes are committed atomically.
func (s *SQLStore) RollbackTransaction(ctx context.Context, txid *chainhash.Hash) error {
	if txid == nil {
		return fmt.Errorf("transaction ID must not be nil")
	}
	if err := s.ensureSchema(); err != nil {
		return fmt.Errorf("failed to ensure ecosystem-alias schema: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin ecosystem-alias rollback: %w", err)
	}
	defer tx.Rollback()

	deleteQ := s.newQB()
	deleteStatement := fmt.Sprintf(
		`DELETE FROM ecosystem_alias_claims WHERE %stxid = %s`,
		deleteQ.topicWhere(), deleteQ.ph(txid.String()),
	)
	if _, err := tx.ExecContext(ctx, deleteStatement, deleteQ.args...); err != nil {
		return fmt.Errorf("failed to remove ecosystem-alias claims created by %s: %w", txid.String(), err)
	}

	restoreQ := s.newQB()
	restoreStatement := fmt.Sprintf(
		`UPDATE ecosystem_alias_claims SET spending_txid = NULL WHERE %sspending_txid = %s`,
		restoreQ.topicWhere(), restoreQ.ph(txid.String()),
	)
	if _, err := tx.ExecContext(ctx, restoreStatement, restoreQ.args...); err != nil {
		return fmt.Errorf("failed to restore ecosystem-alias claims spent by %s: %w", txid.String(), err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit ecosystem-alias rollback: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanStoredClaim(scanner rowScanner) (StoredClaim, error) {
	var (
		claim      StoredClaim
		txid       string
		blockIndex any
		spending   sql.NullString
	)
	if err := scanner.Scan(
		&txid, &claim.Outpoint.Index, &claim.Alias, &claim.Domain,
		&claim.Confirmed, &claim.BlockHeight, &blockIndex, &spending,
	); err != nil {
		return StoredClaim{}, fmt.Errorf("failed to scan ecosystem-alias claim: %w", err)
	}
	hash, err := hashFromCanonicalText(txid)
	if err != nil {
		return StoredClaim{}, fmt.Errorf("failed to decode ecosystem-alias txid: %w", err)
	}
	claim.Outpoint.Txid = *hash
	claim.BlockIndex, err = parseBlockIndex(blockIndex)
	if err != nil {
		return StoredClaim{}, fmt.Errorf("failed to decode ecosystem-alias block index: %w", err)
	}
	if spending.Valid {
		spendingHash, err := hashFromCanonicalText(spending.String)
		if err != nil {
			return StoredClaim{}, fmt.Errorf("failed to decode ecosystem-alias spender: %w", err)
		}
		claim.SpendingTxID = spendingHash
	}
	return claim, nil
}

func hashFromCanonicalText(value string) (*chainhash.Hash, error) {
	if _, err := normalizeTxid(value); err != nil {
		return nil, err
	}
	hash, err := chainhash.NewHashFromHex(value)
	if err != nil {
		return nil, err
	}
	if hash.String() != value {
		return nil, fmt.Errorf("transaction ID is not canonical lowercase display hex")
	}
	return hash, nil
}

func formatBlockIndex(value uint64) string {
	return fmt.Sprintf("%020d", value)
}

func parseBlockIndex(value any) (uint64, error) {
	var text string
	switch v := value.(type) {
	case string:
		text = v
	case []byte:
		text = string(v)
	case int64:
		if v < 0 {
			return 0, fmt.Errorf("negative value %d", v)
		}
		return uint64(v), nil
	case float64:
		if v < 0 || v != float64(uint64(v)) {
			return 0, fmt.Errorf("invalid numeric value %v", v)
		}
		return uint64(v), nil
	default:
		return 0, fmt.Errorf("unsupported value type %T", value)
	}
	parsed, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return 0, err
	}
	return parsed, nil
}

var _ ClaimStore = (*SQLStore)(nil)
