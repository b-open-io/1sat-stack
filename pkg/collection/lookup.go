package collection

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Shared mint schema used in every collection-related topic DB.
// Discovery topic stores collections; tm_col_{id} stores items for that id.
// Role is implied by topic — there is no kind column.
const entrySchema = `
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

// LookupService indexes admitted collections and items into per-topic DBs.
type LookupService struct {
	topicDB overlaystorage.Factory
	ready   sync.Map
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
	if _, ok := l.ready.Load(topic); !ok {
		if _, err := ts.DB().Exec(entrySchema); err != nil {
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
		collectionID = NormalizeCollectionID(fields.CollectionID, outpoint)
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

	_, err = ts.DB().ExecContext(ctx, `
		INSERT INTO collection_entries(
			outpoint, collection_id, name, signer, content_type,
			mint_number, rank, map_json, score
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(outpoint) DO UPDATE SET
			collection_id=excluded.collection_id,
			name=excluded.name,
			signer=excluded.signer,
			content_type=excluded.content_type,
			mint_number=excluded.mint_number,
			rank=excluded.rank,
			map_json=excluded.map_json,
			score=excluded.score
	`, outpoint.Bytes(), collectionID, nullStr(fields.Name), sigma.SignerAddress,
		nullStr(ContentType(out.LockingScript)), mintNumber, rank, nullStr(mapJSON), score)
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

// ListCollections returns collections from the discovery topic.
func (l *LookupService) ListCollections(ctx context.Context, limit int, reverse bool) ([]*Entry, error) {
	return l.queryEntries(ctx, DiscoveryTopic,
		`SELECT `+selectCols+` FROM collection_entries ORDER BY score `+orderSQL(reverse)+limitSQL(limit))
}

// GetCollection returns a collection by collectionId (its outpoint) from discovery storage.
func (l *LookupService) GetCollection(ctx context.Context, collectionID string) (*Entry, error) {
	entries, err := l.queryEntries(ctx, DiscoveryTopic,
		`SELECT `+selectCols+` FROM collection_entries WHERE collection_id = ? LIMIT 1`, collectionID)
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
	topic := ItemTopic(collectionID)
	return l.queryEntries(ctx, topic,
		`SELECT `+selectCols+` FROM collection_entries WHERE collection_id = ? ORDER BY score `+orderSQL(reverse)+limitSQL(limit),
		collectionID)
}

// GetItem returns a single item by outpoint within a collection topic.
// outpointStr may be "txid.vout" or ordinal "txid_vout".
func (l *LookupService) GetItem(ctx context.Context, collectionID, outpointStr string) (*Entry, error) {
	op, err := parseOutpoint(outpointStr)
	if err != nil {
		return nil, fmt.Errorf("invalid outpoint: %w", err)
	}
	topic := ItemTopic(collectionID)
	entries, err := l.queryEntries(ctx, topic,
		`SELECT `+selectCols+` FROM collection_entries WHERE outpoint = ? LIMIT 1`, op.Bytes())
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

func (l *LookupService) queryEntries(ctx context.Context, topic, q string, args ...any) ([]*Entry, error) {
	ts, err := l.db(topic)
	if err != nil {
		return nil, err
	}
	rows, err := ts.DB().QueryContext(ctx, q, args...)
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
