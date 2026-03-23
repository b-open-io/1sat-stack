package faucet

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const schema = `
CREATE TABLE IF NOT EXISTS faucet_config (
	name TEXT PRIMARY KEY,
	slug TEXT UNIQUE NOT NULL,
	instance_master_pubkey_hex TEXT NOT NULL DEFAULT '',
	counterparty_pubkey TEXT NOT NULL DEFAULT '',
	derivation_invoice_num TEXT NOT NULL DEFAULT '',
	next_change_invoice_idx INTEGER NOT NULL DEFAULT 0,
	next_deposit_invoice_idx INTEGER NOT NULL DEFAULT 0,
	drop_sats INTEGER,
	api_key_hash TEXT,
	target_buckets INTEGER,
	current_bucket_idx INTEGER,
	max_consolidation_inputs INTEGER,
	owner_pubkey TEXT,
	created_at DATETIME NOT NULL DEFAULT (datetime('now')),
	updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS faucet_api_keys (
	faucet_name TEXT NOT NULL,
	user_public_key_hex TEXT NOT NULL,
	hashed_public_key TEXT NOT NULL DEFAULT '',
	label TEXT,
	created_at DATETIME NOT NULL DEFAULT (datetime('now')),
	updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
	PRIMARY KEY (faucet_name, user_public_key_hex)
);

CREATE INDEX IF NOT EXISTS idx_faucet_api_keys_hashed ON faucet_api_keys(faucet_name, hashed_public_key);
CREATE INDEX IF NOT EXISTS idx_faucet_api_keys_faucet ON faucet_api_keys(faucet_name);

CREATE TABLE IF NOT EXISTS faucet_activity_log (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	faucet_name TEXT NOT NULL,
	timestamp DATETIME NOT NULL DEFAULT (datetime('now')),
	type TEXT NOT NULL,
	txid TEXT,
	amount_sat INTEGER,
	description TEXT,
	details TEXT
);

CREATE INDEX IF NOT EXISTS idx_faucet_activity_faucet ON faucet_activity_log(faucet_name, timestamp DESC);
`

// Store provides SQLite-backed persistence for faucet configuration, API keys,
// and activity logs.
type Store struct {
	writer *sql.DB
	reader *sql.DB
	logger *slog.Logger
	once   sync.Once
}

// NewStore opens (or creates) a SQLite database at dbPath with WAL mode and
// separate reader/writer connections, then runs the schema migration.
func NewStore(dbPath string, logger *slog.Logger) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("create faucet store dir %s: %w", filepath.Dir(dbPath), err)
	}

	writer, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("open faucet writer %s: %w", dbPath, err)
	}
	writer.SetMaxOpenConns(1)

	if _, err := writer.Exec(schema); err != nil {
		writer.Close()
		return nil, fmt.Errorf("init faucet schema %s: %w", dbPath, err)
	}

	reader, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&mode=ro")
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("open faucet reader %s: %w", dbPath, err)
	}
	reader.SetMaxOpenConns(4)

	logger.Info("faucet store opened", "path", dbPath)
	return &Store{writer: writer, reader: reader, logger: logger}, nil
}

// Close shuts down both database connections.
func (s *Store) Close() error {
	s.reader.Close()
	return s.writer.Close()
}

// ---------------------------------------------------------------------------
// Faucet config
// ---------------------------------------------------------------------------

const configColumns = `name, slug, instance_master_pubkey_hex, counterparty_pubkey,
	derivation_invoice_num, next_change_invoice_idx, next_deposit_invoice_idx,
	drop_sats, api_key_hash, created_at, updated_at,
	target_buckets, current_bucket_idx, max_consolidation_inputs, owner_pubkey`

func scanConfig(row interface{ Scan(dest ...any) error }) (*FaucetConfig, error) {
	c := &FaucetConfig{}
	err := row.Scan(
		&c.FaucetName,
		&c.Slug,
		&c.InstanceMasterPublicKeyHex,
		&c.DerivationCpPubKeyHex,
		&c.DerivationInvoiceNum,
		&c.NextChangeInvoiceIdx,
		&c.NextDepositInvoiceIdx,
		&c.FixedDropSats,
		&c.APIKeyHash,
		&c.CreatedAt,
		&c.UpdatedAt,
		&c.TargetBuckets,
		&c.CurrentBucketIdx,
		&c.MaxConsolidationInputs,
		&c.OwnerUserPublicKeyHex,
	)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// LoadFaucetConfig loads a faucet config by name.
func (s *Store) LoadFaucetConfig(ctx context.Context, name string) (*FaucetConfig, error) {
	row := s.reader.QueryRowContext(ctx,
		`SELECT `+configColumns+` FROM faucet_config WHERE name = ?`, name)
	c, err := scanConfig(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("faucet %q not found", name)
		}
		return nil, fmt.Errorf("load faucet config %q: %w", name, err)
	}
	return c, nil
}

// LoadFaucetConfigBySlug loads a faucet config by its URL slug.
func (s *Store) LoadFaucetConfigBySlug(ctx context.Context, slug string) (*FaucetConfig, error) {
	row := s.reader.QueryRowContext(ctx,
		`SELECT `+configColumns+` FROM faucet_config WHERE slug = ?`, slug)
	c, err := scanConfig(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("faucet with slug %q not found", slug)
		}
		return nil, fmt.Errorf("load faucet config by slug %q: %w", slug, err)
	}
	return c, nil
}

// SaveFaucetConfig inserts a new faucet config. Returns the number of rows
// affected (0 if the name already exists due to ON CONFLICT DO NOTHING).
func (s *Store) SaveFaucetConfig(ctx context.Context, cfg *FaucetConfig) (int64, error) {
	result, err := s.writer.ExecContext(ctx, `
		INSERT INTO faucet_config (
			name, slug, instance_master_pubkey_hex, counterparty_pubkey,
			derivation_invoice_num, next_change_invoice_idx, next_deposit_invoice_idx,
			drop_sats, api_key_hash, created_at, updated_at,
			target_buckets, current_bucket_idx, max_consolidation_inputs, owner_pubkey
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'), ?, ?, ?, ?)
		ON CONFLICT (name) DO NOTHING`,
		cfg.FaucetName,
		cfg.Slug,
		cfg.InstanceMasterPublicKeyHex,
		cfg.DerivationCpPubKeyHex,
		cfg.DerivationInvoiceNum,
		cfg.NextChangeInvoiceIdx,
		cfg.NextDepositInvoiceIdx,
		cfg.FixedDropSats,
		cfg.APIKeyHash,
		cfg.TargetBuckets,
		cfg.CurrentBucketIdx,
		cfg.MaxConsolidationInputs,
		cfg.OwnerUserPublicKeyHex,
	)
	if err != nil {
		return 0, fmt.Errorf("save faucet config %q: %w", cfg.FaucetName, err)
	}
	return result.RowsAffected()
}

// UpdateFaucetConfig dynamically builds an UPDATE from a map of column→value
// pairs. Only the provided keys are modified; updated_at is always set.
func (s *Store) UpdateFaucetConfig(ctx context.Context, name string, updates map[string]any) error {
	if len(updates) == 0 {
		return fmt.Errorf("no fields to update")
	}

	setClauses := make([]string, 0, len(updates)+1)
	args := make([]any, 0, len(updates)+2)

	for col, val := range updates {
		setClauses = append(setClauses, col+" = ?")
		args = append(args, val)
	}
	setClauses = append(setClauses, "updated_at = datetime('now')")
	args = append(args, name)

	query := fmt.Sprintf("UPDATE faucet_config SET %s WHERE name = ?",
		strings.Join(setClauses, ", "))

	_, err := s.writer.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update faucet config %q: %w", name, err)
	}
	return nil
}

// ListFaucetConfigs returns all faucet configs ordered by creation time.
func (s *Store) ListFaucetConfigs(ctx context.Context) ([]*FaucetConfig, error) {
	rows, err := s.reader.QueryContext(ctx,
		`SELECT `+configColumns+` FROM faucet_config ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list faucet configs: %w", err)
	}
	defer rows.Close()

	var configs []*FaucetConfig
	for rows.Next() {
		c, err := scanConfig(rows)
		if err != nil {
			return nil, fmt.Errorf("scan faucet config row: %w", err)
		}
		configs = append(configs, c)
	}
	return configs, rows.Err()
}

// ListFaucetConfigsByOwner returns configs owned by the given public key.
func (s *Store) ListFaucetConfigsByOwner(ctx context.Context, ownerPubkey string) ([]*FaucetConfig, error) {
	rows, err := s.reader.QueryContext(ctx,
		`SELECT `+configColumns+` FROM faucet_config WHERE owner_pubkey = ? ORDER BY created_at DESC`,
		ownerPubkey)
	if err != nil {
		return nil, fmt.Errorf("list faucet configs by owner: %w", err)
	}
	defer rows.Close()

	var configs []*FaucetConfig
	for rows.Next() {
		c, err := scanConfig(rows)
		if err != nil {
			return nil, fmt.Errorf("scan faucet config row: %w", err)
		}
		configs = append(configs, c)
	}
	return configs, rows.Err()
}

// ---------------------------------------------------------------------------
// Activity log
// ---------------------------------------------------------------------------

// ActivityEntry represents a single row from the faucet_activity_log table.
type ActivityEntry struct {
	ID          int64           `json:"id"`
	FaucetName  string          `json:"faucet_name"`
	Timestamp   time.Time       `json:"timestamp"`
	Type        string          `json:"type"`
	TxID        string          `json:"txid,omitempty"`
	AmountSat   int64           `json:"amount_sat,omitempty"`
	Description string          `json:"description,omitempty"`
	Details     json.RawMessage `json:"details,omitempty"`
}

// LogActivity inserts a row into the activity log.
func (s *Store) LogActivity(ctx context.Context, faucetName, activityType, txid string, amountSat int64, description string, details string) error {
	var txidDB sql.NullString
	if txid != "" {
		txidDB = sql.NullString{String: txid, Valid: true}
	}

	var amountDB sql.NullInt64
	if amountSat != 0 {
		amountDB = sql.NullInt64{Int64: amountSat, Valid: true}
	}

	var descDB sql.NullString
	if description != "" {
		descDB = sql.NullString{String: description, Valid: true}
	}

	var detailsDB sql.NullString
	if details != "" {
		detailsDB = sql.NullString{String: details, Valid: true}
	}

	_, err := s.writer.ExecContext(ctx, `
		INSERT INTO faucet_activity_log (faucet_name, timestamp, type, txid, amount_sat, description, details)
		VALUES (?, datetime('now'), ?, ?, ?, ?, ?)`,
		faucetName, activityType, txidDB, amountDB, descDB, detailsDB,
	)
	if err != nil {
		return fmt.Errorf("log faucet activity: %w", err)
	}
	return nil
}

// GetActivity returns activity log entries for a faucet with pagination.
func (s *Store) GetActivity(ctx context.Context, faucetName string, limit, offset int) ([]ActivityEntry, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.reader.QueryContext(ctx, `
		SELECT id, faucet_name, timestamp, type, txid, amount_sat, description, details
		FROM faucet_activity_log
		WHERE faucet_name = ?
		ORDER BY timestamp DESC
		LIMIT ? OFFSET ?`,
		faucetName, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("get faucet activity: %w", err)
	}
	defer rows.Close()

	var entries []ActivityEntry
	for rows.Next() {
		var e ActivityEntry
		var txid sql.NullString
		var amount sql.NullInt64
		var desc sql.NullString
		var details []byte

		if err := rows.Scan(&e.ID, &e.FaucetName, &e.Timestamp, &e.Type, &txid, &amount, &desc, &details); err != nil {
			return nil, fmt.Errorf("scan activity row: %w", err)
		}
		if txid.Valid {
			e.TxID = txid.String
		}
		if amount.Valid {
			e.AmountSat = amount.Int64
		}
		if desc.Valid {
			e.Description = desc.String
		}
		if len(details) > 0 && string(details) != "null" {
			e.Details = json.RawMessage(details)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// ---------------------------------------------------------------------------
// API keys
// ---------------------------------------------------------------------------

// APIKeyRow represents a row from the faucet_api_keys table.
type APIKeyRow struct {
	FaucetName       string    `json:"faucet_name"`
	UserPublicKeyHex string    `json:"user_public_key_hex"`
	HashedPublicKey  string    `json:"hashed_public_key"`
	Label            *string   `json:"label,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// ListAPIKeys returns all API key entries for a faucet.
func (s *Store) ListAPIKeys(ctx context.Context, faucetName string) ([]APIKeyRow, error) {
	rows, err := s.reader.QueryContext(ctx, `
		SELECT faucet_name, user_public_key_hex, hashed_public_key, label, created_at, updated_at
		FROM faucet_api_keys
		WHERE faucet_name = ?
		ORDER BY created_at DESC`,
		faucetName,
	)
	if err != nil {
		return nil, fmt.Errorf("list api keys for %q: %w", faucetName, err)
	}
	defer rows.Close()

	var keys []APIKeyRow
	for rows.Next() {
		var k APIKeyRow
		var label sql.NullString
		if err := rows.Scan(&k.FaucetName, &k.UserPublicKeyHex, &k.HashedPublicKey, &label, &k.CreatedAt, &k.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan api key row: %w", err)
		}
		if label.Valid {
			k.Label = &label.String
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// AddAPIKey inserts an API key entry. Returns the number of rows affected
// (0 if the composite key already exists).
func (s *Store) AddAPIKey(ctx context.Context, faucetName, userPubKeyHex, hashedPubKey, label string) (int64, error) {
	var labelDB sql.NullString
	if label != "" {
		labelDB = sql.NullString{String: label, Valid: true}
	}

	result, err := s.writer.ExecContext(ctx, `
		INSERT INTO faucet_api_keys (faucet_name, user_public_key_hex, hashed_public_key, label, created_at, updated_at)
		VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))
		ON CONFLICT (faucet_name, user_public_key_hex) DO NOTHING`,
		faucetName, userPubKeyHex, hashedPubKey, labelDB,
	)
	if err != nil {
		return 0, fmt.Errorf("add api key for %q: %w", faucetName, err)
	}
	return result.RowsAffected()
}

// DeleteAPIKey removes an API key entry by faucet name and user public key.
func (s *Store) DeleteAPIKey(ctx context.Context, faucetName, userPubKeyHex string) (int64, error) {
	result, err := s.writer.ExecContext(ctx,
		`DELETE FROM faucet_api_keys WHERE faucet_name = ? AND user_public_key_hex = ?`,
		faucetName, userPubKeyHex,
	)
	if err != nil {
		return 0, fmt.Errorf("delete api key for %q: %w", faucetName, err)
	}
	return result.RowsAffected()
}

// LookupAPIKeyByHash finds the user public key hex associated with a hashed
// API key for a given faucet. Returns sql.ErrNoRows if not found.
func (s *Store) LookupAPIKeyByHash(ctx context.Context, faucetName, hashedKey string) (string, error) {
	var userPubKeyHex string
	err := s.reader.QueryRowContext(ctx, `
		SELECT user_public_key_hex FROM faucet_api_keys
		WHERE faucet_name = ? AND hashed_public_key = ?`,
		faucetName, hashedKey,
	).Scan(&userPubKeyHex)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", err
		}
		return "", fmt.Errorf("lookup api key by hash for %q: %w", faucetName, err)
	}
	return userPubKeyHex, nil
}
