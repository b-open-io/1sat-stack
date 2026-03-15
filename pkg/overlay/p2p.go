package overlay

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/p2p"
	"github.com/b-open-io/1sat-stack/pkg/store"
	msgbus "github.com/bsv-blockchain/go-p2p-message-bus"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
)

// P2PConfig holds configuration for the overlay P2P network.
type P2PConfig struct {
	Enabled        bool     `mapstructure:"enabled"`
	Port           int      `mapstructure:"port"`
	StoragePath    string   `mapstructure:"storage_path"`
	DHTMode        string   `mapstructure:"dht_mode"`
	BootstrapPeers []string `mapstructure:"bootstrap_peers"`
}

// P2PBus wraps a msgbus.Client for overlay topic pub/sub.
type P2PBus struct {
	client msgbus.Client
	store  store.Store
	logger *slog.Logger
}

// NewP2PBus creates a P2PBus from a msgbus.Client.
func NewP2PBus(client msgbus.Client, s store.Store, logger *slog.Logger) *P2PBus {
	return &P2PBus{
		client: client,
		store:  s,
		logger: logger,
	}
}

// PublishSteak serializes and publishes a steak message for a single topic.
func (b *P2PBus) PublishSteak(ctx context.Context, topic string, txid *chainhash.Hash, instructions *overlay.AdmittanceInstructions, beef []byte) {
	if instructions == nil {
		return
	}

	msg := &p2p.SteakMessage{
		Version:      p2p.SteakVersion2,
		Txid:         *txid,
		Instructions: *instructions,
		TxSize:       uint32(len(beef)),
		PriceSats:    0, // free for now — pricing engine will set this
	}

	if err := b.client.Publish(ctx, topic, msg.Serialize()); err != nil {
		b.logger.Error("failed to publish steak", "topic", topic, "txid", txid, "error", err)
	}
}

// OnAdmission is a callback suitable for engine.Engine.OnAdmission.
// It publishes one message per topic in the steak.
func (b *P2PBus) OnAdmission(txid *chainhash.Hash, steak *overlay.Steak, beef []byte) {
	if steak == nil {
		return
	}

	ctx := context.Background()
	for topic, instructions := range *steak {
		if instructions == nil || len(instructions.OutputsToAdmit) == 0 {
			continue
		}
		b.PublishSteak(ctx, topic, txid, instructions, beef)
	}
}

// Subscribe starts listening on a topic and queues inbound steaks for GASP processing.
// Returns a cancel function to unsubscribe.
func (b *P2PBus) Subscribe(ctx context.Context, topicName string) context.CancelFunc {
	subCtx, cancel := context.WithCancel(ctx)
	ch := b.client.Subscribe(topicName)

	go func() {
		for {
			select {
			case <-subCtx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}

				if msg.FromID == b.client.GetID() {
					continue
				}

				steakMsg, err := p2p.DeserializeSteakMessage(msg.Data)
				if err != nil {
					b.logger.Error("failed to deserialize steak message",
						"topic", topicName,
						"from", msg.FromID,
						"error", err,
					)
					continue
				}

				queueKey := []byte("q:" + topicName)
				for _, vout := range steakMsg.Instructions.OutputsToAdmit {
					outpoint := fmt.Sprintf("%s_%d", steakMsg.Txid, vout)
					if err := b.store.ZAdd(ctx, queueKey, store.ScoredMember{
						Member: []byte(outpoint),
						Score:  float64(time.Now().Unix()),
					}); err != nil {
						b.logger.Error("failed to queue inbound outpoint",
							"topic", topicName,
							"outpoint", outpoint,
							"error", err,
						)
					}
				}

				b.logger.Debug("queued inbound steak",
					"topic", topicName,
					"txid", steakMsg.Txid,
					"outputs", len(steakMsg.Instructions.OutputsToAdmit),
					"from", msg.FromID,
				)
			}
		}
	}()

	b.logger.Info("subscribed to overlay topic", "topic", topicName)
	return cancel
}
