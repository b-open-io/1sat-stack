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
	BapId          string         `json:"idKey" bson:"_id"`
	FirstSeen      uint32         `json:"firstSeen" bson:"firstSeen"`
	FirstSeenTxid  string         `json:"_" bson:"firstSeenTxid"`
	RootAddress    string         `json:"rootAddress" bson:"rootAddress"`
	CurrentAddress string         `json:"currentAddress" bson:"currentAddress"`
	Addresses      []Address      `json:"addresses" bson:"addresses"`
	Profile        map[string]any `json:"identity,omitempty" bson:"profile"`
}

type Signer struct {
	UrnHash   string `json:"_" bson:"urnHash"`
	BapID     string `json:"idKey" bson:"idKey"`
	Address   string `json:"signingAddress" bson:"signingAddress"`
	Sequence  uint64 `json:"sequence" bson:"sequence"`
	Block     uint32 `json:"block" bson:"block"`
	Txid      string `json:"txId" bson:"txId"`
	Timestamp uint32 `json:"timestamp" bson:"timestamp"`
	Revoked   bool   `json:"revoked" bson:"revoked"`
}

type Attestation struct {
	UrnHash string    `json:"hash"`
	Signers []*Signer `json:"signers"`
}

type Profile struct {
	BapID string          `json:"_id"`
	Data  json.RawMessage `json:"data"`
}
