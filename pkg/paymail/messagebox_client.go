package paymail

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

// MessageBoxClient sends messages to a remote messagebox server via authenticated HTTP.
type MessageBoxClient struct {
	baseURL   string
	authFetch *authhttp.AuthFetch
	logger    *slog.Logger
}

// NewMessageBoxClient creates a client pointing at the given messagebox server URL.
func NewMessageBoxClient(baseURL string, wallet sdk.Interface, logger *slog.Logger) *MessageBoxClient {
	return &MessageBoxClient{
		baseURL:   baseURL,
		authFetch: authhttp.New(wallet, authhttp.WithLogger(logger)),
		logger:    logger,
	}
}

type sendMessageRequest struct {
	Message sendMessageBody `json:"message"`
}

type sendMessageBody struct {
	Recipient  string          `json:"recipient"`
	MessageBox string          `json:"messageBox"`
	MessageID  string          `json:"messageId"`
	Body       json.RawMessage `json:"body"`
}

// SendMessage sends a message to a recipient's message box on the remote server.
func (c *MessageBoxClient) SendMessage(ctx context.Context, recipient, messageBox, messageID string, body []byte) error {
	reqBody, err := json.Marshal(sendMessageRequest{
		Message: sendMessageBody{
			Recipient:  recipient,
			MessageBox: messageBox,
			MessageID:  messageID,
			Body:       json.RawMessage(body),
		},
	})
	if err != nil {
		return fmt.Errorf("marshal send message request: %w", err)
	}

	resp, err := c.authFetch.Fetch(ctx, c.baseURL+"/sendMessage", &authhttp.SimplifiedFetchRequestOptions{
		Method:  "POST",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    reqBody,
	})
	if err != nil {
		return fmt.Errorf("fetch send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("send message returned status %d: %s", resp.StatusCode, bytes.TrimSpace(respBody))
	}

	return nil
}
