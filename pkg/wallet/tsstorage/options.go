package tsstorage

import (
	"log/slog"
	"net/http"

	"github.com/filecoin-project/go-jsonrpc"
)

// ClientOptions is a function that can be used to override internal dependencies.
// This is meant to be used for testing purposes.
type ClientOptions = func(*clientOptions)

type clientOptions struct {
	rpcOptions []jsonrpc.Option
	httpClient *http.Client
	logger     *slog.Logger
}

func defaultClientOptions() clientOptions {
	return clientOptions{
		rpcOptions: []jsonrpc.Option{
			jsonrpc.WithMethodNameFormatter(jsonrpc.NewMethodNameFormatter(false, jsonrpc.LowerFirstCharCase)),
		},
	}
}

// WithClientLogger sets the logger for the client.
func WithClientLogger(logger *slog.Logger) ClientOptions {
	return func(o *clientOptions) {
		o.logger = logger
	}
}

// WithHttpClient overrides the http.Client used by the client.
// This is meant to be used for testing purposes.
func WithHttpClient(httpClient *http.Client) ClientOptions {
	return func(o *clientOptions) {
		o.httpClient = httpClient
	}
}
