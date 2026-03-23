package faucet

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	sdk "github.com/bsv-blockchain/go-sdk/wallet"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/brc29"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/defs"
)

// HashPubKey hashes a public key for auth comparison (SHA256).
func HashPubKey(pubKeyHex string) string {
	hash := sha256.Sum256([]byte(pubKeyHex))
	return hex.EncodeToString(hash[:])
}

// HashAPIKey creates a SHA256 hash of an API key.
func HashAPIKey(apiKey string) string {
	hash := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(hash[:])
}

// ValidatePublicKey validates a hex-encoded public key can be parsed.
func ValidatePublicKey(pubKeyHex string) error {
	pubKeyBytes, err := hex.DecodeString(pubKeyHex)
	if err != nil {
		return fmt.Errorf("invalid hex format: %w", err)
	}
	_, err = ec.ParsePubKey(pubKeyBytes)
	if err != nil {
		return fmt.Errorf("invalid public key format: %w", err)
	}
	return nil
}

// OriginatorFromIdentity builds an originator string from an identity pubkey hex.
func OriginatorFromIdentity(identityHex string) string {
	if identityHex == "" {
		return "droplit.dashboard"
	}
	return fmt.Sprintf("droplit.api.k-%s", normalizeOriginatorPart(HashPubKey(identityHex)))
}

func normalizeOriginatorPart(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "unknown"
	}

	var b strings.Builder
	b.Grow(len(raw))
	lastWasDash := false

	for _, r := range raw {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			b.WriteRune(r)
			lastWasDash = false
			continue
		}
		if !lastWasDash && b.Len() > 0 {
			b.WriteByte('-')
			lastWasDash = true
		}
	}

	part := strings.Trim(b.String(), "-")
	if part == "" {
		part = "unknown"
	}
	if len(part) > 61 {
		sum := sha256.Sum256([]byte(part))
		part = hex.EncodeToString(sum[:20])
	}
	return part
}

// BRC29DepositKeyID constructs a BRC-29 key ID for deposit address derivation.
func BRC29DepositKeyID(faucetName string, invoice string) brc29.KeyID {
	return brc29.KeyID{
		DerivationPrefix: base64.StdEncoding.EncodeToString([]byte("droplit:" + faucetName)),
		DerivationSuffix: base64.StdEncoding.EncodeToString([]byte("deposit:" + invoice)),
	}
}

// DeriveBRC100DepositAddress derives a BRC-29 deposit address for a faucet.
func DeriveBRC100DepositAddress(faucetName string, invoice string, faucetPrivKey *ec.PrivateKey, network defs.BSVNetwork) (string, *sdk.Payment, error) {
	_, anyonePub := sdk.AnyoneKey()
	keyID := BRC29DepositKeyID(faucetName, invoice)

	var addr string
	if network == defs.NetworkTestnet {
		derived, err := brc29.AddressForSelf(anyonePub, keyID, faucetPrivKey, brc29.WithTestNet())
		if err != nil {
			return "", nil, fmt.Errorf("failed to derive testnet BRC-29 address: %w", err)
		}
		addr = derived.AddressString
	} else {
		derived, err := brc29.AddressForSelf(anyonePub, keyID, faucetPrivKey, brc29.WithMainNet())
		if err != nil {
			return "", nil, fmt.Errorf("failed to derive mainnet BRC-29 address: %w", err)
		}
		addr = derived.AddressString
	}

	prefixBytes, err := base64.StdEncoding.DecodeString(keyID.DerivationPrefix)
	if err != nil {
		return "", nil, fmt.Errorf("failed to decode derivation prefix: %w", err)
	}
	suffixBytes, err := base64.StdEncoding.DecodeString(keyID.DerivationSuffix)
	if err != nil {
		return "", nil, fmt.Errorf("failed to decode derivation suffix: %w", err)
	}

	return addr, &sdk.Payment{
		DerivationPrefix:  prefixBytes,
		DerivationSuffix:  suffixBytes,
		SenderIdentityKey: anyonePub,
	}, nil
}

// PrettyTime formats a time.Time into a human-readable "ago" string.
func PrettyTime(t time.Time) string {
	diff := time.Since(t)
	seconds := int(diff.Seconds())
	minutes := int(diff.Minutes())
	hours := int(diff.Hours())
	days := hours / 24

	switch {
	case seconds < 60:
		return fmt.Sprintf("%d seconds ago", seconds)
	case minutes < 60:
		if minutes == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	case hours < 24:
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	default:
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

// BuildOpReturnTemplate constructs a transaction template with only an OP_RETURN output.
func BuildOpReturnTemplate(opReturnDataStrings []string, encoding string) (*transaction.Transaction, error) {
	processedData, err := decodeOpReturnData(opReturnDataStrings, encoding)
	if err != nil {
		return nil, err
	}

	tx := transaction.NewTransaction()
	if err := tx.AddOpReturnPartsOutput(processedData); err != nil {
		return nil, fmt.Errorf("failed to add OP_RETURN output: %w", err)
	}
	return tx, nil
}

func decodeOpReturnData(parts []string, encoding string) ([][]byte, error) {
	normalized := encoding
	if normalized == "" {
		normalized = "utf8"
	}

	out := make([][]byte, 0, len(parts))
	for idx, item := range parts {
		var decoded []byte
		var err error

		switch normalized {
		case "hex":
			decoded, err = hex.DecodeString(item)
		case "base64":
			decoded, err = base64.StdEncoding.DecodeString(item)
		case "utf8":
			decoded = []byte(item)
		default:
			return nil, fmt.Errorf("invalid encoding for OP_RETURN data part %d: %s", idx, normalized)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to decode OP_RETURN data part %d (encoding: %s): %w", idx, normalized, err)
		}
		out = append(out, decoded)
	}
	return out, nil
}

// CreateInscriptionScript creates an ordinal inscription script.
func CreateInscriptionScript(contentType string, data []byte, bsv21Metadata *BSV21Metadata) (*script.Script, error) {
	var scriptBytes []byte

	scriptBytes = append(scriptBytes, script.OpFALSE)
	scriptBytes = append(scriptBytes, script.OpRETURN)

	ordMarker := []byte("ord")
	scriptBytes = append(scriptBytes, byte(len(ordMarker)))
	scriptBytes = append(scriptBytes, ordMarker...)

	scriptBytes = append(scriptBytes, script.Op1)

	contentTypeBytes := []byte(contentType)
	scriptBytes = append(scriptBytes, byte(len(contentTypeBytes)))
	scriptBytes = append(scriptBytes, contentTypeBytes...)

	scriptBytes = append(scriptBytes, script.Op0)

	maxChunkSize := 520
	for i := 0; i < len(data); i += maxChunkSize {
		end := i + maxChunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[i:end]

		chunkLen := len(chunk)
		if chunkLen <= 75 {
			scriptBytes = append(scriptBytes, byte(chunkLen))
		} else if chunkLen <= 255 {
			scriptBytes = append(scriptBytes, 0x4c)
			scriptBytes = append(scriptBytes, byte(chunkLen))
		} else {
			scriptBytes = append(scriptBytes, 0x4d)
			scriptBytes = append(scriptBytes, byte(chunkLen&0xff))
			scriptBytes = append(scriptBytes, byte((chunkLen>>8)&0xff))
		}
		scriptBytes = append(scriptBytes, chunk...)
	}

	return script.NewFromBytes(scriptBytes), nil
}
