package ordlock

import "encoding/hex"

// OrdLock v2 (batch, tag-output binding) — compiled from the Rúnar
// OrdLockV2Batch contract. Unlike v1 (recognised by prefix+suffix around two
// early constructor pushes), v2 places its two constructor args near the end,
// so recognition uses the invariant leading prefix and the constructor slots.
//
// OrdLockV2Template is the full 489-byte locking script with OP_0 (0x00)
// placeholders at each constructor slot. OrdLockV2Prefix is the invariant
// leading run up to the first slot — identical for every v2 listing, so it is
// the recognizer. Slots: param 1 = payOutput @461, param 0 = seller @485.
var OrdLockV2Template, _ = hex.DecodeString("76009c637576ab76aa517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e01007e8100011f80517e9321414136d08c5ed2bf3ba048afe6dcaebafeffffffffffffffffffffffffffffff007d97785296789f527952798d9495937776927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e827c7e23022079be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798027c7e827c7e01307c7e01c17e2102b405d7f0322a89d0f9f3a98e6f938fdc1c969a8d1382a2bf66a71ae74a1e83b0ad690c000000000000000027006a247801447f7701247f757e537a007e7c7e7b7eaa7c820128947f7701207f758767519d7b0a6f6c323a63616e63656c8876a90088ac68")

// OrdLockV2Prefix is OrdLockV2Template[:firstSlotOffset] (461 bytes).
var OrdLockV2Prefix = OrdLockV2Template[:OrdLockV2FirstSlot]

const OrdLockV2FirstSlot = 461

// OrdLockV2Slot maps a constructor paramIndex to its byte offset in the template.
type ordLockV2Slot struct {
	ParamIndex int
	ByteOffset int
}

var OrdLockV2Slots = []ordLockV2Slot{
	{ParamIndex: 1, ByteOffset: 461}, // payOutput (serialized output)
	{ParamIndex: 0, ByteOffset: 498}, // seller / cancel pubkey hash (20 bytes)
}
