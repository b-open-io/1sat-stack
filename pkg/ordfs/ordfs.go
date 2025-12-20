package ordfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/b-open-io/go-junglebus"
	"github.com/bitcoin-sv/go-templates/template/bitcom"
	"github.com/bitcoin-sv/go-templates/template/inscription"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/redis/go-redis/v9"
)

const (
	lockTTL             = 15 * time.Second
	lockRefreshInterval = 5 * time.Second
	lockCheckInterval   = 10 * time.Second
	resolveTimeout      = 60 * time.Second
	cacheTTL            = 30 * 24 * time.Hour
)

var ErrNotFound = errors.New("not found")

// Ordfs handles ordinal file system operations
type Ordfs struct {
	jb     *junglebus.Client
	cache  *redis.Client
	logger *slog.Logger
}

// New creates a new Ordfs service
func New(jb *junglebus.Client, cache *redis.Client, logger *slog.Logger) *Ordfs {
	if logger == nil {
		logger = slog.Default()
	}
	return &Ordfs{
		jb:     jb,
		cache:  cache,
		logger: logger,
	}
}

// Load loads content by request
func (o *Ordfs) Load(ctx context.Context, req *Request) (*Response, error) {
	if req.Txid != nil {
		return o.loadByTxid(ctx, req)
	}

	if req.Outpoint == nil {
		return nil, fmt.Errorf("either txid or outpoint is required")
	}

	output, err := o.loadOutput(ctx, req.Outpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to load output: %w", err)
	}

	// Fast path: no ordinal tracking if not a 1-sat output or no seq requested
	if output.Satoshis != 1 || req.Seq == nil {
		resp := o.parseOutput(ctx, req.Outpoint, output, req.Content)
		resp.Outpoint = req.Outpoint
		if !req.Content {
			resp.Content = nil
		}
		if !req.Map {
			resp.Map = nil
		}
		return resp, nil
	}

	// Full ordinal resolution
	resolution, err := o.Resolve(ctx, req.Outpoint, *req.Seq)
	if err != nil {
		return nil, err
	}

	return o.loadResolution(ctx, req, resolution)
}

// loadByTxid loads content by scanning all outputs of a transaction
func (o *Ordfs) loadByTxid(ctx context.Context, req *Request) (*Response, error) {
	tx, err := o.loadTx(ctx, req.Txid.String())
	if err != nil {
		return nil, fmt.Errorf("transaction not found: %w", err)
	}

	for i, output := range tx.Outputs {
		outpoint := &transaction.Outpoint{
			Txid:  *req.Txid,
			Index: uint32(i),
		}
		resp := o.parseOutput(ctx, outpoint, output, req.Content)
		if resp.Content != nil {
			resp.Outpoint = outpoint
			resp.Sequence = 0

			if !req.Content {
				resp.Content = nil
			}
			if !req.Map {
				resp.Map = nil
			}

			return resp, nil
		}
	}

	return nil, fmt.Errorf("no inscription or B protocol content found: %w", ErrNotFound)
}

// loadTx loads a transaction from junglebus
func (o *Ordfs) loadTx(ctx context.Context, txid string) (*transaction.Transaction, error) {
	rawTx, err := o.jb.GetRawTransaction(ctx, txid)
	if err != nil {
		return nil, err
	}
	return transaction.NewTransactionFromBytes(rawTx)
}

// loadOutput loads a specific output from junglebus
func (o *Ordfs) loadOutput(ctx context.Context, outpoint *transaction.Outpoint) (*transaction.TransactionOutput, error) {
	outputBytes, err := o.jb.GetTxo(ctx, outpoint.Txid.String(), outpoint.Index)
	if err != nil {
		return nil, err
	}
	output := &transaction.TransactionOutput{}
	if _, err := output.ReadFrom(bytes.NewReader(outputBytes)); err != nil {
		return nil, err
	}
	return output, nil
}

// loadSpend gets the spending txid for an outpoint
func (o *Ordfs) loadSpend(ctx context.Context, outpoint *transaction.Outpoint) (*chainhash.Hash, error) {
	spendBytes, err := o.jb.GetSpend(ctx, outpoint.Txid.String(), outpoint.Index)
	if err != nil {
		return nil, err
	}
	if len(spendBytes) == 0 {
		return nil, nil
	}
	return chainhash.NewHash(spendBytes)
}

// parseOutput parses a transaction output for inscription or B protocol content
func (o *Ordfs) parseOutput(ctx context.Context, outpoint *transaction.Outpoint, output *transaction.TransactionOutput, loadContent bool) *Response {
	lockingScript := script.Script(*output.LockingScript)

	var contentType string
	var content []byte
	var mapData map[string]string
	var parent *transaction.Outpoint

	// Try inscription first
	if insc := inscription.Decode(&lockingScript); insc != nil {
		if insc.File.Content != nil {
			contentType = insc.File.Type
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			if loadContent {
				content = insc.File.Content
			}
		}

		if insc.Parent != nil {
			parent = insc.Parent
		}
	}

	// Try B protocol
	if bc := bitcom.Decode(&lockingScript); bc != nil {
		for _, proto := range bc.Protocols {
			switch proto.Protocol {
			case bitcom.MapPrefix:
				if mapProto := bitcom.DecodeMap(proto.Script); mapProto != nil && mapProto.Cmd == bitcom.MapCmdSet {
					if mapData == nil {
						mapData = make(map[string]string)
					}
					for k, v := range mapProto.Data {
						mapData[k] = v
					}
				}
			case bitcom.BPrefix:
				bProto := bitcom.DecodeB(proto.Script)
				if bProto != nil && len(bProto.Data) > 0 {
					if contentType == "" {
						contentType = string(bProto.MediaType)
						if contentType == "" {
							contentType = "application/octet-stream"
						}
					}
					if content == nil && loadContent {
						content = bProto.Data
					}
				}
			}
		}
	}

	// Cache parsed output
	if contentType != "" || len(mapData) > 0 {
		o.cacheParsedOutput(ctx, outpoint, contentType, len(content), mapData)
	}

	var mapJSON json.RawMessage
	if mapData != nil {
		mapDataAny := make(map[string]any)
		for k, v := range mapData {
			mapDataAny[k] = v
		}

		// Parse nested JSON fields
		if subTypeData, ok := mapData["subTypeData"]; ok && subTypeData != "" {
			var parsedSubTypeData map[string]any
			if err := json.Unmarshal([]byte(subTypeData), &parsedSubTypeData); err == nil {
				mapDataAny["subTypeData"] = parsedSubTypeData
			}
		}

		if royalties, ok := mapData["royalties"]; ok && royalties != "" {
			var parsedRoyalties []map[string]any
			if err := json.Unmarshal([]byte(royalties), &parsedRoyalties); err == nil {
				mapDataAny["royalties"] = parsedRoyalties
			}
		}

		if mapBytes, err := json.Marshal(mapDataAny); err == nil {
			mapJSON = mapBytes
		}
	}

	return &Response{
		ContentType:   contentType,
		Content:       content,
		ContentLength: len(content),
		Map:           mapJSON,
		Parent:        parent,
	}
}

func (o *Ordfs) cacheParsedOutput(ctx context.Context, outpoint *transaction.Outpoint, contentType string, contentLength int, mapData map[string]string) {
	cacheKey := fmt.Sprintf("parsed:%s", outpoint.String())
	fields := map[string]interface{}{
		"contentType":   contentType,
		"contentLength": contentLength,
	}
	if len(mapData) > 0 {
		if mapBytes, err := json.Marshal(mapData); err == nil {
			fields["map"] = string(mapBytes)
		}
	}
	o.cache.HSet(ctx, cacheKey, fields)
	o.cache.Expire(ctx, cacheKey, cacheTTL)
}

// Locking helpers
func (o *Ordfs) lockKey(outpoint *transaction.Outpoint) string {
	return fmt.Sprintf("lock:%s", outpoint.String())
}

func (o *Ordfs) channelKey(outpoint *transaction.Outpoint) string {
	return fmt.Sprintf("channel:%s", outpoint.String())
}

func (o *Ordfs) setLock(ctx context.Context, outpoint *transaction.Outpoint) error {
	ok, err := o.cache.SetNX(ctx, o.lockKey(outpoint), "1", lockTTL).Result()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("lock already held")
	}
	return nil
}

func (o *Ordfs) releaseLock(outpoint *transaction.Outpoint) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	o.cache.Del(ctx, o.lockKey(outpoint))
}

func (o *Ordfs) publishCrawlComplete(outpoints []*transaction.Outpoint, origin string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, outpoint := range outpoints {
		o.cache.Publish(ctx, o.channelKey(outpoint), origin)
	}
}

func (o *Ordfs) publishCrawlFailure(outpoints []*transaction.Outpoint) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, outpoint := range outpoints {
		o.cache.Publish(ctx, o.channelKey(outpoint), "")
	}
}

func (o *Ordfs) waitForCrawl(ctx context.Context, outpoint *transaction.Outpoint) error {
	pubsub := o.cache.Subscribe(ctx, o.channelKey(outpoint))
	defer pubsub.Close()

	ch := pubsub.Channel()
	ticker := time.NewTicker(lockCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg := <-ch:
			if msg.Payload != "" {
				return nil
			}
			return fmt.Errorf("crawl failed")
		case <-ticker.C:
			exists, err := o.cache.Exists(ctx, o.lockKey(outpoint)).Result()
			if err != nil {
				return fmt.Errorf("failed to check lock: %w", err)
			}
			if exists == 0 {
				return nil
			}
		}
	}
}

// calculateOrdinalOutput finds the output that receives the ordinal from a spent input
func (o *Ordfs) calculateOrdinalOutput(ctx context.Context, spendTx *transaction.Transaction, spentOutpoint *transaction.Outpoint) (*transaction.Outpoint, error) {
	var inputIndex int = -1
	var ordinalOffset uint64 = 0

	for i, input := range spendTx.Inputs {
		if input.SourceTXID != nil && input.SourceTXID.Equal(spentOutpoint.Txid) && input.SourceTxOutIndex == spentOutpoint.Index {
			inputIndex = i
			break
		}

		prevOutpoint := &transaction.Outpoint{
			Txid:  *input.SourceTXID,
			Index: input.SourceTxOutIndex,
		}

		prevOutput, err := o.loadOutput(ctx, prevOutpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to load input output %s: %w", prevOutpoint.String(), err)
		}

		ordinalOffset += prevOutput.Satoshis
	}

	if inputIndex == -1 {
		return nil, fmt.Errorf("outpoint not found in spending transaction inputs")
	}

	var cumulativeSats uint64 = 0
	for i, output := range spendTx.Outputs {
		if output.Satoshis == 0 {
			continue
		}

		if cumulativeSats == ordinalOffset {
			if output.Satoshis != 1 {
				return nil, nil
			}
			return &transaction.Outpoint{
				Txid:  *spendTx.TxID(),
				Index: uint32(i),
			}, nil
		}

		cumulativeSats += output.Satoshis
		if cumulativeSats > ordinalOffset {
			break
		}
	}

	return nil, fmt.Errorf("ordinal output not found")
}

// calculatePreviousOrdinalInput finds the input that provided the ordinal to a 1-sat output
func (o *Ordfs) calculatePreviousOrdinalInput(ctx context.Context, tx *transaction.Transaction, currentOutpoint *transaction.Outpoint) (*transaction.Outpoint, error) {
	if int(currentOutpoint.Index) >= len(tx.Outputs) {
		return nil, fmt.Errorf("invalid outpoint index")
	}

	currentOutput := tx.Outputs[currentOutpoint.Index]
	if currentOutput.Satoshis != 1 {
		return nil, fmt.Errorf("output is not a 1-sat output")
	}

	var ordinalOffset uint64 = 0
	for i := 0; i < int(currentOutpoint.Index); i++ {
		if tx.Outputs[i].Satoshis > 0 {
			ordinalOffset += tx.Outputs[i].Satoshis
		}
	}

	var cumulativeSats uint64 = 0
	for _, input := range tx.Inputs {
		prevOutpoint := &transaction.Outpoint{
			Txid:  *input.SourceTXID,
			Index: input.SourceTxOutIndex,
		}

		prevOutput, err := o.loadOutput(ctx, prevOutpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to load input output %s: %w", prevOutpoint.String(), err)
		}

		if cumulativeSats == ordinalOffset {
			if prevOutput.Satoshis != 1 {
				return nil, nil // Origin found - input is not 1-sat
			}
			return prevOutpoint, nil
		}

		cumulativeSats += prevOutput.Satoshis
		if cumulativeSats > ordinalOffset {
			return nil, nil // Origin - ordinal offset is within a multi-sat input
		}
	}

	return nil, nil // Origin - no exact match
}

// backwardCrawl crawls backward from an outpoint to find the origin
func (o *Ordfs) backwardCrawl(ctx context.Context, requestedOutpoint *transaction.Outpoint) (*transaction.Outpoint, error) {
	lockedOutpoints := []*transaction.Outpoint{}
	defer func() {
		for _, outpoint := range lockedOutpoints {
			o.releaseLock(outpoint)
		}
	}()

	crawlCtx, cancelCrawl := context.WithCancel(ctx)
	defer cancelCrawl()

	// Lock refresh goroutine
	go func() {
		ticker := time.NewTicker(lockRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-crawlCtx.Done():
				return
			case <-ticker.C:
				for _, outpoint := range lockedOutpoints {
					o.cache.Set(crawlCtx, o.lockKey(outpoint), "1", lockTTL)
				}
			}
		}
	}()

	currentOutpoint := requestedOutpoint
	relativeSeq := 0
	var chain []ChainEntry

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Check if origin is already known
		knownOrigin := o.cache.HGet(ctx, "origins", currentOutpoint.String()).Val()
		if knownOrigin != "" {
			origin, err := transaction.OutpointFromString(knownOrigin)
			if err != nil {
				return nil, fmt.Errorf("failed to parse known origin: %w", err)
			}
			if err := o.migrateToOrigin(ctx, requestedOutpoint, origin, chain); err != nil {
				o.publishCrawlFailure(lockedOutpoints)
				return nil, fmt.Errorf("migration failed: %w", err)
			}
			o.publishCrawlComplete(lockedOutpoints, origin.String())
			return origin, nil
		}

		// Try to acquire lock
		if err := o.setLock(ctx, currentOutpoint); err != nil {
			// Wait for another crawl to complete
			if err := o.waitForCrawl(ctx, currentOutpoint); err != nil {
				return nil, err
			}

			knownOrigin = o.cache.HGet(ctx, "origins", currentOutpoint.String()).Val()
			if knownOrigin != "" {
				origin, err := transaction.OutpointFromString(knownOrigin)
				if err != nil {
					return nil, fmt.Errorf("failed to parse origin after wait: %w", err)
				}
				if err := o.migrateToOrigin(ctx, requestedOutpoint, origin, chain); err != nil {
					o.publishCrawlFailure(lockedOutpoints)
					return nil, fmt.Errorf("migration failed: %w", err)
				}
				o.publishCrawlComplete(lockedOutpoints, origin.String())
				return origin, nil
			}

			if err := o.setLock(ctx, currentOutpoint); err != nil {
				return nil, fmt.Errorf("failed to acquire lock after wait: %w", err)
			}
		}

		lockedOutpoints = append(lockedOutpoints, currentOutpoint)

		currentTx, err := o.loadTx(ctx, currentOutpoint.Txid.String())
		if err != nil {
			return nil, fmt.Errorf("failed to load tx %s: %w", currentOutpoint.Txid.String(), err)
		}

		if int(currentOutpoint.Index) >= len(currentTx.Outputs) {
			return nil, fmt.Errorf("invalid outpoint index")
		}

		currentOutput := currentTx.Outputs[currentOutpoint.Index]
		resp := o.parseOutput(ctx, currentOutpoint, currentOutput, true)

		var entryContentOutpoint, entryMapOutpoint, entryParentOutpoint *transaction.Outpoint
		if resp.Content != nil {
			entryContentOutpoint = currentOutpoint
		}
		if resp.Map != nil {
			entryMapOutpoint = currentOutpoint
		}
		if resp.Parent != nil {
			entryParentOutpoint = currentOutpoint
		}

		chain = append(chain, ChainEntry{
			Outpoint:        currentOutpoint,
			RelativeSeq:     relativeSeq,
			ContentOutpoint: entryContentOutpoint,
			MapOutpoint:     entryMapOutpoint,
			ParentOutpoint:  entryParentOutpoint,
		})

		prevOutpoint, err := o.calculatePreviousOrdinalInput(ctx, currentTx, currentOutpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate previous input: %w", err)
		}

		if prevOutpoint == nil {
			// Found origin
			if err := o.migrateToOrigin(ctx, requestedOutpoint, currentOutpoint, chain); err != nil {
				o.publishCrawlFailure(lockedOutpoints)
				return nil, fmt.Errorf("migration failed: %w", err)
			}
			o.publishCrawlComplete(lockedOutpoints, currentOutpoint.String())
			return currentOutpoint, nil
		}

		relativeSeq--
		currentOutpoint = prevOutpoint
	}
}

// migrateToOrigin migrates chain entries to use the discovered origin
func (o *Ordfs) migrateToOrigin(ctx context.Context, requestedOutpoint, origin *transaction.Outpoint, chain []ChainEntry) error {
	var offset int
	if len(chain) > 0 {
		offset = -chain[len(chain)-1].RelativeSeq
	}

	pipe := o.cache.Pipeline()

	originSeqKey := fmt.Sprintf("seq:%s", origin.String())
	originRevKey := fmt.Sprintf("rev:%s", origin.String())
	originMapKey := fmt.Sprintf("map:%s", origin.String())
	originParentKey := fmt.Sprintf("parents:%s", origin.String())

	members := make([]redis.Z, len(chain))
	originUpdates := make(map[string]interface{})

	for i, entry := range chain {
		absoluteSeq := entry.RelativeSeq + offset
		members[i] = redis.Z{
			Score:  float64(absoluteSeq),
			Member: entry.Outpoint.String(),
		}
		originUpdates[entry.Outpoint.String()] = origin.String()

		if entry.ContentOutpoint != nil {
			pipe.ZAdd(ctx, originRevKey, redis.Z{
				Score:  float64(absoluteSeq),
				Member: entry.ContentOutpoint.String(),
			})
		}

		if entry.MapOutpoint != nil {
			pipe.ZAdd(ctx, originMapKey, redis.Z{
				Score:  float64(absoluteSeq),
				Member: entry.MapOutpoint.String(),
			})
		}

		if entry.ParentOutpoint != nil {
			pipe.ZAdd(ctx, originParentKey, redis.Z{
				Score:  float64(absoluteSeq),
				Member: entry.ParentOutpoint.String(),
			})
		}
	}

	pipe.ZAdd(ctx, originSeqKey, members...)
	pipe.HSet(ctx, "origins", originUpdates)

	if requestedOutpoint.String() != origin.String() {
		tempSeqKey := fmt.Sprintf("seq:%s", requestedOutpoint.String())
		pipe.Del(ctx, tempSeqKey)
	}

	_, err := pipe.Exec(ctx)
	return err
}

// forwardCrawl crawls forward from an outpoint to find a target sequence
func (o *Ordfs) forwardCrawl(ctx context.Context, origin, startOutpoint *transaction.Outpoint, startSeq, targetSeq int) (*transaction.Outpoint, int, error) {
	seqKey := fmt.Sprintf("seq:%s", origin.String())
	revKey := fmt.Sprintf("rev:%s", origin.String())
	mapKey := fmt.Sprintf("map:%s", origin.String())
	parentKey := fmt.Sprintf("parents:%s", origin.String())

	currentOutpoint := startOutpoint
	currentSeq := startSeq

	for {
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		default:
		}

		o.cache.HSet(ctx, "origins", currentOutpoint.String(), origin.String())

		output, err := o.loadOutput(ctx, currentOutpoint)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to load output: %w", err)
		}

		resp := o.parseOutput(ctx, currentOutpoint, output, true)

		o.cache.ZAdd(ctx, seqKey, redis.Z{
			Score:  float64(currentSeq),
			Member: currentOutpoint.String(),
		})

		if resp.Content != nil {
			o.cache.ZAdd(ctx, revKey, redis.Z{
				Score:  float64(currentSeq),
				Member: currentOutpoint.String(),
			})
		}

		if resp.Map != nil {
			o.cache.ZAdd(ctx, mapKey, redis.Z{
				Score:  float64(currentSeq),
				Member: currentOutpoint.String(),
			})
		}

		if resp.Parent != nil {
			o.cache.ZAdd(ctx, parentKey, redis.Z{
				Score:  float64(currentSeq),
				Member: currentOutpoint.String(),
			})
		}

		if targetSeq >= 0 && currentSeq >= targetSeq {
			break
		}

		spendTxid, err := o.loadSpend(ctx, currentOutpoint)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to get spend: %w", err)
		}
		if spendTxid == nil {
			break // End of chain
		}

		spendTx, err := o.loadTx(ctx, spendTxid.String())
		if err != nil {
			return nil, 0, fmt.Errorf("failed to load spending tx: %w", err)
		}

		nextOutpoint, err := o.calculateOrdinalOutput(ctx, spendTx, currentOutpoint)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to calculate ordinal output: %w", err)
		}
		if nextOutpoint == nil {
			break // Ordinal destroyed
		}

		currentOutpoint = nextOutpoint
		currentSeq++
	}

	return currentOutpoint, currentSeq, nil
}

// Resolve resolves an outpoint to a specific sequence in the ordinal chain
func (o *Ordfs) Resolve(ctx context.Context, requestedOutpoint *transaction.Outpoint, seq int) (*Resolution, error) {
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	// Check for known origin
	knownOriginStr := o.cache.HGet(ctx, "origins", requestedOutpoint.String()).Val()
	var origin *transaction.Outpoint

	if knownOriginStr != "" {
		var err error
		origin, err = transaction.OutpointFromString(knownOriginStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse known origin: %w", err)
		}
	} else {
		var err error
		origin, err = o.backwardCrawl(ctx, requestedOutpoint)
		if err != nil {
			return nil, fmt.Errorf("backward crawl failed: %w", err)
		}
	}

	seqKey := fmt.Sprintf("seq:%s", origin.String())
	targetAbsoluteSeq := seq

	// Check if target is already cached
	var targetOutpoint *transaction.Outpoint
	if targetAbsoluteSeq >= 0 {
		seqMembers := o.cache.ZRangeByScore(ctx, seqKey, &redis.ZRangeBy{
			Min:   fmt.Sprintf("%d", targetAbsoluteSeq),
			Max:   fmt.Sprintf("%d", targetAbsoluteSeq),
			Count: 1,
		}).Val()
		if len(seqMembers) > 0 {
			var err error
			targetOutpoint, err = transaction.OutpointFromString(seqMembers[0])
			if err != nil {
				return nil, fmt.Errorf("failed to parse cached target outpoint: %w", err)
			}
		}
	}

	// Forward crawl if needed
	if targetOutpoint == nil {
		var crawlStartOutpoint *transaction.Outpoint
		var crawlStartSeq int

		lastMembers := o.cache.ZRevRangeWithScores(ctx, seqKey, 0, 0).Val()
		if len(lastMembers) > 0 {
			crawlStartSeq = int(lastMembers[0].Score)
			var err error
			crawlStartOutpoint, err = transaction.OutpointFromString(lastMembers[0].Member.(string))
			if err != nil {
				return nil, fmt.Errorf("failed to parse crawl start outpoint: %w", err)
			}
		} else {
			crawlStartOutpoint = origin
			crawlStartSeq = 0
		}

		finalOutpoint, finalSeq, err := o.forwardCrawl(ctx, origin, crawlStartOutpoint, crawlStartSeq, targetAbsoluteSeq)
		if err != nil {
			return nil, fmt.Errorf("forward crawl failed: %w", err)
		}

		if seq == -1 {
			targetAbsoluteSeq = finalSeq
			targetOutpoint = finalOutpoint
		} else if targetAbsoluteSeq >= 0 {
			targetMembers := o.cache.ZRangeByScore(ctx, seqKey, &redis.ZRangeBy{
				Min:   fmt.Sprintf("%d", targetAbsoluteSeq),
				Max:   fmt.Sprintf("%d", targetAbsoluteSeq),
				Count: 1,
			}).Val()
			if len(targetMembers) > 0 {
				targetOutpoint, _ = transaction.OutpointFromString(targetMembers[0])
			} else {
				return nil, fmt.Errorf("target sequence %d not found (chain ends at %d): %w", targetAbsoluteSeq, finalSeq, ErrNotFound)
			}
		}
	}

	// Build resolution
	resolution := &Resolution{
		Origin:   origin,
		Current:  targetOutpoint,
		Sequence: targetAbsoluteSeq,
	}

	// Get content outpoint
	revKey := fmt.Sprintf("rev:%s", origin.String())
	revMembers := o.cache.ZRevRangeByScore(ctx, revKey, &redis.ZRangeBy{
		Min: "0",
		Max: fmt.Sprintf("%d", targetAbsoluteSeq),
	}).Val()
	if len(revMembers) > 0 {
		resolution.Content, _ = transaction.OutpointFromString(revMembers[0])
	}

	// Get map outpoint
	mapKey := fmt.Sprintf("map:%s", origin.String())
	mapMembers := o.cache.ZRevRangeByScore(ctx, mapKey, &redis.ZRangeBy{
		Min: "0",
		Max: fmt.Sprintf("%d", targetAbsoluteSeq),
	}).Val()
	if len(mapMembers) > 0 {
		resolution.Map, _ = transaction.OutpointFromString(mapMembers[0])
	}

	// Get parent outpoint
	parentKey := fmt.Sprintf("parents:%s", origin.String())
	parentMembers := o.cache.ZRevRangeByScore(ctx, parentKey, &redis.ZRangeBy{
		Min: "0",
		Max: fmt.Sprintf("%d", targetAbsoluteSeq),
	}).Val()
	if len(parentMembers) > 0 {
		resolution.Parent, _ = transaction.OutpointFromString(parentMembers[0])
	}

	if resolution.Content == nil {
		return nil, fmt.Errorf("no inscription found: %w", ErrNotFound)
	}

	return resolution, nil
}

// loadResolution loads a full response from a resolution
func (o *Ordfs) loadResolution(ctx context.Context, req *Request, resolution *Resolution) (*Response, error) {
	response := &Response{
		Outpoint: resolution.Current,
		Origin:   resolution.Origin,
		Sequence: resolution.Sequence,
	}

	if resolution.Content != nil {
		output, err := o.loadOutput(ctx, resolution.Content)
		if err != nil {
			return nil, fmt.Errorf("failed to load content output: %w", err)
		}
		parsed := o.parseOutput(ctx, resolution.Content, output, req.Content)
		response.ContentType = parsed.ContentType
		response.Content = parsed.Content
		response.ContentLength = parsed.ContentLength
	}

	if req.Map && resolution.Map != nil {
		mergedMap, err := o.loadMergedMap(ctx, resolution.Origin, resolution.Map)
		if err != nil {
			return nil, fmt.Errorf("failed to load merged map: %w", err)
		}
		if mergedJSON, err := json.Marshal(mergedMap); err == nil {
			response.Map = mergedJSON
		}
	}

	if req.Parent && resolution.Parent != nil {
		output, err := o.loadOutput(ctx, resolution.Parent)
		if err != nil {
			return nil, fmt.Errorf("failed to load parent output: %w", err)
		}
		parsed := o.parseOutput(ctx, resolution.Parent, output, false)
		response.Parent = parsed.Parent
	}

	return response, nil
}

// loadMergedMap loads and merges all MAP data up to a given outpoint
func (o *Ordfs) loadMergedMap(ctx context.Context, origin, mapOutpoint *transaction.Outpoint) (map[string]any, error) {
	mergedKey := fmt.Sprintf("merged:%s", mapOutpoint.String())

	// Check cache
	cached := o.cache.Get(ctx, mergedKey).Val()
	if cached != "" {
		var mergedMap map[string]any
		if err := json.Unmarshal([]byte(cached), &mergedMap); err == nil {
			return mergedMap, nil
		}
	}

	mapKey := fmt.Sprintf("map:%s", origin.String())
	mapScore := o.cache.ZScore(ctx, mapKey, mapOutpoint.String()).Val()

	mapOutpoints := o.cache.ZRangeByScore(ctx, mapKey, &redis.ZRangeBy{
		Min: "0",
		Max: fmt.Sprintf("%f", mapScore),
	}).Val()

	mergedMap := make(map[string]any)
	for _, outpointStr := range mapOutpoints {
		outpoint, err := transaction.OutpointFromString(outpointStr)
		if err != nil {
			continue
		}

		cacheKey := fmt.Sprintf("parsed:%s", outpoint.String())
		mapJSON := o.cache.HGet(ctx, cacheKey, "map").Val()

		var individualMap map[string]any
		if mapJSON != "" {
			json.Unmarshal([]byte(mapJSON), &individualMap)
		} else {
			output, err := o.loadOutput(ctx, outpoint)
			if err != nil {
				continue
			}
			resp := o.parseOutput(ctx, outpoint, output, false)
			if resp.Map != nil {
				json.Unmarshal(resp.Map, &individualMap)
			}
		}

		for k, v := range individualMap {
			mergedMap[k] = v
		}
	}

	// Cache merged map
	if mergedJSON, err := json.Marshal(mergedMap); err == nil {
		o.cache.Set(ctx, mergedKey, string(mergedJSON), cacheTTL)
	}

	return mergedMap, nil
}

// StreamContent streams content from an ordinal chain
func (o *Ordfs) StreamContent(ctx context.Context, outpoint *transaction.Outpoint, rangeStart, rangeEnd *int64, writer io.Writer) (*StreamResponse, error) {
	knownOriginStr := o.cache.HGet(ctx, "origins", outpoint.String()).Val()
	var origin *transaction.Outpoint

	if knownOriginStr != "" {
		origin, _ = transaction.OutpointFromString(knownOriginStr)
	} else {
		var err error
		origin, err = o.backwardCrawl(ctx, outpoint)
		if err != nil {
			return nil, fmt.Errorf("backward crawl failed: %w", err)
		}
	}

	currentOutpoint := outpoint
	relativeSeq := 0
	var cumulativeBytes int64 = 0
	var bytesWritten int64 = 0
	var contentType string
	rangeStartFound := rangeStart == nil

	for {
		select {
		case <-ctx.Done():
			return &StreamResponse{
				Origin:        origin,
				ContentType:   contentType,
				BytesWritten:  bytesWritten,
				FinalSequence: relativeSeq,
				StreamEnded:   false,
			}, ctx.Err()
		default:
		}

		output, err := o.loadOutput(ctx, currentOutpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to load output: %w", err)
		}

		resp := o.parseOutput(ctx, currentOutpoint, output, true)

		if relativeSeq == 0 {
			contentType = resp.ContentType
		}

		if resp.Content != nil && len(resp.Content) > 0 {
			chunkSize := int64(len(resp.Content))
			chunkStart := int64(0)
			chunkEnd := chunkSize

			if rangeStart != nil && !rangeStartFound {
				if cumulativeBytes+chunkSize > *rangeStart {
					chunkStart = *rangeStart - cumulativeBytes
					rangeStartFound = true
				}
			}

			if rangeEnd != nil && rangeStartFound {
				bytesFromRangeStart := cumulativeBytes - *rangeStart
				if bytesFromRangeStart+chunkSize > *rangeEnd-*rangeStart {
					chunkEnd = *rangeEnd - *rangeStart - bytesFromRangeStart
				}
			}

			if rangeStartFound && chunkStart < chunkEnd {
				n, err := writer.Write(resp.Content[chunkStart:chunkEnd])
				if err != nil {
					return &StreamResponse{
						Origin:        origin,
						ContentType:   contentType,
						BytesWritten:  bytesWritten,
						FinalSequence: relativeSeq,
						StreamEnded:   false,
					}, fmt.Errorf("failed to write content: %w", err)
				}
				bytesWritten += int64(n)

				if rangeEnd != nil && bytesWritten >= *rangeEnd-*rangeStart {
					return &StreamResponse{
						Origin:        origin,
						ContentType:   contentType,
						BytesWritten:  bytesWritten,
						FinalSequence: relativeSeq,
						StreamEnded:   true,
					}, nil
				}
			}

			cumulativeBytes += chunkSize
		}

		// Check if stream should continue
		if relativeSeq > 0 && resp.ContentType != "ordfs/stream" {
			return &StreamResponse{
				Origin:        origin,
				ContentType:   contentType,
				BytesWritten:  bytesWritten,
				FinalSequence: relativeSeq,
				StreamEnded:   true,
			}, nil
		}

		spendTxid, err := o.loadSpend(ctx, currentOutpoint)
		if err != nil || spendTxid == nil {
			return &StreamResponse{
				Origin:        origin,
				ContentType:   contentType,
				BytesWritten:  bytesWritten,
				FinalSequence: relativeSeq,
				StreamEnded:   true,
			}, nil
		}

		spendTx, err := o.loadTx(ctx, spendTxid.String())
		if err != nil {
			return nil, fmt.Errorf("failed to load spending tx: %w", err)
		}

		nextOutpoint, err := o.calculateOrdinalOutput(ctx, spendTx, currentOutpoint)
		if err != nil || nextOutpoint == nil {
			return &StreamResponse{
				Origin:        origin,
				ContentType:   contentType,
				BytesWritten:  bytesWritten,
				FinalSequence: relativeSeq,
				StreamEnded:   true,
			}, nil
		}

		currentOutpoint = nextOutpoint
		relativeSeq++
	}
}

// StreamResponse holds the result of streaming
type StreamResponse struct {
	Origin        *transaction.Outpoint
	ContentType   string
	BytesWritten  int64
	FinalSequence int
	StreamEnded   bool
}

// ParseOutputForContent parses a single output for content (useful for indexer integration)
func ParseOutputForContent(output *transaction.TransactionOutput) (contentType string, content []byte, mapJSON string, parent *transaction.Outpoint) {
	lockingScript := script.Script(*output.LockingScript)

	var mapData map[string]string

	if insc := inscription.Decode(&lockingScript); insc != nil {
		if insc.File.Content != nil {
			contentType = insc.File.Type
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			content = insc.File.Content
		}

		if insc.Parent != nil {
			parent = insc.Parent
		}
	}

	if bc := bitcom.Decode(&lockingScript); bc != nil {
		for _, proto := range bc.Protocols {
			switch proto.Protocol {
			case bitcom.MapPrefix:
				if mapProto := bitcom.DecodeMap(proto.Script); mapProto != nil && mapProto.Cmd == bitcom.MapCmdSet {
					if mapData == nil {
						mapData = make(map[string]string)
					}
					for k, v := range mapProto.Data {
						mapData[k] = v
					}
				}
			case bitcom.BPrefix:
				bProto := bitcom.DecodeB(proto.Script)
				if bProto != nil && len(bProto.Data) > 0 {
					if contentType == "" {
						contentType = string(bProto.MediaType)
						if contentType == "" {
							contentType = "application/octet-stream"
						}
					}
					if content == nil {
						content = bProto.Data
					}
				}
			}
		}
	}

	if mapData != nil {
		mapDataAny := make(map[string]any)
		for k, v := range mapData {
			mapDataAny[k] = v
		}
		if mapBytes, err := json.Marshal(mapDataAny); err == nil {
			mapJSON = string(mapBytes)
		}
	}

	return
}
