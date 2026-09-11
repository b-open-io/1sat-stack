package txo

import "strings"

// isPublicOrdLockSearch reports whether a TXO search is a public deprecated
// listing feed (event key "ordlock") rather than an owner-scoped wallet lookup.
func isPublicOrdLockSearch(keys [][]byte) bool {
	hasOrdLock := false
	hasOwner := false
	for _, k := range keys {
		s := string(k)
		if isOwnerSearchKey(s) {
			hasOwner = true
			continue
		}
		if isOrdLockSearchKey(s) {
			hasOrdLock = true
		}
	}
	return hasOrdLock && !hasOwner
}

func isOwnerSearchKey(key string) bool {
	key = strings.TrimPrefix(key, PfxEvent)
	return strings.HasPrefix(key, "own:")
}

func isOrdLockSearchKey(key string) bool {
	key = strings.TrimPrefix(key, PfxEvent)
	return key == "ordlock"
}
