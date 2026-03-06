package wallet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/bsv-blockchain/arcade/models"
	arcadeservice "github.com/bsv-blockchain/arcade/service"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/wdk"
)

const maxGetBeefDepth = 100

// LocalWalletServices implements wdk.Services backed entirely by local services.
// No external API calls. If a local service fails, we see the error — no silent fallback.
type LocalWalletServices struct {
	chaintracks chaintracks.Chaintracks
	arcade      arcadeservice.ArcadeService
	beefStorage *beef.Storage
	logger      *slog.Logger
}

// NewLocalWalletServices creates a new LocalWalletServices.
func NewLocalWalletServices(
	ct chaintracks.Chaintracks,
	arc arcadeservice.ArcadeService,
	bs *beef.Storage,
	logger *slog.Logger,
) *LocalWalletServices {
	return &LocalWalletServices{
		chaintracks: ct,
		arcade:      arc,
		beefStorage: bs,
		logger:      logger,
	}
}

// --- BlockHeaderLoader ---

func (s *LocalWalletServices) ChainHeaderByHeight(ctx context.Context, height uint32) (*wdk.ChainBlockHeader, error) {
	hdr, err := s.chaintracks.GetHeaderByHeight(ctx, height)
	if err != nil {
		return nil, fmt.Errorf("chaintracks GetHeaderByHeight: %w", err)
	}
	return chainHeaderToWDK(hdr), nil
}

// --- chaintracker.ChainTracker ---

func (s *LocalWalletServices) IsValidRootForHeight(ctx context.Context, root *chainhash.Hash, height uint32) (bool, error) {
	return s.chaintracks.IsValidRootForHeight(ctx, root, height)
}

func (s *LocalWalletServices) CurrentHeight(ctx context.Context) (uint32, error) {
	return s.chaintracks.GetHeight(ctx), nil
}

// --- FindChainTipHeader ---

func (s *LocalWalletServices) FindChainTipHeader(ctx context.Context) (*wdk.ChainBlockHeader, error) {
	tip := s.chaintracks.GetTip(ctx)
	if tip == nil {
		return nil, fmt.Errorf("chaintracks returned nil tip")
	}
	return chainHeaderToWDK(tip), nil
}

// --- PostFromBEEF ---

func (s *LocalWalletServices) PostFromBEEF(ctx context.Context, b *transaction.Beef, txIDs []string) (wdk.PostFromBeefResult, error) {
	results := make([]*wdk.PostFromBEEFServiceResult, 0, len(txIDs))

	for _, txID := range txIDs {
		tx := b.FindTransaction(txID)
		if tx == nil {
			return nil, fmt.Errorf("transaction %s not found in beef", txID)
		}

		// Save to beef storage so the indexer can find it on arcade events
		txHash, _ := chainhash.NewHashFromHex(txID)
		if err := s.beefStorage.SaveBeef(ctx, txHash, b); err != nil {
			s.logger.Warn("failed to save beef before broadcast", "txid", txID, "error", err)
		}

		// Skip already mined txs
		if tx.MerklePath != nil {
			continue
		}

		efBytes, err := tx.EF()
		if err != nil {
			return nil, fmt.Errorf("failed to get EF bytes for %s: %w", txID, err)
		}

		status, err := s.arcade.SubmitTransaction(ctx, efBytes, nil)
		if err != nil {
			results = append(results, &wdk.PostFromBEEFServiceResult{
				Name: "LocalArcade",
				PostedBEEFResult: &wdk.PostedBEEF{
					TxIDResults: []wdk.PostedTxID{{
						TxID:   txID,
						Result: wdk.PostedTxIDResultError,
					}},
				},
			})
			continue
		}

		results = append(results, &wdk.PostFromBEEFServiceResult{
			Name: "LocalArcade",
			PostedBEEFResult: &wdk.PostedBEEF{
				TxIDResults: []wdk.PostedTxID{
					mapArcadeStatus(status, txID),
				},
			},
		})
	}

	return results, nil
}

// --- MerklePath ---

func (s *LocalWalletServices) MerklePath(ctx context.Context, txid string) (*wdk.MerklePathResult, error) {
	// Try BEEF storage first
	txHash, err := chainhash.NewHashFromHex(txid)
	if err != nil {
		return nil, fmt.Errorf("invalid txid %s: %w", txid, err)
	}

	tx, err := s.beefStorage.LoadTx(ctx, txHash)
	if err == nil && tx.MerklePath != nil {
		result := &wdk.MerklePathResult{
			Name:       "LocalBEEF",
			MerklePath: tx.MerklePath,
		}
		if hdr, err := s.chaintracks.GetHeaderByHeight(ctx, tx.MerklePath.BlockHeight); err == nil {
			result.BlockHeader = &wdk.MerklePathBlockHeader{
				Height: hdr.Height,
				Hash:   hdr.Hash.String(),
			}
		}
		return result, nil
	}

	// Fall back to Arcade
	status, err := s.arcade.GetStatus(ctx, txid)
	if err != nil {
		return nil, fmt.Errorf("arcade get status failed for %s: %w", txid, err)
	}

	if status == nil || len(status.MerklePath) == 0 {
		return nil, fmt.Errorf("no merkle path available for %s", txid)
	}

	mp, err := transaction.NewMerklePathFromBinary(status.MerklePath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse merkle path for %s: %w", txid, err)
	}

	return &wdk.MerklePathResult{
		Name:       "LocalArcade",
		MerklePath: mp,
		BlockHeader: &wdk.MerklePathBlockHeader{
			Height: uint32(status.BlockHeight),
			Hash:   status.BlockHash,
		},
	}, nil
}

// --- RawTx ---

func (s *LocalWalletServices) RawTx(ctx context.Context, txID string) (wdk.RawTxResult, error) {
	txHash, err := chainhash.NewHashFromHex(txID)
	if err != nil {
		return wdk.RawTxResult{}, fmt.Errorf("invalid txID %s: %w", txID, err)
	}

	tx, err := s.beefStorage.LoadTx(ctx, txHash)
	if err != nil {
		return wdk.RawTxResult{}, fmt.Errorf("BEEF lookup failed for %s: %w", txID, err)
	}

	return wdk.RawTxResult{
		TxID:  txID,
		Name:  "LocalBEEF",
		RawTx: tx.Bytes(),
	}, nil
}

// --- GetBEEF ---

func (s *LocalWalletServices) GetBEEF(ctx context.Context, txID string, knownTxIDs []string) (*transaction.Beef, error) {
	b := transaction.NewBeefV2()

	knownLookup := make(map[string]struct{}, len(knownTxIDs))
	for _, id := range knownTxIDs {
		knownLookup[id] = struct{}{}
	}

	var txGetter func(txID string, depth uint) error
	txGetter = func(txID string, depth uint) error {
		if depth > maxGetBeefDepth {
			return fmt.Errorf("max depth of recursion reached: %d", maxGetBeefDepth)
		}

		rawResult, err := s.RawTx(ctx, txID)
		if err != nil {
			return fmt.Errorf("failed to get raw tx for %s: %w", txID, err)
		}
		if rawResult.RawTx == nil {
			return fmt.Errorf("raw tx for %s is nil", txID)
		}

		tx, err := transaction.NewTransactionFromBytes(rawResult.RawTx)
		if err != nil {
			return fmt.Errorf("failed to parse tx %s: %w", txID, err)
		}

		mpResult, err := s.MerklePath(ctx, txID)
		if err != nil && !errors.Is(err, wdk.ErrNotFoundError) {
			return fmt.Errorf("failed to get merkle path for %s: %w", txID, err)
		}

		isMined := mpResult != nil && mpResult.MerklePath != nil
		if isMined {
			tx.MerklePath = mpResult.MerklePath
		}

		if _, err := b.MergeTransaction(tx); err != nil {
			return fmt.Errorf("failed to merge tx %s: %w", txID, err)
		}

		if isMined {
			return nil
		}

		for _, input := range tx.Inputs {
			if b.Transactions[*input.SourceTXID] != nil {
				continue
			}
			sourceTxID := input.SourceTXID.String()
			if _, exists := knownLookup[sourceTxID]; exists {
				b.MergeTxidOnly(input.SourceTXID)
				continue
			}
			if err := txGetter(sourceTxID, depth+1); err != nil {
				return fmt.Errorf("failed to get beef for %s at depth %d: %w", sourceTxID, depth, err)
			}
		}

		return nil
	}

	if err := txGetter(txID, 0); err != nil {
		return nil, fmt.Errorf("failed to get BEEF for %s: %w", txID, err)
	}

	return b, nil
}

// --- NLockTimeIsFinal ---

func (s *LocalWalletServices) NLockTimeIsFinal(ctx context.Context, txOrLockTime any) (bool, error) {
	return wdk.NLockTimeIsFinal(ctx, s, txOrLockTime)
}

// --- GetStatusForTxIDs ---

func (s *LocalWalletServices) GetStatusForTxIDs(ctx context.Context, txIDs []string) (*wdk.GetStatusForTxIDsResult, error) {
	results := make([]wdk.TxStatusDetail, 0, len(txIDs))
	for _, txID := range txIDs {
		status, err := s.arcade.GetStatus(ctx, txID)
		if err != nil || status == nil {
			// Arcade doesn't know this tx — check BEEF storage for a validated proof
			if txHash, hashErr := chainhash.NewHashFromHex(txID); hashErr == nil {
				if tx, loadErr := s.beefStorage.LoadTx(ctx, txHash); loadErr == nil && tx.MerklePath != nil {
					if valid, verifyErr := tx.MerklePath.Verify(ctx, txHash, s); verifyErr == nil && valid {
						depth := int(tx.MerklePath.BlockHeight)
						results = append(results, wdk.TxStatusDetail{
							TxID:   txID,
							Status: wdk.ResultStatusForTxIDMined.String(),
							Depth:  &depth,
						})
						continue
					}
				}
			}
			results = append(results, wdk.TxStatusDetail{
				TxID:   txID,
				Status: wdk.ResultStatusForTxIDNotFound.String(),
			})
			continue
		}

		detail := wdk.TxStatusDetail{TxID: txID}
		switch status.Status {
		case models.StatusMined, models.StatusImmutable:
			detail.Status = wdk.ResultStatusForTxIDMined.String()
			if status.BlockHeight > 0 {
				depth := int(status.BlockHeight)
				detail.Depth = &depth
			}
		case models.StatusSeenOnNetwork, models.StatusAcceptedByNetwork,
			models.StatusSentToNetwork, models.StatusReceived:
			detail.Status = wdk.ResultStatusForTxIDKnown.String()
		default:
			detail.Status = wdk.ResultStatusForTxIDNotFound.String()
		}
		results = append(results, detail)
	}

	return &wdk.GetStatusForTxIDsResult{
		Name:    "LocalArcade",
		Status:  wdk.GetStatusSuccess,
		Results: results,
	}, nil
}

// --- Chaintracks event wiring (not part of wdk.Services) ---

func (s *LocalWalletServices) SubscribeReorgs(ctx context.Context) <-chan *chaintracks.ReorgEvent {
	return s.chaintracks.SubscribeReorg(ctx)
}

func (s *LocalWalletServices) SubscribeTips(ctx context.Context) <-chan *chaintracks.BlockHeader {
	return s.chaintracks.Subscribe(ctx)
}

// --- Helpers ---

func chainHeaderToWDK(hdr *chaintracks.BlockHeader) *wdk.ChainBlockHeader {
	return &wdk.ChainBlockHeader{
		ChainBaseBlockHeader: wdk.ChainBaseBlockHeader{
			Version:      uint32(hdr.Version),
			PreviousHash: hdr.PrevHash.String(),
			MerkleRoot:   hdr.MerkleRoot.String(),
			Time:         hdr.Timestamp,
			Bits:         hdr.Bits,
			Nonce:        hdr.Nonce,
		},
		Height: uint(hdr.Height),
		Hash:   hdr.Hash.String(),
	}
}

func mapArcadeStatus(status *models.TransactionStatus, txID string) wdk.PostedTxID {
	if status == nil {
		return wdk.PostedTxID{
			TxID:   txID,
			Result: wdk.PostedTxIDResultError,
		}
	}

	result := wdk.PostedTxID{
		TxID:         status.TxID,
		BlockHash:    status.BlockHash,
		BlockHeight:  uint32(status.BlockHeight),
		CompetingTxs: status.CompetingTxs,
	}

	switch status.Status {
	case models.StatusMined, models.StatusImmutable,
		models.StatusSeenOnNetwork, models.StatusAcceptedByNetwork,
		models.StatusSentToNetwork, models.StatusReceived:
		result.Result = wdk.PostedTxIDResultSuccess
	case models.StatusDoubleSpendAttempted:
		result.Result = wdk.PostedTxIDResultDoubleSpend
		result.DoubleSpend = true
	case models.StatusRejected:
		result.Result = wdk.PostedTxIDResultError
	default:
		result.Result = wdk.PostedTxIDResultError
	}

	if len(status.MerklePath) > 0 {
		mp, err := transaction.NewMerklePathFromBinary(status.MerklePath)
		if err == nil {
			result.MerklePath = mp
		}
	}

	return result
}
