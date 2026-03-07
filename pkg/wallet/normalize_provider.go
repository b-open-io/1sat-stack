package wallet

// TODO: Remove this file once @bsv/sdk publishes the fix from
// https://github.com/bsv-blockchain/ts-sdk/pull/489 and all TS clients
// are updated. This is a temporary workaround to normalize tags, labels,
// and baskets to lowercase on the server side, compensating for the TS SDK
// not lowercasing identifiers on query paths (listOutputs, listActions).

import (
	"context"
	"strings"

	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/storage"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/wdk"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/wdk/primitives"
)

// normalizingProvider wraps a storage.Provider to normalize tags, labels,
// and baskets to lowercase before passing to the underlying provider.
type normalizingProvider struct {
	*storage.Provider
}

func normalizeIdentifier(s primitives.StringUnder300) primitives.StringUnder300 {
	return primitives.StringUnder300(strings.TrimSpace(strings.ToLower(string(s))))
}

func normalizeIdentifiers(ss []primitives.StringUnder300) []primitives.StringUnder300 {
	out := make([]primitives.StringUnder300, len(ss))
	for i, s := range ss {
		out[i] = normalizeIdentifier(s)
	}
	return out
}

func (p *normalizingProvider) ListOutputs(ctx context.Context, auth wdk.AuthID, args wdk.ListOutputsArgs) (*wdk.ListOutputsResult, error) {
	args.Basket = normalizeIdentifier(args.Basket)
	args.Tags = normalizeIdentifiers(args.Tags)
	return p.Provider.ListOutputs(ctx, auth, args)
}

func (p *normalizingProvider) ListActions(ctx context.Context, auth wdk.AuthID, args wdk.ListActionsArgs) (*wdk.ListActionsResult, error) {
	args.Labels = normalizeIdentifiers(args.Labels)
	return p.Provider.ListActions(ctx, auth, args)
}

func (p *normalizingProvider) CreateAction(ctx context.Context, auth wdk.AuthID, args wdk.ValidCreateActionArgs) (*wdk.StorageCreateActionResult, error) {
	args.Labels = normalizeIdentifiers(args.Labels)
	for i := range args.Outputs {
		args.Outputs[i].Tags = normalizeIdentifiers(args.Outputs[i].Tags)
		if args.Outputs[i].Basket != nil {
			b := normalizeIdentifier(*args.Outputs[i].Basket)
			args.Outputs[i].Basket = &b
		}
	}
	return p.Provider.CreateAction(ctx, auth, args)
}

func (p *normalizingProvider) InternalizeAction(ctx context.Context, auth wdk.AuthID, args wdk.InternalizeActionArgs) (*wdk.InternalizeActionResult, error) {
	args.Labels = normalizeIdentifiers(args.Labels)
	for _, output := range args.Outputs {
		if output.InsertionRemittance != nil {
			output.InsertionRemittance.Basket = normalizeIdentifier(output.InsertionRemittance.Basket)
			output.InsertionRemittance.Tags = normalizeIdentifiers(output.InsertionRemittance.Tags)
		}
	}
	return p.Provider.InternalizeAction(ctx, auth, args)
}
