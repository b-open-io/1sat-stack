package ordfs

import (
	"testing"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/spf13/viper"
)

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
			name:      "valid outpoint with sequence",
			path:      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef_0:5",
			expectSeq: true,
		},
		{
			name:       "valid txid with sequence",
			path:       "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef:5",
			expectTxid: true,
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

func TestParsePointerPath(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		expectSeq   *int
		expectFile  string
		expectError bool
	}{
		{
			name:       "simple outpoint",
			path:       "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef_0",
			expectSeq:  nil,
			expectFile: "",
		},
		{
			name:       "outpoint with seq",
			path:       "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef_0:5",
			expectSeq:  intPtr(5),
			expectFile: "",
		},
		{
			name:       "outpoint with file path",
			path:       "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef_0/style.css",
			expectSeq:  nil,
			expectFile: "style.css",
		},
		{
			name:       "outpoint with seq and file path",
			path:       "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef_0:-1/index.html",
			expectSeq:  intPtr(-1),
			expectFile: "index.html",
		},
		{
			name:       "outpoint with nested file path",
			path:       "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef_0/assets/js/app.js",
			expectSeq:  nil,
			expectFile: "assets/js/app.js",
		},
		{
			name:        "empty path",
			path:        "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pp, err := parsePointerPath(tt.path)
			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.expectSeq == nil && pp.Seq != nil {
				t.Errorf("expected nil Seq, got %d", *pp.Seq)
			}
			if tt.expectSeq != nil {
				if pp.Seq == nil {
					t.Error("expected Seq to be set")
				} else if *pp.Seq != *tt.expectSeq {
					t.Errorf("expected Seq=%d, got %d", *tt.expectSeq, *pp.Seq)
				}
			}
			if pp.FilePath != tt.expectFile {
				t.Errorf("expected FilePath=%s, got %s", tt.expectFile, pp.FilePath)
			}
		})
	}
}

func TestResolvePointerToOutpoint(t *testing.T) {
	tests := []struct {
		name        string
		pointer     string
		expectTxid  bool
		expectError bool
	}{
		{
			name:       "valid outpoint with underscore",
			pointer:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef_0",
			expectTxid: false,
		},
		{
			name:       "valid outpoint with dot",
			pointer:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.0",
			expectTxid: false,
		},
		{
			name:       "valid txid only",
			pointer:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			expectTxid: true,
		},
		{
			name:        "invalid pointer",
			pointer:     "invalid",
			expectError: true,
		},
		{
			name:        "too short",
			pointer:     "0123456789abcdef",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outpoint, isTxid, err := resolvePointerToOutpoint(tt.pointer)
			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if outpoint == nil {
				t.Fatal("expected non-nil outpoint")
			}
			if isTxid != tt.expectTxid {
				t.Errorf("expected isTxid=%v, got %v", tt.expectTxid, isTxid)
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
	if v.GetBool("ordfs.enabled") != false {
		t.Errorf("expected enabled=false, got %v", v.GetBool("ordfs.enabled"))
	}
	if !v.GetBool("ordfs.routes.enabled") {
		t.Error("expected routes.enabled=true")
	}
	if v.GetString("ordfs.redis.addr") != "localhost:6379" {
		t.Errorf("expected redis.addr=localhost:6379, got %s", v.GetString("ordfs.redis.addr"))
	}
}

func TestConfigInitializeDisabled(t *testing.T) {
	cfg := &Config{Enabled: false}

	svc, err := cfg.Initialize(nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc != nil {
		t.Error("expected nil services when disabled")
	}
}

func TestConfigInitializeNoJungleBus(t *testing.T) {
	cfg := &Config{Enabled: true}

	_, err := cfg.Initialize(nil, nil, nil)
	if err == nil {
		t.Fatal("expected error when junglebus client is nil")
	}
}

// Helper function
func intPtr(i int) *int {
	return &i
}
