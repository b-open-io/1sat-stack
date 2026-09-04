package storage

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const pgSchema = `
CREATE TABLE IF NOT EXISTS topics (
    id   SERIAL PRIMARY KEY,
    name TEXT UNIQUE NOT NULL
);

CREATE TABLE IF NOT EXISTS outputs (
    topic_id        INTEGER NOT NULL REFERENCES topics(id),
    outpoint        BYTEA NOT NULL,
    txid            BYTEA NOT NULL,
    satoshis        BIGINT,
    spend_txid      BYTEA,
    score           DOUBLE PRECISION NOT NULL,
    deps            BYTEA,
    inputs_consumed BYTEA,
    consumed_by     BYTEA,
    created_at      BIGINT NOT NULL,
    PRIMARY KEY (topic_id, outpoint)
);
CREATE INDEX IF NOT EXISTS idx_pg_outputs_txid ON outputs(topic_id, txid);
CREATE INDEX IF NOT EXISTS idx_pg_outputs_unspent ON outputs(topic_id, score) WHERE spend_txid IS NULL;
CREATE TABLE IF NOT EXISTS overlay_mutations (
    topic_id  INTEGER NOT NULL REFERENCES topics(id),
    txid      BYTEA NOT NULL,
    phase     TEXT NOT NULL CHECK (phase IN ('active', 'applied', 'rollback_pending')),
    created_at BIGINT NOT NULL,
    PRIMARY KEY (topic_id, txid)
);
CREATE INDEX IF NOT EXISTS idx_pg_overlay_mutations_phase ON overlay_mutations(topic_id, phase, created_at);

CREATE TABLE IF NOT EXISTS mutation_outputs (
    topic_id        INTEGER NOT NULL REFERENCES topics(id),
    mutation_txid   BYTEA NOT NULL,
    outpoint        BYTEA NOT NULL,
    txid            BYTEA NOT NULL,
    satoshis        BIGINT,
    spend_txid      BYTEA,
    score           DOUBLE PRECISION NOT NULL,
    deps             BYTEA,
    inputs_consumed BYTEA,
    consumed_by     BYTEA,
    created_at      BIGINT NOT NULL,
    PRIMARY KEY (topic_id, mutation_txid, outpoint)
);
CREATE INDEX IF NOT EXISTS idx_pg_mutation_outputs_txid ON mutation_outputs(topic_id, txid, mutation_txid);

CREATE TABLE IF NOT EXISTS mutation_events (
    topic_id      INTEGER NOT NULL REFERENCES topics(id),
    mutation_txid BYTEA NOT NULL,
    event         TEXT NOT NULL,
    outpoint      BYTEA NOT NULL,
    score         DOUBLE PRECISION NOT NULL,
    PRIMARY KEY (topic_id, mutation_txid, event, outpoint)
);

CREATE TABLE IF NOT EXISTS mutation_evictions (
    topic_id      INTEGER NOT NULL REFERENCES topics(id),
    mutation_txid BYTEA NOT NULL,
    outpoint      BYTEA NOT NULL,
    PRIMARY KEY (topic_id, mutation_txid, outpoint)
);

CREATE TABLE IF NOT EXISTS mutation_reservations (
    topic_id      INTEGER NOT NULL REFERENCES topics(id),
    outpoint      BYTEA NOT NULL,
    mutation_txid BYTEA NOT NULL,
    PRIMARY KEY (topic_id, outpoint)
);

CREATE TABLE IF NOT EXISTS applied_txs (
    topic_id INTEGER NOT NULL REFERENCES topics(id),
    txid     BYTEA NOT NULL,
    score    DOUBLE PRECISION NOT NULL,
    PRIMARY KEY (topic_id, txid)
);

CREATE TABLE IF NOT EXISTS events (
    topic_id INTEGER NOT NULL REFERENCES topics(id),
    event    TEXT NOT NULL,
    outpoint BYTEA NOT NULL,
    score    DOUBLE PRECISION NOT NULL,
    PRIMARY KEY (topic_id, event, outpoint)
);
CREATE INDEX IF NOT EXISTS idx_pg_events_score ON events(topic_id, event, score);

CREATE TABLE IF NOT EXISTS peer_interactions (
    topic_id INTEGER NOT NULL REFERENCES topics(id),
    host     TEXT NOT NULL,
    since    DOUBLE PRECISION NOT NULL,
    PRIMARY KEY (topic_id, host)
);

CREATE TABLE IF NOT EXISTS txid_topics (
    txid     BYTEA NOT NULL,
    topic_id INTEGER NOT NULL REFERENCES topics(id),
    PRIMARY KEY (txid, topic_id)
);
CREATE INDEX IF NOT EXISTS idx_pg_txid_topics_txid ON txid_topics(txid);
`

// PostgresFactory manages a shared PostgreSQL connection pool and resolves
// topic names to numeric IDs via the topics registry table.
type PostgresFactory struct {
	db       *sql.DB
	topicIDs sync.Map // map[string]int
	txIndex  *PostgresTxTopicIndex
}

// NewPostgresFactory opens a connection pool and initializes the schema.
func NewPostgresFactory(connStr string) (*PostgresFactory, error) {
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	if _, err := db.Exec(pgSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init postgres schema: %w", err)
	}

	return &PostgresFactory{
		db:      db,
		txIndex: &PostgresTxTopicIndex{db: db},
	}, nil
}

// Topic returns the TopicStorage for a given topic, resolving the topic name
// to a numeric ID and caching the result.
func (f *PostgresFactory) Topic(topic string) (TopicStorage, error) {
	id, err := f.resolveTopicID(topic)
	if err != nil {
		return nil, err
	}
	return &PostgresStorage{db: f.db, topicID: id}, nil
}

// Factory returns a Factory function for callers that need the func signature.
func (f *PostgresFactory) Factory() Factory {
	return f.Topic
}

// TxTopicIndex returns the cross-topic txid→topics index.
func (f *PostgresFactory) TxTopicIndex() *PostgresTxTopicIndex {
	return f.txIndex
}

// DB returns the shared connection pool.
func (f *PostgresFactory) DB() *sql.DB {
	return f.db
}

// Close closes the shared connection pool.
func (f *PostgresFactory) Close() error {
	return f.db.Close()
}

func (f *PostgresFactory) resolveTopicID(name string) (int, error) {
	if id, ok := f.topicIDs.Load(name); ok {
		return id.(int), nil
	}

	var id int
	err := f.db.QueryRow(
		`INSERT INTO topics (name) VALUES ($1) ON CONFLICT (name) DO NOTHING RETURNING id`,
		name,
	).Scan(&id)
	if err == sql.ErrNoRows {
		// Already exists — fetch it
		err = f.db.QueryRow(`SELECT id FROM topics WHERE name = $1`, name).Scan(&id)
	}
	if err != nil {
		return 0, fmt.Errorf("resolve topic %q: %w", name, err)
	}

	f.topicIDs.Store(name, id)
	return id, nil
}

// PostgresTxTopicIndex maps transaction IDs to the topics they were admitted to.
type PostgresTxTopicIndex struct {
	db *sql.DB
}

// Record stores a txid→topic mapping.
func (idx *PostgresTxTopicIndex) Record(ctx context.Context, txid *chainhash.Hash, topic string) error {
	// Resolve topic ID inline to avoid requiring factory reference
	var topicID int
	err := idx.db.QueryRowContext(ctx, `SELECT id FROM topics WHERE name = $1`, topic).Scan(&topicID)
	if err != nil {
		return fmt.Errorf("resolve topic %q for txid index: %w", topic, err)
	}
	_, err = idx.db.ExecContext(ctx,
		`INSERT INTO txid_topics (txid, topic_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		txid[:], topicID,
	)
	return err
}

// Topics returns all topic names that contain outputs for a given txid.
func (idx *PostgresTxTopicIndex) Topics(ctx context.Context, txid *chainhash.Hash) ([]string, error) {
	rows, err := idx.db.QueryContext(ctx,
		`SELECT t.name FROM txid_topics tt JOIN topics t ON tt.topic_id = t.id WHERE tt.txid = $1`,
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
func (idx *PostgresTxTopicIndex) Delete(ctx context.Context, txid *chainhash.Hash) error {
	_, err := idx.db.ExecContext(ctx, `DELETE FROM txid_topics WHERE txid = $1`, txid[:])
	return err
}
