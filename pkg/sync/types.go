package sync

// AddressDerivation pairs a BRC-29 address with its derivation metadata.
type AddressDerivation struct {
	Address          string
	DerivationPrefix string // base64
	DerivationSuffix string // base64
}

// PaymentMessage is the body format for wallet payment messages
// in the message box. Marshaled by 1sat-stack's paymail service.
type PaymentMessage struct {
	Beef              string `json:"beef"`
	OutputIndex       uint32 `json:"outputIndex"`
	DerivationPrefix  string `json:"derivationPrefix"`
	DerivationSuffix  string `json:"derivationSuffix"`
	SenderIdentityKey string `json:"senderIdentityKey"`
	Satoshis          uint64 `json:"satoshis"`
	Alias             string `json:"alias"`
}

// MessageOut is the envelope returned by the message box API.
type MessageOut struct {
	MessageID string `json:"messageId"`
	Body      string `json:"body"`
	Sender    string `json:"sender"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// SyncOutput is a single output from the address sync SSE stream.
type SyncOutput struct {
	Outpoint  string  `json:"outpoint"`
	Score     float64 `json:"score"`
	SpendTxid string  `json:"spendTxid,omitempty"`
}

// SyncStats reports results of a sync operation.
type SyncStats struct {
	Processed int
	Failed    int
	Skipped   int
}
