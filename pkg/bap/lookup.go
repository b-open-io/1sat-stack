package bap

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bitcoin-sv/go-templates/template/bitcom"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// LookupService implements the engine.LookupService interface for BAP identities.
type LookupService struct {
	store BAPStore
}

// NewLookupService creates a new BAP lookup service backed by the provided store.
func NewLookupService(store BAPStore) *LookupService {
	return &LookupService{store: store}
}

// OutputAdmittedByTopic stores raw BAP facts. Authority is resolved at query time.
func (l *LookupService) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
	_, tx, txid, err := transaction.ParseBeef(payload.AtomicBEEF)
	if err != nil {
		return err
	}
	output := tx.Outputs[payload.OutputIndex]
	bc := bitcom.Decode(output.LockingScript)
	if bc == nil {
		return nil
	}

	score := types.ScoreFromTx(tx, txid)
	var height uint32
	if tx.MerklePath != nil {
		height = tx.MerklePath.BlockHeight
	}

	bap := bitcom.DecodeBAP(bc)
	if bap == nil {
		return nil
	}
	var aip *bitcom.AIP
	for _, a := range bitcom.DecodeAIP(bc) {
		if a.Valid {
			aip = a
			break
		}
	}
	if aip == nil {
		return nil
	}

	txidStr := txid.String()

	switch bap.Type {
	case bitcom.ID:
		if _, err := script.NewAddressFromString(bap.Address); err != nil {
			return nil
		}
		id, err := l.store.LoadIdentityById(ctx, bap.IDKey)
		if err != nil {
			return err
		}
		if id == nil {
			id = &Identity{
				BapId:          bap.IDKey,
				RootAddress:    aip.Address,
				CurrentAddress: bap.Address,
				Addresses:      []Address{},
				FirstSeen:      height,
				FirstSeenTxid:  txidStr,
			}
		}
		id.CurrentAddress = bap.Address
		id.Addresses = append(id.Addresses, Address{
			Address: bap.Address,
			Signer:  aip.Address,
			Txid:    txidStr,
			Block:   height,
			Score:   score,
		})
		if err := l.store.SaveIdentity(ctx, id); err != nil {
			return err
		}

	case bitcom.ATTEST:
		signer := &Signer{
			UrnHash: bap.IDKey,
			Address: aip.Address,
			Txid:    txidStr,
			Block:   height,
			Score:   score,
			Revoked: false,
		}
		if err := l.store.SaveAttestation(ctx, signer); err != nil {
			return err
		}

	case bitcom.REVOKE:
		if err := l.store.RevokeAttestation(ctx, bap.IDKey, aip.Address); err != nil {
			return err
		}

	case bitcom.ALIAS:
		if len(bap.Profile) > 0 {
			p := map[string]any{}
			if err := json.Unmarshal(bap.Profile, &p); err != nil {
				return fmt.Errorf("failed to unmarshal profile: %w", err)
			}
			if err := l.store.SaveProfile(ctx, bap.IDKey, p); err != nil {
				return err
			}
		}
	}
	return nil
}

// OutputSpent is called when a previously-admitted UTXO is spent.
// BAP does not need to track spent outputs.
func (l *LookupService) OutputSpent(ctx context.Context, payload *engine.OutputSpent) error {
	return nil
}

// OutputNoLongerRetainedInHistory is called when historical retention is no longer required.
func (l *LookupService) OutputNoLongerRetainedInHistory(ctx context.Context, outpoint *transaction.Outpoint, topic string) error {
	return nil
}

// OutputEvicted is called when an output is permanently evicted.
func (l *LookupService) OutputEvicted(ctx context.Context, outpoint *transaction.Outpoint) error {
	return nil
}

// OutputBlockHeightUpdated is called when a transaction's block height is updated.
func (l *LookupService) OutputBlockHeightUpdated(ctx context.Context, txid *chainhash.Hash, blockHeight uint32, blockIndex uint64) error {
	return nil
}

// Lookup handles generic lookup queries.
func (l *LookupService) Lookup(ctx context.Context, question *lookup.LookupQuestion) (*lookup.LookupAnswer, error) {
	return nil, nil
}

// GetDocumentation returns documentation for this lookup service.
func (l *LookupService) GetDocumentation() string {
	return "BAP Lookup Service"
}

// GetMetaData returns metadata for this lookup service.
func (l *LookupService) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "BAP",
	}
}

// LoadIdentityById loads an identity by its BAP ID key.
func (l *LookupService) LoadIdentityById(ctx context.Context, id string) (*Identity, error) {
	return l.store.LoadIdentityById(ctx, id)
}

// LoadIdentityByAddress loads an identity by one of its associated addresses.
func (l *LookupService) LoadIdentityByAddress(ctx context.Context, address string) (*Identity, error) {
	return l.store.LoadIdentityByAddress(ctx, address)
}

// SaveProfile updates the profile data for an identity.
func (l *LookupService) SaveProfile(ctx context.Context, bapId string, profile map[string]any) error {
	return l.store.SaveProfile(ctx, bapId, profile)
}

// LoadProfiles returns a paginated list of identities with profile data.
func (l *LookupService) LoadProfiles(ctx context.Context, limit, offset int) ([]Identity, error) {
	return l.store.LoadProfiles(ctx, limit, offset)
}

// Search performs a text search across identities.
func (l *LookupService) Search(ctx context.Context, query string, limit, offset int) ([]Identity, error) {
	return l.store.Search(ctx, query, limit, offset)
}

// ResolveCurrentAddress walks the address chain ordered by score and validates
// that each rotation was authorized by the previous key. Returns the latest
// validated address and the validated chain.
func ResolveCurrentAddress(identity *Identity) (currentAddr string, validChain []Address) {
	if identity == nil || len(identity.Addresses) == 0 {
		return "", nil
	}

	validChain = append(validChain, identity.Addresses[0])
	currentAddr = identity.Addresses[0].Address

	for i := 1; i < len(identity.Addresses); i++ {
		addr := identity.Addresses[i]
		if addr.Signer != currentAddr {
			break
		}
		validChain = append(validChain, addr)
		currentAddr = addr.Address
	}

	return currentAddr, validChain
}

// IsAuthorityAtScore checks whether the given address was the valid authority
// for the identity at the specified score (block height + index).
func IsAuthorityAtScore(identity *Identity, address string, atScore float64) bool {
	if identity == nil || len(identity.Addresses) == 0 {
		return false
	}

	currentAddr := identity.Addresses[0].Address
	for i := 1; i < len(identity.Addresses); i++ {
		addr := identity.Addresses[i]
		if addr.Score > atScore {
			break
		}
		if addr.Signer != currentAddr {
			break
		}
		currentAddr = addr.Address
	}

	return address == currentAddr
}
