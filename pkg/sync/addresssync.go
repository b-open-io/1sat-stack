package sync

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"

	authhttp "github.com/bsv-blockchain/go-sdk/auth/clients/authhttp"
	sdk "github.com/bsv-blockchain/go-sdk/wallet"
)

// AddressSyncClient streams SyncOutput events from the owner/sync SSE endpoint
// and fetches BEEF bytes for individual transactions.
type AddressSyncClient struct {
	ownerSyncURL string
	beefURL      string
	authFetch    *authhttp.AuthFetch
	logger       *slog.Logger
}

// NewAddressSyncClient creates a client that connects to the owner sync and BEEF endpoints.
func NewAddressSyncClient(ownerSyncURL, beefURL string, wallet sdk.Interface, logger *slog.Logger) *AddressSyncClient {
	return &AddressSyncClient{
		ownerSyncURL: ownerSyncURL,
		beefURL:      beefURL,
		authFetch:    authhttp.New(wallet, authhttp.WithLogger(logger)),
		logger:       logger,
	}
}

// Sync connects to the owner/sync SSE endpoint and streams SyncOutput events
// for the given addresses starting from fromScore. The returned channel is closed
// when the stream ends (done event, EOF, or context cancellation).
func (c *AddressSyncClient) Sync(ctx context.Context, addresses []string, fromScore float64) (<-chan SyncOutput, error) {
	u, err := url.Parse(c.ownerSyncURL + "/sync")
	if err != nil {
		return nil, fmt.Errorf("parsing sync URL: %w", err)
	}

	q := u.Query()
	for _, addr := range addresses {
		q.Add("owner", addr)
	}
	q.Set("from", fmt.Sprintf("%f", fromScore))
	u.RawQuery = q.Encode()

	resp, err := c.authFetch.Fetch(ctx, u.String(), &authhttp.SimplifiedFetchRequestOptions{
		Method: "GET",
		Headers: map[string]string{
			"Accept": "text/event-stream",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("fetching sync stream: %w", err)
	}

	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("sync stream returned status %d", resp.StatusCode)
	}

	ch := make(chan SyncOutput, 64)

	go func() {
		defer resp.Body.Close()
		defer close(ch)

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()

			select {
			case <-ctx.Done():
				return
			default:
			}

			if strings.HasPrefix(line, "event: ") {
				if strings.TrimPrefix(line, "event: ") == "done" {
					return
				}
				continue
			}

			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				var out SyncOutput
				if err := json.Unmarshal([]byte(data), &out); err != nil {
					c.logger.Warn("failed to unmarshal sync output", "error", err, "data", data)
					continue
				}
				select {
				case ch <- out:
				case <-ctx.Done():
					return
				}
				continue
			}

			// id: lines and empty lines are ignored
		}

		if err := scanner.Err(); err != nil {
			c.logger.Warn("sync stream scanner error", "error", err)
		}
	}()

	return ch, nil
}

// GetBEEF fetches the BEEF bytes for the given transaction ID.
func (c *AddressSyncClient) GetBEEF(ctx context.Context, txid string) ([]byte, error) {
	resp, err := c.authFetch.Fetch(ctx, c.beefURL+"/"+txid, &authhttp.SimplifiedFetchRequestOptions{
		Method: "GET",
	})
	if err != nil {
		return nil, fmt.Errorf("fetching BEEF for %s: %w", txid, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("BEEF request for %s returned status %d", txid, resp.StatusCode)
	}

	beef, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading BEEF response for %s: %w", txid, err)
	}

	return beef, nil
}
