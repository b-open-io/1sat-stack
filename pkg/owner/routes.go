package owner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"

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

// OwnerTxos streams transaction outputs owned by a specific owner via SSE.
// The stream has three phases:
//  1. Sync phase — if refresh is enabled, progress events report blockchain sync status
//  2. Data phase — each matching TXO is streamed as an individual event
//  3. Done — a final event signals the stream is complete
//
// @Summary Stream owner TXOs via SSE
// @Description Stream transaction outputs owned by a specific owner via Server-Sent Events. When refresh is enabled, sync progress is streamed first, followed by results. On reconnect, the browser sends Last-Event-ID which skips refresh and resumes from that score.
// @Tags owner
// @Produce text/event-stream
// @Param owner path string true "Owner identifier (address, pubkey, or script hash)"
// @Param Last-Event-ID header string false "Score of last received event (sent automatically by EventSource on reconnect). When present, refresh is skipped."
// @Param refresh query bool false "Refresh owner data from blockchain before returning" default(true)
// @Param unspent query bool false "Filter for unspent outputs only" default(true)
// @Param tags query string false "Comma-separated list of tags to include (* for all)"
// @Param sats query bool false "Include satoshi values" default(true)
// @Param spend query bool false "Include spend txid" default(true)
// @Param events query bool false "Include events array" default(true)
// @Param block query bool false "Include block height and index" default(true)
// @Param from query number false "Starting score for pagination"
// @Param rev query bool false "Reverse order"
// @Param limit query int false "Maximum number of results" default(100)
// @Success 200 {string} string "SSE stream of sync progress and TXO events"
// @Failure 400 {string} string "Bad request"
// @Router /{owner}/txos [get]
func (r *Routes) OwnerTxos(c *fiber.Ctx) error {
	owner := c.Params("owner")
	refresh := c.QueryBool("refresh", true)

	// If Last-Event-ID is present, the client is reconnecting after a previous
	// stream — skip the costly refresh and resume from where it left off.
	var from *float64
	if lastEventID := c.Get("Last-Event-ID"); lastEventID != "" {
		if parsed, err := strconv.ParseFloat(lastEventID, 64); err == nil {
			from = &parsed
		}
		refresh = false
	} else if f := c.QueryFloat("from", 0); f != 0 {
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
		FilterSpent:   c.QueryBool("unspent", true),
		IncludeSats:   c.QueryBool("sats", true),
		IncludeSpend:  c.QueryBool("spend", true),
		IncludeEvents: c.QueryBool("events", true),
		IncludeBlock:  c.QueryBool("block", true),
		IncludeTags:   tags,
	}

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("Transfer-Encoding", "chunked")
	c.Set("X-Accel-Buffering", "no")
	c.Set("Access-Control-Allow-Origin", "*")

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		ctx := r.ctx

		// Phase 1: Sync with progress reporting
		if refresh && r.sync != nil {
			progress := make(chan SyncProgress, 16)
			syncDone := make(chan error, 1)

			go func() {
				syncDone <- r.sync.SyncWithProgress(ctx, owner, progress)
				close(progress)
			}()

			for p := range progress {
				data, err := json.Marshal(p)
				if err != nil {
					continue
				}
				fmt.Fprintf(w, "event: sync\ndata: %s\n\n", data)
				if err := w.Flush(); err != nil {
					return // Client disconnected
				}
			}

			if err := <-syncDone; err != nil {
				errData, _ := json.Marshal(SyncProgress{
					Phase: "error",
					Owner: owner,
					Error: err.Error(),
				})
				fmt.Fprintf(w, "event: error\ndata: %s\n\n", errData)
				w.Flush()
				return
			}
		}

		// Phase 2: Stream results
		results, err := r.outputStore.Search(ctx, cfg)
		if err != nil {
			r.logger.Error("OwnerTxos search error", "error", err)
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
			w.Flush()
			return
		}

		outputs, err := r.outputStore.LoadOutputsFromResults(ctx, results, cfg)
		if err != nil {
			r.logger.Error("OwnerTxos load error", "error", err)
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
			w.Flush()
			return
		}

		for i, output := range outputs {
			if output == nil {
				continue
			}
			data, err := json.Marshal(output)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: txo\ndata: %s\nid: %.0f\n\n", data, results[i].Score)
			if err := w.Flush(); err != nil {
				return // Client disconnected
			}
		}

		// Phase 3: Done
		fmt.Fprintf(w, "event: done\ndata: {}\n\n")
		w.Flush()
	})

	return nil
}

// OwnerBalance returns the satoshi balance for a specific owner.
// @Summary Get owner balance
// @Description Get the satoshi balance for a specific owner
// @Tags owner
// @Produce json
// @Param owner path string true "Owner identifier (address, pubkey, or script hash)"
// @Success 200 {object} BalanceResponse
// @Failure 500 {string} string "Internal server error"
// @Router /{owner}/balance [get]
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
// @Description Stream paginated outputs for wallet synchronization via Server-Sent Events. Already-indexed outputs are streamed immediately while JungleBus ingest runs. Newly ingested outputs are streamed only after that prefix, in score order, so Last-Event-ID / lastScore is not advanced past unseen confirmed rows. done is sent after ingest finishes. On reconnect, Last-Event-ID skips ingest and resumes from that score.
// @Tags owner
// @Produce text/event-stream
// @Param owner query []string true "Owner identifier(s) (address, pubkey, or script hash)"
// @Param from query number false "Starting score for pagination"
// @Param Last-Event-ID header string false "Score of last received event (sent automatically by EventSource on reconnect). When present, ingest is skipped."
// @Success 200 {string} string "SSE stream of SyncOutput events"
// @Router /sync [get]
func (r *Routes) OwnerSync(c *fiber.Ctx) error {
	owners := c.Context().QueryArgs().PeekMulti("owner")
	if len(owners) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "owner query parameter required")
	}

	ownerStrs := make([]string, len(owners))
	for i, owner := range owners {
		ownerStrs[i] = string(owner)
	}

	// Check for Last-Event-ID header first (sent by browser on reconnect)
	var from float64
	reconnect := false
	if lastEventID := c.Get("Last-Event-ID"); lastEventID != "" {
		reconnect = true
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
		keys := make([][]byte, 0, len(ownerStrs)*2)
		for _, owner := range ownerStrs {
			ownerKey := "own:" + owner
			keys = append(keys, []byte(ownerKey), []byte(ownerKey+":spnd"))
		}

		if reconnect || r.sync == nil {
			if _, _, ok := r.streamOwnerOutputs(w, keys, from, nil, batchSize, nil); !ok {
				return
			}
			fmt.Fprintf(w, "event: done\ndata: {}\nretry: 60000\n\n")
			w.Flush()
			return
		}

		// High-water of rows already in the store. First pass is capped here so
		// ingest cannot insert a mempool timestamp (~1.7e9) ahead of remaining
		// confirmed scores in the same stream.
		var snapshotMax *float64
		if max, ok := r.ownerHighScore(keys); ok {
			snapshotMax = &max
		}

		var writeMu sync.Mutex
		progress, ingestErr := r.startOwnerIngest(ownerStrs)
		progressDone := make(chan struct{})
		go func() {
			defer close(progressDone)
			for p := range progress {
				data, err := json.Marshal(p)
				if err != nil {
					continue
				}
				writeMu.Lock()
				fmt.Fprintf(w, "event: sync\ndata: %s\n\n", data)
				_ = w.Flush()
				writeMu.Unlock()
			}
		}()

		newFrom := from
		if snapshotMax != nil {
			last, emitted, ok := r.streamOwnerOutputs(w, keys, from, snapshotMax, batchSize, &writeMu)
			if !ok {
				return
			}
			if emitted {
				newFrom = math.Nextafter(last, math.Inf(1))
			}
		}

		err := <-ingestErr
		<-progressDone
		if err != nil {
			errData, _ := json.Marshal(SyncProgress{Phase: "error", Error: err.Error()})
			writeMu.Lock()
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", errData)
			w.Flush()
			writeMu.Unlock()
			return
		}

		if _, _, ok := r.streamOwnerOutputs(w, keys, newFrom, nil, batchSize, &writeMu); !ok {
			return
		}

		writeMu.Lock()
		fmt.Fprintf(w, "event: done\ndata: {}\nretry: 60000\n\n")
		w.Flush()
		writeMu.Unlock()
	})

	return nil
}

func (r *Routes) ownerHighScore(keys [][]byte) (float64, bool) {
	results, err := r.outputStore.Search(r.ctx, &txo.OutputSearchCfg{
		SearchCfg: store.SearchCfg{
			Keys:    keys,
			Limit:   1,
			Reverse: true,
		},
	})
	if err != nil || len(results) == 0 {
		return 0, false
	}
	return results[0].Score, true
}

// streamOwnerOutputs writes SyncOutput events in score order from `from` to `to`
// (nil to = +inf). ok is false if the client disconnected or search failed.
func (r *Routes) streamOwnerOutputs(
	w *bufio.Writer,
	keys [][]byte,
	from float64,
	to *float64,
	batchSize uint32,
	writeMu *sync.Mutex,
) (last float64, emitted bool, ok bool) {
	last = from
	currentFrom := from

	lock := func() {
		if writeMu != nil {
			writeMu.Lock()
		}
	}
	unlock := func() {
		if writeMu != nil {
			writeMu.Unlock()
		}
	}

	for {
		queryLimit := batchSize + 1
		cfg := &txo.OutputSearchCfg{
			SearchCfg: store.SearchCfg{
				Keys:  keys,
				From:  &currentFrom,
				To:    to,
				Limit: queryLimit,
			},
		}

		results, err := r.outputStore.Search(r.ctx, cfg)
		if err != nil {
			r.logger.Error("OwnerSync search error", "error", err)
			lock()
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
			w.Flush()
			unlock()
			return last, emitted, false
		}

		hasMore := len(results) > int(batchSize)
		if hasMore {
			results = results[:batchSize]
		}

		if len(results) == 0 {
			return last, emitted, true
		}

		ops := make([]*txo.Outpoint, len(results))
		for i, result := range results {
			ops[i] = transaction.NewOutpointFromBytes(result.Member)
		}

		spends, err := r.outputStore.GetSpends(r.ctx, ops)
		if err != nil {
			r.logger.Error("OwnerSync GetSpends error", "error", err)
			lock()
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
			w.Flush()
			unlock()
			return last, emitted, false
		}

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

			lock()
			fmt.Fprintf(w, "data: %s\nid: %.0f\n\n", data, results[i].Score)
			flushErr := w.Flush()
			unlock()
			if flushErr != nil {
				return last, emitted, false
			}

			last = results[i].Score
			emitted = true
			currentFrom = results[i].Score
		}

		if !hasMore {
			return last, emitted, true
		}
	}
}

func (r *Routes) startOwnerIngest(owners []string) (<-chan SyncProgress, <-chan error) {
	progress := make(chan SyncProgress, 32)
	done := make(chan error, 1)

	go func() {
		var wg sync.WaitGroup
		var mu sync.Mutex
		var firstErr error

		for _, owner := range owners {
			wg.Add(1)
			go func(owner string) {
				defer wg.Done()
				if err := r.sync.SyncWithProgress(r.ctx, owner, progress); err != nil {
					r.logger.Error("OwnerSync ingest error", "owner", owner, "error", err)
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
				}
			}(owner)
		}

		wg.Wait()
		close(progress)
		done <- firstErr
	}()

	return progress, done
}
