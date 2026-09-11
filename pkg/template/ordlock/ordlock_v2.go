package ordlock

import (
	"bytes"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// IsOrdLockV2 reports whether a locking script is an OrdLock v2 listing,
// recognised by its invariant prefix (arg-independent).
func IsOrdLockV2(scr *script.Script) bool {
	return scr != nil && bytes.HasPrefix(*scr, OrdLockV2Prefix)
}

// DecodeV2 recovers the seller (cancel PKH) and payout from a deployed v2
// listing script. Artifact-driven: it walks OrdLockV2Template and the deployed
// script in lockstep, reading one pushdata at each constructor slot. Returns
// the same OrdLock shape as v1 so downstream code is uniform. nil if not v2.
func DecodeV2(scr *script.Script) *OrdLock {
	if !IsOrdLockV2(scr) {
		return nil
	}
	args, ok := decodeV2Slots(*scr)
	if !ok {
		return nil
	}
	sellerPKH, ok := args[0]
	if !ok {
		return nil
	}
	payoutBytes, ok := args[1]
	if !ok {
		return nil
	}
	payOutput := &transaction.TransactionOutput{}
	if _, err := payOutput.ReadFrom(bytes.NewReader(payoutBytes)); err != nil {
		return nil
	}
	seller, err := script.NewAddressFromPublicKeyHash(sellerPKH, true)
	if err != nil {
		return nil
	}
	return &OrdLock{
		Seller: seller,
		Price:  payOutput.Satoshis,
		PayOut: payOutput.Bytes(),
	}
}

// decodeV2Slots walks the template vs the deployed script; at each OP_0 slot it
// reads the deployed push, keyed by paramIndex. Non-slot regions must match the
// template byte-for-byte.
func decodeV2Slots(deployed []byte) (map[int][]byte, bool) {
	out := map[int][]byte{}
	slotAt := map[int]int{} // byteOffset -> paramIndex
	for _, s := range OrdLockV2Slots {
		slotAt[s.ByteOffset] = s.ParamIndex
	}
	ti, di := 0, 0
	for ti < len(OrdLockV2Template) {
		if pi, isSlot := slotAt[ti]; isSlot {
			if OrdLockV2Template[ti] != 0x00 {
				return nil, false
			}
			val, adv, ok := readPushAt(deployed, di)
			if !ok {
				return nil, false
			}
			out[pi] = val
			ti++
			di += adv
			continue
		}
		if di >= len(deployed) || deployed[di] != OrdLockV2Template[ti] {
			return nil, false
		}
		ti++
		di++
	}
	return out, di == len(deployed)
}

// readPushAt reads one pushdata op at off, returning (data, bytesConsumed, ok).
func readPushAt(b []byte, off int) ([]byte, int, bool) {
	if off >= len(b) {
		return nil, 0, false
	}
	op := b[off]
	switch {
	case op >= 0x01 && op <= 0x4b:
		n := int(op)
		if off+1+n > len(b) {
			return nil, 0, false
		}
		return b[off+1 : off+1+n], 1 + n, true
	case op == 0x4c: // OP_PUSHDATA1
		if off+2 > len(b) {
			return nil, 0, false
		}
		n := int(b[off+1])
		if off+2+n > len(b) {
			return nil, 0, false
		}
		return b[off+2 : off+2+n], 2 + n, true
	case op == 0x4d: // OP_PUSHDATA2
		if off+3 > len(b) {
			return nil, 0, false
		}
		n := int(b[off+1]) | int(b[off+2])<<8
		if off+3+n > len(b) {
			return nil, 0, false
		}
		return b[off+3 : off+3+n], 3 + n, true
	default:
		return nil, 0, false
	}
}
