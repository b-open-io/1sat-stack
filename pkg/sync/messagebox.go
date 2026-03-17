package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"

	authhttp "github.com/bsv-blockchain/go-sdk/auth/clients/authhttp"
	sdk "github.com/bsv-blockchain/go-sdk/wallet"
)

type MessageBoxClient struct {
	baseURL   string
	authFetch *authhttp.AuthFetch
	logger    *slog.Logger
}

func NewMessageBoxClient(baseURL string, wallet sdk.Interface, logger *slog.Logger) *MessageBoxClient {
	return &MessageBoxClient{
		baseURL:   baseURL,
		authFetch: authhttp.New(wallet, authhttp.WithLogger(logger)),
		logger:    logger,
	}
}

type listMessagesRequest struct {
	MessageBox string `json:"messageBox"`
}

type listMessagesResponse struct {
	Status   string       `json:"status"`
	Messages []MessageOut `json:"messages"`
}

func (c *MessageBoxClient) ListMessages(ctx context.Context, messageBox string) ([]MessageOut, error) {
	body, err := json.Marshal(listMessagesRequest{MessageBox: messageBox})
	if err != nil {
		return nil, fmt.Errorf("marshal list messages request: %w", err)
	}

	resp, err := c.authFetch.Fetch(ctx, c.baseURL+"/listMessages", &authhttp.SimplifiedFetchRequestOptions{
		Method:  "POST",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    body,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch list messages: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read list messages response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("list messages returned status %d: %s", resp.StatusCode, bytes.TrimSpace(respBody))
	}

	var result listMessagesResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal list messages response: %w", err)
	}

	return result.Messages, nil
}

type acknowledgeMessagesRequest struct {
	MessageIDs []string `json:"messageIds"`
}

func (c *MessageBoxClient) AcknowledgeMessages(ctx context.Context, messageIDs []string) error {
	body, err := json.Marshal(acknowledgeMessagesRequest{MessageIDs: messageIDs})
	if err != nil {
		return fmt.Errorf("marshal acknowledge messages request: %w", err)
	}

	resp, err := c.authFetch.Fetch(ctx, c.baseURL+"/acknowledgeMessage", &authhttp.SimplifiedFetchRequestOptions{
		Method:  "POST",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    body,
	})
	if err != nil {
		return fmt.Errorf("fetch acknowledge messages: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("acknowledge messages returned status %d: %s", resp.StatusCode, bytes.TrimSpace(respBody))
	}

	return nil
}
