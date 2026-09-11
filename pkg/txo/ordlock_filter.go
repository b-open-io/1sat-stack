package txo

import (
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/store"
)

// isPublicOrdLockSearch reports whether a TXO search is a public deprecated
// listing feed rather than an owner-scoped wallet intersection.
func isPublicOrdLockSearch(keys [][]byte, join store.JoinType) bool {
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
	return hasOrdLock && !(hasOwner && join == store.JoinIntersect)
}

func isOwnerSearchKey(key string) bool {
	key = strings.TrimPrefix(key, PfxEvent)
	return strings.HasPrefix(key, "own:")
}

func isOrdLockSearchKey(key string) bool {
	if key == PfxTopic+"tm_ordlock" {
		return true
	}
	key = strings.TrimPrefix(key, PfxEvent)
	return key == "ordlock"
}
