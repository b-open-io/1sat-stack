package bitcom

import (
	"github.com/bsv-blockchain/go-sdk/script"
)

type Bitcom struct {
	Protocols    []*BitcomProtocol `json:"protos"`
	ScriptPrefix []byte            `json:"prefix,omitempty"`
}
type BitcomProtocol struct {
	Protocol string `json:"proto"`
	Script   []byte `json:"script"`
	Pos      int    `json:"pos"`
}

func Decode(scr *script.Script) (bitcom *Bitcom) {
	// Handle nil script safely
	if scr == nil {
		return &Bitcom{
			Protocols: []*BitcomProtocol{},
		}
	}

	pos := findReturn(scr, 0)
	if pos == -1 {
		return
	}
	var prefix []byte
	if pos > 0 {
		prefix = (*scr)[:pos-1]
	}
	bitcom = &Bitcom{
		ScriptPrefix: prefix,
	}
	pos++

	for pos < len(*scr) {
		pipePos := findPipe(scr, pos)
		p := &BitcomProtocol{
			Pos: pos,
		}
		if op, err := scr.ReadOp(&pos); err != nil {
			return
		} else {
			p.Protocol = string(op.Data)
		}
		bitcom.Protocols = append(bitcom.Protocols, p)
		if pipePos == -1 {
			p.Script = (*scr)[pos:]
			break
		}
		if pipePos < pos {
			break
		}
		p.Script = (*scr)[pos:pipePos]
		pos = pipePos + 2
	}
	return bitcom
}

func (b *Bitcom) Lock() *script.Script {
	s := script.NewFromBytes(b.ScriptPrefix)
	if len(b.Protocols) > 0 {
		_ = s.AppendOpcodes(script.OpRETURN)
		for i, p := range b.Protocols {
			_ = s.AppendPushData([]byte(p.Protocol))
			s = script.NewFromBytes(append(*s, p.Script...))
			if i < len(b.Protocols)-1 {
				_ = s.AppendPushData([]byte("|"))
			}
		}
	}
	return s
}

func findReturn(scr *script.Script, from int) int {
	if scr != nil {
		i := from
		for i < len(*scr) {
			startPos := i
			if op, err := scr.ReadOp(&i); err != nil {
				break
			} else if op.Op == script.OpRETURN {
				return startPos
			}
		}
	}
	return -1
}

func findPipe(scr *script.Script, from int) int {
	if scr != nil {
		i := from
		for i < len(*scr) {
			startPos := i

			if op, err := scr.ReadOp(&i); err != nil {
				break
			} else if op.Op == script.OpDATA1 && len(op.Data) > 0 && op.Data[0] == '|' {
				return startPos
			}
		}
	}
	return -1
}

// ToScript converts a []byte to a script.Script or returns a script directly
// This is a helper function that can be used by all decoders
func ToScript(data any) *script.Script {
	switch d := data.(type) {
	case *script.Script:
		return d
	case script.Script:
		return &d
	case []byte:
		if d == nil {
			return nil
		}
		// Convert bytes to script
		s := script.NewFromBytes(d)
		return s
	default:
		return nil
	}
}
