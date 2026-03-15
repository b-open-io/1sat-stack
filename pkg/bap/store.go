package bap

import "context"

// BAPStore defines the storage interface for BAP identity and attestation data.
type BAPStore interface {
	LoadIdentityById(ctx context.Context, id string) (*Identity, error)
	LoadIdentityByAddress(ctx context.Context, address string) (*Identity, error)
	SaveIdentity(ctx context.Context, identity *Identity) error
	SaveAttestation(ctx context.Context, signer *Signer) error
	RevokeAttestation(ctx context.Context, urnHash, signingAddress string) error
	SaveProfile(ctx context.Context, bapId string, profile map[string]any) error
	LoadProfiles(ctx context.Context, limit, offset int) ([]Identity, error)
	Search(ctx context.Context, query string, limit, offset int) ([]Identity, error)
}

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS bap_identities (
	bap_id TEXT PRIMARY KEY,
	first_seen INTEGER NOT NULL DEFAULT 0,
	first_seen_txid TEXT NOT NULL DEFAULT '',
	root_address TEXT NOT NULL DEFAULT '',
	current_address TEXT NOT NULL DEFAULT '',
	profile TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS bap_identity_addresses (
	bap_id TEXT NOT NULL,
	address TEXT NOT NULL,
	signer TEXT NOT NULL DEFAULT '',
	txid TEXT NOT NULL DEFAULT '',
	block INTEGER NOT NULL DEFAULT 0,
	score REAL NOT NULL DEFAULT 0,
	PRIMARY KEY (bap_id, address),
	FOREIGN KEY (bap_id) REFERENCES bap_identities(bap_id)
);

CREATE TABLE IF NOT EXISTS bap_attestations (
	urn_hash TEXT NOT NULL,
	signing_address TEXT NOT NULL DEFAULT '',
	sequence INTEGER NOT NULL DEFAULT 0,
	block INTEGER NOT NULL DEFAULT 0,
	score REAL NOT NULL DEFAULT 0,
	txid TEXT NOT NULL DEFAULT '',
	timestamp INTEGER NOT NULL DEFAULT 0,
	revoked INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (urn_hash, signing_address)
);

CREATE INDEX IF NOT EXISTS idx_bap_identity_addresses_address ON bap_identity_addresses(address);
CREATE INDEX IF NOT EXISTS idx_bap_identity_addresses_txid ON bap_identity_addresses(txid);
CREATE INDEX IF NOT EXISTS idx_bap_identities_root_address ON bap_identities(root_address);
CREATE INDEX IF NOT EXISTS idx_bap_identities_current_address ON bap_identities(current_address);
CREATE INDEX IF NOT EXISTS idx_bap_attestations_address ON bap_attestations(signing_address);
CREATE INDEX IF NOT EXISTS idx_bap_attestations_txid ON bap_attestations(txid);
`

const postgresSchema = `
CREATE TABLE IF NOT EXISTS bap_identities (
	topic_id INTEGER NOT NULL,
	bap_id TEXT NOT NULL,
	first_seen INTEGER NOT NULL DEFAULT 0,
	first_seen_txid TEXT NOT NULL DEFAULT '',
	root_address TEXT NOT NULL DEFAULT '',
	current_address TEXT NOT NULL DEFAULT '',
	profile JSONB NOT NULL DEFAULT '{}',
	PRIMARY KEY (topic_id, bap_id)
);

CREATE TABLE IF NOT EXISTS bap_identity_addresses (
	topic_id INTEGER NOT NULL,
	bap_id TEXT NOT NULL,
	address TEXT NOT NULL,
	signer TEXT NOT NULL DEFAULT '',
	txid TEXT NOT NULL DEFAULT '',
	block INTEGER NOT NULL DEFAULT 0,
	score REAL NOT NULL DEFAULT 0,
	PRIMARY KEY (topic_id, bap_id, address),
	FOREIGN KEY (topic_id, bap_id) REFERENCES bap_identities(topic_id, bap_id)
);

CREATE TABLE IF NOT EXISTS bap_attestations (
	topic_id INTEGER NOT NULL,
	urn_hash TEXT NOT NULL,
	signing_address TEXT NOT NULL DEFAULT '',
	sequence INTEGER NOT NULL DEFAULT 0,
	block INTEGER NOT NULL DEFAULT 0,
	score REAL NOT NULL DEFAULT 0,
	txid TEXT NOT NULL DEFAULT '',
	timestamp INTEGER NOT NULL DEFAULT 0,
	revoked BOOLEAN NOT NULL DEFAULT FALSE,
	PRIMARY KEY (topic_id, urn_hash, signing_address)
);

CREATE INDEX IF NOT EXISTS idx_bap_identity_addresses_address ON bap_identity_addresses(topic_id, address);
CREATE INDEX IF NOT EXISTS idx_bap_identity_addresses_txid ON bap_identity_addresses(topic_id, txid);
CREATE INDEX IF NOT EXISTS idx_bap_identities_root_address ON bap_identities(topic_id, root_address);
CREATE INDEX IF NOT EXISTS idx_bap_identities_current_address ON bap_identities(topic_id, current_address);
CREATE INDEX IF NOT EXISTS idx_bap_attestations_address ON bap_attestations(topic_id, signing_address);
CREATE INDEX IF NOT EXISTS idx_bap_attestations_txid ON bap_attestations(topic_id, txid);
`
