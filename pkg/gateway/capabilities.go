package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

// capabilityTimeout bounds each per-backend capabilities fetch.
const capabilityTimeout = 5 * time.Second

// mergedCapabilities fans out to every configured backend's
// {base}{basePath}/capabilities, unions the returned label arrays, dedupes,
// and returns them sorted. Unreachable backends are logged and omitted so the
// gateway degrades gracefully instead of failing the whole response.
func (g *Gateway) mergedCapabilities(ctx context.Context) []string {
	set := map[string]struct{}{}
	var mu sync.Mutex
	var wg sync.WaitGroup

	for service, base := range g.backends {
		wg.Add(1)
		go func(service, base string) {
			defer wg.Done()
			caps, err := fetchCapabilities(ctx, base, g.basePath)
			if err != nil {
				g.logger.Warn("gateway capabilities fetch failed", "service", service, "backend", base, "error", err)
				return
			}
			mu.Lock()
			for _, c := range caps {
				set[c] = struct{}{}
			}
			mu.Unlock()
		}(service, base)
	}
	wg.Wait()

	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func fetchCapabilities(ctx context.Context, base, basePath string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, capabilityTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+basePath+"/capabilities", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var caps []string
	if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil {
		return nil, err
	}
	return caps, nil
}
