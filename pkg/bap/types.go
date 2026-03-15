package bap

import (
	"encoding/json"
)

var IdentityKey = "identity"
var ProfileKey = "profile"
var ProfileSeqKey = "prof:seq"
var AddressIdentityKey = "add:id"
var AttestationKey = "attestation"

type Address struct {
	Address   string `json:"address"`
	Txid      string `json:"txId"`
	Block     uint32 `json:"block"`
	Timestamp uint32 `json:"-"`
}

type Identity struct {
	BapId          string         `json:"idKey"`
	FirstSeen      uint32         `json:"firstSeen"`
	FirstSeenTxid  string         `json:"_"`
	RootAddress    string         `json:"rootAddress"`
	CurrentAddress string         `json:"currentAddress"`
	Addresses      []Address      `json:"addresses"`
	Profile        map[string]any `json:"identity,omitempty"`
}

type Signer struct {
	UrnHash   string `json:"_"`
	BapID     string `json:"idKey"`
	Address   string `json:"signingAddress"`
	Sequence  uint64 `json:"sequence"`
	Block     uint32 `json:"block"`
	Txid      string `json:"txId"`
	Timestamp uint32 `json:"timestamp"`
	Revoked   bool   `json:"revoked"`
}

type Attestation struct {
	UrnHash string    `json:"hash"`
	Signers []*Signer `json:"signers"`
}

type Profile struct {
	BapID string          `json:"_id"`
	Data  json.RawMessage `json:"data"`
}

type ValidByAddressRequest struct {
	Address string `json:"address"`
	Block   uint32 `json:"block"`
}

type ValidityRecord struct {
	Valid bool   `json:"valid"`
	Block uint32 `json:"block"`
}

type ValidByAddressResponse struct {
	Identity       Identity       `json:"identity"`
	ValidityRecord ValidityRecord `json:"validityRecord"`
	Profile        map[string]any `json:"profile,omitempty"`
}
