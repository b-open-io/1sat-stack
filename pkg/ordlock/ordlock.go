package ordlock

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	_ "github.com/mattn/go-sqlite3"
)

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS listings (
    outpoint      BLOB PRIMARY KEY,
    origin        BLOB,
    name          TEXT,
    content_type  TEXT,
    price         INTEGER NOT NULL,
    seller        TEXT NOT NULL,
    spend_txid    BLOB,
    spend_type    TEXT,
    spend_score   REAL,
    score         REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_listings_search ON listings(spend_type, content_type, name COLLATE NOCASE, score);
CREATE INDEX IF NOT EXISTS idx_listings_name ON listings(spend_type, name COLLATE NOCASE);
CREATE INDEX IF NOT EXISTS idx_listings_origin ON listings(origin, spend_type);
CREATE INDEX IF NOT EXISTS idx_listings_sales ON listings(spend_type, spend_score);
`

const postgresSchema = `
CREATE TABLE IF NOT EXISTS listings (
    topic_id      INTEGER NOT NULL,
    outpoint      BYTEA NOT NULL,
    origin        BYTEA,
    name          TEXT,
    content_type  TEXT,
    price         BIGINT NOT NULL,
    seller        TEXT NOT NULL,
    spend_txid    BYTEA,
    spend_type    TEXT,
    spend_score   DOUBLE PRECISION,
    score         DOUBLE PRECISION NOT NULL,
    PRIMARY KEY (topic_id, outpoint)
);
CREATE INDEX IF NOT EXISTS idx_listings_search ON listings(topic_id, spend_type, content_type, name, score);
CREATE INDEX IF NOT EXISTS idx_listings_origin ON listings(topic_id, origin, spend_type);
CREATE INDEX IF NOT EXISTS idx_listings_sales ON listings(topic_id, spend_type, spend_score);
`

type OrdLock struct {
	db      *sql.DB
	topicID int
	ordfs   *ordfs.Ordfs
	logger  *slog.Logger
	once    sync.Once
	initErr error
}

func New(db *sql.DB, topicID int, ordfsClient *ordfs.Ordfs, logger *slog.Logger) *OrdLock {
	return &OrdLock{
		db:      db,
		topicID: topicID,
		ordfs:   ordfsClient,
		logger:  logger,
	}
}

func (o *OrdLock) ensureSchema() error {
	o.once.Do(func() {
		schema := sqliteSchema
		if o.topicID > 0 {
			schema = postgresSchema
		}
		_, o.initErr = o.db.Exec(schema)
	})
	return o.initErr
}

func (o *OrdLock) Close() error {
	return o.db.Close()
}

// qb is a query builder that handles placeholder numbering and topic_id scoping.
type qb struct {
	topicID int
	args    []any
	n       int
}

func (o *OrdLock) newQB() *qb {
	q := &qb{topicID: o.topicID}
	if o.topicID > 0 {
		q.args = append(q.args, o.topicID)
		q.n = 1
	}
	return q
}

func (q *qb) ph(val any) string {
	q.n++
	q.args = append(q.args, val)
	if q.topicID > 0 {
		return fmt.Sprintf("$%d", q.n)
	}
	return "?"
}

func (q *qb) topicWhere() string {
	if q.topicID > 0 {
		return "topic_id = $1 AND "
	}
	return ""
}

func (q *qb) topicCols() string {
	if q.topicID > 0 {
		return "topic_id, "
	}
	return ""
}

func (q *qb) topicVals() string {
	if q.topicID > 0 {
		return "$1, "
	}
	return ""
}

func (q *qb) conflictTarget() string {
	if q.topicID > 0 {
		return "(topic_id, outpoint)"
	}
	return "(outpoint)"
}

type listingData struct {
	origin      *transaction.Outpoint
	name        string
	contentType string
	price       uint64
	seller      string
}

// classifySpend determines whether a spend is a sale or cancel by examining
// the unlocking script. The OrdLock contract uses the last byte as a branch
// selector: OP_0 (0x00) for purchase, OP_1 (0x51) for cancel.
func classifySpend(unlockingScript *script.Script) string {
	if unlockingScript == nil {
		return "unknown"
	}
	raw := []byte(*unlockingScript)
	if len(raw) == 0 {
		return "unknown"
	}
	switch raw[len(raw)-1] {
	case 0x00:
		return "sale"
	case 0x51:
		return "cancel"
	default:
		return "unknown"
	}
}

func (o *OrdLock) UpsertListing(ctx context.Context, outpoint *transaction.Outpoint, ld *listingData, score float64) error {
	if err := o.ensureSchema(); err != nil {
		return err
	}
	q := o.newQB()
	query := fmt.Sprintf(
		`INSERT INTO listings (%soutpoint, origin, name, content_type, price, seller, score)
		VALUES (%s%s, %s, %s, %s, %s, %s, %s)
		ON CONFLICT %s DO UPDATE SET
			origin = EXCLUDED.origin,
			name = EXCLUDED.name,
			content_type = EXCLUDED.content_type,
			price = EXCLUDED.price,
			seller = EXCLUDED.seller,
			score = EXCLUDED.score`,
		q.topicCols(),
		q.topicVals(),
		q.ph(outpoint.Bytes()), q.ph(ld.origin.Bytes()), q.ph(ld.name), q.ph(ld.contentType),
		q.ph(ld.price), q.ph(ld.seller), q.ph(score),
		q.conflictTarget(),
	)
	_, err := o.db.ExecContext(ctx, query, q.args...)
	return err
}

func (o *OrdLock) MarkSpent(ctx context.Context, outpoint *transaction.Outpoint, spendTxid *chainhash.Hash, spendType string, spendScore float64) error {
	if err := o.ensureSchema(); err != nil {
		return err
	}
	q := o.newQB()
	query := fmt.Sprintf(
		`UPDATE listings SET spend_txid = %s, spend_type = %s, spend_score = %s WHERE %soutpoint = %s`,
		q.ph(spendTxid[:]), q.ph(spendType), q.ph(spendScore),
		q.topicWhere(), q.ph(outpoint.Bytes()),
	)
	_, err := o.db.ExecContext(ctx, query, q.args...)
	return err
}

func (o *OrdLock) DeleteListing(ctx context.Context, outpoint *transaction.Outpoint) error {
	if err := o.ensureSchema(); err != nil {
		return err
	}
	q := o.newQB()
	query := fmt.Sprintf(`DELETE FROM listings WHERE %soutpoint = %s`, q.topicWhere(), q.ph(outpoint.Bytes()))
	_, err := o.db.ExecContext(ctx, query, q.args...)
	return err
}

const listingCols = `outpoint, origin, name, content_type, price, seller, spend_txid, spend_type, score, spend_score`

func (o *OrdLock) SearchListings(ctx context.Context, status, contentType, query string, limit int, from float64, rev bool) ([]*txo.IndexedOutput, error) {
	if err := o.ensureSchema(); err != nil {
		return nil, err
	}
	qb := o.newQB()
	sql := fmt.Sprintf(`SELECT %s FROM listings WHERE %s`, listingCols, qb.topicWhere())

	scoreCol := "score"
	switch status {
	case "sale", "cancel":
		sql += fmt.Sprintf("spend_type = %s", qb.ph(status))
		scoreCol = "spend_score"
	default:
		sql += "spend_type IS NULL"
	}

	if contentType != "" {
		sql += fmt.Sprintf(" AND content_type = %s", qb.ph(contentType))
	}

	if query != "" {
		if o.topicID > 0 {
			sql += fmt.Sprintf(" AND name ILIKE %s", qb.ph(query+"%"))
		} else {
			sql += fmt.Sprintf(" AND name LIKE %s", qb.ph(query+"%"))
		}
	}

	if from > 0 {
		if rev {
			sql += fmt.Sprintf(" AND %s < %s", scoreCol, qb.ph(from))
		} else {
			sql += fmt.Sprintf(" AND %s > %s", scoreCol, qb.ph(from))
		}
	}

	if rev {
		sql += fmt.Sprintf(" ORDER BY %s DESC", scoreCol)
	} else {
		sql += fmt.Sprintf(" ORDER BY %s ASC", scoreCol)
	}

	if limit > 0 {
		sql += fmt.Sprintf(" LIMIT %s", qb.ph(limit))
	}

	rows, err := o.db.QueryContext(ctx, sql, qb.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Non-nil so a no-match search marshals to [] rather than null; clients
	// type this as an array and a nil slice breaks them.
	results := make([]*txo.IndexedOutput, 0)
	for rows.Next() {
		out, err := scanListing(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, out)
	}
	return results, rows.Err()
}

func (o *OrdLock) GetListing(ctx context.Context, outpoint []byte) (*txo.IndexedOutput, error) {
	if err := o.ensureSchema(); err != nil {
		return nil, err
	}
	qb := o.newQB()
	query := fmt.Sprintf(`SELECT %s FROM listings WHERE %soutpoint = %s`, listingCols, qb.topicWhere(), qb.ph(outpoint))
	rows, err := o.db.QueryContext(ctx, query, qb.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	return scanListing(rows)
}

func (o *OrdLock) GetListingByOrigin(ctx context.Context, origin *transaction.Outpoint) (*txo.IndexedOutput, error) {
	if err := o.ensureSchema(); err != nil {
		return nil, err
	}
	qb := o.newQB()
	query := fmt.Sprintf(`SELECT %s FROM listings WHERE %sorigin = %s AND spend_type IS NULL`, listingCols, qb.topicWhere(), qb.ph(origin.Bytes()))
	rows, err := o.db.QueryContext(ctx, query, qb.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	return scanListing(rows)
}

func (o *OrdLock) GetListingsByOrigins(ctx context.Context, origins []*transaction.Outpoint) (map[string]*txo.IndexedOutput, error) {
	if len(origins) == 0 {
		return nil, nil
	}
	if err := o.ensureSchema(); err != nil {
		return nil, err
	}

	qb := o.newQB()
	phs := make([]string, len(origins))
	for i, op := range origins {
		phs[i] = qb.ph(op.Bytes())
	}

	query := fmt.Sprintf(`SELECT %s FROM listings WHERE %sorigin IN (%s) AND spend_type IS NULL`,
		listingCols, qb.topicWhere(), strings.Join(phs, ","))

	rows, err := o.db.QueryContext(ctx, query, qb.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make(map[string]*txo.IndexedOutput)
	for rows.Next() {
		out, err := scanListing(rows)
		if err != nil {
			return nil, err
		}
		if listing, ok := out.Data["ordlock"].(map[string]any); ok {
			if origin, ok := listing["origin"].(string); ok {
				results[origin] = out
			}
		}
	}
	return results, rows.Err()
}

func scanListing(rows *sql.Rows) (*txo.IndexedOutput, error) {
	var opBytes, originBytes, spendBytes []byte
	var name, contentType, seller, spendType sql.NullString
	var price uint64
	var score, spendScore sql.NullFloat64

	if err := rows.Scan(&opBytes, &originBytes, &name, &contentType, &price, &seller, &spendBytes, &spendType, &score, &spendScore); err != nil {
		return nil, err
	}

	outpoint := transaction.NewOutpointFromBytes(opBytes)
	if outpoint == nil {
		return nil, fmt.Errorf("invalid outpoint bytes: %x", opBytes)
	}

	out := &txo.IndexedOutput{
		Outpoint: *outpoint,
		Score:    score.Float64,
	}

	if len(spendBytes) == 32 {
		out.SpendTxid = &chainhash.Hash{}
		copy(out.SpendTxid[:], spendBytes)
	}

	listing := map[string]any{
		"price":  price,
		"seller": seller.String,
	}
	if spendScore.Valid {
		listing["spend_score"] = spendScore.Float64
	}
	if origin := transaction.NewOutpointFromBytes(originBytes); origin != nil {
		listing["origin"] = origin.String()
	}
	if name.Valid {
		listing["name"] = name.String
	}
	if contentType.Valid {
		listing["content_type"] = contentType.String
	}
	if spendType.Valid {
		listing["spend_type"] = spendType.String
	}

	out.Data = map[string]any{
		"ordlock": listing,
	}

	return out, nil
}
