package paymail

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// SQLiteStore implements PendingStore backed by a SQLite database.
type SQLiteStore struct {
	db *sql.DB
}

const createTableSQL = `
CREATE TABLE IF NOT EXISTS pending_payments (
	reference        TEXT PRIMARY KEY,
	alias            TEXT NOT NULL,
	domain           TEXT NOT NULL,
	identity_pub_key TEXT NOT NULL,
	derivation_prefix TEXT NOT NULL,
	derivation_suffix TEXT NOT NULL,
	satoshis         INTEGER NOT NULL,
	output_script    TEXT NOT NULL,
	txid             TEXT NOT NULL DEFAULT '',
	created_at       DATETIME NOT NULL,
	expires_at       DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pending_expires ON pending_payments(expires_at);
`

// NewSQLiteStore opens or creates a SQLite database and starts a cleanup goroutine.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open paymail db: %w", err)
	}

	if _, err := db.Exec(createTableSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create pending_payments table: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Create(ctx context.Context, p *PendingPayment) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO pending_payments (
			reference, alias, domain, identity_pub_key,
			derivation_prefix, derivation_suffix,
			satoshis, output_script, txid, created_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Reference, p.Alias, p.Domain, p.IdentityPubKey,
		p.DerivationPrefix, p.DerivationSuffix,
		p.Satoshis, p.OutputScript, p.TxID, p.CreatedAt, p.ExpiresAt,
	)
	return err
}

func (s *SQLiteStore) Get(ctx context.Context, reference string) (*PendingPayment, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT reference, alias, domain, identity_pub_key,
			derivation_prefix, derivation_suffix,
			satoshis, output_script, txid, created_at, expires_at
		FROM pending_payments
		WHERE reference = ?`,
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

func (s *SQLiteStore) Update(ctx context.Context, p *PendingPayment) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE pending_payments SET
			alias = ?, domain = ?, identity_pub_key = ?,
			derivation_prefix = ?, derivation_suffix = ?,
			satoshis = ?, output_script = ?, txid = ?,
			created_at = ?, expires_at = ?
		WHERE reference = ?`,
		p.Alias, p.Domain, p.IdentityPubKey,
		p.DerivationPrefix, p.DerivationSuffix,
		p.Satoshis, p.OutputScript, p.TxID,
		p.CreatedAt, p.ExpiresAt,
		p.Reference,
	)
	return err
}

func (s *SQLiteStore) Delete(ctx context.Context, reference string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pending_payments WHERE reference = ?`, reference)
	return err
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

