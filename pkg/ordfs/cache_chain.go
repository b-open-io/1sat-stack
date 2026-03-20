package ordfs

import "context"

// CacheChain walks a list of caches in order. On Get, it returns the first hit
// and writes back to all faster caches. On Set/Delete, it applies to all caches.
type CacheChain struct {
	caches []Cache
}

func NewCacheChain(caches ...Cache) *CacheChain {
	return &CacheChain{caches: caches}
}

func (cc *CacheChain) Get(ctx context.Context, key string) ([]byte, error) {
	for i, c := range cc.caches {
		val, err := c.Get(ctx, key)
		if err != nil {
			return nil, err
		}
		if val != nil {
			// Write back to faster caches
			for j := 0; j < i; j++ {
				cc.caches[j].Set(ctx, key, val)
			}
			return val, nil
		}
	}
	return nil, nil
}

func (cc *CacheChain) Set(ctx context.Context, key string, value []byte) error {
	for _, c := range cc.caches {
		if err := c.Set(ctx, key, value); err != nil {
			return err
		}
	}
	return nil
}

func (cc *CacheChain) Delete(ctx context.Context, key string) error {
	for _, c := range cc.caches {
		if err := c.Delete(ctx, key); err != nil {
			return err
		}
	}
	return nil
}
