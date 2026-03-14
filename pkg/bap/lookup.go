package bap

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/bitcoin-sv/go-templates/template/bitcom"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// LookupService implements the engine.LookupService interface for BAP identities.
type LookupService struct {
	db *mongo.Database
}

// NewLookupService creates a new BAP lookup service using the provided database.
func NewLookupService(db *mongo.Database) *LookupService {
	return &LookupService{
		db: db,
	}
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
	id, err := l.LoadIdentityByAddress(ctx, aip.Address)
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
			id.CurrentAddress = bap.Address
			id.Addresses = append(id.Addresses, Address{
				Address: bap.Address,
				Txid:    txidStr,
				Block:   height,
			})
		}
		if err := l.SaveIdentity(ctx, id); err != nil {
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

		if err := l.SaveAttestation(ctx, signer); err != nil {
			return err
		}
	case bitcom.REVOKE:
		if err := l.RevokeAttestation(ctx, id.BapId, bap.IDKey); err != nil {
			return err
		}

	case bitcom.ALIAS:
		if id == nil {
			return fmt.Errorf("identity not found for address %s", aip.Address)
		}
		if len(bap.Profile) > 0 && bap.IDKey == id.BapId {
			if err := l.SaveProfile(ctx, bap.IDKey, bap.Profile); err != nil {
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
	identity := &Identity{}
	err := l.db.Collection("identities").FindOne(ctx, bson.M{"_id": id}).Decode(identity)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return identity, nil
}

// LoadIdentityByAddress loads an identity by one of its associated addresses.
func (l *LookupService) LoadIdentityByAddress(ctx context.Context, address string) (*Identity, error) {
	identity := &Identity{}
	err := l.db.Collection("identities").FindOne(ctx, bson.M{"addresses.address": address}).Decode(identity)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return identity, nil
}

// SaveIdentity upserts an identity document.
func (l *LookupService) SaveIdentity(ctx context.Context, id *Identity) error {
	_, err := l.db.Collection("identities").UpdateOne(ctx,
		bson.M{"_id": id.BapId},
		bson.M{"$set": id},
		options.UpdateOne().SetUpsert(true),
	)
	return err
}

// LoadAttestation loads all signers for a given URN hash.
func (l *LookupService) LoadAttestation(ctx context.Context, urnHash string) (*Attestation, error) {
	cursor, err := l.db.Collection("attestations").Find(ctx, bson.M{"urnHash": urnHash})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	attestation := &Attestation{
		UrnHash: urnHash,
	}
	for cursor.Next(ctx) {
		signer := &Signer{}
		if err := cursor.Decode(signer); err != nil {
			slog.Error("failed to decode signer", "error", err)
			return nil, err
		}
		attestation.Signers = append(attestation.Signers, signer)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return attestation, nil
}

// SaveAttestation upserts an attestation signer document.
func (l *LookupService) SaveAttestation(ctx context.Context, signer *Signer) error {
	_, err := l.db.Collection("attestations").UpdateOne(ctx,
		bson.M{"urnHash": signer.UrnHash, "idKey": signer.BapID},
		bson.M{"$set": signer},
		options.UpdateOne().SetUpsert(true),
	)
	return err
}

// RevokeAttestation marks an attestation as revoked.
func (l *LookupService) RevokeAttestation(ctx context.Context, bapId string, urnHash string) error {
	_, err := l.db.Collection("attestations").UpdateOne(ctx,
		bson.M{"urnHash": urnHash, "idKey": bapId},
		bson.M{"$set": bson.M{"revoked": true}},
		options.UpdateOne().SetUpsert(true),
	)
	return err
}

// DeleteAttestation removes an attestation signer document.
func (l *LookupService) DeleteAttestation(ctx context.Context, signer *Signer) error {
	_, err := l.db.Collection("attestations").DeleteOne(ctx,
		bson.M{"urnHash": signer.UrnHash, "idKey": signer.BapID},
	)
	return err
}

// SaveProfile updates the profile data for an identity.
func (l *LookupService) SaveProfile(ctx context.Context, bapId string, profile json.RawMessage) error {
	p := bson.M{}
	if err := json.Unmarshal(profile, &p); err != nil {
		return fmt.Errorf("failed to unmarshal profile: %w", err)
	}
	_, err := l.db.Collection("identities").UpdateOne(ctx,
		bson.M{"_id": bapId},
		bson.M{"$set": bson.M{"profile": p}},
	)
	return err
}

// LoadProfiles returns a paginated list of profiles.
func (l *LookupService) LoadProfiles(ctx context.Context, limit int, offset int) ([]*Profile, error) {
	cursor, err := l.db.Collection("identities").Find(
		ctx,
		bson.M{},
		options.Find().
			SetLimit(int64(limit)).
			SetSkip(int64(offset)).
			SetProjection(bson.M{
				"_id":     1,
				"profile": 1,
			}),
	)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	profiles := make([]*Profile, 0, limit)
	for cursor.Next(ctx) {
		var profile Profile
		if err := cursor.Decode(&profile); err != nil {
			slog.Error("failed to decode profile", "error", err)
			continue
		}
		profiles = append(profiles, &profile)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return profiles, nil
}

// Search performs a full-text search across identities using MongoDB Atlas Search.
func (l *LookupService) Search(ctx context.Context, query string, limit int, offset int) ([]*Identity, error) {
	pipeline := mongo.Pipeline{
		{
			bson.E{Key: "$search", Value: bson.D{
				{Key: "index", Value: "default"},
				{Key: "text", Value: bson.D{
					{Key: "query", Value: query},
					{Key: "path", Value: bson.D{{Key: "wildcard", Value: "*"}}},
				}},
			}},
		},
		{
			bson.E{Key: "$skip", Value: int64(offset)},
		},
		{
			bson.E{Key: "$limit", Value: int64(limit)},
		},
	}

	cursor, err := l.db.Collection("identities").Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("failed to execute search aggregation: %w", err)
	}
	defer cursor.Close(ctx)

	var identities []*Identity
	if err = cursor.All(ctx, &identities); err != nil {
		return nil, fmt.Errorf("failed to decode search results: %w", err)
	}

	return identities, nil
}
