package collection

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/b-open-io/1sat-stack/pkg/parse"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// SQLite keeps one database per topic. Postgres keeps one table and scopes
// every row by topic_id. Role is implied by topic — there is no kind column.
const sqliteEntrySchema = `
CREATE TABLE IF NOT EXISTS collection_entries (
    outpoint       BLOB PRIMARY KEY,
    collection_id  TEXT NOT NULL,
    name           TEXT,
    signer         TEXT NOT NULL,
    content_type   TEXT,
    mint_number    INTEGER,
    rank           INTEGER,
    map_json       TEXT,
    score          REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_collection_entries_id ON collection_entries(collection_id, score);
`

const postgresEntrySchema = `
CREATE TABLE IF NOT EXISTS collection_entries (
    topic_id       INTEGER NOT NULL,
    outpoint       BYTEA NOT NULL,
    collection_id  TEXT NOT NULL,
    name           TEXT,
    signer         TEXT NOT NULL,
    content_type   TEXT,
    mint_number    INTEGER,
    rank           INTEGER,
    map_json       TEXT,
    score          DOUBLE PRECISION NOT NULL,
    PRIMARY KEY (topic_id, outpoint)
);
CREATE INDEX IF NOT EXISTS idx_collection_entries_id ON collection_entries(topic_id, collection_id, score);
`

// LookupService indexes admitted collections and items into topic storage.
type LookupService struct {
	topicDB overlaystorage.Factory
	ready   sync.Map // sqlite topic -> schema created
	pgOnce  sync.Once
	pgErr   error
}

// NewLookupService creates a collection lookup backed by the overlay topic factory.
func NewLookupService(topicDB overlaystorage.Factory) *LookupService {
	return &LookupService{topicDB: topicDB}
}

func (l *LookupService) db(topic string) (overlaystorage.TopicStorage, error) {
	ts, err := l.topicDB(topic)
	if err != nil {
		return nil, err
	}
	if ts.TopicID() > 0 {
		l.pgOnce.Do(func() {
			_, l.pgErr = ts.DB().Exec(postgresEntrySchema)
		})
		if l.pgErr != nil {
			return nil, fmt.Errorf("create collection_entries schema: %w", l.pgErr)
		}
		return ts, nil
	}
	if _, ok := l.ready.Load(topic); !ok {
		if _, err := ts.DB().Exec(sqliteEntrySchema); err != nil {
			return nil, fmt.Errorf("create collection_entries schema for %s: %w", topic, err)
		}
		l.ready.Store(topic, true)
	}
	return ts, nil
}

// OutputAdmittedByTopic indexes a newly admitted collection or item mint.
func (l *LookupService) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
	if payload == nil {
		return nil
	}
	if !IsDiscoveryTopic(payload.Topic) && !IsItemTopic(payload.Topic) {
		return nil
	}

	_, tx, txid, err := transaction.ParseBeef(payload.AtomicBEEF)
	if err != nil {
		return err
	}
	if int(payload.OutputIndex) >= len(tx.Outputs) {
		return nil
	}

	out := tx.Outputs[payload.OutputIndex]
	fields := DecodeMapFields(out.LockingScript)
	if fields == nil {
		return nil
	}
	sigma := FirstValidSigma(tx, int(payload.OutputIndex))
	if sigma == nil {
		return nil
	}

	outpoint := &transaction.Outpoint{Txid: *txid, Index: payload.OutputIndex}
	score := types.ScoreFromTx(tx, txid)

	var collectionID string
	switch {
	case IsDiscoveryTopic(payload.Topic) && fields.SubType == SubTypeCollection:
		// Collection identity is its own outpoint.
		collectionID = outpoint.OrdinalString()
	case IsItemTopic(payload.Topic) && fields.SubType == SubTypeCollectionItem:
		collectionID = parse.NormalizeRelativeOutpoint(fields.CollectionID, outpoint)
		if expected := CollectionIDFromTopic(payload.Topic); expected != "" && collectionID != expected {
			return nil
		}
	default:
		return nil
	}

	var mapJSON string
	if fields.Raw != nil {
		if b, err := json.Marshal(fields.Raw); err == nil {
			mapJSON = string(b)
		}
	}

	ts, err := l.db(payload.Topic)
	if err != nil {
		return err
	}

	var mintNumber, rank any
	if fields.MintNumber != nil {
		mintNumber = *fields.MintNumber
	}
	if fields.Rank != nil {
		rank = *fields.Rank
	}

	return l.upsertEntry(ctx, ts, entryWrite{
		outpoint:     outpoint.Bytes(),
		collectionID: collectionID,
		name:         nullStr(fields.Name),
		signer:       sigma.SignerAddress,
		contentType:  nullStr(ContentType(out.LockingScript)),
		mintNumber:   mintNumber,
		rank:         rank,
		mapJSON:      nullStr(mapJSON),
		score:        score,
	})
}

type entryWrite struct {
	outpoint     []byte
	collectionID string
	name         any
	signer       string
	contentType  any
	mintNumber   any
	rank         any
	mapJSON      any
	score        float64
}

func (l *LookupService) upsertEntry(ctx context.Context, ts overlaystorage.TopicStorage, row entryWrite) error {
	b := newSQL(ts.TopicID())
	q := `
		INSERT INTO collection_entries(
			` + b.topicCols() + `outpoint, collection_id, name, signer, content_type,
			mint_number, rank, map_json, score
		) VALUES (` + b.topicVals() + b.ph(row.outpoint) + `, ` + b.ph(row.collectionID) + `, ` + b.ph(row.name) + `, ` + b.ph(row.signer) + `, ` + b.ph(row.contentType) + `, ` + b.ph(row.mintNumber) + `, ` + b.ph(row.rank) + `, ` + b.ph(row.mapJSON) + `, ` + b.ph(row.score) + `)
		ON CONFLICT ` + b.conflict() + ` DO UPDATE SET
			collection_id=excluded.collection_id,
			name=excluded.name,
			signer=excluded.signer,
			content_type=excluded.content_type,
			mint_number=excluded.mint_number,
			rank=excluded.rank,
			map_json=excluded.map_json,
			score=excluded.score`
	_, err := ts.DB().ExecContext(ctx, q, b.args...)
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// OutputSpent is a no-op for mint-only indexing.
func (l *LookupService) OutputSpent(ctx context.Context, payload *engine.OutputSpent) error {
	return nil
}

// OutputNoLongerRetainedInHistory is a no-op.
func (l *LookupService) OutputNoLongerRetainedInHistory(ctx context.Context, outpoint *transaction.Outpoint, topic string) error {
	return nil
}

// OutputEvicted is a no-op (no topic context for cross-DB cleanup).
func (l *LookupService) OutputEvicted(ctx context.Context, outpoint *transaction.Outpoint) error {
	return nil
}

// OutputBlockHeightUpdated is a no-op.
func (l *LookupService) OutputBlockHeightUpdated(ctx context.Context, txid *chainhash.Hash, blockHeight uint32, blockIndex uint64) error {
	return nil
}

// Lookup handles generic overlay lookup questions (unused; use typed methods).
func (l *LookupService) Lookup(ctx context.Context, question *lookup.LookupQuestion) (*lookup.LookupAnswer, error) {
	return &lookup.LookupAnswer{Type: lookup.AnswerTypeFormula}, nil
}

// GetDocumentation returns documentation for this lookup service.
func (l *LookupService) GetDocumentation() string {
	return "1Sat Collection Lookup — collections and items (mint-only, SIGMA signer stored)"
}

// GetMetaData returns metadata for this lookup service.
func (l *LookupService) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{Name: "collection"}
}

// Entry is a stored collection or collection-item mint.
type Entry struct {
	Outpoint     string         `json:"outpoint"`
	CollectionID string         `json:"collectionId"`
	Name         string         `json:"name,omitempty"`
	Signer       string         `json:"signer"`
	ContentType  string         `json:"contentType,omitempty"`
	MintNumber   *int           `json:"mintNumber,omitempty"`
	Rank         *int           `json:"rank,omitempty"`
	Map          map[string]any `json:"map,omitempty"`
	Score        float64        `json:"score"`
}

const selectCols = `outpoint, collection_id, name, signer, content_type, mint_number, rank, map_json, score`

// Count returns the number of entries stored in a topic database.
func (l *LookupService) Count(ctx context.Context, topic string) (int64, error) {
	ts, err := l.db(topic)
	if err != nil {
		return 0, err
	}
	b := newSQL(ts.TopicID())
	q := `SELECT COUNT(*) FROM collection_entries`
	if ts.TopicID() > 0 {
		q += ` WHERE topic_id = $1`
	}
	var n int64
	err = ts.DB().QueryRowContext(ctx, q, b.args...).Scan(&n)
	return n, err
}

// ListCollections returns collections from the discovery topic.
func (l *LookupService) ListCollections(ctx context.Context, limit int, reverse bool) ([]*Entry, error) {
	return l.queryEntries(ctx, DiscoveryTopic, entryFilter{order: orderSQL(reverse), limit: limit})
}

// GetCollection returns a collection by collectionId (its outpoint) from discovery storage.
func (l *LookupService) GetCollection(ctx context.Context, collectionID string) (*Entry, error) {
	entries, err := l.queryEntries(ctx, DiscoveryTopic, entryFilter{collectionID: collectionID, limit: 1})
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return entries[0], nil
}

// ListItems returns items for a collection from its item topic DB.
func (l *LookupService) ListItems(ctx context.Context, collectionID string, limit int, reverse bool) ([]*Entry, error) {
	return l.queryEntries(ctx, ItemTopic(collectionID), entryFilter{
		collectionID: collectionID,
		order:        orderSQL(reverse),
		limit:        limit,
	})
}

// GetItem returns a single item by outpoint within a collection topic.
// outpointStr may be "txid.vout" or ordinal "txid_vout".
func (l *LookupService) GetItem(ctx context.Context, collectionID, outpointStr string) (*Entry, error) {
	op, err := parseOutpoint(outpointStr)
	if err != nil {
		return nil, fmt.Errorf("invalid outpoint: %w", err)
	}
	entries, err := l.queryEntries(ctx, ItemTopic(collectionID), entryFilter{
		outpoint: op.Bytes(),
		limit:    1,
	})
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return entries[0], nil
}

func parseOutpoint(s string) (*transaction.Outpoint, error) {
	if op, err := transaction.OutpointFromString(s); err == nil {
		return op, nil
	}
	if len(s) >= 66 && s[64] == '_' {
		return transaction.OutpointFromString(s[:64] + "." + s[65:])
	}
	return nil, fmt.Errorf("invalid outpoint %q", s)
}

func orderSQL(reverse bool) string {
	if reverse {
		return "DESC"
	}
	return "ASC"
}

func limitSQL(limit int) string {
	if limit <= 0 {
		return ""
	}
	return fmt.Sprintf(" LIMIT %d", limit)
}

// sqlb numbers placeholders and scopes rows by topic_id on Postgres.
// TopicID 0 is SQLite: one database per topic, "?" placeholders.
type sqlb struct {
	topicID int
	args    []any
	n       int
}

func newSQL(topicID int) *sqlb {
	b := &sqlb{topicID: topicID}
	if topicID > 0 {
		b.args = append(b.args, topicID)
		b.n = 1
	}
	return b
}

func (b *sqlb) ph(val any) string {
	b.n++
	b.args = append(b.args, val)
	if b.topicID > 0 {
		return fmt.Sprintf("$%d", b.n)
	}
	return "?"
}

func (b *sqlb) topicCols() string {
	if b.topicID > 0 {
		return "topic_id, "
	}
	return ""
}

func (b *sqlb) topicVals() string {
	if b.topicID > 0 {
		return "$1, "
	}
	return ""
}

func (b *sqlb) conflict() string {
	if b.topicID > 0 {
		return "(topic_id, outpoint)"
	}
	return "(outpoint)"
}

type entryFilter struct {
	collectionID string
	outpoint     []byte
	order        string
	limit        int
}

func (l *LookupService) queryEntries(ctx context.Context, topic string, f entryFilter) ([]*Entry, error) {
	ts, err := l.db(topic)
	if err != nil {
		return nil, err
	}
	b := newSQL(ts.TopicID())
	var where []string
	if ts.TopicID() > 0 {
		where = append(where, "topic_id = $1")
	}
	if f.collectionID != "" {
		where = append(where, "collection_id = "+b.ph(f.collectionID))
	}
	if len(f.outpoint) > 0 {
		where = append(where, "outpoint = "+b.ph(f.outpoint))
	}
	q := `SELECT ` + selectCols + ` FROM collection_entries`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, " AND ")
	}
	if f.order != "" {
		q += ` ORDER BY score ` + f.order
	}
	q += limitSQL(f.limit)
	rows, err := ts.DB().QueryContext(ctx, q, b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Entry
	for rows.Next() {
		var (
			opBytes                   []byte
			collectionID              string
			name, signer, contentType sql.NullString
			mintNumber, rank          sql.NullInt64
			mapJSON                   sql.NullString
			score                     float64
		)
		if err := rows.Scan(&opBytes, &collectionID, &name, &signer, &contentType, &mintNumber, &rank, &mapJSON, &score); err != nil {
			return nil, err
		}
		op := transaction.NewOutpointFromBytes(opBytes)
		if op == nil {
			return nil, fmt.Errorf("invalid outpoint bytes in collection_entries")
		}
		e := &Entry{
			Outpoint:     op.OrdinalString(),
			CollectionID: collectionID,
			Signer:       signer.String,
			Score:        score,
		}
		if name.Valid {
			e.Name = name.String
		}
		if contentType.Valid {
			e.ContentType = contentType.String
		}
		if mintNumber.Valid {
			n := int(mintNumber.Int64)
			e.MintNumber = &n
		}
		if rank.Valid {
			n := int(rank.Int64)
			e.Rank = &n
		}
		if mapJSON.Valid && mapJSON.String != "" {
			var m map[string]any
			if json.Unmarshal([]byte(mapJSON.String), &m) == nil {
				e.Map = m
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
