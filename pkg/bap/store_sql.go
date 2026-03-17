package bap

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
)

// SQLStore implements BAPStore using database/sql for SQLite or Postgres.
type SQLStore struct {
	db      *sql.DB
	topicID int
	once    sync.Once
	initErr error
}

// NewSQLStore creates a new SQL-backed BAP store.
// When topicID > 0, all queries are scoped by topic_id (Postgres multi-tenant mode).
// When topicID == 0, topic_id columns are omitted (SQLite single-tenant mode).
func NewSQLStore(db *sql.DB, topicID int) *SQLStore {
	return &SQLStore{
		db:      db,
		topicID: topicID,
	}
}

// qb is a query builder that handles placeholder numbering and topic_id scoping.
type qb struct {
	topicID int
	args    []any
	n       int
}

func (s *SQLStore) newQB() *qb {
	q := &qb{topicID: s.topicID}
	if s.topicID > 0 {
		q.args = append(q.args, s.topicID)
		q.n = 1
	}
	return q
}

// ph returns the next placeholder ($1, $2, ...) and appends the value.
func (q *qb) ph(val any) string {
	q.n++
	q.args = append(q.args, val)
	return fmt.Sprintf("$%d", q.n)
}

// topicWhere returns a "topic_id = $1 AND " prefix for Postgres, or "" for SQLite.
func (q *qb) topicWhere() string {
	if q.topicID > 0 {
		return "topic_id = $1 AND "
	}
	return ""
}

// topicCols returns "topic_id, " for Postgres or "" for SQLite.
func (q *qb) topicCols() string {
	if q.topicID > 0 {
		return "topic_id, "
	}
	return ""
}

// topicVals returns "$1, " for Postgres or "" for SQLite.
func (q *qb) topicVals() string {
	if q.topicID > 0 {
		return "$1, "
	}
	return ""
}

func (s *SQLStore) ensureSchema() error {
	s.once.Do(func() {
		schema := sqliteSchema
		if s.topicID > 0 {
			schema = postgresSchema
		}
		_, s.initErr = s.db.Exec(schema)
	})
	return s.initErr
}

func (s *SQLStore) LoadIdentityById(ctx context.Context, id string) (*Identity, error) {
	if err := s.ensureSchema(); err != nil {
		return nil, fmt.Errorf("failed to ensure schema: %w", err)
	}

	q := s.newQB()
	query := fmt.Sprintf(
		`SELECT bap_id, first_seen, first_seen_txid, root_address, current_address, profile
		FROM bap_identities WHERE %sbap_id = %s`,
		q.topicWhere(), q.ph(id),
	)

	identity := &Identity{}
	var profileJSON string
	err := s.db.QueryRowContext(ctx, query, q.args...).Scan(
		&identity.BapId, &identity.FirstSeen, &identity.FirstSeenTxid,
		&identity.RootAddress, &identity.CurrentAddress, &profileJSON,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load identity %s: %w", id, err)
	}

	if err := json.Unmarshal([]byte(profileJSON), &identity.Profile); err != nil {
		identity.Profile = nil
	}

	addresses, err := s.loadAddresses(ctx, id)
	if err != nil {
		return nil, err
	}
	identity.Addresses = addresses

	return identity, nil
}

func (s *SQLStore) LoadIdentityByAddress(ctx context.Context, address string) (*Identity, error) {
	if err := s.ensureSchema(); err != nil {
		return nil, fmt.Errorf("failed to ensure schema: %w", err)
	}

	q := s.newQB()
	query := fmt.Sprintf(
		`SELECT bap_id FROM bap_identity_addresses WHERE %saddress = %s LIMIT 1`,
		q.topicWhere(), q.ph(address),
	)

	var bapID string
	err := s.db.QueryRowContext(ctx, query, q.args...).Scan(&bapID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to look up address %s: %w", address, err)
	}

	return s.LoadIdentityById(ctx, bapID)
}

func (s *SQLStore) SaveIdentity(ctx context.Context, identity *Identity) error {
	if err := s.ensureSchema(); err != nil {
		return fmt.Errorf("failed to ensure schema: %w", err)
	}

	profileJSON, err := json.Marshal(identity.Profile)
	if err != nil {
		return fmt.Errorf("failed to marshal profile: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	q := s.newQB()
	var conflictTarget, updateSet string
	if s.topicID > 0 {
		conflictTarget = "(topic_id, bap_id)"
		updateSet = `first_seen = excluded.first_seen,
			first_seen_txid = excluded.first_seen_txid,
			root_address = excluded.root_address,
			current_address = excluded.current_address,
			profile = excluded.profile`
	} else {
		conflictTarget = "(bap_id)"
		updateSet = `first_seen = excluded.first_seen,
			first_seen_txid = excluded.first_seen_txid,
			root_address = excluded.root_address,
			current_address = excluded.current_address,
			profile = excluded.profile`
	}

	upsertQuery := fmt.Sprintf(
		`INSERT INTO bap_identities (%sbap_id, first_seen, first_seen_txid, root_address, current_address, profile)
		VALUES (%s%s, %s, %s, %s, %s, %s)
		ON CONFLICT %s DO UPDATE SET %s`,
		q.topicCols(),
		q.topicVals(),
		q.ph(identity.BapId), q.ph(identity.FirstSeen), q.ph(identity.FirstSeenTxid),
		q.ph(identity.RootAddress), q.ph(identity.CurrentAddress), q.ph(string(profileJSON)),
		conflictTarget, updateSet,
	)

	if _, err = tx.ExecContext(ctx, upsertQuery, q.args...); err != nil {
		return fmt.Errorf("failed to upsert identity %s: %w", identity.BapId, err)
	}

	// Delete existing addresses
	dq := s.newQB()
	delQuery := fmt.Sprintf(
		`DELETE FROM bap_identity_addresses WHERE %sbap_id = %s`,
		dq.topicWhere(), dq.ph(identity.BapId),
	)
	if _, err = tx.ExecContext(ctx, delQuery, dq.args...); err != nil {
		return fmt.Errorf("failed to delete addresses for %s: %w", identity.BapId, err)
	}

	// Insert addresses
	for _, addr := range identity.Addresses {
		aq := s.newQB()
		insQuery := fmt.Sprintf(
			`INSERT INTO bap_identity_addresses (%sbap_id, address, signer, txid, block, score) VALUES (%s%s, %s, %s, %s, %s, %s)`,
			aq.topicCols(), aq.topicVals(),
			aq.ph(identity.BapId), aq.ph(addr.Address), aq.ph(addr.Signer), aq.ph(addr.Txid), aq.ph(addr.Block), aq.ph(addr.Score),
		)
		if _, err = tx.ExecContext(ctx, insQuery, aq.args...); err != nil {
			return fmt.Errorf("failed to insert address %s for %s: %w", addr.Address, identity.BapId, err)
		}
	}

	return tx.Commit()
}

func (s *SQLStore) SaveAttestation(ctx context.Context, signer *Signer) error {
	if err := s.ensureSchema(); err != nil {
		return fmt.Errorf("failed to ensure schema: %w", err)
	}

	q := s.newQB()
	var conflictTarget string
	if s.topicID > 0 {
		conflictTarget = "(topic_id, urn_hash, signing_address)"
	} else {
		conflictTarget = "(urn_hash, signing_address)"
	}

	query := fmt.Sprintf(
		`INSERT INTO bap_attestations (%surn_hash, signing_address, sequence, block, score, txid, timestamp, revoked)
		VALUES (%s%s, %s, %s, %s, %s, %s, %s, %s)
		ON CONFLICT %s DO UPDATE SET
			sequence = excluded.sequence,
			block = excluded.block,
			score = excluded.score,
			txid = excluded.txid,
			timestamp = excluded.timestamp,
			revoked = excluded.revoked`,
		q.topicCols(), q.topicVals(),
		q.ph(signer.UrnHash), q.ph(signer.Address), q.ph(signer.Sequence),
		q.ph(signer.Block), q.ph(signer.Score), q.ph(signer.Txid), q.ph(signer.Timestamp), q.ph(signer.Revoked),
		conflictTarget,
	)

	if _, err := s.db.ExecContext(ctx, query, q.args...); err != nil {
		return fmt.Errorf("failed to save attestation %s/%s: %w", signer.UrnHash, signer.Address, err)
	}
	return nil
}

func (s *SQLStore) RevokeAttestation(ctx context.Context, urnHash, signingAddress string) error {
	if err := s.ensureSchema(); err != nil {
		return fmt.Errorf("failed to ensure schema: %w", err)
	}

	q := s.newQB()
	revokedVal := 1
	query := fmt.Sprintf(
		`UPDATE bap_attestations SET revoked = %s WHERE %surn_hash = %s AND signing_address = %s`,
		q.ph(revokedVal), q.topicWhere(), q.ph(urnHash), q.ph(signingAddress),
	)

	if _, err := s.db.ExecContext(ctx, query, q.args...); err != nil {
		return fmt.Errorf("failed to revoke attestation %s/%s: %w", urnHash, signingAddress, err)
	}
	return nil
}

func (s *SQLStore) SaveProfile(ctx context.Context, bapId string, profile map[string]any) error {
	if err := s.ensureSchema(); err != nil {
		return fmt.Errorf("failed to ensure schema: %w", err)
	}

	profileJSON, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("failed to marshal profile: %w", err)
	}

	q := s.newQB()
	query := fmt.Sprintf(
		`UPDATE bap_identities SET profile = %s WHERE %sbap_id = %s`,
		q.ph(string(profileJSON)), q.topicWhere(), q.ph(bapId),
	)

	if _, err = s.db.ExecContext(ctx, query, q.args...); err != nil {
		return fmt.Errorf("failed to save profile for %s: %w", bapId, err)
	}
	return nil
}

func (s *SQLStore) LoadProfiles(ctx context.Context, limit, offset int) ([]Identity, error) {
	if err := s.ensureSchema(); err != nil {
		return nil, fmt.Errorf("failed to ensure schema: %w", err)
	}

	q := s.newQB()
	var where string
	if s.topicID > 0 {
		where = "WHERE topic_id = $1 "
	}
	query := fmt.Sprintf(
		`SELECT bap_id, profile FROM bap_identities %sLIMIT %s OFFSET %s`,
		where, q.ph(limit), q.ph(offset),
	)

	rows, err := s.db.QueryContext(ctx, query, q.args...)
	if err != nil {
		return nil, fmt.Errorf("failed to load profiles: %w", err)
	}
	defer rows.Close()

	var identities []Identity
	for rows.Next() {
		var id Identity
		var profileJSON string
		if err := rows.Scan(&id.BapId, &profileJSON); err != nil {
			return nil, fmt.Errorf("failed to scan profile row: %w", err)
		}
		if err := json.Unmarshal([]byte(profileJSON), &id.Profile); err != nil {
			id.Profile = nil
		}
		identities = append(identities, id)
	}
	return identities, rows.Err()
}

func (s *SQLStore) Search(ctx context.Context, query string, limit, offset int) ([]Identity, error) {
	if err := s.ensureSchema(); err != nil {
		return nil, fmt.Errorf("failed to ensure schema: %w", err)
	}

	pattern := "%" + query + "%"

	var likeOp, profileCast string
	if s.topicID > 0 {
		likeOp = "ILIKE"
		profileCast = "i.profile::text"
	} else {
		likeOp = "LIKE"
		profileCast = "i.profile"
	}

	q := s.newQB()
	var topicFilter string
	if s.topicID > 0 {
		topicFilter = "i.topic_id = $1 AND "
	}

	phs := make([]string, 5)
	for i := range phs {
		phs[i] = q.ph(pattern)
	}

	sqlQuery := fmt.Sprintf(
		`SELECT DISTINCT i.bap_id, i.first_seen, i.first_seen_txid, i.root_address, i.current_address, i.profile
		FROM bap_identities i
		LEFT JOIN bap_identity_addresses a ON i.bap_id = a.bap_id%s
		WHERE %s(i.bap_id %s %s
			OR i.root_address %s %s
			OR i.current_address %s %s
			OR %s %s %s
			OR a.address %s %s)
		LIMIT %s OFFSET %s`,
		s.topicJoinSuffix(),
		topicFilter,
		likeOp, phs[0],
		likeOp, phs[1],
		likeOp, phs[2],
		profileCast, likeOp, phs[3],
		likeOp, phs[4],
		q.ph(limit), q.ph(offset),
	)

	rows, err := s.db.QueryContext(ctx, sqlQuery, q.args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search identities: %w", err)
	}
	defer rows.Close()

	var identities []Identity
	for rows.Next() {
		var id Identity
		var profileJSON string
		if err := rows.Scan(&id.BapId, &id.FirstSeen, &id.FirstSeenTxid,
			&id.RootAddress, &id.CurrentAddress, &profileJSON); err != nil {
			return nil, fmt.Errorf("failed to scan search result: %w", err)
		}
		if err := json.Unmarshal([]byte(profileJSON), &id.Profile); err != nil {
			id.Profile = nil
		}
		identities = append(identities, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range identities {
		addresses, err := s.loadAddresses(ctx, identities[i].BapId)
		if err != nil {
			return nil, err
		}
		identities[i].Addresses = addresses
	}

	return identities, nil
}

func (s *SQLStore) UpdateBlockHeightByTxid(ctx context.Context, txid string, blockHeight uint32) error {
	if err := s.ensureSchema(); err != nil {
		return fmt.Errorf("failed to ensure schema: %w", err)
	}

	q := s.newQB()
	addrQuery := fmt.Sprintf(
		`UPDATE bap_identity_addresses SET block = %s WHERE %stxid = %s`,
		q.ph(blockHeight), q.topicWhere(), q.ph(txid),
	)
	if _, err := s.db.ExecContext(ctx, addrQuery, q.args...); err != nil {
		return fmt.Errorf("failed to update address block height for %s: %w", txid, err)
	}

	q2 := s.newQB()
	attestQuery := fmt.Sprintf(
		`UPDATE bap_attestations SET block = %s WHERE %stxid = %s`,
		q2.ph(blockHeight), q2.topicWhere(), q2.ph(txid),
	)
	if _, err := s.db.ExecContext(ctx, attestQuery, q2.args...); err != nil {
		return fmt.Errorf("failed to update attestation block height for %s: %w", txid, err)
	}

	return nil
}

// topicJoinSuffix returns the extra join condition for Postgres topic scoping.
func (s *SQLStore) topicJoinSuffix() string {
	if s.topicID > 0 {
		return " AND i.topic_id = a.topic_id"
	}
	return ""
}

func (s *SQLStore) loadAddresses(ctx context.Context, bapID string) ([]Address, error) {
	q := s.newQB()
	query := fmt.Sprintf(
		`SELECT address, signer, txid, block, score FROM bap_identity_addresses WHERE %sbap_id = %s ORDER BY score ASC`,
		q.topicWhere(), q.ph(bapID),
	)

	rows, err := s.db.QueryContext(ctx, query, q.args...)
	if err != nil {
		return nil, fmt.Errorf("failed to load addresses for %s: %w", bapID, err)
	}
	defer rows.Close()

	var addresses []Address
	for rows.Next() {
		var addr Address
		if err := rows.Scan(&addr.Address, &addr.Signer, &addr.Txid, &addr.Block, &addr.Score); err != nil {
			return nil, fmt.Errorf("failed to scan address row: %w", err)
		}
		addresses = append(addresses, addr)
	}
	return addresses, rows.Err()
}
