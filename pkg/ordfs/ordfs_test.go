package ordfs

import (
	"context"
	"testing"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/spf13/viper"
)

// mockLoader implements the Loader interface for testing
type mockLoader struct {
	txs     map[string]*transaction.Transaction
	outputs map[string]*transaction.TransactionOutput
	spends  map[string]*chainhash.Hash
}

func newMockLoader() *mockLoader {
	return &mockLoader{
		txs:     make(map[string]*transaction.Transaction),
		outputs: make(map[string]*transaction.TransactionOutput),
		spends:  make(map[string]*chainhash.Hash),
	}
}

func (m *mockLoader) LoadTx(txid string) (*transaction.Transaction, error) {
	if tx, ok := m.txs[txid]; ok {
		return tx, nil
	}
	return nil, ErrNotFound
}

func (m *mockLoader) LoadOutput(outpoint *transaction.Outpoint) (*transaction.TransactionOutput, error) {
	key := outpoint.String()
	if output, ok := m.outputs[key]; ok {
		return output, nil
	}
	// Try to get from tx
	if tx, ok := m.txs[outpoint.Txid.String()]; ok {
		if int(outpoint.Index) < len(tx.Outputs) {
			return tx.Outputs[outpoint.Index], nil
		}
	}
	return nil, ErrNotFound
}

func (m *mockLoader) LoadSpend(outpoint string) (*chainhash.Hash, error) {
	if spend, ok := m.spends[outpoint]; ok {
		return spend, nil
	}
	return nil, nil
}

var ErrNotFound = &notFoundError{}

type notFoundError struct{}

func (e *notFoundError) Error() string { return "not found" }

func TestNewContentService(t *testing.T) {
	loader := newMockLoader()
	svc := NewContentService(loader, nil)

	if svc == nil {
		t.Fatal("expected non-nil content service")
	}
	if svc.loader != loader {
		t.Error("loader not set correctly")
	}
	if svc.logger == nil {
		t.Error("logger should default to slog.Default()")
	}
}

func TestParseContentPath(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		expectTxid  bool
		expectSeq   bool
		expectError bool
	}{
		{
			name:       "valid outpoint",
			path:       "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef_0",
			expectTxid: false,
		},
		{
			name:       "valid txid only",
			path:       "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			expectTxid: true,
		},
		{
			name:       "valid sequence format",
			path:       "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef:5",
			expectSeq:  true,
		},
		{
			name:        "invalid format",
			path:        "invalid",
			expectError: true,
		},
		{
			name:        "invalid txid in outpoint",
			path:        "invalid_0",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := parseContentPath(tt.path)
			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.expectTxid && req.Txid == nil {
				t.Error("expected Txid to be set")
			}
			if tt.expectSeq && req.Seq == nil {
				t.Error("expected Seq to be set")
			}
		})
	}
}

func TestParseOutputForContent(t *testing.T) {
	// Test with empty output (no content)
	emptyScript := &script.Script{}
	output := &transaction.TransactionOutput{
		LockingScript: emptyScript,
		Satoshis:      1000,
	}

	contentType, content, mapJSON, parent := ParseOutputForContent(output)
	if contentType != "" {
		t.Errorf("expected empty contentType, got %s", contentType)
	}
	if content != nil {
		t.Error("expected nil content")
	}
	if mapJSON != "" {
		t.Errorf("expected empty mapJSON, got %s", mapJSON)
	}
	if parent != nil {
		t.Error("expected nil parent")
	}
}

func TestConfigSetDefaults(t *testing.T) {
	cfg := &Config{}
	v := viper.New()
	cfg.SetDefaults(v, "ordfs")

	// Verify defaults are set
	if v.GetString("ordfs.mode") != ModeDisabled {
		t.Errorf("expected mode=disabled, got %s", v.GetString("ordfs.mode"))
	}
	if !v.GetBool("ordfs.routes.enabled") {
		t.Error("expected routes.enabled=true")
	}
}

func TestConfigInitializeDisabled(t *testing.T) {
	cfg := &Config{Mode: ModeDisabled}
	ctx := context.Background()

	svc, err := cfg.Initialize(ctx, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc != nil {
		t.Error("expected nil services when disabled")
	}
}

func TestConfigInitializeEmbeddedNoLoader(t *testing.T) {
	cfg := &Config{Mode: ModeEmbedded}
	ctx := context.Background()

	_, err := cfg.Initialize(ctx, nil, nil)
	if err == nil {
		t.Fatal("expected error when loader is nil")
	}
}

func TestConfigInitializeEmbedded(t *testing.T) {
	cfg := &Config{
		Mode: ModeEmbedded,
		Routes: RoutesConfig{
			Enabled: true,
			Prefix:  "/ordfs",
		},
	}
	ctx := context.Background()
	loader := newMockLoader()

	svc, err := cfg.Initialize(ctx, nil, loader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc == nil {
		t.Fatal("expected non-nil services")
	}
	if svc.Content == nil {
		t.Error("expected Content service")
	}
	if svc.Routes == nil {
		t.Error("expected Routes when enabled")
	}

	// Test close
	if err := svc.Close(); err != nil {
		t.Errorf("unexpected close error: %v", err)
	}
}

func TestBeefLoader(t *testing.T) {
	// Create a mock tx storage
	mockStorage := &mockTxStorage{
		txs: make(map[string]*transaction.Transaction),
	}

	// Create a test transaction
	txid, _ := chainhash.NewHashFromHex("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	lockingScript := &script.Script{}
	tx := &transaction.Transaction{
		Outputs: []*transaction.TransactionOutput{
			{LockingScript: lockingScript, Satoshis: 1000},
		},
	}
	mockStorage.txs[txid.String()] = tx

	loader := NewBeefLoader(context.Background(), mockStorage)

	// Test LoadTx
	loadedTx, err := loader.LoadTx(txid.String())
	if err != nil {
		t.Fatalf("LoadTx error: %v", err)
	}
	if loadedTx == nil {
		t.Fatal("expected non-nil tx")
	}

	// Test LoadOutput
	outpoint := &transaction.Outpoint{
		Txid:  *txid,
		Index: 0,
	}
	output, err := loader.LoadOutput(outpoint)
	if err != nil {
		t.Fatalf("LoadOutput error: %v", err)
	}
	if output == nil {
		t.Fatal("expected non-nil output")
	}

	// Test LoadSpend (should return nil when not supported)
	spend, err := loader.LoadSpend(outpoint.String())
	if err != nil {
		t.Fatalf("LoadSpend error: %v", err)
	}
	if spend != nil {
		t.Error("expected nil spend when tracking not supported")
	}
}

// mockTxStorage implements TxStorage for testing
type mockTxStorage struct {
	txs map[string]*transaction.Transaction
}

func (m *mockTxStorage) LoadTx(ctx context.Context, txid *chainhash.Hash) (*transaction.Transaction, error) {
	if tx, ok := m.txs[txid.String()]; ok {
		return tx, nil
	}
	return nil, ErrNotFound
}
