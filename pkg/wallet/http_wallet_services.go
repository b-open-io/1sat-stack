package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/wdk"
)

// HTTPWalletServices implements wdk.Services by calling a remote 1sat-stack HTTP API.
// It is the HTTP counterpart of LocalWalletServices — same interface, network backend.
type HTTPWalletServices struct {
	baseURL string
	client  *http.Client
	logger  *slog.Logger
}

// NewHTTPWalletServices creates an HTTPWalletServices pointing at the given 1sat-stack
// base URL (e.g. "https://api.1sat.app/1sat"). The URL should NOT have a trailing slash.
func NewHTTPWalletServices(baseURL string, logger *slog.Logger) *HTTPWalletServices {
	return &HTTPWalletServices{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}
}

// --- JSON response types (matching 1sat-stack HTTP API shapes) ---

// blockHeaderJSON matches the chaintracks /header/height/:height and /tip JSON responses.
// Fields come from go-sdk block.Header (embedded) + chaintracks.BlockHeader.
type blockHeaderJSON struct {
	Version      int32  `json:"version"`
	PreviousHash string `json:"previousHash"`
	MerkleRoot   string `json:"merkleRoot"`
	Time         uint32 `json:"time"`
	Bits         uint32 `json:"bits"`
	Nonce        uint32 `json:"nonce"`
	Height       uint32 `json:"height"`
	Hash         string `json:"hash"`
}

func (h *blockHeaderJSON) toWDK() *wdk.ChainBlockHeader {
	return &wdk.ChainBlockHeader{
		ChainBaseBlockHeader: wdk.ChainBaseBlockHeader{
			Version:      uint32(h.Version),
			PreviousHash: h.PreviousHash,
			MerkleRoot:   h.MerkleRoot,
			Time:         h.Time,
			Bits:         h.Bits,
			Nonce:        h.Nonce,
		},
		Height: uint(h.Height),
		Hash:   h.Hash,
	}
}

// txStatusJSON matches the arcade /tx/:txid and POST /tx JSON responses.
type txStatusJSON struct {
	TxID         string   `json:"txid"`
	Status       string   `json:"txStatus"`
	BlockHash    string   `json:"blockHash,omitempty"`
	BlockHeight  uint64   `json:"blockHeight,omitempty"`
	MerklePath   string   `json:"merklePath,omitempty"` // hex-encoded binary
	CompetingTxs []string `json:"competingTxs"`
}

// --- BlockHeaderLoader ---

func (s *HTTPWalletServices) ChainHeaderByHeight(ctx context.Context, height uint32) (*wdk.ChainBlockHeader, error) {
	var hdr blockHeaderJSON
	if err := s.getJSON(ctx, fmt.Sprintf("/chaintracks/header/height/%d", height), &hdr); err != nil {
		return nil, fmt.Errorf("chaintracks header by height %d: %w", height, err)
	}
	return hdr.toWDK(), nil
}

// --- chaintracker.ChainTracker ---

func (s *HTTPWalletServices) IsValidRootForHeight(ctx context.Context, root *chainhash.Hash, height uint32) (bool, error) {
	hdr, err := s.ChainHeaderByHeight(ctx, height)
	if err != nil {
		return false, err
	}
	return hdr.MerkleRoot == root.String(), nil
}

func (s *HTTPWalletServices) CurrentHeight(ctx context.Context) (uint32, error) {
	tip, err := s.FindChainTipHeader(ctx)
	if err != nil {
		return 0, err
	}
	return uint32(tip.Height), nil
}

// --- FindChainTipHeader ---

func (s *HTTPWalletServices) FindChainTipHeader(ctx context.Context) (*wdk.ChainBlockHeader, error) {
	var hdr blockHeaderJSON
	if err := s.getJSON(ctx, "/chaintracks/tip", &hdr); err != nil {
		return nil, fmt.Errorf("chaintracks tip: %w", err)
	}
	return hdr.toWDK(), nil
}

// --- PostFromBEEF ---

func (s *HTTPWalletServices) PostFromBEEF(ctx context.Context, b *transaction.Beef, txIDs []string) (wdk.PostFromBeefResult, error) {
	results := make([]*wdk.PostFromBEEFServiceResult, 0, len(txIDs))

	for _, txID := range txIDs {
		tx := b.FindTransaction(txID)
		if tx == nil {
			return nil, fmt.Errorf("transaction %s not found in beef", txID)
		}
		if tx.MerklePath != nil {
			continue
		}

		txHash, err := chainhash.NewHashFromHex(txID)
		if err != nil {
			return nil, fmt.Errorf("invalid txid %s: %w", txID, err)
		}

		beefBytes, err := b.AtomicBytes(txHash)
		if err != nil {
			return nil, fmt.Errorf("failed to get BEEF bytes for %s: %w", txID, err)
		}

		var status txStatusJSON
		if err := s.postBinary(ctx, "/arcade/tx", beefBytes, &status); err != nil {
			results = append(results, &wdk.PostFromBEEFServiceResult{
				Name: "HTTPArcade",
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
			Name: "HTTPArcade",
			PostedBEEFResult: &wdk.PostedBEEF{
				TxIDResults: []wdk.PostedTxID{
					mapHTTPArcadeStatus(&status),
				},
			},
		})
	}

	return results, nil
}

// --- MerklePath ---

func (s *HTTPWalletServices) MerklePath(ctx context.Context, txid string) (*wdk.MerklePathResult, error) {
	proofBytes, err := s.getBinary(ctx, fmt.Sprintf("/beef/%s/proof", txid))
	if err != nil {
		return nil, fmt.Errorf("beef proof for %s: %w", txid, err)
	}

	mp, err := transaction.NewMerklePathFromBinary(proofBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse merkle path for %s: %w", txid, err)
	}

	txHash, err := chainhash.NewHashFromHex(txid)
	if err != nil {
		return nil, fmt.Errorf("invalid txid %s: %w", txid, err)
	}
	if valid, err := mp.Verify(ctx, txHash, s); err != nil {
		return nil, fmt.Errorf("failed to verify merkle proof for %s: %w", txid, err)
	} else if !valid {
		return nil, fmt.Errorf("merkle proof invalid for %s", txid)
	}

	result := &wdk.MerklePathResult{
		Name:       "HTTPBEEF",
		MerklePath: mp,
	}

	if hdr, err := s.ChainHeaderByHeight(ctx, mp.BlockHeight); err == nil {
		result.BlockHeader = &wdk.MerklePathBlockHeader{
			Height:     uint32(hdr.Height),
			MerkleRoot: hdr.MerkleRoot,
			Hash:       hdr.Hash,
		}
	}

	return result, nil
}

// --- RawTx ---

func (s *HTTPWalletServices) RawTx(ctx context.Context, txID string) (wdk.RawTxResult, error) {
	raw, err := s.getBinary(ctx, fmt.Sprintf("/beef/%s/tx", txID))
	if err != nil {
		return wdk.RawTxResult{}, fmt.Errorf("beef raw tx for %s: %w", txID, err)
	}
	return wdk.RawTxResult{
		TxID:  txID,
		Name:  "HTTPBEEF",
		RawTx: raw,
	}, nil
}

// --- GetBEEF ---

func (s *HTTPWalletServices) GetBEEF(ctx context.Context, txID string, _ []string) (*transaction.Beef, error) {
	beefBytes, err := s.getBinary(ctx, fmt.Sprintf("/beef/%s", txID))
	if err != nil {
		return nil, fmt.Errorf("beef get for %s: %w", txID, err)
	}
	return transaction.NewBeefFromBytes(beefBytes)
}

// --- NLockTimeIsFinal ---

func (s *HTTPWalletServices) NLockTimeIsFinal(ctx context.Context, txOrLockTime any) (bool, error) {
	return wdk.NLockTimeIsFinal(ctx, s, txOrLockTime)
}

// --- GetStatusForTxIDs ---

func (s *HTTPWalletServices) GetStatusForTxIDs(ctx context.Context, txIDs []string) (*wdk.GetStatusForTxIDsResult, error) {
	results := make([]wdk.TxStatusDetail, 0, len(txIDs))

	for _, txID := range txIDs {
		var status txStatusJSON
		if err := s.getJSON(ctx, fmt.Sprintf("/arcade/tx/%s", txID), &status); err != nil {
			results = append(results, wdk.TxStatusDetail{
				TxID:   txID,
				Status: wdk.ResultStatusForTxIDNotFound.String(),
			})
			continue
		}

		detail := wdk.TxStatusDetail{TxID: txID}
		switch status.Status {
		case "MINED", "IMMUTABLE":
			detail.Status = wdk.ResultStatusForTxIDMined.String()
			if status.BlockHeight > 0 {
				depth := int(status.BlockHeight)
				detail.Depth = &depth
			}
		case "SEEN_ON_NETWORK", "ACCEPTED_BY_NETWORK", "SENT_TO_NETWORK", "RECEIVED":
			detail.Status = wdk.ResultStatusForTxIDKnown.String()
		default:
			detail.Status = wdk.ResultStatusForTxIDNotFound.String()
		}

		results = append(results, detail)
	}

	return &wdk.GetStatusForTxIDsResult{
		Name:    "HTTPArcade",
		Status:  wdk.GetStatusSuccess,
		Results: results,
	}, nil
}

// --- HTTP helpers ---

func (s *HTTPWalletServices) getJSON(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

func (s *HTTPWalletServices) getBinary(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(resp.Body)
}

func (s *HTTPWalletServices) postBinary(ctx context.Context, path string, data []byte, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

// --- Arcade status mapping ---

func mapHTTPArcadeStatus(status *txStatusJSON) wdk.PostedTxID {
	result := wdk.PostedTxID{
		TxID:         status.TxID,
		BlockHash:    status.BlockHash,
		BlockHeight:  uint32(status.BlockHeight),
		CompetingTxs: status.CompetingTxs,
	}

	switch status.Status {
	case "MINED", "IMMUTABLE", "SEEN_ON_NETWORK", "ACCEPTED_BY_NETWORK",
		"SENT_TO_NETWORK", "RECEIVED":
		result.Result = wdk.PostedTxIDResultSuccess
	case "DOUBLE_SPEND_ATTEMPTED":
		result.Result = wdk.PostedTxIDResultDoubleSpend
		result.DoubleSpend = true
	default:
		result.Result = wdk.PostedTxIDResultError
	}

	if status.MerklePath != "" {
		if mpBytes, err := hex.DecodeString(status.MerklePath); err == nil {
			if mp, err := transaction.NewMerklePathFromBinary(mpBytes); err == nil {
				result.MerklePath = mp
			}
		}
	}

	return result
}
