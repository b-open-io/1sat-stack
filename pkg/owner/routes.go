package owner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

// Routes provides HTTP handlers for owner queries.
type Routes struct {
	ctx         context.Context
	sync        *OwnerSync
	outputStore *txo.OutputStore
	logger      *slog.Logger
}

// NewRoutes creates a new Routes instance.
func NewRoutes(ctx context.Context, sync *OwnerSync, outputStore *txo.OutputStore, logger *slog.Logger) *Routes {
	if logger == nil {
		logger = slog.Default()
	}
	return &Routes{
		ctx:         ctx,
		sync:        sync,
		outputStore: outputStore,
		logger:      logger,
	}
}

// Register registers all owner routes on the given router.
func (r *Routes) Register(router fiber.Router) {
	router.Get("/sync", r.OwnerSync) // Multi-owner sync via query params
	router.Get("/:owner/txos", r.OwnerTxos)
	router.Get("/:owner/balance", r.OwnerBalance)
}

// OwnerTxos returns transaction outputs owned by a specific owner.
// @Summary Get owner TXOs
// @Description Get transaction outputs owned by a specific owner (address/pubkey/script hash)
// @Tags owner
// @Produce json
// @Param owner path string true "Owner identifier (address, pubkey, or script hash)"
// @Param refresh query bool false "Refresh owner data from blockchain before returning" default(true)
// @Param unspent query bool false "Filter for unspent outputs only" default(true)
// @Param tags query string false "Comma-separated list of tags to include"
// @Param from query number false "Starting score for pagination"
// @Param rev query bool false "Reverse order"
// @Param limit query int false "Maximum number of results" default(100)
// @Success 200 {array} txo.IndexedOutput
// @Failure 500 {string} string "Internal server error"
// @Router /owner/{owner}/txos [get]
func (r *Routes) OwnerTxos(c *fiber.Ctx) error {
	owner := c.Params("owner")

	// Sync by default
	if c.QueryBool("refresh", true) && r.sync != nil {
		if err := r.sync.Sync(c.Context(), owner); err != nil {
			return err
		}
	}

	var from *float64
	if f := c.QueryFloat("from", 0); f != 0 {
		from = &f
	}

	var tags []string
	if tagsQuery := c.Query("tags", ""); tagsQuery != "" {
		tags = strings.Split(tagsQuery, ",")
	}

	cfg := &txo.OutputSearchCfg{
		SearchCfg: store.SearchCfg{
			Keys:    [][]byte{[]byte("own:" + owner)},
			Limit:   uint32(c.QueryInt("limit", 100)),
			Reverse: c.QueryBool("rev", false),
			From:    from,
		},
		FilterSpent: c.QueryBool("unspent", true),
		IncludeTags: tags,
	}

	outputs, err := r.outputStore.SearchOutputs(c.Context(), cfg)
	if err != nil {
		return err
	}

	return c.JSON(outputs)
}

// OwnerBalance returns the satoshi balance for a specific owner.
// @Summary Get owner balance
// @Description Get the satoshi balance for a specific owner
// @Tags owner
// @Produce json
// @Param owner path string true "Owner identifier (address, pubkey, or script hash)"
// @Success 200 {object} BalanceResponse
// @Failure 500 {string} string "Internal server error"
// @Router /owner/{owner}/balance [get]
func (r *Routes) OwnerBalance(c *fiber.Ctx) error {
	owner := c.Params("owner")

	cfg := &txo.OutputSearchCfg{
		SearchCfg: store.SearchCfg{
			Keys: [][]byte{[]byte("own:" + owner)},
		},
	}

	balance, count, err := r.outputStore.SearchBalance(c.Context(), cfg)
	if err != nil {
		return err
	}

	return c.JSON(BalanceResponse{
		Balance: balance,
		Count:   count,
	})
}

// BalanceResponse is the response for balance queries.
type BalanceResponse struct {
	Balance uint64 `json:"balance"`
	Count   int    `json:"count"`
}

// SyncOutput represents an outpoint with its score and optional spend txid
type SyncOutput struct {
	Outpoint  string  `json:"outpoint"`
	Score     float64 `json:"score"`
	SpendTxid string  `json:"spendTxid,omitempty"`
}

// OwnerSync streams owner sync via SSE.
// @Summary Stream owner sync via SSE
// @Description Stream paginated outputs for wallet synchronization via Server-Sent Events. Streams all outputs until exhausted, then triggers background sync and sends retry directive.
// @Tags owner
// @Produce text/event-stream
// @Param owner query []string true "Owner identifier(s) (address, pubkey, or script hash)"
// @Param from query number false "Starting score for pagination"
// @Success 200 {string} string "SSE stream of SyncOutput events"
// @Router /owner/sync [get]
func (r *Routes) OwnerSync(c *fiber.Ctx) error {
	owners := c.Context().QueryArgs().PeekMulti("owner")
	if len(owners) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "owner query parameter required")
	}

	// Check for Last-Event-ID header first (sent by browser on reconnect)
	var from float64
	if lastEventID := c.Get("Last-Event-ID"); lastEventID != "" {
		if parsed, err := strconv.ParseFloat(lastEventID, 64); err == nil {
			from = parsed
		}
	} else {
		from = c.QueryFloat("from", 0)
	}

	const batchSize uint32 = 10000

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("Transfer-Encoding", "chunked")
	c.Set("X-Accel-Buffering", "no")
	c.Set("Access-Control-Allow-Origin", "*")

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		// Build keys for all owners
		keys := make([][]byte, 0, len(owners)*2)
		for _, owner := range owners {
			ownerKey := "own:" + string(owner)
			keys = append(keys, []byte(ownerKey), []byte(ownerKey+":spnd"))
		}
		currentFrom := from

		for {
			// Query batchSize+1 to detect if there are more results
			queryLimit := batchSize + 1

			cfg := &txo.OutputSearchCfg{
				SearchCfg: store.SearchCfg{
					Keys:  keys,
					From:  &currentFrom,
					Limit: queryLimit,
				},
			}

			results, err := r.outputStore.Search(c.Context(), cfg)
			if err != nil {
				r.logger.Error("OwnerSync search error", "error", err)
				fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
				w.Flush()
				return
			}

			hasMore := len(results) > int(batchSize)
			if hasMore {
				results = results[:batchSize]
			}

			if len(results) == 0 {
				// Trigger sync in background so new items are ready when client returns
				if r.sync != nil {
					for _, owner := range owners {
						ownerStr := string(owner)
						go func() {
							if err := r.sync.Sync(r.ctx, ownerStr); err != nil {
								r.logger.Error("OwnerSync background sync error", "error", err)
							}
						}()
					}
				}
				// No more results - tell client to retry in 60 seconds
				fmt.Fprintf(w, "event: done\ndata: {}\nretry: 60000\n\n")
				w.Flush()
				return
			}

			// Parse binary outpoints from results
			ops := make([]*txo.Outpoint, len(results))
			for i, result := range results {
				ops[i] = transaction.NewOutpointFromBytes(result.Member)
			}

			spends, err := r.outputStore.GetSpends(c.Context(), ops)
			if err != nil {
				r.logger.Error("OwnerSync GetSpends error", "error", err)
				fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
				w.Flush()
				return
			}

			// Stream each output as an SSE event
			for i := range results {
				if ops[i] == nil {
					continue
				}
				output := SyncOutput{
					Outpoint: ops[i].String(),
					Score:    results[i].Score,
				}
				if spends[i] != nil {
					output.SpendTxid = spends[i].String()
				}

				data, err := json.Marshal(output)
				if err != nil {
					continue
				}

				fmt.Fprintf(w, "data: %s\nid: %.0f\n\n", data, results[i].Score)
				if err := w.Flush(); err != nil {
					// Client disconnected
					return
				}

				currentFrom = results[i].Score
			}

			if !hasMore {
				// Trigger sync in background so new items are ready when client returns
				if r.sync != nil {
					for _, owner := range owners {
						ownerStr := string(owner)
						go func() {
							if err := r.sync.Sync(r.ctx, ownerStr); err != nil {
								r.logger.Error("OwnerSync background sync error", "error", err)
							}
						}()
					}
				}
				// No more results - tell client to retry in 60 seconds
				fmt.Fprintf(w, "event: done\ndata: {}\nretry: 60000\n\n")
				w.Flush()
				return
			}
		}
	})

	return nil
}
