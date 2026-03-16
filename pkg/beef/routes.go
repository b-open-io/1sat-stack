package beef

import (
	"context"

	"github.com/b-open-io/1sat-stack/pkg/httputil"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

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
// @Description Retrieves the BEEF (BSV Envelope Format) for a specific transaction
// @Tags beef
// @Produce application/octet-stream
// @Param txid path string true "Transaction ID"
// @Success 200 {file} binary "BEEF bytes"
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

	c.Set("Content-Type", "application/octet-stream")
	r.setBeefCache(c, beefBytes)
	return c.Send(beefBytes)
}

// getRawTx handles GET /:txid/tx - returns raw transaction bytes
// @Summary Get transaction
// @Description Retrieves just the raw transaction bytes (without proof)
// @Tags beef
// @Produce application/octet-stream
// @Param txid path string true "Transaction ID"
// @Success 200 {file} binary "Transaction bytes"
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

	c.Set("Content-Type", "application/octet-stream")
	httputil.SetImmutable(c)
	return c.Send(rawTx)
}

// getProof handles GET /:txid/proof - returns merkle proof bytes
// @Summary Get merkle proof
// @Description Retrieves just the merkle proof bytes for a transaction
// @Tags beef
// @Produce application/octet-stream
// @Param txid path string true "Transaction ID"
// @Success 200 {file} binary "Merkle proof bytes"
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

	c.Set("Content-Type", "application/octet-stream")

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

	return c.Send(proof)
}
