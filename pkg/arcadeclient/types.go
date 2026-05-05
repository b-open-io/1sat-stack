package arcadeclient

// Status constants matching arcade's transaction lifecycle. Source of truth is
// github.com/bsv-blockchain/arcade/models on origin/main; we mirror the values
// here so this package doesn't have to track arcade's package layout.
const (
	StatusUnknown              = "UNKNOWN"
	StatusReceived             = "RECEIVED"
	StatusSentToNetwork        = "SENT_TO_NETWORK"
	StatusAcceptedByNetwork    = "ACCEPTED_BY_NETWORK"
	StatusSeenOnNetwork        = "SEEN_ON_NETWORK"
	StatusSeenMultipleNodes    = "SEEN_MULTIPLE_NODES"
	StatusPendingRetry         = "PENDING_RETRY"
	StatusStumpProcessing      = "STUMP_PROCESSING"
	StatusRejected             = "REJECTED"
	StatusDoubleSpendAttempted = "DOUBLE_SPEND_ATTEMPTED"
	StatusMined                = "MINED"
	StatusImmutable            = "IMMUTABLE"
)

// TransactionStatus is the response shape returned by GET /tx/:txid.
type TransactionStatus struct {
	Txid         string   `json:"txid"`
	TxStatus     string   `json:"txStatus"`
	Timestamp    string   `json:"timestamp"`
	BlockHash    string   `json:"blockHash,omitempty"`
	BlockHeight  uint64   `json:"blockHeight,omitempty"`
	MerklePath   string   `json:"merklePath,omitempty"`
	ExtraInfo    string   `json:"extraInfo,omitempty"`
	CompetingTxs []string `json:"competingTxs,omitempty"`
}

// SubmitOptions configures a single POST /tx request. Empty fields are not sent.
type SubmitOptions struct {
	CallbackURL       string // X-CallbackUrl
	CallbackToken     string // X-CallbackToken — overrides Client.callbackToken if set
	FullStatusUpdates bool   // X-FullStatusUpdates: true
}

// SSEEvent is a parsed status event from the /events stream.
// The payload is intentionally slim; callers fetch full TransactionStatus via GetStatus.
type SSEEvent struct {
	ID        string `json:"-"` // SSE event id (nanosecond timestamp); used for Last-Event-ID resume
	Txid      string `json:"txid"`
	TxStatus  string `json:"txStatus"`
	Timestamp string `json:"timestamp"`
}

// IsTerminal reports whether status is a final outcome — no further updates expected.
func IsTerminal(status string) bool {
	switch status {
	case StatusMined, StatusImmutable, StatusRejected, StatusDoubleSpendAttempted:
		return true
	}
	return false
}

// IsAccepted reports whether status indicates the tx has propagated to the network
// or better. Used as a "good enough" stop point for SubmitAndWait.
func IsAccepted(status string) bool {
	switch status {
	case StatusSentToNetwork, StatusAcceptedByNetwork, StatusSeenOnNetwork, StatusSeenMultipleNodes,
		StatusMined, StatusImmutable:
		return true
	}
	return false
}

// IsRejected reports whether status indicates the tx will not make it to chain.
func IsRejected(status string) bool {
	switch status {
	case StatusRejected, StatusDoubleSpendAttempted:
		return true
	}
	return false
}
