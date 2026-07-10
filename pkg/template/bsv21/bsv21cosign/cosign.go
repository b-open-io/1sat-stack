// Package bsv21cosign provides functionality for creating and decoding Bitcoin scripts
// that combine BSV21 tokens with cosign locking scripts.
//
// BSV21Cosign allows you to create scripts that both contain BSV21 token data and
// are spendable using a cosign script that requires signatures from both the owner
// and an approver. This enables the creation of token contracts with co-signer requirements.
package bsv21cosign

import (
	"encoding/hex"
	"encoding/json"
	"strconv"

	"github.com/b-open-io/1sat-stack/pkg/template/bsv21"
	"github.com/b-open-io/1sat-stack/pkg/template/cosign"
	"github.com/b-open-io/1sat-stack/pkg/template/inscription"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	sighash "github.com/bsv-blockchain/go-sdk/transaction/sighash"
)

// OrdCosign represents a BSV21 token with a Cosign locking script
type OrdCosign struct {
	Token  *bsv21.Bsv21   `json:"token"`  // The BSV21 token data
	Cosign *cosign.Cosign `json:"cosign"` // The cosign data (owner and approver)
}

// Decode attempts to extract an OrdCosign from a script
func Decode(s *script.Script) *OrdCosign {
	if s == nil {
		return nil
	}

	// Try to decode the inscription directly to see what it contains
	insc := inscription.Decode(s)
	if insc != nil {
		// We have an inscription, let's manually check for BSV21 token format
		if insc.File.Type == "application/bsv-20" {
			var data map[string]interface{}
			if err := json.Unmarshal(insc.File.Content, &data); err == nil {
				// Check if this is a BSV21 token (has p=bsv-20)
				if p, ok := data["p"]; ok && p == "bsv-20" {
					// This looks like a BSV21 token, create one manually
					token := &bsv21.Bsv21{
						Insc: insc,
					}

					// Add required fields
					if op, ok := data["op"].(string); ok {
						token.Op = op
					}

					if amt, ok := data["amt"].(float64); ok {
						token.Amt = uint64(amt)
					} else if amtStr, ok := data["amt"].(string); ok {
						if amtVal, err := strconv.ParseUint(amtStr, 10, 64); err == nil {
							token.Amt = amtVal
						}
					}

					// Add optional fields
					if sym, ok := data["sym"].(string); ok {
						token.Symbol = &sym
					}

					if dec, ok := data["dec"].(float64); ok {
						decValue := uint8(dec)
						token.Decimals = &decValue
					} else if decStr, ok := data["dec"].(string); ok {
						if decVal, err := strconv.ParseUint(decStr, 10, 8); err == nil {
							decValue := uint8(decVal)
							token.Decimals = &decValue
						}
					}

					if id, ok := data["id"].(string); ok {
						token.Id = id
					}

					// Try to extract cosign data
					var cosignData *cosign.Cosign

					// Check for cosign in script suffix
					if len(insc.ScriptSuffix) > 0 {
						suffix := script.NewFromBytes(insc.ScriptSuffix)
						cosignData = cosign.Decode(suffix)
					}

					// If no cosign data found, try the full script
					if cosignData == nil {
						cosignData = cosign.Decode(s)
					}

					// If still no cosign data, look for a P2PKH-like script
					if cosignData == nil {
						chunks, err := s.Chunks()
						if err == nil {
							// Look for DUP HASH160 pattern that starts P2PKH scripts
							for i := 0; i < len(chunks); i++ {
								if i+4 < len(chunks) &&
									chunks[i].Op == script.OpDUP &&
									chunks[i+1].Op == script.OpHASH160 &&
									len(chunks[i+2].Data) == 20 &&
									chunks[i+3].Op == script.OpEQUALVERIFY &&
									chunks[i+4].Op == script.OpCHECKSIG {

									// Extract the address
									addr, err := script.NewAddressFromPublicKeyHash(chunks[i+2].Data, true)
									if err == nil {
										// Create a minimal Cosign with just the address
										cosignData = &cosign.Cosign{
											Address: addr.AddressString,
										}
										break
									}
								}
							}
						}
					}

					// If we still don't have cosign data, this isn't a valid OrdCosign
					if cosignData == nil {
						return nil
					}

					// Create and return the OrdCosign
					return &OrdCosign{
						Token:  token,
						Cosign: cosignData,
					}
				}
			}
		}
	}

	// Fall back to standard BSV21 decode
	token := bsv21.Decode(s)
	if token == nil {
		return nil
	}

	// Try to extract cosign data from the script or its suffix
	var cosignData *cosign.Cosign

	// First check if the token has an inscription with a suffix
	if token.Insc != nil && len(token.Insc.ScriptSuffix) > 0 {
		suffix := script.NewFromBytes(token.Insc.ScriptSuffix)
		cosignData = cosign.Decode(suffix)
	}

	// If no cosign data found in suffix, try the full script
	if cosignData == nil {
		cosignData = cosign.Decode(s)
	}

	// If still no cosign data, look for a P2PKH-like script
	if cosignData == nil {
		chunks, err := s.Chunks()
		if err == nil {
			// Look for DUP HASH160 pattern that starts P2PKH scripts
			for i := 0; i < len(chunks); i++ {
				if i+4 < len(chunks) &&
					chunks[i].Op == script.OpDUP &&
					chunks[i+1].Op == script.OpHASH160 &&
					len(chunks[i+2].Data) == 20 &&
					chunks[i+3].Op == script.OpEQUALVERIFY &&
					chunks[i+4].Op == script.OpCHECKSIG {

					// Extract the address
					addr, err := script.NewAddressFromPublicKeyHash(chunks[i+2].Data, true)
					if err == nil {
						// Create a minimal Cosign with just the address
						cosignData = &cosign.Cosign{
							Address: addr.AddressString,
						}
						break
					}
				}
			}
		}
	}

	// If we still don't have cosign data, this isn't a valid OrdCosign
	if cosignData == nil {
		return nil
	}

	// Create and return the OrdCosign
	return &OrdCosign{
		Token:  token,
		Cosign: cosignData,
	}
}

// Lock creates a combined script that includes a BSV21 token with a Cosign locking script.
func (oc *OrdCosign) Lock(approverPubKey *ec.PublicKey) (*script.Script, error) {
	// Check if we have a Token and a Cosign
	if oc.Token == nil || oc.Cosign == nil {
		return nil, nil
	}

	// Get the address from the Cosign data
	address, err := script.NewAddressFromString(oc.Cosign.Address)
	if err != nil {
		return nil, err
	}

	// Create the cosign locking script
	cosignScript, err := cosign.Lock(address, approverPubKey)
	if err != nil {
		return nil, err
	}

	// Create a BSV21 format token with the "p":"bsv-20" field included
	tokenData := map[string]interface{}{
		"p":   "bsv-20",
		"op":  oc.Token.Op,
		"amt": oc.Token.Amt,
	}

	// Add optional fields if they exist
	if oc.Token.Symbol != nil {
		tokenData["sym"] = *oc.Token.Symbol
	}
	if oc.Token.Decimals != nil {
		tokenData["dec"] = *oc.Token.Decimals
	}
	if oc.Token.Icon != nil {
		tokenData["icon"] = *oc.Token.Icon
	}
	if oc.Token.Id != "" {
		tokenData["id"] = oc.Token.Id
	}

	// Marshal to JSON
	tokenJSON, err := json.Marshal(tokenData)
	if err != nil {
		return nil, err
	}

	// Create an inscription with BSV21 token data
	insc := &inscription.Inscription{
		File: inscription.File{
			Content: tokenJSON,
			Type:    "application/bsv-20",
		},
		ScriptSuffix: *cosignScript,
	}

	return insc.Lock()
}

// Create a new OrdCosign with the given address, approver, and token
func Create(address *script.Address, approverPubKey *ec.PublicKey, token *bsv21.Bsv21) (*OrdCosign, error) {
	// Create a Cosign object using the existing template
	cosignData := &cosign.Cosign{
		Address:  address.AddressString,
		Cosigner: hex.EncodeToString(approverPubKey.Compressed()),
	}

	// Return the combined OrdCosign
	return &OrdCosign{
		Token:  token,
		Cosign: cosignData,
	}, nil
}

// OwnerUnlock creates an unlocking template for the owner of the token
func (oc *OrdCosign) OwnerUnlock(key *ec.PrivateKey, sigHashFlag *sighash.Flag) (*cosign.CosignOwnerTemplate, error) {
	return cosign.OwnerUnlock(key, sigHashFlag)
}

// ApproverUnlock creates an unlocking template for the approver of the token
func (oc *OrdCosign) ApproverUnlock(key *ec.PrivateKey, userScript *script.Script, sigHashFlag *sighash.Flag) (*cosign.CosignApproverTemplate, error) {
	return cosign.ApproverUnlock(key, userScript, sigHashFlag)
}

// ToUnlocker creates a transaction input unlocker for this OrdCosign
func (oc *OrdCosign) ToUnlocker(ownerKey *ec.PrivateKey, approverKey *ec.PrivateKey, sigHashFlag *sighash.Flag) (*OrdCosignUnlocker, error) {
	if sigHashFlag == nil {
		shf := sighash.AllForkID
		sigHashFlag = &shf
	}

	// Return a custom unlocker that handles the OrdCosign unlocking process
	return &OrdCosignUnlocker{
		OwnerKey:    ownerKey,
		ApproverKey: approverKey,
		SigHashFlag: sigHashFlag,
	}, nil
}

// OrdCosignUnlocker is a transaction unlocker for OrdCosign
type OrdCosignUnlocker struct {
	OwnerKey    *ec.PrivateKey
	ApproverKey *ec.PrivateKey
	SigHashFlag *sighash.Flag
}

// Sign implements the transaction.Unlocker interface
func (u *OrdCosignUnlocker) Sign(tx *transaction.Transaction, inputIndex uint32) (*script.Script, error) {
	// Use the cosign package's functions to create the unlocking signatures
	ownerTemplate, err := cosign.OwnerUnlock(u.OwnerKey, u.SigHashFlag)
	if err != nil {
		return nil, err
	}

	ownerScript, err := ownerTemplate.Sign(tx, inputIndex)
	if err != nil {
		return nil, err
	}

	// Then get the approver's signature using the owner's script
	approverTemplate, err := cosign.ApproverUnlock(u.ApproverKey, ownerScript, u.SigHashFlag)
	if err != nil {
		return nil, err
	}

	// Return the combined script
	return approverTemplate.Sign(tx, inputIndex)
}

// EstimateLength implements the transaction.UnlockingScriptTemplate interface
func (u *OrdCosignUnlocker) EstimateLength(tx *transaction.Transaction, inputIndex uint32) uint32 {
	// A cosign unlocking script is typically around 180-200 bytes
	return 200
}
