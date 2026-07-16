package collection

import (
	"encoding/json"

	"github.com/b-open-io/1sat-stack/pkg/parse"
	"github.com/b-open-io/1sat-stack/pkg/template/bitcom"
	"github.com/b-open-io/1sat-stack/pkg/template/inscription"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Collection MAP subTypes.
const (
	SubTypeCollection     = "collection"
	SubTypeCollectionItem = "collectionItem"
)

// MapFields is the subset of MAP data used for collection admission and indexing.
type MapFields struct {
	Type         string
	SubType      string
	Name         string
	CollectionID string
	MintNumber   *int
	Rank         *int
	SubTypeData  string
	Raw          map[string]string
}

// DecodeMapFields extracts MAP SET fields from a locking script.
func DecodeMapFields(lockingScript *script.Script) *MapFields {
	if lockingScript == nil {
		return nil
	}
	bc := bitcom.Decode(lockingScript)
	if bc == nil {
		return nil
	}
	for _, proto := range bc.Protocols {
		if proto.Protocol != bitcom.MapPrefix {
			continue
		}
		m := bitcom.DecodeMap(proto.Script)
		if m == nil || m.Data == nil {
			continue
		}
		fields := &MapFields{
			Type:        m.Data["type"],
			SubType:     m.Data["subType"],
			Name:        m.Data["name"],
			SubTypeData: m.Data["subTypeData"],
			Raw:         m.Data,
		}
		// collectionId / mintNumber / rank live in subTypeData JSON (collectionItem subtype).
		if fields.SubTypeData != "" {
			var sub struct {
				CollectionID string `json:"collectionId"`
				MintNumber   *int   `json:"mintNumber"`
				Rank         *int   `json:"rank"`
			}
			if json.Unmarshal([]byte(fields.SubTypeData), &sub) == nil {
				fields.CollectionID = sub.CollectionID
				fields.MintNumber = sub.MintNumber
				fields.Rank = sub.Rank
			}
		}
		return fields
	}
	return nil
}

// IsCollectionMintOutput reports whether an output can be a collection or item mint:
// exactly 1 satoshi and an inscription envelope (1Sat ordinal).
func IsCollectionMintOutput(output *transaction.TransactionOutput) bool {
	if output == nil || output.Satoshis != 1 || output.LockingScript == nil {
		return false
	}
	return inscription.Decode(output.LockingScript) != nil
}

// NormalizeCollectionID expands same-tx "_N" references using the given outpoint.
func NormalizeCollectionID(collectionID string, outpoint *transaction.Outpoint) string {
	return parse.NormalizeCollectionID(collectionID, outpoint)
}

// FirstValidSigma returns the first SIGMA signature that verifies against the
// transaction for the given output index.
func FirstValidSigma(tx *transaction.Transaction, vout int) *bitcom.Sigma {
	if tx == nil || vout < 0 || vout >= len(tx.Outputs) {
		return nil
	}
	out := tx.Outputs[vout]
	if out == nil || out.LockingScript == nil {
		return nil
	}
	bc := bitcom.Decode(out.LockingScript)
	if bc == nil {
		return nil
	}
	sigmas := bitcom.DecodeSIGMA(bc)
	for i, s := range sigmas {
		if s == nil || s.SignerAddress == "" || s.SignatureValue == "" {
			continue
		}
		s.Transaction = tx
		s.TargetOutput = vout
		s.SigmaInstance = i
		if err := s.VerifyTransactionSignature(); err != nil {
			continue
		}
		if s.Valid {
			return s
		}
	}
	return nil
}

// ContentType returns the inscription media type when present.
func ContentType(lockingScript *script.Script) string {
	if lockingScript == nil {
		return ""
	}
	insc := inscription.Decode(lockingScript)
	if insc == nil {
		return ""
	}
	return insc.File.Type
}
