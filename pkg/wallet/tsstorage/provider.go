// Package tsstorage provides a wdk.WalletStorageProvider that speaks the
// legacy JSON-RPC contract (POST /) served by the TypeScript wallet-toolbox
// StorageServer. Method implementations are a verbatim carry-over of the
// pre-V1 Go storage client; only the two Go-only methods with no TypeScript
// counterpart (ListTransactions, GetBalance) are stubbed.
package tsstorage

import (
	"context"
	"fmt"

	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/wdk"
)

// Compile-time check: Client satisfies the full provider interface.
var _ wdk.WalletStorageProvider = (*Client)(nil)

// Client is a JSON-RPC wallet storage provider for TypeScript backends.
type Client struct {
	client *rpcWalletStorageProvider
}

// Migrate migrates a wallet storage database.
func (c *Client) Migrate(ctx context.Context, storageName string, storageIdentityKey string) (string, error) {
	return c.client.Migrate(ctx, storageName, storageIdentityKey)
}

// MakeAvailable makes the storage available storage for user.
func (c *Client) MakeAvailable(ctx context.Context) (*wdk.TableSettings, error) {
	return c.client.MakeAvailable(ctx)
}

// SetActive updates the active storage identity key for the authenticated user.
func (c *Client) SetActive(ctx context.Context, auth wdk.AuthID, newActiveStorageIdentityKey string) error {
	return c.client.SetActive(ctx, auth, newActiveStorageIdentityKey)
}

// FindOrInsertUser retrieves an existing user or inserts a new one based on the given identity key.
func (c *Client) FindOrInsertUser(ctx context.Context, identityKey string) (*wdk.FindOrInsertUserResponse, error) {
	return c.client.FindOrInsertUser(ctx, identityKey)
}

// InternalizeAction handles the internalization of a transaction from the outside of the wallet.
func (c *Client) InternalizeAction(ctx context.Context, auth wdk.AuthID, args wdk.InternalizeActionArgs) (*wdk.InternalizeActionResult, error) {
	return c.client.InternalizeAction(ctx, auth, args)
}

// CreateAction creates a new transaction ready to be signed and processed later.
func (c *Client) CreateAction(ctx context.Context, auth wdk.AuthID, args wdk.ValidCreateActionArgs) (*wdk.StorageCreateActionResult, error) {
	return c.client.CreateAction(ctx, auth, args)
}

// ProcessAction processes a signed transaction created by CreateAction.
func (c *Client) ProcessAction(ctx context.Context, auth wdk.AuthID, args wdk.ProcessActionArgs) (*wdk.ProcessActionResult, error) {
	return c.client.ProcessAction(ctx, auth, args)
}

// InsertCertificateAuth adds a new certificate for a user.
func (c *Client) InsertCertificateAuth(ctx context.Context, auth wdk.AuthID, certificate *wdk.TableCertificateX) (uint, error) {
	return c.client.InsertCertificateAuth(ctx, auth, certificate)
}

// RelinquishCertificate revokes the specified certificate from the users certificates.
func (c *Client) RelinquishCertificate(ctx context.Context, auth wdk.AuthID, args wdk.RelinquishCertificateArgs) error {
	return c.client.RelinquishCertificate(ctx, auth, args)
}

// RelinquishOutput removes the specified output from the users outputs.
func (c *Client) RelinquishOutput(ctx context.Context, auth wdk.AuthID, args wdk.RelinquishOutputArgs) error {
	return c.client.RelinquishOutput(ctx, auth, args)
}

// ListCertificates retrieves a paginated list of certificates based on the provided filter and pagination arguments.
func (c *Client) ListCertificates(ctx context.Context, auth wdk.AuthID, args wdk.ListCertificatesArgs) (*wdk.ListCertificatesResult, error) {
	return c.client.ListCertificates(ctx, auth, args)
}

// ListOutputs retrieves a list of wallet outputs based on the provided query parameters in the arguments.
func (c *Client) ListOutputs(ctx context.Context, auth wdk.AuthID, args wdk.ListOutputsArgs) (*wdk.ListOutputsResult, error) {
	return c.client.ListOutputs(ctx, auth, args)
}

// ListActions retrieves a list of wallet actions based on the provided query parameters in the arguments.
func (c *Client) ListActions(ctx context.Context, auth wdk.AuthID, args wdk.ListActionsArgs) (*wdk.ListActionsResult, error) {
	return c.client.ListActions(ctx, auth, args)
}

// GetSyncChunk retrieves a chunk of sync data for a user between two storages using the provided synchronization arguments.
func (c *Client) GetSyncChunk(ctx context.Context, args wdk.RequestSyncChunkArgs) (*wdk.SyncChunk, error) {
	return c.client.GetSyncChunk(ctx, args)
}

// FindOrInsertSyncStateAuth retrieves an existing sync state or inserts a new one based on the provided authentication and storage details.
func (c *Client) FindOrInsertSyncStateAuth(ctx context.Context, auth wdk.AuthID, storageIdentityKey string, storageName string) (*wdk.FindOrInsertSyncStateAuthResponse, error) {
	return c.client.FindOrInsertSyncStateAuth(ctx, auth, storageIdentityKey, storageName)
}

// ProcessSyncChunk processes a sync chunk for a user, applying the changes contained within it.
func (c *Client) ProcessSyncChunk(ctx context.Context, args wdk.RequestSyncChunkArgs, chunk *wdk.SyncChunk) (*wdk.ProcessSyncChunkResult, error) {
	return c.client.ProcessSyncChunk(ctx, args, chunk)
}

// AbortAction aborts a transaction that is in progress and has not yet been finalized or sent to the network.
func (c *Client) AbortAction(ctx context.Context, auth wdk.AuthID, args wdk.AbortActionArgs) (*wdk.AbortActionResult, error) {
	return c.client.AbortAction(ctx, auth, args)
}

// FindOutputBasketsAuth finds output baskets for the authenticated user based on the provided filters.
func (c *Client) FindOutputBasketsAuth(ctx context.Context, auth wdk.AuthID, filters wdk.FindOutputBasketsArgs) (wdk.TableOutputBaskets, error) {
	return c.client.FindOutputBasketsAuth(ctx, auth, filters)
}

// FindOutputsAuth finds outputs for the authenticated user based on the provided filters.
func (c *Client) FindOutputsAuth(ctx context.Context, auth wdk.AuthID, filters wdk.FindOutputsArgs) (wdk.TableOutputs, error) {
	return c.client.FindOutputsAuth(ctx, auth, filters)
}

// ListTransactions has no TypeScript counterpart and is stubbed.
func (c *Client) ListTransactions(_ context.Context, _ wdk.AuthID, _ wdk.ListTransactionsArgs) (*wdk.ListTransactionsResult, error) {
	return nil, fmt.Errorf("tsstorage: ListTransactions not supported by TypeScript storage backend")
}

// GetBalance has no TypeScript counterpart and is stubbed.
func (c *Client) GetBalance(_ context.Context, _ wdk.AuthID, _ string) (uint64, error) {
	return 0, fmt.Errorf("tsstorage: GetBalance not supported by TypeScript storage backend")
}

type rpcWalletStorageProvider struct {
	Migrate                   func(context.Context, string, string) (string, error)
	MakeAvailable             func(context.Context) (*wdk.TableSettings, error)
	SetActive                 func(context.Context, wdk.AuthID, string) error
	FindOrInsertUser          func(context.Context, string) (*wdk.FindOrInsertUserResponse, error)
	InternalizeAction         func(context.Context, wdk.AuthID, wdk.InternalizeActionArgs) (*wdk.InternalizeActionResult, error)
	CreateAction              func(context.Context, wdk.AuthID, wdk.ValidCreateActionArgs) (*wdk.StorageCreateActionResult, error)
	ProcessAction             func(context.Context, wdk.AuthID, wdk.ProcessActionArgs) (*wdk.ProcessActionResult, error)
	InsertCertificateAuth     func(context.Context, wdk.AuthID, *wdk.TableCertificateX) (uint, error)
	RelinquishCertificate     func(context.Context, wdk.AuthID, wdk.RelinquishCertificateArgs) error
	RelinquishOutput          func(context.Context, wdk.AuthID, wdk.RelinquishOutputArgs) error
	ListCertificates          func(context.Context, wdk.AuthID, wdk.ListCertificatesArgs) (*wdk.ListCertificatesResult, error)
	ListOutputs               func(context.Context, wdk.AuthID, wdk.ListOutputsArgs) (*wdk.ListOutputsResult, error)
	ListActions               func(context.Context, wdk.AuthID, wdk.ListActionsArgs) (*wdk.ListActionsResult, error)
	GetSyncChunk              func(context.Context, wdk.RequestSyncChunkArgs) (*wdk.SyncChunk, error)
	FindOrInsertSyncStateAuth func(context.Context, wdk.AuthID, string, string) (*wdk.FindOrInsertSyncStateAuthResponse, error)
	ProcessSyncChunk          func(context.Context, wdk.RequestSyncChunkArgs, *wdk.SyncChunk) (*wdk.ProcessSyncChunkResult, error)
	AbortAction               func(context.Context, wdk.AuthID, wdk.AbortActionArgs) (*wdk.AbortActionResult, error)
	FindOutputBasketsAuth     func(context.Context, wdk.AuthID, wdk.FindOutputBasketsArgs) (wdk.TableOutputBaskets, error)
	FindOutputsAuth           func(context.Context, wdk.AuthID, wdk.FindOutputsArgs) (wdk.TableOutputs, error)
}
