package bsocial

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bitcoinschema/go-bmap"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// LookupService implements the engine.LookupService interface for BSocial data.
type LookupService struct {
	db *mongo.Database
}

// NewLookupService creates a new BSocial lookup service using the provided database.
func NewLookupService(db *mongo.Database) *LookupService {
	return &LookupService{
		db: db,
	}
}

// EnsureIndexes creates the required MongoDB indexes for BSocial collections.
func (l *LookupService) EnsureIndexes(ctx context.Context) error {
	if _, err := l.db.Collection("post").Indexes().CreateMany(
		ctx,
		[]mongo.IndexModel{
			{Keys: bson.M{"timestamp": -1}},
			{Keys: bson.D{{Key: "AIP.address", Value: 1}, {Key: "timestamp", Value: -1}}, Options: options.Index().SetSparse(true)},
			{Keys: bson.D{{Key: "MAP.tx", Value: 1}}, Options: options.Index().SetSparse(true)},
		},
	); err != nil {
		return fmt.Errorf("failed to create post indexes: %w", err)
	}
	if _, err := l.db.Collection("like").Indexes().CreateMany(
		ctx,
		[]mongo.IndexModel{
			{Keys: bson.M{"timestamp": -1}},
			{Keys: bson.D{{Key: "AIP.address", Value: 1}, {Key: "timestamp", Value: -1}}, Options: options.Index().SetSparse(true)},
			{Keys: bson.D{{Key: "MAP.tx", Value: 1}, {Key: "timestamp", Value: -1}}, Options: options.Index().SetSparse(true)},
		},
	); err != nil {
		return fmt.Errorf("failed to create like indexes: %w", err)
	}
	if _, err := l.db.Collection("follow").Indexes().CreateMany(
		ctx,
		[]mongo.IndexModel{
			{Keys: bson.D{{Key: "AIP.address", Value: 1}, {Key: "blk.i", Value: 1}}, Options: options.Index().SetSparse(true)},
			{Keys: bson.D{{Key: "MAP.idKey", Value: 1}, {Key: "blk.i", Value: 1}}, Options: options.Index().SetSparse(true)},
		},
	); err != nil {
		return fmt.Errorf("failed to create follow indexes: %w", err)
	}
	if _, err := l.db.Collection("unfollow").Indexes().CreateMany(
		ctx,
		[]mongo.IndexModel{
			{Keys: bson.D{{Key: "AIP.address", Value: 1}, {Key: "blk.i", Value: 1}}, Options: options.Index().SetSparse(true)},
			{Keys: bson.D{{Key: "MAP.idKey", Value: 1}, {Key: "blk.i", Value: 1}}, Options: options.Index().SetSparse(true)},
		},
	); err != nil {
		return fmt.Errorf("failed to create unfollow indexes: %w", err)
	}
	return nil
}

// OutputAdmittedByTopic processes a newly admitted BSocial output and upserts it into the appropriate collection.
func (l *LookupService) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
	_, tx, _, err := transaction.ParseBeef(payload.AtomicBEEF)
	if err != nil {
		return err
	}

	bmapTx, err := bmap.NewFromTx(tx)
	if err != nil {
		return err
	}
	if tx.MerklePath != nil {
		bmapTx.Blk.I = tx.MerklePath.BlockHeight
	}
	bsonData, err := PrepareForIngestion(bmapTx)
	if err != nil {
		return err
	}

	var collectionName string
	var ok bool
	if collectionName, ok = bsonData["collection"].(string); !ok {
		return nil
	}
	delete(bsonData, "collection")
	collection := l.db.Collection(collectionName)
	_, err = collection.UpdateOne(
		ctx,
		bson.M{"_id": bsonData["_id"]},
		bson.M{
			"$set": bsonData,
			"$setOnInsert": bson.M{
				"timestamp": time.Now().UnixMilli(),
			},
		},
		options.UpdateOne().SetUpsert(true),
	)
	return err
}

// OutputSpent is called when a previously-admitted UTXO is spent.
// BSocial does not need to track spent outputs.
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
	return "BSocial Lookup Service"
}

// GetMetaData returns metadata for this lookup service.
func (l *LookupService) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "BSocial",
	}
}

// Search performs a full-text search across posts using MongoDB Atlas Search.
func (l *LookupService) Search(ctx context.Context, query string, limit int, offset int) ([]map[string]any, error) {
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

	cursor, err := l.db.Collection("post").Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("failed to execute search aggregation: %w", err)
	}
	defer cursor.Close(ctx)

	var results []map[string]any
	if err = cursor.All(ctx, &results); err != nil {
		return nil, fmt.Errorf("failed to decode search results: %w", err)
	}

	return results, nil
}

// PrepareForIngestion converts a bmap.Tx into a BSON document suitable for MongoDB storage.
func PrepareForIngestion(bmapData *bmap.Tx) (bsonData bson.M, err error) {
	// delete input.Tape from the inputs and outputs
	for i := range bmapData.Tx.In {
		bmapData.Tx.In[i].Tape = nil
	}

	for i := range bmapData.Tx.Out {
		bmapData.Tx.Out[i].Tape = nil
	}

	bsonData = bson.M{
		"_id": bmapData.Tx.Tx.H,
		"tx":  bmapData.Tx.Tx,
		"blk": bmapData.Tx.Blk,
		"in":  bmapData.Tx.In,
		"out": bmapData.Tx.Out,
	}

	if len(bmapData.AIP) > 0 {
		aips := make([]bson.M, len(bmapData.AIP))
		for i, a := range bmapData.AIP {
			aips[i] = bson.M{
				"algorithm": a.Algorithm,
				"address":   a.AlgorithmSigningComponent,
				"indices":   a.Indices,
				"signature": a.Signature,
			}
		}
		bsonData["AIP"] = aips
	}

	if len(bmapData.Sigma) > 0 {
		bsonData["SIGMA"] = bmapData.Sigma
	}

	if bmapData.BAP != nil {
		bsonData["BAP"] = bmapData.BAP
	}

	if bmapData.Ord != nil {
		// remove the data
		for _, o := range bmapData.Ord {
			o.Data = []byte{}

			// take only the first 255 characters
			if len(o.ContentType) > 255 {
				o.ContentType = o.ContentType[:255]
			}
		}

		bsonData["Ord"] = bmapData.Ord
	}

	bs := []bson.M{}
	for _, b := range bmapData.B {
		item := bson.M{
			"content-type": b.MediaType,
			"encoding":     b.Encoding,
			"filename":     b.Filename,
		}

		if strings.HasPrefix(b.MediaType, "text") {
			if len(b.Data) > 256*1024 {
				item["content"] = string(b.Data[:256*1024])
			} else {
				item["content"] = string(b.Data)
			}
		}
		bs = append(bs, item)
	}
	bsonData["B"] = bs

	if bmapData.BOOST != nil {
		bsonData["BOOST"] = bmapData.BOOST
	}

	if bmapData.MAP == nil {
		slog.Info("No MAP data")
		return
	}

	bsonData["MAP"] = bmapData.MAP

	if collection, ok := bmapData.MAP[0]["type"].(string); ok {
		bsonData["collection"] = collection
	} else {
		return
	}
	if _, ok := bmapData.MAP[0]["app"].(string); !ok {
		return
	}

	for key, value := range bsonData {
		if str, ok := value.(string); ok {
			if !utf8.ValidString(str) {
				slog.Warn("Invalid UTF-8 detected", "key", key, "value", str)
				return
			}
		}
	}

	return
}
