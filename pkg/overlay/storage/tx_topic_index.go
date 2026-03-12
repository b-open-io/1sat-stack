package storage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	_ "github.com/mattn/go-sqlite3"
)

const txTopicSchema = `
CREATE TABLE IF NOT EXISTS txid_topics (
    txid  BLOB NOT NULL,
    topic TEXT NOT NULL,
    PRIMARY KEY (txid, topic)
);
CREATE INDEX IF NOT EXISTS idx_txid ON txid_topics(txid);
`

// TxTopicIndex maps transaction IDs to the topics they were admitted to.
// Used by rollback handlers to know which per-topic databases to clean up.
type TxTopicIndex struct {
	writer *sql.DB
	reader *sql.DB
}

// NewTxTopicIndex opens (or creates) the txid→topics index database.
func NewTxTopicIndex(path string) (*TxTopicIndex, error) {
	writer, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("open tx topic index writer: %w", err)
	}
	writer.SetMaxOpenConns(1)

	if _, err := writer.Exec(txTopicSchema); err != nil {
		writer.Close()
		return nil, fmt.Errorf("init tx topic index schema: %w", err)
	}

	reader, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000&mode=ro")
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("open tx topic index reader: %w", err)
	}
	reader.SetMaxOpenConns(4)

	return &TxTopicIndex{writer: writer, reader: reader}, nil
}

// Record stores a txid→topic mapping.
func (idx *TxTopicIndex) Record(ctx context.Context, txid *chainhash.Hash, topic string) error {
	_, err := idx.writer.ExecContext(ctx,
		`INSERT OR IGNORE INTO txid_topics (txid, topic) VALUES (?, ?)`,
		txid[:], topic,
	)
	return err
}

// Topics returns all topics that contain outputs for a given txid.
func (idx *TxTopicIndex) Topics(ctx context.Context, txid *chainhash.Hash) ([]string, error) {
	rows, err := idx.reader.QueryContext(ctx,
		`SELECT topic FROM txid_topics WHERE txid = ?`,
		txid[:],
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var topics []string
	for rows.Next() {
		var topic string
		if err := rows.Scan(&topic); err != nil {
			return nil, err
		}
		topics = append(topics, topic)
	}
	return topics, rows.Err()
}

// Delete removes all topic mappings for a txid.
func (idx *TxTopicIndex) Delete(ctx context.Context, txid *chainhash.Hash) error {
	_, err := idx.writer.ExecContext(ctx,
		`DELETE FROM txid_topics WHERE txid = ?`,
		txid[:],
	)
	return err
}

// Close closes both database connections.
func (idx *TxTopicIndex) Close() error {
	idx.reader.Close()
	return idx.writer.Close()
}
