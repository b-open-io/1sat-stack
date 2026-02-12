package arcade

import "github.com/bsv-blockchain/arcade/models"

// The following are swagger documentation stubs for arcade routes.
// The actual handlers are provided by the arcade library.

// TransactionRequest represents a transaction submission request.
type TransactionRequest struct {
	RawTx string `json:"rawTx" example:"0100000001..."`
}

// FeeAmount represents fee amount in bytes and satoshis.
type FeeAmount struct {
	Bytes    uint64 `json:"bytes"`
	Satoshis uint64 `json:"satoshis"`
}

// PolicyResponse represents the policy configuration.
type PolicyResponse struct {
	MaxScriptSizePolicy     uint64    `json:"maxscriptsizepolicy"`
	MaxTxSigOpsCountsPolicy uint64    `json:"maxtxsigopscountspolicy"`
	MaxTxSizePolicy         uint64    `json:"maxtxsizepolicy"`
	MiningFee               FeeAmount `json:"miningFee"`
}

// submitTransaction submits a single transaction
// @Summary Submit transaction
// @Description Submit a single transaction for broadcast. Accepts raw transaction bytes, hex string, or JSON with rawTx field.
// @Tags arcade
// @Accept json,application/octet-stream,text/plain
// @Produce json
// @Param transaction body TransactionRequest true "Transaction data"
// @Param X-CallbackUrl header string false "URL for status callbacks"
// @Param X-CallbackToken header string false "Token for SSE event filtering"
// @Param X-FullStatusUpdates header string false "Send all status updates (true/false)"
// @Param X-SkipFeeValidation header string false "Skip fee validation (true/false)"
// @Param X-SkipScriptValidation header string false "Skip script validation (true/false)"
// @Success 200 {object} models.TransactionStatus
// @Failure 400 {object} map[string]string
// @Failure 465 {object} map[string]string "ARC validation error"
// @Failure 500 {object} map[string]string
// @Router /arcade/tx [post]
func submitTransaction() {}

// submitTransactions submits multiple transactions
// @Summary Submit multiple transactions
// @Description Submit multiple transactions for broadcast
// @Tags arcade
// @Accept json
// @Produce json
// @Param transactions body []TransactionRequest true "Array of transactions"
// @Param X-CallbackUrl header string false "URL for status callbacks"
// @Param X-CallbackToken header string false "Token for SSE event filtering"
// @Param X-FullStatusUpdates header string false "Send all status updates (true/false)"
// @Param X-SkipFeeValidation header string false "Skip fee validation (true/false)"
// @Param X-SkipScriptValidation header string false "Skip script validation (true/false)"
// @Success 200 {array} models.TransactionStatus
// @Failure 400 {object} map[string]string
// @Failure 465 {object} map[string]string "ARC validation error"
// @Router /arcade/txs [post]
func submitTransactions() {}

// getTransactionStatus retrieves transaction status
// @Summary Get transaction status
// @Description Get the current status of a submitted transaction
// @Tags arcade
// @Produce json
// @Param txid path string true "Transaction ID"
// @Success 200 {object} models.TransactionStatus
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /arcade/tx/{txid} [get]
func getTransactionStatus() {}

// getPolicy returns the policy configuration
// @Summary Get policy
// @Description Returns the transaction policy configuration including fee rates and limits
// @Tags arcade
// @Produce json
// @Success 200 {object} PolicyResponse
// @Router /arcade/policy [get]
func getPolicy() {}

// streamTransactionEvents streams transaction status updates via SSE
// @Summary Stream transaction events
// @Description Server-Sent Events stream of transaction status updates for transactions associated with the callback token
// @Tags arcade
// @Produce text/event-stream
// @Param callbackToken path string true "Callback token from transaction submission"
// @Success 200 {string} string "SSE stream of transaction status updates"
// @Router /arcade/events/{callbackToken} [get]
func streamTransactionEvents() {}

// Ensure models import is used
var _ models.TransactionStatus
