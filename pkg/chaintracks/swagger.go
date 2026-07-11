package chaintracks

// The following are swagger documentation stubs for chaintracks routes.
// The actual handlers are provided by the go-chaintracks library.

// BlockHeader represents a block header response.
type BlockHeader struct {
	Version      uint32 `json:"version"`      // 4 bytes - Block version
	PreviousHash []byte `json:"previousHash"` // 32 bytes - Previous block hash
	MerkleRoot   []byte `json:"merkleRoot"`   // 32 bytes - Merkle root hash
	Time         uint32 `json:"time"`         // 4 bytes - Block timestamp (Unix time)
	Bits         uint32 `json:"bits"`         // 4 bytes - Difficulty target
	Nonce        uint32 `json:"nonce"`        // 4 bytes - Nonce
	Height       uint32 `json:"height"`       // Block height in the chain
	Hash         []byte `json:"hash"`
}

// NetworkResponse represents the response for the network endpoint.
type NetworkResponse struct {
	Network string `json:"network" example:"mainnet"`
}

// HeightResponse represents the response for the height endpoint.
type HeightResponse struct {
	Height uint32 `json:"height" example:"874123"`
}

// ErrorResponse represents an error response.
type ErrorResponse struct {
	Error string `json:"error" example:"Header not found"`
}

// getNetwork returns the network name
// @Summary Get network name
// @Description Returns the Bitcoin network this service is connected to
// @Tags chaintracks
// @Produce json
// @Success 200 {object} NetworkResponse
// @Failure 500 {object} ErrorResponse
// @Router /network [get]
func getNetwork() {}

// getHeight returns the current chain height
// @Summary Get chain height
// @Description Returns the current blockchain height
// @Tags chaintracks
// @Produce json
// @Success 200 {object} HeightResponse
// @Router /height [get]
func getHeight() {}

// getTip returns the current chain tip
// @Summary Get chain tip
// @Description Returns the current chain tip block header
// @Tags chaintracks
// @Produce json
// @Success 200 {object} BlockHeader
// @Failure 404 {object} ErrorResponse
// @Router /tip [get]
func getTip() {}

// streamTipUpdates streams chain tip updates via SSE
// @Summary Stream chain tip updates
// @Description Server-Sent Events stream of chain tip updates. Sends the current tip immediately, then broadcasts new tips as they arrive.
// @Tags chaintracks
// @Produce text/event-stream
// @Success 200 {string} string "SSE stream of BlockHeader JSON objects"
// @Router /tip/stream [get]
func streamTipUpdates() {}

// getHeaderByHeight returns a block header by height
// @Summary Get header by height
// @Description Returns a block header at the specified height
// @Tags chaintracks
// @Produce json
// @Param height path int true "Block height"
// @Success 200 {object} BlockHeader
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /header/height/{height} [get]
func getHeaderByHeight() {}

// getHeaderByHash returns a block header by hash
// @Summary Get header by hash
// @Description Returns a block header with the specified hash
// @Tags chaintracks
// @Produce json
// @Param hash path string true "Block hash (hex)"
// @Success 200 {object} BlockHeader
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /header/hash/{hash} [get]
func getHeaderByHash() {}

// getHeaders returns multiple block headers as binary data
// @Summary Get multiple headers
// @Description Returns block headers starting from height as binary data (80 bytes per header)
// @Tags chaintracks
// @Produce application/octet-stream
// @Param height query int true "Starting block height"
// @Param count query int true "Number of headers to return"
// @Success 200 {string} binary "Concatenated 80-byte headers"
// @Failure 400 {object} ErrorResponse
// @Router /headers [get]
func getHeaders() {}
