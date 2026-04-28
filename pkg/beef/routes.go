package beef

import (
	"context"
	"encoding/base64"
	"encoding/hex"

	"github.com/b-open-io/1sat-stack/pkg/httputil"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

// sendEncoded writes data to the response in the requested encoding.
// format may be "", "binary", "hex", or "base64". Empty/binary preserves
// the original octet-stream behavior.
func sendEncoded(c *fiber.Ctx, data []byte) error {
	switch c.Query("format") {
	case "hex":
		c.Set("Content-Type", "text/plain; charset=utf-8")
		return c.SendString(hex.EncodeToString(data))
	case "base64":
		c.Set("Content-Type", "text/plain; charset=utf-8")
		return c.SendString(base64.StdEncoding.EncodeToString(data))
	case "", "binary":
		c.Set("Content-Type", "application/octet-stream")
		return c.Send(data)
	default:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid format, expected binary|hex|base64"})
	}
}

// Routes provides HTTP routes for BEEF operations
type Routes struct {
	storage   *Storage
	tipHeight func(ctx context.Context) uint32
}

// NewRoutes creates a new Routes instance
func NewRoutes(storage *Storage, tipHeight func(ctx context.Context) uint32) *Routes {
	return &Routes{storage: storage, tipHeight: tipHeight}
}

// Register registers routes with a fiber router group
func (r *Routes) Register(router fiber.Router) {
	router.Get("/:txid", r.getBeef)
	router.Get("/:txid/tx", r.getRawTx)
	router.Get("/:txid/proof", r.getProof)
}

// setBeefCache sets cache headers based on merkle proof depth.
func (r *Routes) setBeefCache(c *fiber.Ctx, beefBytes []byte) {
	tip := r.tipHeight(c.Context())
	if tip == 0 {
		httputil.SetNoStore(c)
		return
	}

	beef, _, _, err := transaction.ParseBeef(beefBytes)
	if err != nil || beef == nil {
		httputil.SetNoStore(c)
		return
	}

	var maxHeight uint32
	for _, bump := range beef.BUMPs {
		if bump.BlockHeight > maxHeight {
			maxHeight = bump.BlockHeight
		}
	}

	if maxHeight == 0 {
		httputil.SetNoStore(c)
		return
	}

	httputil.SetConfirmationCache(c, maxHeight, tip)
}

// getBeef handles GET /:txid - returns BEEF for a transaction
// @Summary Get BEEF for a transaction
// @Description Retrieves the BEEF (BSV Envelope Format) for a specific transaction. Use the format query parameter to choose binary (default), hex, or base64 encoding.
// @Tags beef
// @Produce application/octet-stream
// @Produce text/plain
// @Param txid path string true "Transaction ID"
// @Param format query string false "Response encoding" Enums(binary, hex, base64) default(binary)
// @Success 200 {file} binary "BEEF bytes (binary) or hex/base64 string"
// @Failure 400 {object} map[string]string "Invalid txid or format"
// @Failure 404 {object} map[string]string "Transaction not found"
// @Router /beef/{txid} [get]
func (r *Routes) getBeef(c *fiber.Ctx) error {
	txidStr := c.Params("txid")
	txid, err := chainhash.NewHashFromHex(txidStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid txid"})
	}

	beef, err := r.storage.LoadBeef(c.Context(), txid)
	if err != nil {
		if err.Error() == "transaction "+txidStr+" not found in BEEF" {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	beefBytes, err := beef.AtomicBytes(txid)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	r.setBeefCache(c, beefBytes)
	return sendEncoded(c, beefBytes)
}

// getRawTx handles GET /:txid/tx - returns raw transaction bytes
// @Summary Get transaction
// @Description Retrieves just the raw transaction bytes (without proof). Use the format query parameter to choose binary (default), hex, or base64 encoding.
// @Tags beef
// @Produce application/octet-stream
// @Produce text/plain
// @Param txid path string true "Transaction ID"
// @Param format query string false "Response encoding" Enums(binary, hex, base64) default(binary)
// @Success 200 {file} binary "Transaction bytes (binary) or hex/base64 string"
// @Failure 400 {object} map[string]string "Invalid txid or format"
// @Failure 404 {object} map[string]string "Transaction not found"
// @Router /beef/{txid}/tx [get]
func (r *Routes) getRawTx(c *fiber.Ctx) error {
	txidStr := c.Params("txid")
	txid, err := chainhash.NewHashFromHex(txidStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid txid"})
	}

	rawTx, err := r.storage.LoadRawTx(c.Context(), txid)
	if err != nil {
		if err == ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	httputil.SetImmutable(c)
	return sendEncoded(c, rawTx)
}

// getProof handles GET /:txid/proof - returns merkle proof bytes
// @Summary Get merkle proof
// @Description Retrieves just the merkle proof bytes for a transaction. Use the format query parameter to choose binary (default), hex, or base64 encoding.
// @Tags beef
// @Produce application/octet-stream
// @Produce text/plain
// @Param txid path string true "Transaction ID"
// @Param format query string false "Response encoding" Enums(binary, hex, base64) default(binary)
// @Success 200 {file} binary "Merkle proof bytes (binary) or hex/base64 string"
// @Failure 400 {object} map[string]string "Invalid txid or format"
// @Failure 404 {object} map[string]string "Proof not found"
// @Router /beef/{txid}/proof [get]
func (r *Routes) getProof(c *fiber.Ctx) error {
	txidStr := c.Params("txid")
	txid, err := chainhash.NewHashFromHex(txidStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid txid"})
	}

	proof, err := r.storage.LoadProof(c.Context(), txid)
	if err != nil {
		if err == ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	// For proof, we need to parse just enough to get the block height.
	// LoadBeef is heavier but gives us the BUMP data.
	beefData, err := r.storage.LoadBeef(c.Context(), txid)
	if err == nil && beefData != nil {
		if beefBytes, err := beefData.AtomicBytes(txid); err == nil {
			r.setBeefCache(c, beefBytes)
		} else {
			httputil.SetNoStore(c)
		}
	} else {
		httputil.SetNoStore(c)
	}

	return sendEncoded(c, proof)
}
