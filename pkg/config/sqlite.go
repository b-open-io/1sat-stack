package config

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

const configSchema = `CREATE TABLE IF NOT EXISTS config (key TEXT PRIMARY KEY, value TEXT NOT NULL)`

// SQLiteStore implements Store backed by a SQLite database.
type SQLiteStore struct {
	writer *sql.DB
	reader *sql.DB
}

// NewSQLiteStore opens (or creates) a SQLite config database at path with WAL
// mode and separate read/write connections.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create config dir %s: %w", filepath.Dir(path), err)
	}

	writer, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("open config writer %s: %w", path, err)
	}
	writer.SetMaxOpenConns(1)

	if _, err := writer.Exec(configSchema); err != nil {
		writer.Close()
		return nil, fmt.Errorf("init config schema %s: %w", path, err)
	}

	reader, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000&mode=ro")
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("open config reader %s: %w", path, err)
	}
	reader.SetMaxOpenConns(4)

	return &SQLiteStore{writer: writer, reader: reader}, nil
}

func (s *SQLiteStore) Get(ctx context.Context, key string) (string, error) {
	var value string
	err := s.reader.QueryRowContext(ctx, `SELECT value FROM config WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get config %q: %w", key, err)
	}
	return value, nil
}

func (s *SQLiteStore) Set(ctx context.Context, key string, value string) error {
	_, err := s.writer.ExecContext(ctx,
		`INSERT OR REPLACE INTO config (key, value) VALUES (?, ?)`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("set config %q: %w", key, err)
	}
	return nil
}

func (s *SQLiteStore) Delete(ctx context.Context, key string) error {
	_, err := s.writer.ExecContext(ctx, `DELETE FROM config WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("delete config %q: %w", key, err)
	}
	return nil
}

func (s *SQLiteStore) List(ctx context.Context, prefix string) (map[string]string, error) {
	var rows *sql.Rows
	var err error
	if prefix == "" {
		rows, err = s.reader.QueryContext(ctx, `SELECT key, value FROM config`)
	} else {
		rows, err = s.reader.QueryContext(ctx, `SELECT key, value FROM config WHERE key LIKE ?`, prefix+"%")
	}
	if err != nil {
		return nil, fmt.Errorf("list config prefix %q: %w", prefix, err)
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan config row: %w", err)
		}
		result[k] = v
	}
	return result, rows.Err()
}

func (s *SQLiteStore) IsFirstRun(ctx context.Context) (bool, error) {
	var count int
	err := s.reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM config`).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check first run: %w", err)
	}
	return count == 0, nil
}

func (s *SQLiteStore) Close() error {
	s.reader.Close()
	return s.writer.Close()
}
