package ordfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/spends"
	"github.com/bitcoin-sv/go-templates/template/bitcom"
	"github.com/bitcoin-sv/go-templates/template/inscription"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

const (
	ResolveTimeout = 60 * time.Second // Default timeout for web-facing resolve calls

	// SeqOrigin resolves to the origin outpoint and returns its data directly,
	// without forward crawling or merging reinscriptions/MAP data.
	SeqOrigin = -2
)

var ErrNotFound = errors.New("not found")

// Ordfs handles ordinal file system operations
type Ordfs struct {
	spends      *spends.Storage
	beef        *beef.Storage
	origins     OriginStore
	cache       Cache
	coordinator CrawlCoordinator
	logger      *slog.Logger
}

// New creates a new Ordfs service
func New(spendsStorage *spends.Storage, beefStorage *beef.Storage, origins OriginStore, cache Cache, coordinator CrawlCoordinator, logger *slog.Logger) *Ordfs {
	if logger == nil {
		logger = slog.Default()
	}
	return &Ordfs{
		spends:      spendsStorage,
		beef:        beefStorage,
		origins:     origins,
		cache:       cache,
		coordinator: coordinator,
		logger:      logger,
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

	// Origin-only resolution: backward crawl to origin, return its data directly
	if *req.Seq == SeqOrigin {
		origin, err := o.backwardCrawl(ctx, req.Outpoint)
		if err != nil {
			return nil, fmt.Errorf("origin resolution failed: %w", err)
		}
		originOutput, err := o.loadOutput(ctx, origin)
		if err != nil {
			return nil, fmt.Errorf("failed to load origin output: %w", err)
		}
		resp := o.parseOutput(ctx, origin, originOutput, req.Content)
		resp.Outpoint = req.Outpoint
		resp.Origin = origin
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

// loadTx loads a transaction via beef storage
func (o *Ordfs) loadTx(ctx context.Context, txid string) (*transaction.Transaction, error) {
	h, err := chainhash.NewHashFromHex(txid)
	if err != nil {
		return nil, err
	}
	return o.beef.LoadTx(ctx, h)
}

// loadOutput loads a specific output via beef storage
func (o *Ordfs) loadOutput(ctx context.Context, outpoint *transaction.Outpoint) (*transaction.TransactionOutput, error) {
	tx, err := o.beef.LoadTx(ctx, &outpoint.Txid)
	if err != nil {
		return nil, err
	}
	if int(outpoint.Index) >= len(tx.Outputs) {
		return nil, fmt.Errorf("output index %d out of range for tx %s", outpoint.Index, outpoint.Txid.String())
	}
	return tx.Outputs[outpoint.Index], nil
}

// loadSpend gets the spending txid for an outpoint
func (o *Ordfs) loadSpend(ctx context.Context, outpoint *transaction.Outpoint) (*chainhash.Hash, error) {
	return o.spends.GetSpend(ctx, outpoint)
}

// parseOutput parses a transaction output for inscription or B protocol content
func (o *Ordfs) parseOutput(ctx context.Context, outpoint *transaction.Outpoint, output *transaction.TransactionOutput, loadContent bool) *Response {
	lockingScript := script.Script(*output.LockingScript)

	var contentType string
	var content []byte
	var contentLength int
	var mapData map[string]string
	var parent *transaction.Outpoint

	// Try inscription first
	if insc := inscription.Decode(&lockingScript); insc != nil {
		if insc.File.Content != nil {
			contentType = insc.File.Type
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			contentLength = len(insc.File.Content)
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
					if contentLength == 0 {
						contentLength = len(bProto.Data)
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
		o.cacheParsedOutput(ctx, outpoint, contentType, contentLength, mapData)
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
		ContentLength: contentLength,
		Map:           mapJSON,
		Parent:        parent,
	}
}

// parsedEntry is the serialized form of cached parsed output metadata.
type parsedEntry struct {
	ContentType   string `json:"contentType"`
	ContentLength int    `json:"contentLength"`
	Map           string `json:"map,omitempty"`
}

func (o *Ordfs) cacheParsedOutput(ctx context.Context, outpoint *transaction.Outpoint, contentType string, contentLength int, mapData map[string]string) {
	entry := parsedEntry{
		ContentType:   contentType,
		ContentLength: contentLength,
	}
	if len(mapData) > 0 {
		if mapBytes, err := json.Marshal(mapData); err == nil {
			entry.Map = string(mapBytes)
		}
	}
	if data, err := json.Marshal(entry); err == nil {
		o.cache.Set(ctx, fmt.Sprintf("parsed:%s", outpoint.String()), data)
	}
}

func (o *Ordfs) getCachedParsed(ctx context.Context, outpoint *transaction.Outpoint) *parsedEntry {
	data, err := o.cache.Get(ctx, fmt.Sprintf("parsed:%s", outpoint.String()))
	if err != nil || data == nil {
		return nil
	}
	var entry parsedEntry
	if json.Unmarshal(data, &entry) != nil {
		return nil
	}
	return &entry
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
			o.coordinator.ReleaseLock(outpoint)
		}
	}()

	crawlCtx, cancelCrawl := context.WithCancel(ctx)
	defer cancelCrawl()

	// Lock refresh goroutine
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-crawlCtx.Done():
				return
			case <-ticker.C:
				o.coordinator.RefreshLocks(crawlCtx, lockedOutpoints)
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
		origin, err := o.origins.GetOrigin(ctx, currentOutpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to check origin: %w", err)
		}
		if origin != nil {
			if err := o.migrateToOrigin(ctx, requestedOutpoint, origin, chain); err != nil {
				o.coordinator.PublishFailure(lockedOutpoints)
				return nil, fmt.Errorf("migration failed: %w", err)
			}
			o.coordinator.PublishComplete(lockedOutpoints, origin.String())
			return origin, nil
		}

		// Try to acquire lock
		acquired, err := o.coordinator.AcquireLock(ctx, currentOutpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to acquire lock: %w", err)
		}
		if !acquired {
			if err := o.coordinator.WaitForCrawl(ctx, currentOutpoint); err != nil {
				return nil, err
			}

			origin, err = o.origins.GetOrigin(ctx, currentOutpoint)
			if err != nil {
				return nil, fmt.Errorf("failed to check origin after wait: %w", err)
			}
			if origin != nil {
				if err := o.migrateToOrigin(ctx, requestedOutpoint, origin, chain); err != nil {
					o.coordinator.PublishFailure(lockedOutpoints)
					return nil, fmt.Errorf("migration failed: %w", err)
				}
				o.coordinator.PublishComplete(lockedOutpoints, origin.String())
				return origin, nil
			}

			acquired, err = o.coordinator.AcquireLock(ctx, currentOutpoint)
			if err != nil || !acquired {
				return nil, fmt.Errorf("failed to acquire lock after wait")
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
		if resp.ContentType != "" {
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
			ContentType:     resp.ContentType,
			ContentLength:   resp.ContentLength,
		})

		prevOutpoint, err := o.calculatePreviousOrdinalInput(ctx, currentTx, currentOutpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate previous input: %w", err)
		}

		if prevOutpoint == nil {
			if err := o.migrateToOrigin(ctx, requestedOutpoint, currentOutpoint, chain); err != nil {
				o.coordinator.PublishFailure(lockedOutpoints)
				return nil, fmt.Errorf("migration failed: %w", err)
			}
			o.coordinator.PublishComplete(lockedOutpoints, currentOutpoint.String())
			return currentOutpoint, nil
		}

		relativeSeq--
		currentOutpoint = prevOutpoint
	}
}

// migrateToOrigin migrates chain entries to use the discovered origin
func (o *Ordfs) migrateToOrigin(ctx context.Context, _ *transaction.Outpoint, origin *transaction.Outpoint, chain []ChainEntry) error {
	if len(chain) == 0 {
		return nil
	}

	offset := -chain[len(chain)-1].RelativeSeq

	batch := &OriginBatch{
		Origin:  origin,
		Entries: make([]OriginEntry, len(chain)),
		Origins: make([]*transaction.Outpoint, len(chain)),
	}

	for i, entry := range chain {
		absoluteSeq := uint32(entry.RelativeSeq + offset)
		batch.Entries[i] = OriginEntry{
			Outpoint:      entry.Outpoint,
			Seq:           absoluteSeq,
			HasRev:        entry.ContentOutpoint != nil,
			HasMap:        entry.MapOutpoint != nil,
			HasPar:        entry.ParentOutpoint != nil,
			ContentType:   entry.ContentType,
			ContentLength: uint32(entry.ContentLength),
		}
		batch.Origins[i] = entry.Outpoint
	}

	return o.origins.WriteBatch(ctx, batch)
}

// forwardCrawl crawls forward from an outpoint to find a target sequence
func (o *Ordfs) forwardCrawl(ctx context.Context, origin, startOutpoint *transaction.Outpoint, startSeq, targetSeq int) (*transaction.Outpoint, int, error) {
	currentOutpoint := startOutpoint
	currentSeq := startSeq

	for {
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		default:
		}

		output, err := o.loadOutput(ctx, currentOutpoint)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to load output: %w", err)
		}

		resp := o.parseOutput(ctx, currentOutpoint, output, true)

		entry := &OriginEntry{
			Outpoint:      currentOutpoint,
			Seq:           uint32(currentSeq),
			HasRev:        resp.ContentType != "",
			HasMap:        resp.Map != nil,
			HasPar:        resp.Parent != nil,
			ContentType:   resp.ContentType,
			ContentLength: uint32(resp.ContentLength),
		}
		if err := o.origins.AddEntry(ctx, origin, entry); err != nil {
			return nil, 0, fmt.Errorf("failed to add origin entry: %w", err)
		}

		if targetSeq >= 0 && currentSeq >= targetSeq {
			break
		}

		spendTxid, err := o.loadSpend(ctx, currentOutpoint)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to get spend: %w", err)
		}
		if spendTxid == nil {
			break
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
			break
		}

		currentOutpoint = nextOutpoint
		currentSeq++
	}

	return currentOutpoint, currentSeq, nil
}

// Resolve resolves an outpoint to a specific sequence in the ordinal chain
func (o *Ordfs) Resolve(ctx context.Context, requestedOutpoint *transaction.Outpoint, seq int) (*Resolution, error) {
	origin, err := o.origins.GetOrigin(ctx, requestedOutpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to check origin: %w", err)
	}
	if origin == nil {
		origin, err = o.backwardCrawl(ctx, requestedOutpoint)
		if err != nil {
			return nil, fmt.Errorf("backward crawl failed: %w", err)
		}
	}

	targetAbsoluteSeq := seq

	var targetOutpoint *transaction.Outpoint
	if targetAbsoluteSeq >= 0 {
		targetOutpoint, err = o.origins.GetSeqAt(ctx, origin, uint32(targetAbsoluteSeq))
		if err != nil {
			return nil, fmt.Errorf("failed to lookup sequence: %w", err)
		}
	}

	if targetOutpoint == nil {
		crawlStartOutpoint, crawlStartSeq, err := o.origins.GetLatestSeq(ctx, origin)
		if err != nil {
			return nil, fmt.Errorf("failed to get latest sequence: %w", err)
		}
		if crawlStartOutpoint == nil {
			crawlStartOutpoint = origin
			crawlStartSeq = 0
		}

		finalOutpoint, finalSeq, err := o.forwardCrawl(ctx, origin, crawlStartOutpoint, int(crawlStartSeq), targetAbsoluteSeq)
		if err != nil {
			return nil, fmt.Errorf("forward crawl failed: %w", err)
		}

		if seq == -1 {
			targetAbsoluteSeq = finalSeq
			targetOutpoint = finalOutpoint
		} else if targetAbsoluteSeq >= 0 {
			targetOutpoint, err = o.origins.GetSeqAt(ctx, origin, uint32(targetAbsoluteSeq))
			if err != nil {
				return nil, fmt.Errorf("failed to lookup sequence after crawl: %w", err)
			}
			if targetOutpoint == nil {
				return nil, fmt.Errorf("target sequence %d not found (chain ends at %d): %w", targetAbsoluteSeq, finalSeq, ErrNotFound)
			}
		}
	}

	resolution := &Resolution{
		Origin:   origin,
		Current:  targetOutpoint,
		Sequence: targetAbsoluteSeq,
	}

	revEntry, _ := o.origins.GetLatestRevBefore(ctx, origin, uint32(targetAbsoluteSeq))
	resolution.Content = revEntry
	resolution.Map, _ = o.origins.GetLatestMapBefore(ctx, origin, uint32(targetAbsoluteSeq))
	resolution.Parent, _ = o.origins.GetLatestParentBefore(ctx, origin, uint32(targetAbsoluteSeq))

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
		response.ContentType = resolution.Content.ContentType
		response.ContentLength = int(resolution.Content.ContentLength)
		if req.Content {
			output, err := o.loadOutput(ctx, resolution.Content.Outpoint)
			if err != nil {
				return nil, fmt.Errorf("failed to load content output: %w", err)
			}
			parsed := o.parseOutput(ctx, resolution.Content.Outpoint, output, true)
			response.Content = parsed.Content
		}
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

	if cached, err := o.cache.Get(ctx, mergedKey); err == nil && cached != nil {
		var mergedMap map[string]any
		if json.Unmarshal(cached, &mergedMap) == nil {
			return mergedMap, nil
		}
	}

	mapSeq, err := o.origins.GetMapSeq(ctx, origin, mapOutpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to get map sequence: %w", err)
	}

	mapOutpoints, err := o.origins.GetAllMapUpTo(ctx, origin, mapSeq)
	if err != nil {
		return nil, fmt.Errorf("failed to get map outpoints: %w", err)
	}

	mergedMap := make(map[string]any)
	for _, outpoint := range mapOutpoints {
		var individualMap map[string]any

		if cached := o.getCachedParsed(ctx, outpoint); cached != nil && cached.Map != "" {
			json.Unmarshal([]byte(cached.Map), &individualMap)
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

	if mergedJSON, err := json.Marshal(mergedMap); err == nil {
		o.cache.Set(ctx, mergedKey, mergedJSON)
	}

	return mergedMap, nil
}

// StreamContent streams content from an ordinal chain
func (o *Ordfs) StreamContent(ctx context.Context, outpoint *transaction.Outpoint, rangeStart, rangeEnd *int64, writer io.Writer) (*StreamResponse, error) {
	origin, err := o.origins.GetOrigin(ctx, outpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to check origin: %w", err)
	}
	if origin == nil {
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
