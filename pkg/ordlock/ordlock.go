package ordlock

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	_ "github.com/mattn/go-sqlite3"
)

const schema = `
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

type OrdLock struct {
	db     *sql.DB
	ordfs  *ordfs.Ordfs
	logger *slog.Logger
}

func New(db *sql.DB, ordfsClient *ordfs.Ordfs, logger *slog.Logger) (*OrdLock, error) {
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("failed to create listings schema: %w", err)
	}
	return &OrdLock{
		db:     db,
		ordfs:  ordfsClient,
		logger: logger,
	}, nil
}

func (o *OrdLock) Close() error {
	return o.db.Close()
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

// SearchListings searches for listings with optional filters.
func (o *OrdLock) SearchListings(ctx context.Context, status, contentType, query string, limit int, from float64, rev bool) ([]*txo.IndexedOutput, error) {
	q := `SELECT outpoint, origin, name, content_type, price, seller, spend_txid, spend_type, score, spend_score FROM listings WHERE `
	var args []any

	scoreCol := "score"
	switch status {
	case "sale", "cancel":
		q += "spend_type = ?"
		args = append(args, status)
		scoreCol = "spend_score"
	default:
		q += "spend_type IS NULL"
	}

	if contentType != "" {
		q += " AND content_type = ?"
		args = append(args, contentType)
	}

	if query != "" {
		q += " AND name LIKE ?"
		args = append(args, query+"%")
	}

	if from > 0 {
		if rev {
			q += fmt.Sprintf(" AND %s < ?", scoreCol)
		} else {
			q += fmt.Sprintf(" AND %s > ?", scoreCol)
		}
		args = append(args, from)
	}

	if rev {
		q += fmt.Sprintf(" ORDER BY %s DESC", scoreCol)
	} else {
		q += fmt.Sprintf(" ORDER BY %s ASC", scoreCol)
	}

	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := o.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*txo.IndexedOutput
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
	rows, err := o.db.QueryContext(ctx,
		`SELECT outpoint, origin, name, content_type, price, seller, spend_txid, spend_type, score, spend_score FROM listings WHERE outpoint = ?`,
		outpoint,
	)
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
	rows, err := o.db.QueryContext(ctx,
		`SELECT outpoint, origin, name, content_type, price, seller, spend_txid, spend_type, score, spend_score FROM listings WHERE origin = ? AND spend_type IS NULL`,
		origin.Bytes(),
	)
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

	placeholders := strings.Repeat("?,", len(origins))
	placeholders = placeholders[:len(placeholders)-1]

	args := make([]any, len(origins))
	for i, op := range origins {
		args[i] = op.Bytes()
	}

	rows, err := o.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT outpoint, origin, name, content_type, price, seller, spend_txid, spend_type, score, spend_score FROM listings WHERE origin IN (%s) AND spend_type IS NULL`, placeholders),
		args...,
	)
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
