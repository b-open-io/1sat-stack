package lookup

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/b-open-io/1sat-stack/pkg/parse"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/template/bsv21"
	"github.com/b-open-io/1sat-stack/pkg/template/cosign"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
)

const bsv21Schema = `
CREATE TABLE IF NOT EXISTS token_outputs (
    outpoint     BLOB PRIMARY KEY,
    token_id     TEXT NOT NULL,
    op           TEXT NOT NULL,
    lock_type    TEXT NOT NULL,
    address      TEXT NOT NULL,
    amount       TEXT NOT NULL,
    sym          TEXT,
    dec          INTEGER,
    icon         TEXT,
    spend_txid   BLOB,
    score        REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_token_utxos ON token_outputs(token_id, lock_type, address, score) WHERE spend_txid IS NULL;
CREATE INDEX IF NOT EXISTS idx_token_history ON token_outputs(token_id, lock_type, address, score);
CREATE INDEX IF NOT EXISTS idx_token_deploy ON token_outputs(token_id) WHERE op IN ('deploy+mint', 'deploy+auth');
`

// BSV21Lookup implements the LookupService interface for BSV21.
// Uses the overlay storage factory to resolve per-topic databases.
// Each topic's database gets its own token_outputs table.
type BSV21Lookup struct {
	topicDB     overlaystorage.Factory
	outputStore *txo.OutputStore
	ready       sync.Map // tracks which topics have had schema created
	mintCache   sync.Map
}

// NewBSV21Lookup creates a new BSV21 lookup service backed by per-topic overlay storage.
func NewBSV21Lookup(topicDB overlaystorage.Factory, outputStores ...*txo.OutputStore) *BSV21Lookup {
	lookup := &BSV21Lookup{topicDB: topicDB}
	if len(outputStores) > 0 {
		lookup.outputStore = outputStores[0]
	}
	return lookup
}

// db resolves the TopicStorage for a topic and ensures the token_outputs schema exists.
func (l *BSV21Lookup) db(topic string) (overlaystorage.TopicStorage, error) {
	ts, err := l.topicDB(topic)
	if err != nil {
		return nil, err
	}
	if _, ok := l.ready.Load(topic); !ok {
		if _, err := ts.DB().Exec(bsv21Schema); err != nil {
			return nil, fmt.Errorf("failed to create token_outputs schema for %s: %w", topic, err)
		}
		l.ready.Store(topic, true)
	}
	return ts, nil
}

// tokenDB is a convenience for resolving a per-token topic database.
func (l *BSV21Lookup) tokenDB(tokenId string) (overlaystorage.TopicStorage, error) {
	return l.db("tm_" + tokenId)
}

// OutputAdmittedByTopic indexes a newly admitted BSV21 output into the per-topic token_outputs table.
func (l *BSV21Lookup) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
	_, tx, txid, err := transaction.ParseBeef(payload.AtomicBEEF)
	if err != nil {
		return err
	}

	if int(payload.OutputIndex) >= len(tx.Outputs) {
		return nil
	}

	outpoint := &transaction.Outpoint{
		Txid:  *txid,
		Index: payload.OutputIndex,
	}

	b := bsv21.Decode(tx.Outputs[int(payload.OutputIndex)].LockingScript)
	if b == nil {
		return nil
	}

	if b.Op == string(bsv21.OpDeployMint) || b.Op == string(bsv21.OpDeployAuth) {
		b.Id = outpoint.OrdinalString()
	}

	score := types.ScoreFromTx(tx, txid)

	var lockType, address string
	suffix := script.NewFromBytes(b.Insc.ScriptSuffix)
	if p := p2pkh.Decode(suffix, true); p != nil {
		lockType = "p2pkh"
		address = p.AddressString
	} else if c := cosign.Decode(suffix); c != nil {
		lockType = "cos"
		address = c.Address
	} else {
		return nil
	}

	ts, err := l.db(payload.Topic)
	if err != nil {
		return err
	}

	_, err = ts.DB().ExecContext(ctx,
		`INSERT OR REPLACE INTO token_outputs (outpoint, token_id, op, lock_type, address, amount, sym, dec, icon, score) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		outpoint.Bytes(), b.Id, b.Op, lockType, address, strconv.FormatUint(b.Amt, 10), b.Symbol, b.Decimals, b.Icon, score,
	)
	return err
}

// OutputSpent marks a BSV21 output as spent in the per-topic token_outputs table.
func (l *BSV21Lookup) OutputSpent(ctx context.Context, payload *engine.OutputSpent) error {
	_, tx, txid, err := transaction.ParseBeef(payload.SpendingAtomicBEEF)
	if err != nil {
		return err
	}

	if int(payload.InputIndex) >= len(tx.Inputs) {
		return nil
	}

	ts, err := l.db(payload.Topic)
	if err != nil {
		return err
	}

	_, err = ts.DB().ExecContext(ctx,
		`UPDATE token_outputs SET spend_txid = ? WHERE outpoint = ?`,
		txid[:], payload.Outpoint.Bytes(),
	)
	return err
}

// OutputNoLongerRetainedInHistory is called when historical retention is no longer required.
func (l *BSV21Lookup) OutputNoLongerRetainedInHistory(ctx context.Context, outpoint *transaction.Outpoint, topic string) error {
	return nil
}

// OutputEvicted permanently removes a BSV21 output from the index.
func (l *BSV21Lookup) OutputEvicted(ctx context.Context, outpoint *transaction.Outpoint) error {
	// No topic context — this is a best-effort cleanup.
	// In practice, the engine calls DeleteOutput (which cleans the outputs table)
	// before calling OutputEvicted on lookup services.
	return nil
}

// OutputBlockHeightUpdated is called when a transaction's block height is updated.
func (l *BSV21Lookup) OutputBlockHeightUpdated(ctx context.Context, txid *chainhash.Hash, blockHeight uint32, blockIndex uint64) error {
	return nil
}

// Lookup handles generic lookup queries.
func (l *BSV21Lookup) Lookup(ctx context.Context, question *lookup.LookupQuestion) (*lookup.LookupAnswer, error) {
	return &lookup.LookupAnswer{
		Type: lookup.AnswerTypeFormula,
	}, nil
}

// GetDocumentation returns documentation for this lookup service.
func (l *BSV21Lookup) GetDocumentation() string {
	return "BSV21 Lookup Service"
}

// GetMetaData returns metadata for this lookup service.
func (l *BSV21Lookup) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "BSV21",
	}
}

// --- Token discovery (for TokenManager) ---

// TokenInfo holds token identity and metadata from the discovery topic.
type TokenInfo struct {
	TokenID  string
	Symbol   *string
	Decimals *uint8
	Icon     *string
}

// ListTokens returns all known tokens with their metadata from the discovery topic.
func (l *BSV21Lookup) ListTokens(ctx context.Context) ([]*TokenInfo, error) {
	ts, err := l.db("tm_bsv21")
	if err != nil {
		return nil, err
	}

	rows, err := ts.DB().QueryContext(ctx, `SELECT token_id, sym, dec, icon FROM token_outputs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []*TokenInfo
	for rows.Next() {
		var id string
		var sym, icon sql.NullString
		var dec sql.NullInt64
		if err := rows.Scan(&id, &sym, &dec, &icon); err != nil {
			return nil, err
		}
		t := &TokenInfo{TokenID: id}
		if sym.Valid {
			t.Symbol = &sym.String
		}
		if dec.Valid {
			d := uint8(dec.Int64)
			t.Decimals = &d
		}
		if icon.Valid {
			t.Icon = &icon.String
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// CountOutputs returns the count of unspent outputs in a topic's token_outputs table.
func (l *BSV21Lookup) CountOutputs(ctx context.Context, topic string) (int64, error) {
	ts, err := l.db(topic)
	if err != nil {
		return 0, err
	}

	var count int64
	err = ts.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM token_outputs WHERE spend_txid IS NULL`,
	).Scan(&count)
	return count, err
}

// --- Per-token queries (routes use these with tokenId) ---

// SearchUTXOs searches for unspent token outputs.
func (l *BSV21Lookup) SearchUTXOs(ctx context.Context, tokenId, lockType, address string, cfg *store.SearchCfg) ([]*transaction.Outpoint, error) {
	return l.searchOutpoints(ctx, tokenId, lockType, address, true, cfg)
}

// SearchMultiUTXOs searches for unspent outputs across multiple addresses.
func (l *BSV21Lookup) SearchMultiUTXOs(ctx context.Context, tokenId, lockType string, addresses []string, cfg *store.SearchCfg) ([]*transaction.Outpoint, error) {
	return l.searchMultiOutpoints(ctx, tokenId, lockType, addresses, true, cfg)
}

// SearchHistory searches for all outputs (including spent).
func (l *BSV21Lookup) SearchHistory(ctx context.Context, tokenId, lockType, address string, cfg *store.SearchCfg) ([]*transaction.Outpoint, error) {
	return l.searchOutpoints(ctx, tokenId, lockType, address, false, cfg)
}

// SearchMultiHistory searches history across multiple addresses.
func (l *BSV21Lookup) SearchMultiHistory(ctx context.Context, tokenId, lockType string, addresses []string, cfg *store.SearchCfg) ([]*transaction.Outpoint, error) {
	return l.searchMultiOutpoints(ctx, tokenId, lockType, addresses, false, cfg)
}

// GetBalance calculates the total balance of BSV21 tokens for an address.
func (l *BSV21Lookup) GetBalance(ctx context.Context, tokenId, lockType, address string) (uint64, int, error) {
	ts, err := l.tokenDB(tokenId)
	if err != nil {
		return 0, 0, err
	}

	rows, err := ts.DB().QueryContext(ctx,
		`SELECT amount FROM token_outputs WHERE token_id = ? AND lock_type = ? AND address = ? AND spend_txid IS NULL`,
		tokenId, lockType, address,
	)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	var total uint64
	var count int
	for rows.Next() {
		var amtStr string
		if err := rows.Scan(&amtStr); err != nil {
			return 0, 0, err
		}
		amt, err := strconv.ParseUint(amtStr, 10, 64)
		if err != nil {
			return 0, 0, err
		}
		total += amt
		count++
	}
	return total, count, rows.Err()
}

// GetMultiBalance calculates the total balance across multiple addresses.
func (l *BSV21Lookup) GetMultiBalance(ctx context.Context, tokenId, lockType string, addresses []string) (uint64, int, error) {
	if len(addresses) == 0 {
		return 0, 0, nil
	}

	ts, err := l.tokenDB(tokenId)
	if err != nil {
		return 0, 0, err
	}

	query := `SELECT amount FROM token_outputs WHERE token_id = ? AND lock_type = ? AND spend_txid IS NULL AND address IN (`
	args := []any{tokenId, lockType}
	for i, addr := range addresses {
		if i > 0 {
			query += ","
		}
		query += "?"
		args = append(args, addr)
	}
	query += ")"

	rows, err := ts.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	var total uint64
	var count int
	for rows.Next() {
		var amtStr string
		if err := rows.Scan(&amtStr); err != nil {
			return 0, 0, err
		}
		amt, err := strconv.ParseUint(amtStr, 10, 64)
		if err != nil {
			return 0, 0, err
		}
		total += amt
		count++
	}
	return total, count, rows.Err()
}

// GetToken returns the parsed BSV21 deploy data for a specific token.
// Queries tm_bsv21 (discovery topic) since that's where deploys are stored.
func (l *BSV21Lookup) GetToken(ctx context.Context, outpoint *transaction.Outpoint) (*parse.BSV21, error) {
	tokenId := outpoint.OrdinalString()

	if cached, ok := l.mintCache.Load(tokenId); ok {
		return cached.(*parse.BSV21), nil
	}

	ts, err := l.db("tm_bsv21")
	if err != nil {
		return nil, err
	}

	var op string
	var amtStr string
	var sym, icon sql.NullString
	var dec sql.NullInt64

	err = ts.DB().QueryRowContext(ctx,
		`SELECT op, amount, sym, dec, icon FROM token_outputs WHERE token_id = ? LIMIT 1`,
		tokenId,
	).Scan(&op, &amtStr, &sym, &dec, &icon)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("token not found")
	}
	if err != nil {
		return nil, err
	}

	amount, err := strconv.ParseUint(amtStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid amount %q: %w", amtStr, err)
	}

	token := &parse.BSV21{
		Id:  tokenId,
		Op:  op,
		Amt: amount,
	}
	if sym.Valid {
		token.Symbol = &sym.String
	}
	if dec.Valid {
		d := uint8(dec.Int64)
		token.Decimals = &d
	}
	if icon.Valid {
		token.Icon = &icon.String
	}

	l.mintCache.Store(tokenId, token)
	return token, nil
}

// ResolveCollection loads a BSV-21 deploy from the general TXO index and
// returns the normalized collectionId from its collectionItem MAP metadata.
func (l *BSV21Lookup) ResolveCollection(ctx context.Context, tokenId string) (string, error) {
	if l.outputStore == nil {
		return "", fmt.Errorf("general txo store is not configured")
	}
	outpoint, err := transaction.OutpointFromString(tokenId)
	if err != nil {
		return "", fmt.Errorf("invalid token ID %q: %w", tokenId, err)
	}

	output, err := l.outputStore.LoadOutput(ctx, outpoint, &txo.OutputSearchCfg{
		IncludeTags: []string{parse.TagMAP},
	})
	if err != nil {
		return "", err
	}
	if output == nil || output.Data == nil {
		return "", fmt.Errorf("token collection not found")
	}

	rawMAP, ok := output.Data[parse.TagMAP]
	if !ok {
		return "", fmt.Errorf("token collection not found")
	}
	encoded, err := json.Marshal(rawMAP)
	if err != nil {
		return "", fmt.Errorf("failed to encode token MAP: %w", err)
	}
	var mapData struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(encoded, &mapData); err != nil {
		return "", fmt.Errorf("failed to decode token MAP: %w", err)
	}
	if mapData.Data["subType"] != "collectionItem" {
		return "", fmt.Errorf("token collection not found")
	}

	var subTypeData struct {
		CollectionID string `json:"collectionId"`
	}
	if err := json.Unmarshal([]byte(mapData.Data["subTypeData"]), &subTypeData); err != nil {
		return "", fmt.Errorf("failed to decode token collection metadata: %w", err)
	}
	if subTypeData.CollectionID == "" {
		return "", fmt.Errorf("token collection not found")
	}
	return parse.NormalizeCollectionID(subTypeData.CollectionID, outpoint), nil
}

// LoadOutputs loads full output data for a list of outpoints from a token's database.
func (l *BSV21Lookup) LoadOutputs(ctx context.Context, tokenId string, outpoints []*transaction.Outpoint) ([]*txo.IndexedOutput, error) {
	if len(outpoints) == 0 {
		return nil, nil
	}

	ts, err := l.tokenDB(tokenId)
	if err != nil {
		return nil, err
	}

	query := `SELECT outpoint, token_id, op, lock_type, address, amount, sym, dec, icon, spend_txid, score FROM token_outputs WHERE outpoint IN (`
	args := make([]any, len(outpoints))
	for i, op := range outpoints {
		if i > 0 {
			query += ","
		}
		query += "?"
		args[i] = op.Bytes()
	}
	query += ")"

	rows, err := ts.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*txo.IndexedOutput
	for rows.Next() {
		out, err := scanTokenOutput(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, out)
	}
	return results, rows.Err()
}

// FindByTxid finds all token outputs for a given transaction by joining
// the overlay's outputs table (which has a txid column) with token_outputs.
func (l *BSV21Lookup) FindByTxid(ctx context.Context, tokenId string, txid *chainhash.Hash) ([]*txo.IndexedOutput, error) {
	ts, err := l.tokenDB(tokenId)
	if err != nil {
		return nil, err
	}

	rows, err := ts.DB().QueryContext(ctx,
		`SELECT t.outpoint, t.token_id, t.op, t.lock_type, t.address, t.amount, t.sym, t.dec, t.icon, t.spend_txid, t.score
		FROM token_outputs t
		JOIN outputs o ON t.outpoint = o.outpoint
		WHERE o.txid = ?`,
		txid[:],
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*txo.IndexedOutput
	for rows.Next() {
		out, err := scanTokenOutput(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, out)
	}
	return results, rows.Err()
}

// GetInputsConsumed reads the inputs_consumed blob from the overlay's outputs table.
func (l *BSV21Lookup) GetInputsConsumed(ctx context.Context, tokenId string, outpoint *transaction.Outpoint) ([]*transaction.Outpoint, error) {
	ts, err := l.tokenDB(tokenId)
	if err != nil {
		return nil, err
	}

	var data []byte
	err = ts.DB().QueryRowContext(ctx,
		`SELECT inputs_consumed FROM outputs WHERE outpoint = ?`,
		outpoint.Bytes(),
	).Scan(&data)
	if err == sql.ErrNoRows || len(data) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data)%36 != 0 {
		return nil, nil
	}

	inputs := make([]*transaction.Outpoint, len(data)/36)
	for i := range inputs {
		inputs[i] = transaction.NewOutpointFromBytes(data[i*36 : (i+1)*36])
	}
	return inputs, nil
}

// searchOutpoints queries token_outputs for outpoints matching the criteria.
func (l *BSV21Lookup) searchOutpoints(ctx context.Context, tokenId, lockType, address string, unspentOnly bool, cfg *store.SearchCfg) ([]*transaction.Outpoint, error) {
	ts, err := l.tokenDB(tokenId)
	if err != nil {
		return nil, err
	}

	query := `SELECT outpoint FROM token_outputs WHERE token_id = ? AND lock_type = ? AND address = ?`
	args := []any{tokenId, lockType, address}

	if unspentOnly {
		query += " AND spend_txid IS NULL"
	}

	query, args = applySearchOpts(query, args, cfg)

	rows, err := ts.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanOutpoints(rows)
}

// searchMultiOutpoints queries across multiple addresses.
func (l *BSV21Lookup) searchMultiOutpoints(ctx context.Context, tokenId, lockType string, addresses []string, unspentOnly bool, cfg *store.SearchCfg) ([]*transaction.Outpoint, error) {
	if len(addresses) == 0 {
		return nil, nil
	}

	ts, err := l.tokenDB(tokenId)
	if err != nil {
		return nil, err
	}

	query := `SELECT outpoint FROM token_outputs WHERE token_id = ? AND lock_type = ? AND address IN (`
	args := []any{tokenId, lockType}
	for i, addr := range addresses {
		if i > 0 {
			query += ","
		}
		query += "?"
		args = append(args, addr)
	}
	query += ")"

	if unspentOnly {
		query += " AND spend_txid IS NULL"
	}

	query, args = applySearchOpts(query, args, cfg)

	rows, err := ts.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanOutpoints(rows)
}

// applySearchOpts adds ORDER BY, LIMIT, and score range filters to a query.
func applySearchOpts(query string, args []any, cfg *store.SearchCfg) (string, []any) {
	if cfg.From != nil {
		query += " AND score > ?"
		args = append(args, *cfg.From)
	}
	if cfg.To != nil {
		query += " AND score < ?"
		args = append(args, *cfg.To)
	}

	if cfg.Reverse {
		query += " ORDER BY score DESC"
	} else {
		query += " ORDER BY score ASC"
	}

	if cfg.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, cfg.Limit)
	}

	return query, args
}

// scanOutpoints reads outpoint blobs from rows and converts to Outpoint pointers.
func scanOutpoints(rows *sql.Rows) ([]*transaction.Outpoint, error) {
	var results []*transaction.Outpoint
	for rows.Next() {
		var opBytes []byte
		if err := rows.Scan(&opBytes); err != nil {
			return nil, err
		}
		if op := transaction.NewOutpointFromBytes(opBytes); op != nil {
			results = append(results, op)
		}
	}
	return results, rows.Err()
}

// scanTokenOutput scans a single row from token_outputs into an IndexedOutput
// with Data["bsv21"] populated for API compatibility.
func scanTokenOutput(rows *sql.Rows) (*txo.IndexedOutput, error) {
	var opBytes, spendBytes []byte
	var tokenId, op, lockType, address, amtStr string
	var sym, icon sql.NullString
	var dec sql.NullInt64
	var score float64

	if err := rows.Scan(&opBytes, &tokenId, &op, &lockType, &address, &amtStr, &sym, &dec, &icon, &spendBytes, &score); err != nil {
		return nil, err
	}

	outpoint := transaction.NewOutpointFromBytes(opBytes)
	if outpoint == nil {
		return nil, fmt.Errorf("invalid outpoint bytes: %x", opBytes)
	}

	out := &txo.IndexedOutput{
		Outpoint: *outpoint,
		Score:    score,
	}

	if len(spendBytes) == 32 {
		out.SpendTxid = &chainhash.Hash{}
		copy(out.SpendTxid[:], spendBytes)
	}

	bsv21Data := map[string]any{
		"id":  tokenId,
		"op":  op,
		"amt": amtStr,
	}
	if sym.Valid {
		bsv21Data["sym"] = sym.String
	}
	if dec.Valid {
		bsv21Data["dec"] = strconv.FormatUint(uint64(dec.Int64), 10)
	}
	if icon.Valid {
		bsv21Data["icon"] = icon.String
	}

	out.Data = map[string]any{
		"bsv21": bsv21Data,
	}

	return out, nil
}
