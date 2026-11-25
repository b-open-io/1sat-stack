package ordlock

import (
	"context"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// SpendEvent is the indexer's spend event for v2 listings. Its member is
// "<spending txid>_<source vout>" (see pkg/txo output_store).
const SpendEvent = "spend:" + QueueName

// SpendSync records OrdLock v2 spends straight from the indexer's spend
// events, bypassing the overlay engine. The engine only notifies OutputSpent
// for coins it has already admitted, so a spend that reaches it before (or
// alongside) its listing is dropped and the market keeps showing the listing
// as active. This path has no such dependency: the spending transaction's
// full BEEF carries the listing output, and UpsertSpend creates or updates
// the row either way.
type SpendSync struct {
	pubsub pubsub.PubSub
	beef   *beef.Storage
	lookup *LookupServiceV2
	logger *slog.Logger
}

func NewSpendSync(ps pubsub.PubSub, beefStorage *beef.Storage, lookup *LookupServiceV2, logger *slog.Logger) *SpendSync {
	if logger == nil {
		logger = slog.Default()
	}
	return &SpendSync{pubsub: ps, beef: beefStorage, lookup: lookup, logger: logger.With("component", "spend-sync")}
}

// Start subscribes to the spend event and records spends until ctx ends.
func (s *SpendSync) Start(ctx context.Context) error {
	ch, err := s.pubsub.Subscribe(ctx, []string{SpendEvent})
	if err != nil {
		return err
	}
	go s.run(ctx, ch)
	return nil
}

func (s *SpendSync) run(ctx context.Context, ch <-chan pubsub.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := s.Process(ctx, ev.Member); err != nil {
				s.logger.Error("failed to record spend", "member", ev.Member, "error", err)
			}
		}
	}
}

// Process records the v2 listing spends of the transaction named by a spend
// event member.
func (s *SpendSync) Process(ctx context.Context, member string) error {
	op, err := transaction.OutpointFromString(member)
	if err != nil {
		return err
	}
	tx, err := s.beef.BuildFullBeefTx(ctx, &op.Txid)
	if err != nil {
		return err
	}
	n, err := s.lookup.RecordSpends(ctx, tx, &op.Txid)
	if err != nil {
		return err
	}
	if n > 0 {
		s.logger.Info("ordlock v2 spends recorded", "txid", op.Txid.String(), "listings", n)
	}
	return nil
}
