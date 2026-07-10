package bitcom

import (
	"bytes"
	"strings"

	"github.com/bsv-blockchain/go-sdk/script"
)

const MapPrefix = "1PuQa7K62MiKCtssSLKy1kh56WWU7MtUR5"

type MapCmd string

var ZERO = 0

var (
	MapCmdSet    MapCmd = "SET"
	MapCmdDel    MapCmd = "DEL"
	MapCmdAdd    MapCmd = "ADD"
	MapCmdSelect MapCmd = "SELECT"
)

type Map struct {
	Cmd  MapCmd            `json:"cmd"`
	Data map[string]string `json:"data"`
	Adds []string          `json:"adds,omitempty"`
}

// DecodeMap decodes the map data from the transaction script
func DecodeMap(data any) *Map {
	scr := ToScript(data)
	if scr == nil || len(*scr) == 0 {
		return nil
	}

	pos := ZERO
	var op *script.ScriptChunk
	var err error

	// If length is < minimum, return nil
	if len(*scr) < 6 {
		return nil
	}

	// Read command
	if op, err = scr.ReadOp(&pos); err != nil {
		return nil
	}
	cmd := MapCmd(op.Data)

	// Create map
	m := &Map{
		Cmd:  cmd,
		Data: make(map[string]string),
	}

	// Handle SET command
	if cmd == MapCmdSet {
		for {
			// Save position to revert if needed
			keyPos := pos

			// Try to read key
			if op, err = scr.ReadOp(&pos); err != nil {
				break
			}
			opKey := strings.ReplaceAll(string(bytes.ReplaceAll(op.Data, []byte{0}, []byte{' '})), "\\u0000", " ")

			// Try to read value
			if op, err = scr.ReadOp(&pos); err != nil {
				// Couldn't read value, revert to position before key and break
				pos = keyPos
				break
			}

			// Clean up value, replacing invalid UTF-8 sequences with spaces
			// rather than skipping the entire key-value pair
			cleanValue := strings.ReplaceAll(string(bytes.ReplaceAll(op.Data, []byte{0}, []byte{' '})), "\\u0000", " ")

			m.Data[opKey] = cleanValue
		}
	}

	return m
}
