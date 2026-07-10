package bitcom

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/bsv-blockchain/go-sdk/script"
)

// BAPPrefix is the bitcom protocol prefix for Bitcoin Attestation Protocol (BAP)
const BAPPrefix = "1BAPSuaPnfGnSBM3GLV9yhxUdYe4vGbdMT"
const pipeSeparator string = "|"

// AttestationType is an enum for BAP Type Constants
type AttestationType string

// BAP attestation type constants
const (
	ATTEST AttestationType = "ATTEST"
	ID     AttestationType = "ID"
	REVOKE AttestationType = "REVOKE"
	ALIAS  AttestationType = "ALIAS"
)

// Bap represents a Bitcoin Attestation Protocol data structure
type Bap struct {
	BitcomIndex  uint            `json:"ii,omitempty"` // Index of the AIP in the Bitcom transaction
	Type         AttestationType `json:"type"`
	IDKey        string          `json:"id_key,omitempty"`  // ID: Identity key, ATTEST: URN Hash
	Address      string          `json:"address,omitempty"` // Address value
	Sequence     uint64          `json:"sequence"`
	Algorithm    string          `json:"algorithm,omitempty"`    // AIP algorithm
	SignerAddr   string          `json:"signer_addr,omitempty"`  // AIP signing address
	Signature    string          `json:"signature,omitempty"`    // AIP signature
	RootAddress  string          `json:"root_address,omitempty"` // For ID
	IsSignedByID bool            `json:"is_signed_by_id"`        // Whether it's signed by the ID
	Profile      json.RawMessage `json:"profile,omitempty"`      // Profile for ID
}

// DecodeBAP decodes a BAP protocol message from a Bitcom structure
func DecodeBAP(b *Bitcom) *Bap {
	// Safety check for nil
	if b == nil || len(b.Protocols) == 0 {
		return nil
	}

	// Look for the BAP protocol data
	for ii, proto := range b.Protocols {
		// Check if this is a BAP protocol entry
		if proto.Protocol == BAPPrefix {
			// Create a BAP struct to hold the decoded data
			bap := &Bap{
				BitcomIndex: uint(ii),
			}

			// Parse script into chunks for analysis
			scr := script.NewFromBytes(proto.Script)
			if scr == nil {
				continue
			}

			/*
				I fixed this in bitcom.Lock(). It was constructing the script improperly and pushing it as one big pushdata
			*/

			// // Try a direct approach to extract the data
			// s := proto.Script
			// var pos int

			// // Skip the first byte if it's a length byte (like 0x3c which is 60 in decimal)
			// if len(s) > 0 && s[0] > 0 && s[0] < 0x4c {
			// 	pos = 1
			// }

			// // Create a temp slice for the script data without the length byte
			// scriptData := s[pos:]
			// tempScr := script.NewFromBytes(scriptData)
			// if tempScr == nil {
			// 	continue
			// }

			// Now try to get the chunks
			chunks, err := scr.Chunks()
			if err != nil || len(chunks) < 2 { // Need at least TYPE and one other field
				// If parsing as chunks failed, try a different approach
				// Check if we can find the ID or ATTEST type in the script
				scriptStr := string(*scr)

				if strings.Contains(scriptStr, string(ID)) {
					// Found ID type
					parts := strings.SplitN(scriptStr, string(ID), 2)
					if len(parts) > 1 {
						bap.Type = ID
						remainingParts := strings.SplitN(parts[1], " ", 3)
						if len(remainingParts) >= 2 {
							bap.IDKey = strings.TrimSpace(remainingParts[0])
							bap.Address = strings.TrimSpace(remainingParts[1])
							return bap
						}
					}
				} else if strings.Contains(scriptStr, string(ATTEST)) {
					// Found ATTEST type
					parts := strings.SplitN(scriptStr, string(ATTEST), 2)
					if len(parts) > 1 {
						bap.Type = ATTEST
						remainingParts := strings.SplitN(parts[1], " ", 3)
						if len(remainingParts) >= 2 {
							bap.IDKey = strings.TrimSpace(remainingParts[0])
							bap.Sequence, _ = strconv.ParseUint(remainingParts[1], 10, 64)
							return bap
						}
					}
				}

				continue
			}

			// Parse BAP data fields
			// First chunk should be the TYPE (ATTEST, ID, REVOKE, ALIAS)
			bap.Type = AttestationType(chunks[0].Data)

			// Process based on the BAP type
			switch bap.Type {
			case ID:
				// ID structure: ID <identity key> <address>
				if len(chunks) >= 3 {
					bap.IDKey = string(chunks[1].Data)
					bap.Address = string(chunks[2].Data)

					// Look for AIP signature data which follows a pipe separator
					pipeIdx := -1
					for i := 3; i < len(chunks); i++ {
						if string(chunks[i].Data) == pipeSeparator {
							pipeIdx = i
							break
						}
					}

					if pipeIdx >= 0 && pipeIdx+3 < len(chunks) {
						// AIP signature data found
						bap.Algorithm = string(chunks[pipeIdx+2].Data)
						bap.SignerAddr = string(chunks[pipeIdx+3].Data)
						if pipeIdx+4 < len(chunks) {
							bap.Signature = string(chunks[pipeIdx+4].Data)
							bap.RootAddress = bap.SignerAddr // In ID, the signer is the root address
							bap.IsSignedByID = true
						}
					}
				}

			case ATTEST:
				// ATTEST structure: ATTEST <txid> <sequence number>
				if len(chunks) >= 3 {
					bap.IDKey = string(chunks[1].Data) // TXID being attested to
					bap.Sequence, _ = strconv.ParseUint(string(chunks[2].Data), 10, 64)

					// Look for AIP signature data
					pipeIdx := -1
					for i := 3; i < len(chunks); i++ {
						if string(chunks[i].Data) == pipeSeparator {
							pipeIdx = i
							break
						}
					}

					if pipeIdx >= 0 && pipeIdx+3 < len(chunks) {
						// AIP signature data found
						bap.Algorithm = string(chunks[pipeIdx+2].Data)
						bap.SignerAddr = string(chunks[pipeIdx+3].Data)
						if pipeIdx+4 < len(chunks) {
							bap.Signature = string(chunks[pipeIdx+4].Data)
							// Check if signer matches an ID pattern - would require additional context
							bap.IsSignedByID = false // Default to false until we verify
						}
					}
				}

			case REVOKE:
				// REVOKE structure: REVOKE <txid> <sequence number>
				if len(chunks) >= 3 {
					bap.IDKey = string(chunks[1].Data) // TXID being revoked
					bap.Sequence, _ = strconv.ParseUint(string(chunks[2].Data), 10, 64)

					// Look for AIP signature data
					pipeIdx := -1
					for i := 3; i < len(chunks); i++ {
						if string(chunks[i].Data) == pipeSeparator {
							pipeIdx = i
							break
						}
					}

					if pipeIdx >= 0 && pipeIdx+3 < len(chunks) {
						// AIP signature data found
						bap.Algorithm = string(chunks[pipeIdx+2].Data)
						bap.SignerAddr = string(chunks[pipeIdx+3].Data)
						if pipeIdx+4 < len(chunks) {
							bap.Signature = string(chunks[pipeIdx+4].Data)
							// Check if signer matches an ID pattern - would require additional context
							bap.IsSignedByID = false // Default to false until we verify
						}
					}
				}

			case ALIAS:
				// ALIAS structure: ALIAS <alias> <address>
				if len(chunks) >= 3 {
					bap.IDKey = string(chunks[1].Data) // Alias
					bap.Profile = chunks[2].Data

					// Look for AIP signature data
					pipeIdx := -1
					for i := 3; i < len(chunks); i++ {
						if string(chunks[i].Data) == pipeSeparator {
							pipeIdx = i
							break
						}
					}

					if pipeIdx >= 0 && pipeIdx+3 < len(chunks) {
						// AIP signature data found
						bap.Algorithm = string(chunks[pipeIdx+2].Data)
						bap.SignerAddr = string(chunks[pipeIdx+3].Data)
						if pipeIdx+4 < len(chunks) {
							bap.Signature = string(chunks[pipeIdx+4].Data)
							// Check if signer matches an ID pattern - would require additional context
							bap.IsSignedByID = false // Default to false until we verify
						}
					}
				}
			}

			return bap
		}
	}

	return nil
}
