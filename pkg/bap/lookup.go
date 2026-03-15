package bap

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bitcoin-sv/go-templates/template/bitcom"
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

// OutputAdmittedByTopic processes a newly admitted BAP output and updates identity/attestation state.
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
	id, err := l.store.LoadIdentityByAddress(ctx, aip.Address)
	if err != nil {
		return err
	}

	txidStr := txid.String()

	switch bap.Type {
	case bitcom.ID:
		if id == nil {
			id = &Identity{
				BapId:          bap.IDKey,
				RootAddress:    aip.Address,
				CurrentAddress: bap.Address,
				Addresses: []Address{
					{
						Address: bap.Address,
						Txid:    txidStr,
						Block:   height,
					},
				},
				FirstSeen:     height,
				FirstSeenTxid: txidStr,
			}
		} else {
			id.CurrentAddress = aip.Address
			id.Addresses = append(id.Addresses, Address{
				Address: bap.Address,
				Txid:    txidStr,
				Block:   height,
			})
		}
		if err := l.store.SaveIdentity(ctx, id); err != nil {
			return err
		}
	case bitcom.ATTEST:
		if id == nil {
			return fmt.Errorf("identity not found for address %s", aip.Address)
		}
		signer := &Signer{
			BapID:   id.BapId,
			UrnHash: bap.IDKey,
			Address: aip.Address,
			Txid:    txidStr,
			Revoked: false,
		}
		if err := l.store.SaveAttestation(ctx, signer.UrnHash, signer.BapID, signer); err != nil {
			return err
		}
	case bitcom.REVOKE:
		if err := l.store.RevokeAttestation(ctx, bap.IDKey, id.BapId); err != nil {
			return err
		}
	case bitcom.ALIAS:
		if id == nil {
			return fmt.Errorf("identity not found for address %s", aip.Address)
		}
		if len(bap.Profile) > 0 && bap.IDKey == id.BapId {
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
