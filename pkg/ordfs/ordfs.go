package ordfs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/bsv-blockchain/go-script-templates/template/bitcom"
	"github.com/bsv-blockchain/go-script-templates/template/inscription"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// ContentService handles content resolution and parsing
type ContentService struct {
	loader Loader
	logger *slog.Logger
}

// NewContentService creates a new content service
func NewContentService(loader Loader, logger *slog.Logger) *ContentService {
	if logger == nil {
		logger = slog.Default()
	}
	return &ContentService{
		loader: loader,
		logger: logger,
	}
}

// Load loads content by request
func (s *ContentService) Load(ctx context.Context, req *Request) (*Response, error) {
	if req.Txid != nil {
		return s.loadByTxid(ctx, req)
	}

	if req.Outpoint == nil {
		return nil, fmt.Errorf("either txid or outpoint is required")
	}

	output, err := s.loader.LoadOutput(req.Outpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to load output: %w", err)
	}

	resp := s.parseOutput(req.Outpoint, output)
	resp.Outpoint = req.Outpoint

	if !req.Content {
		resp.Content = nil
	}
	if !req.Map {
		resp.Map = ""
	}
	if req.Output {
		resp.Output = output.Bytes()
	}

	return resp, nil
}

// loadByTxid loads content by scanning all outputs of a transaction
func (s *ContentService) loadByTxid(ctx context.Context, req *Request) (*Response, error) {
	tx, err := s.loader.LoadTx(req.Txid.String())
	if err != nil {
		return nil, fmt.Errorf("transaction not found: %w", err)
	}

	for i, output := range tx.Outputs {
		outpoint := &transaction.Outpoint{
			Txid:  *req.Txid,
			Index: uint32(i),
		}
		resp := s.parseOutput(outpoint, output)
		if resp.Content != nil {
			resp.Outpoint = outpoint
			resp.Sequence = 0

			if !req.Content {
				resp.Content = nil
			}
			if !req.Map {
				resp.Map = ""
			}
			if req.Output {
				resp.Output = output.Bytes()
			}

			return resp, nil
		}
	}

	return nil, fmt.Errorf("no inscription or B protocol content found")
}

// parseOutput parses a transaction output for inscription or B protocol content
func (s *ContentService) parseOutput(outpoint *transaction.Outpoint, output *transaction.TransactionOutput) *Response {
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
			content = insc.File.Content
		}

		// Extract parent if present
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
				// B protocol content
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

	var mapJSON string
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
			mapJSON = string(mapBytes)
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

// ParseOutputForContent parses a single output for content (useful for indexer integration)
func ParseOutputForContent(output *transaction.TransactionOutput) (contentType string, content []byte, mapJSON string, parent *transaction.Outpoint) {
	lockingScript := script.Script(*output.LockingScript)

	var mapData map[string]string

	// Try inscription first
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

	// Try B protocol and MAP
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
