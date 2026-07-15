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
	if !v.GetBool("ordfs.enabled") {
		t.Errorf("expected enabled=true, got %v", v.GetBool("ordfs.enabled"))
	}
	if !v.GetBool("ordfs.routes.enabled") {
		t.Error("expected routes.enabled=true")
	}
	if v.GetString("ordfs.routes.prefix") != "/ordfs" {
		t.Errorf("expected routes.prefix=/ordfs, got %s", v.GetString("ordfs.routes.prefix"))
	}
	if v.GetInt("ordfs.cache.lru_size") != 10000 {
		t.Errorf("expected cache.lru_size=10000, got %d", v.GetInt("ordfs.cache.lru_size"))
	}
}

func TestConfigInitializeDisabled(t *testing.T) {
	cfg := &Config{Enabled: false}

	svc, err := cfg.Initialize(nil, nil, "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc != nil {
		t.Error("expected nil services when disabled")
	}
}

func TestConfigInitializeNilDeps(t *testing.T) {
	cfg := &Config{Enabled: true}

	_, err := cfg.Initialize(nil, nil, "", nil, nil)
	if err == nil {
		t.Fatal("expected error when beef storage is nil")
	}
}

func TestParseRelativeVout(t *testing.T) {
	tests := []struct {
		name      string
		pointer   string
		wantVout  uint32
		wantMatch bool
	}{
		{"_0", "_0", 0, true},
		{"_1", "_1", 1, true},
		{"_8", "_8", 8, true},
		{"_42", "_42", 42, true},
		{"full outpoint", "aabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccdd_0", 0, false},
		{"bare txid", "aabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccdd", 0, false},
		{"no underscore", "0", 0, false},
		{"just underscore", "_", 0, false},
		{"negative", "_-1", 0, false},
		{"text", "_abc", 0, false},
		{"ord prefix", "ord://_0", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vout, ok := parseRelativeVout(tt.pointer)
			if ok != tt.wantMatch {
				t.Errorf("parseRelativeVout(%q) match = %v, want %v", tt.pointer, ok, tt.wantMatch)
			}
			if ok && vout != tt.wantVout {
				t.Errorf("parseRelativeVout(%q) vout = %d, want %d", tt.pointer, vout, tt.wantVout)
			}
		})
	}
}

func TestIsContentRef(t *testing.T) {
	tests := []struct {
		contentType string
		want        bool
	}{
		{"image/png; ref=ordfs", true},
		{"image/png;ref=ordfs", true},
		{"image/png; charset=utf-8; ref=ordfs", true},
		{"video/mp4; stream=ordfs; ref=ordfs", true},
		{"image/png; ref = ordfs", true},
		{"image/png", false},
		{"image/png; stream=ordfs", false},
		{"ord-fs/json", false},
		{"ordfs/stream", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			if got := IsContentRef(tt.contentType); got != tt.want {
				t.Errorf("IsContentRef(%q) = %v, want %v", tt.contentType, got, tt.want)
			}
		})
	}
}

func TestResolveContentRefNoOp(t *testing.T) {
	o := &Ordfs{}
	resp := &Response{
		ContentType: "image/png",
		Content:     []byte("png-bytes"),
	}
	got, err := o.ResolveContentRef(t.Context(), resp)
	if err != nil {
		t.Fatal(err)
	}
	if got != resp {
		t.Error("expected same response for non-ref")
	}
}

func TestResolveContentRefEmptyPointer(t *testing.T) {
	o := &Ordfs{}
	_, err := o.ResolveContentRef(t.Context(), &Response{
		ContentType: "image/png; ref=ordfs",
		Content:     []byte("   "),
	})
	if err == nil {
		t.Fatal("expected error for empty pointer")
	}
}

// Helper function
func intPtr(i int) *int {
	return &i
}
