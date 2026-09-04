package ecosystemalias

import (
	"context"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// StoredClaim is the lookup state retained for an admitted ecosystem-alias
// output. Hash pointers returned by a store are independent of its internal
// state and may be safely mutated by the caller.
type StoredClaim struct {
	Outpoint     transaction.Outpoint
	Alias        string
	Domain       string
	Confirmed    bool
	BlockHeight  uint32
	BlockIndex   uint64
	SpendingTxID *chainhash.Hash
}

// ClaimStore persists the query and lifecycle state for ecosystem-alias
// outputs. Implementations must keep conflicting alias and domain claims; the
// outpoint, rather than either claimed value, is the identity of a row.
type ClaimStore interface {
	UpsertClaim(ctx context.Context, claim *StoredClaim) error
	QueryClaims(ctx context.Context, query Query, cursor *Cursor, limit int) ([]StoredClaim, error)
	PlacementForOutpoint(ctx context.Context, outpoint *transaction.Outpoint) (*Placement, error)
	MarkSpent(ctx context.Context, outpoint *transaction.Outpoint, spendingTxID *chainhash.Hash) error
	UpdatePlacementByTxid(ctx context.Context, txid *chainhash.Hash, confirmed bool, height uint32, blockIndex uint64) error
	DeleteOutpoint(ctx context.Context, outpoint *transaction.Outpoint) error
	RollbackTransaction(ctx context.Context, txid *chainhash.Hash) error
}

const sqliteClaimSchema = `
CREATE TABLE IF NOT EXISTS ecosystem_alias_claims (
	txid TEXT NOT NULL,
	vout INTEGER NOT NULL,
	alias TEXT NOT NULL,
	domain TEXT NOT NULL,
	confirmed INTEGER NOT NULL DEFAULT 0,
	block_height INTEGER NOT NULL DEFAULT 0,
	block_index TEXT NOT NULL DEFAULT '00000000000000000000',
	spending_txid TEXT,
	PRIMARY KEY (txid, vout)
);

CREATE INDEX IF NOT EXISTS idx_ecosystem_alias_claims_alias_lookup
	ON ecosystem_alias_claims (
		alias,
		confirmed DESC,
		(CASE WHEN confirmed THEN block_height ELSE 0 END),
		(CASE WHEN confirmed THEN block_index ELSE '00000000000000000000' END),
		txid,
		vout
	) WHERE spending_txid IS NULL;
CREATE INDEX IF NOT EXISTS idx_ecosystem_alias_claims_domain_lookup
	ON ecosystem_alias_claims (
		domain,
		confirmed DESC,
		(CASE WHEN confirmed THEN block_height ELSE 0 END),
		(CASE WHEN confirmed THEN block_index ELSE '00000000000000000000' END),
		txid,
		vout
	) WHERE spending_txid IS NULL;
CREATE INDEX IF NOT EXISTS idx_ecosystem_alias_claims_enumeration
	ON ecosystem_alias_claims (txid, vout) WHERE spending_txid IS NULL;
CREATE INDEX IF NOT EXISTS idx_ecosystem_alias_claims_spender
	ON ecosystem_alias_claims (spending_txid) WHERE spending_txid IS NOT NULL;
`

const postgresClaimSchema = `
CREATE TABLE IF NOT EXISTS ecosystem_alias_claims (
	topic_id INTEGER NOT NULL,
	txid TEXT COLLATE "C" NOT NULL,
	vout BIGINT NOT NULL,
	alias TEXT COLLATE "C" NOT NULL,
	domain TEXT COLLATE "C" NOT NULL,
	confirmed BOOLEAN NOT NULL DEFAULT FALSE,
	block_height BIGINT NOT NULL DEFAULT 0,
	block_index NUMERIC(20, 0) NOT NULL DEFAULT 0,
	spending_txid TEXT COLLATE "C",
	PRIMARY KEY (topic_id, txid, vout)
);

CREATE INDEX IF NOT EXISTS idx_ecosystem_alias_claims_alias_lookup
	ON ecosystem_alias_claims (
		topic_id,
		alias,
		confirmed DESC,
		(CASE WHEN confirmed THEN block_height ELSE 0 END),
		(CASE WHEN confirmed THEN block_index ELSE 0 END),
		txid,
		vout
	) WHERE spending_txid IS NULL;
CREATE INDEX IF NOT EXISTS idx_ecosystem_alias_claims_domain_lookup
	ON ecosystem_alias_claims (
		topic_id,
		domain,
		confirmed DESC,
		(CASE WHEN confirmed THEN block_height ELSE 0 END),
		(CASE WHEN confirmed THEN block_index ELSE 0 END),
		txid,
		vout
	) WHERE spending_txid IS NULL;
CREATE INDEX IF NOT EXISTS idx_ecosystem_alias_claims_enumeration
	ON ecosystem_alias_claims (topic_id, txid, vout) WHERE spending_txid IS NULL;
CREATE INDEX IF NOT EXISTS idx_ecosystem_alias_claims_spender
	ON ecosystem_alias_claims (topic_id, spending_txid) WHERE spending_txid IS NOT NULL;
`
