package beef

import (
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/gofiber/fiber/v2"
)

// Routes provides HTTP routes for BEEF operations
type Routes struct {
	storage *Storage
}

// NewRoutes creates a new Routes instance
func NewRoutes(storage *Storage) *Routes {
	return &Routes{storage: storage}
}

// Register registers routes with a fiber router group
func (r *Routes) Register(router fiber.Router) {
	router.Get("/:txid", r.getBeef)
	router.Get("/:txid/raw", r.getRawTx)
	router.Get("/:txid/proof", r.getProof)
}

// getBeef handles GET /:txid - returns BEEF for a transaction
// @Summary Get BEEF for a transaction
// @Description Retrieves the BEEF (BSV Envelope Format) for a specific transaction
// @Tags beef
// @Produce application/octet-stream
// @Param txid path string true "Transaction ID"
// @Success 200 {file} binary "BEEF bytes"
// @Failure 404 {object} map[string]string "Transaction not found"
// @Router /api/beef/{txid} [get]
func (r *Routes) getBeef(c *fiber.Ctx) error {
	txidStr := c.Params("txid")
	txid, err := chainhash.NewHashFromHex(txidStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid txid"})
	}

	beefBytes, err := r.storage.LoadBeef(c.Context(), txid)
	if err != nil {
		if err.Error() == "transaction "+txidStr+" not found in BEEF" {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	c.Set("Content-Type", "application/octet-stream")
	return c.Send(beefBytes)
}

// getRawTx handles GET /:txid/raw - returns raw transaction bytes
// @Summary Get raw transaction
// @Description Retrieves just the raw transaction bytes (without proof)
// @Tags beef
// @Produce application/octet-stream
// @Param txid path string true "Transaction ID"
// @Success 200 {file} binary "Raw transaction bytes"
// @Failure 404 {object} map[string]string "Transaction not found"
// @Router /api/beef/{txid}/raw [get]
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
// @Router /api/beef/{txid}/proof [get]
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
	return c.Send(proof)
}
