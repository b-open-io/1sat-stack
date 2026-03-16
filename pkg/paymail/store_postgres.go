package paymail

import (
	"context"
	"database/sql"
	"fmt"
)

const pgCreateTableSQL = `
CREATE TABLE IF NOT EXISTS pending_payments (
	reference         TEXT PRIMARY KEY,
	alias             TEXT NOT NULL,
	domain            TEXT NOT NULL,
	identity_pub_key  TEXT NOT NULL,
	derivation_prefix TEXT NOT NULL,
	derivation_suffix TEXT NOT NULL,
	satoshis          BIGINT NOT NULL,
	output_script     TEXT NOT NULL,
	txid              BYTEA,
	created_at        TIMESTAMPTZ NOT NULL,
	expires_at        TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pending_expires ON pending_payments(expires_at);
`

// PostgresStore implements PendingStore backed by PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore opens or creates a PostgreSQL-backed pending payments store.
func NewPostgresStore(db *sql.DB) (*PostgresStore, error) {
	if _, err := db.Exec(pgCreateTableSQL); err != nil {
		return nil, fmt.Errorf("failed to create pending_payments table: %w", err)
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) Create(ctx context.Context, p *PendingPayment) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO pending_payments (
			reference, alias, domain, identity_pub_key,
			derivation_prefix, derivation_suffix,
			satoshis, output_script, txid, created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		p.Reference, p.Alias, p.Domain, p.IdentityPubKey,
		p.DerivationPrefix, p.DerivationSuffix,
		p.Satoshis, p.OutputScript, p.TxID, p.CreatedAt, p.ExpiresAt,
	)
	return err
}

func (s *PostgresStore) Get(ctx context.Context, reference string) (*PendingPayment, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT reference, alias, domain, identity_pub_key,
			derivation_prefix, derivation_suffix,
			satoshis, output_script, txid, created_at, expires_at
		FROM pending_payments
		WHERE reference = $1`,
		reference,
	)

	p := &PendingPayment{}
	err := row.Scan(
		&p.Reference, &p.Alias, &p.Domain, &p.IdentityPubKey,
		&p.DerivationPrefix, &p.DerivationSuffix,
		&p.Satoshis, &p.OutputScript, &p.TxID, &p.CreatedAt, &p.ExpiresAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

func (s *PostgresStore) Update(ctx context.Context, p *PendingPayment) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE pending_payments SET
			alias = $1, domain = $2, identity_pub_key = $3,
			derivation_prefix = $4, derivation_suffix = $5,
			satoshis = $6, output_script = $7, txid = $8,
			created_at = $9, expires_at = $10
		WHERE reference = $11`,
		p.Alias, p.Domain, p.IdentityPubKey,
		p.DerivationPrefix, p.DerivationSuffix,
		p.Satoshis, p.OutputScript, p.TxID,
		p.CreatedAt, p.ExpiresAt,
		p.Reference,
	)
	return err
}

func (s *PostgresStore) Delete(ctx context.Context, reference string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pending_payments WHERE reference = $1`, reference)
	return err
}

func (s *PostgresStore) Close() error {
	return s.db.Close()
}
