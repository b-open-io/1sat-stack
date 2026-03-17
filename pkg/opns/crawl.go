package opns

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/bitcoin-sv/go-templates/template/opns"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	sdkoverlay "github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/util"
)

// CrawlConfig configures the OpNS genesis crawl worker.
type CrawlConfig struct {
	Enabled      bool `mapstructure:"enabled"`
	Concurrency  int  `mapstructure:"concurrency"`
	JungleBusURL string
}

// GenesisCrawl walks the OpNS mine tree from genesis, discovering all mine
// transactions and name origins by following spend chains via JungleBus.
type GenesisCrawl struct {
	config      CrawlConfig
	beefStorage *beef.Storage
	engine      *engine.Engine
	topicName   string
	httpClient  *http.Client
	cancel      context.CancelFunc
	logger      *slog.Logger
}

// NewGenesisCrawl creates a new genesis crawl worker.
func NewGenesisCrawl(
	cfg CrawlConfig,
	beefStorage *beef.Storage,
	eng *engine.Engine,
	logger *slog.Logger,
) *GenesisCrawl {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 8
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &GenesisCrawl{
		config:      cfg,
		beefStorage: beefStorage,
		engine:      eng,
		topicName:   "tm_opns",
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		logger:      logger,
	}
}

// Start begins the genesis crawl. Blocks until the tree is fully walked or ctx is cancelled.
func (c *GenesisCrawl) Start(ctx context.Context) error {
	ctx, c.cancel = context.WithCancel(ctx)

	queue := make(chan *chainhash.Hash, 1000)
	limiter := make(chan struct{}, c.config.Concurrency)
	var wg sync.WaitGroup
	var txCount atomic.Int64

	// Status ticker
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.logger.Info("crawl progress", "processed", txCount.Load())
			}
		}
	}()

	// Seed with genesis txid
	queue <- &opns.GENESIS.Txid

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			c.logger.Info("crawl cancelled", "processed", txCount.Load())
			return ctx.Err()

		case txid := <-queue:
			limiter <- struct{}{}
			wg.Add(1)
			go func(txid *chainhash.Hash) {
				defer func() { <-limiter; wg.Done() }()
				if err := c.processTransaction(ctx, txid, queue); err != nil {
					c.logger.Error("failed to process transaction",
						"txid", txid.String(),
						"error", err,
					)
				}
				txCount.Add(1)
			}(txid)

		default:
			// Queue empty — wait for in-flight work to finish
			wg.Wait()
			// Check queue one more time after all work drained
			select {
			case txid := <-queue:
				limiter <- struct{}{}
				wg.Add(1)
				go func(txid *chainhash.Hash) {
					defer func() { <-limiter; wg.Done() }()
					if err := c.processTransaction(ctx, txid, queue); err != nil {
						c.logger.Error("failed to process transaction",
							"txid", txid.String(),
							"error", err,
						)
					}
					txCount.Add(1)
				}(txid)
			default:
				// Queue empty, no in-flight work — crawl complete
				c.logger.Info("genesis crawl complete", "processed", txCount.Load())
				return nil
			}
		}
	}
}

// Stop cancels the crawl.
func (c *GenesisCrawl) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

// processTransaction builds BEEF, submits to the overlay engine, and enqueues
// spend lookups for mine contract outputs (0 and 1 only).
func (c *GenesisCrawl) processTransaction(ctx context.Context, txid *chainhash.Hash, queue chan<- *chainhash.Hash) error {
	beefBytes, err := c.beefStorage.BuildFullBeef(ctx, txid)
	if err != nil {
		return fmt.Errorf("failed to build BEEF for %s: %w", txid.String(), err)
	}

	steak, err := c.engine.Submit(ctx, sdkoverlay.TaggedBEEF{
		Beef:   beefBytes,
		Topics: []string{c.topicName},
	}, engine.SubmitModeHistorical, nil)
	if err != nil {
		return fmt.Errorf("failed to submit %s: %w", txid.String(), err)
	}

	admittance := steak[c.topicName]
	if admittance == nil {
		return nil
	}

	// Only follow mine contract outputs (0 and 1), NOT ordinal output (2)
	for _, vout := range admittance.OutputsToAdmit {
		if vout > 1 {
			continue
		}

		outpoint := &transaction.Outpoint{
			Txid:  *txid,
			Index: vout,
		}

		spend, err := c.lookupSpend(ctx, outpoint)
		if err != nil {
			c.logger.Warn("spend lookup failed",
				"outpoint", outpoint.OrdinalString(),
				"error", err,
			)
			continue
		}
		if spend != nil {
			queue <- spend
		}
	}

	return nil
}

// lookupSpend calls the JungleBus REST API to find which transaction spent an outpoint.
// Returns nil if the output is unspent (leaf of the mine tree).
func (c *GenesisCrawl) lookupSpend(ctx context.Context, outpoint *transaction.Outpoint) (*chainhash.Hash, error) {
	url := fmt.Sprintf("%s/v1/txo/spend/%s", c.config.JungleBusURL, outpoint.OrdinalString())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("spend lookup failed for %s: %w", outpoint.OrdinalString(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("spend lookup returned %d for %s", resp.StatusCode, outpoint.OrdinalString())
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if len(body) == 0 {
		return nil, nil // Unspent — leaf of the mine tree
	}

	// JungleBus returns 32 bytes in reverse byte order
	return chainhash.NewHash(util.ReverseBytes(body))
}
