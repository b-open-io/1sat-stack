package spends

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// HTTPSpendStorage resolves spends against a remote index service's txo spend
// endpoints (GET /:outpoint/spend, POST /spends). It is a read-only tier: spend
// records are owned and written by the index service, so PutSpend and
// DeleteSpend are no-ops. baseURL points at the txo mount, e.g.
// https://api.1sat.app/1sat/txo.
type HTTPSpendStorage struct {
	baseURL string
	client  *http.Client
}

// NewHTTPSpendStorage creates an HTTP-backed spend storage. A nil client uses
// http.DefaultClient.
func NewHTTPSpendStorage(baseURL string, client *http.Client) *HTTPSpendStorage {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPSpendStorage{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  client,
	}
}

// spendResponse mirrors txo.SpendResponse.
type spendResponse struct {
	SpendTxid *string `json:"spendTxid"`
}

func (s *HTTPSpendStorage) GetSpend(ctx context.Context, outpoint *transaction.Outpoint) (*chainhash.Hash, error) {
	if outpoint == nil {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/"+outpoint.String()+"/spend", nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("spends remote GetSpend: unexpected status %d", resp.StatusCode)
	}
	var sr spendResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, err
	}
	return parseSpendTxid(sr.SpendTxid)
}

func (s *HTTPSpendStorage) GetSpends(ctx context.Context, outpoints []*transaction.Outpoint) ([]*chainhash.Hash, error) {
	results := make([]*chainhash.Hash, len(outpoints))
	if len(outpoints) == 0 {
		return results, nil
	}
	body := make([]string, len(outpoints))
	for i, op := range outpoints {
		if op != nil {
			body[i] = op.String()
		}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/spends", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("spends remote GetSpends: unexpected status %d", resp.StatusCode)
	}
	var srs []spendResponse
	if err := json.NewDecoder(resp.Body).Decode(&srs); err != nil {
		return nil, err
	}
	for i := range srs {
		if i >= len(results) {
			break
		}
		hash, err := parseSpendTxid(srs[i].SpendTxid)
		if err != nil {
			return nil, err
		}
		results[i] = hash
	}
	return results, nil
}

func (s *HTTPSpendStorage) PutSpend(ctx context.Context, outpoint *transaction.Outpoint, spendTxid *chainhash.Hash) error {
	return nil
}

func (s *HTTPSpendStorage) DeleteSpend(ctx context.Context, outpoint *transaction.Outpoint) error {
	return nil
}

func (s *HTTPSpendStorage) Close() error {
	return nil
}

func parseSpendTxid(hex *string) (*chainhash.Hash, error) {
	if hex == nil || *hex == "" {
		return nil, nil
	}
	return chainhash.NewHashFromHex(*hex)
}
