// Package gasp provides GASP (Graph Aware Sync Protocol) implementations for topic-based sync.
package gasp

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/gasp"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/redis/go-redis/v9"
)

// BeefRemote implements gasp.Remote by reading from local BEEF storage.
// This is used when transaction data is available locally (e.g., from JungleBus dispatcher).
type BeefRemote struct {
	queueKey    string        // Store key for initial response queries (optional)
	redis       *redis.Client // Redis for queue operations
	beefStorage *beef.Storage // BEEF transaction storage
}

// NewBeefRemote creates a new BeefRemote for reading from local BEEF storage.
// The queueKey is optional - only needed if using GetInitialResponse for bulk sync.
func NewBeefRemote(beefStorage *beef.Storage, r *redis.Client, queueKey string) *BeefRemote {
	return &BeefRemote{
		queueKey:    queueKey,
		redis:       r,
		beefStorage: beefStorage,
	}
}

// GetInitialResponse returns UTXOs from the queue as a GASP initial response.
// The queue members are txids scored by block height; we convert them to Output structs.
func (r *BeefRemote) GetInitialResponse(ctx context.Context, request *gasp.InitialRequest) (*gasp.InitialResponse, error) {
	opt := &redis.ZRangeBy{
		Min: "(" + strconv.FormatFloat(request.Since, 'f', -1, 64),
		Max: "+inf",
	}
	if request.Limit > 0 {
		opt.Count = int64(request.Limit)
	}

	members, err := r.redis.ZRangeByScoreWithScores(ctx, r.queueKey, opt).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to query queue: %w", err)
	}

	utxoList := make([]*gasp.Output, 0, len(members))
	var maxScore float64

	for _, member := range members {
		txid, err := chainhash.NewHashFromHex(member.Member.(string))
		if err != nil {
			continue
		}

		utxoList = append(utxoList, &gasp.Output{
			Txid:        *txid,
			OutputIndex: 0,
			Score:       member.Score,
		})

		if member.Score > maxScore {
			maxScore = member.Score
		}
	}

	return &gasp.InitialResponse{
		UTXOList: utxoList,
		Since:    maxScore,
	}, nil
}

// RequestNode loads raw transaction and proof from BEEF storage and returns as a GASP Node.
func (r *BeefRemote) RequestNode(ctx context.Context, graphID, outpoint *transaction.Outpoint, _ bool) (*gasp.Node, error) {
	if graphID == nil {
		graphID = outpoint
	}

	rawTx, err := r.beefStorage.LoadRawTx(ctx, &outpoint.Txid)
	if err != nil {
		return nil, fmt.Errorf("failed to load raw tx %s: %w", outpoint.Txid.String(), err)
	}

	node := &gasp.Node{
		GraphID:     graphID,
		RawTx:       fmt.Sprintf("%x", rawTx),
		OutputIndex: outpoint.Index,
	}

	proof, err := r.beefStorage.LoadProof(ctx, &outpoint.Txid)
	if err != nil && err != beef.ErrNotFound {
		return nil, fmt.Errorf("failed to load proof %s: %w", outpoint.Txid.String(), err)
	}
	if len(proof) > 0 {
		proofHex := fmt.Sprintf("%x", proof)
		node.Proof = &proofHex
	}

	return node, nil
}

// GetInitialReply is not needed for local BEEF sync (unidirectional).
func (r *BeefRemote) GetInitialReply(_ context.Context, _ *gasp.InitialResponse) (*gasp.InitialReply, error) {
	return &gasp.InitialReply{UTXOList: []*gasp.Output{}}, nil
}

// SubmitNode is not needed for local BEEF sync (we're reading, not writing).
func (r *BeefRemote) SubmitNode(_ context.Context, _ *gasp.Node) (*gasp.NodeResponse, error) {
	return &gasp.NodeResponse{RequestedInputs: map[transaction.Outpoint]*gasp.NodeResponseData{}}, nil
}

// Ensure math import is used
var _ = math.MaxFloat64
