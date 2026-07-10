package inscription

import (
	"bytes"
	"crypto/sha256"
	"unicode/utf8"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

type File struct {
	Hash    []byte `json:"hash"`
	Size    uint32 `json:"size"`
	Type    string `json:"type"`
	Content []byte `json:"-"`
}

type Inscription struct {
	File         File                  `json:"file,omitempty"`
	Parent       *transaction.Outpoint `json:"parent,omitempty"`
	Fields       map[string][]byte     `json:"fields,omitempty"`
	ScriptPrefix []byte                `json:"prefix,omitempty"`
	ScriptSuffix []byte                `json:"suffix,omitempty"`
}

func Decode(scr *script.Script) *Inscription {
	for pos := 0; pos < len(*scr); {
		startI := pos
		if op, err := scr.ReadOp(&pos); err != nil {
			break
		} else if startI >= 2 && op.Op == script.OpDATA3 && bytes.Equal(op.Data, []byte("ord")) && (*scr)[startI-2] == 0 && (*scr)[startI-1] == script.OpIF {
			insc := &Inscription{
				ScriptPrefix: (*scr)[:startI-2],
				Fields:       make(map[string][]byte),
			}

		ordLoop:
			for {
				var field int
				var err error
				var op, op2 *script.ScriptChunk
				if op, err = scr.ReadOp(&pos); err != nil || op.Op > script.Op16 {
					return insc
				} else if op2, err = scr.ReadOp(&pos); err != nil || op2.Op > script.Op16 {
					return insc
				} else if op.Op > script.OpPUSHDATA4 && op.Op <= script.Op16 {
					field = int(op.Op) - 80
				} else if len(op.Data) == 1 {
					field = int(op.Data[0])
				} else if len(op.Data) > 1 {
					insc.Fields[string(op.Data)] = op2.Data
					continue
				}
				switch field {
				case 0:
					insc.File.Content = op2.Data
					insc.File.Size = uint32(len(insc.File.Content))
					hash := sha256.Sum256(insc.File.Content)
					insc.File.Hash = hash[:]
					break ordLoop
				case 1:
					if len(op2.Data) < 256 && utf8.Valid(op2.Data) {
						insc.File.Type = string(op2.Data)
					}
				case 3:
					if len(op2.Data) == 36 {
						insc.Parent = transaction.NewOutpointFromBytes(op2.Data)
					} else if len(op2.Data) == 32 {
						if txid, err := chainhash.NewHash(op2.Data); err == nil {
							insc.Parent = &transaction.Outpoint{Txid: *txid, Index: 0}
						}
					}
				}

			}
			op, err := scr.ReadOp(&pos)
			if err != nil || op.Op == script.OpENDIF {
				insc.ScriptSuffix = (*scr)[pos:]
				return insc
			}
		}
	}
	return nil
}

func (i *Inscription) Lock() (*script.Script, error) {
	s := script.NewFromBytes(i.ScriptPrefix)
	_ = s.AppendOpcodes(script.Op0, script.OpIF)
	_ = s.AppendPushData([]byte("ord"))

	// Add file type if available
	// if i.File.Type != "" {
	_ = s.AppendOpcodes(script.Op1)
	_ = s.AppendPushDataString(i.File.Type)

	// Add content
	_ = s.AppendOpcodes(script.Op0)
	_ = s.AppendPushData(i.File.Content)

	_ = s.AppendOpcodes(script.OpENDIF)
	return script.NewFromBytes(append(*s, i.ScriptSuffix...)), nil
}
