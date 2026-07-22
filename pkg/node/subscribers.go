package node

import (
	"context"
	"log/slog"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/jbsync"
	ordlockpkg "github.com/b-open-io/1sat-stack/pkg/ordlock"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/txo"
)

// StartSubscribers starts all JungleBus subscribers in background goroutines.
// The subscribers will run until the context is cancelled.
func (svc *Services) StartSubscribers(ctx context.Context, logger *slog.Logger) {
	for _, sub := range svc.JBSubscribers {
		go func(s *jbsync.Subscriber) {
			if err := s.Start(ctx); err != nil {
				logger.Error("JungleBus subscriber error", "error", err)
			}
		}(sub)
	}
	if len(svc.JBSubscribers) > 0 {
		logger.Info("started JungleBus subscribers", "count", len(svc.JBSubscribers))
	}

	// Start EventBridges (PubSub → overlay queues + direct submit)
	if svc.PubSub != nil && svc.Beef != nil {
		if svc.BAP != nil && svc.BAP.Sync != nil {
			bridge := overlay.NewEventBridge(&overlay.EventBridgeConfig{
				PubSub:   svc.PubSub.PubSub,
				Store:    svc.Store.Store,
				Patterns: []string{"bap:*"},
				QueueFunc: func(ev pubsub.Event) string {
					return string(txo.KeyQueue("bap"))
				},
				Logger:       logger,
				Engine:       svc.BAP.Engine,
				BeefStorage:  svc.Beef.Storage,
				SubmitBuffer: 64,
			})
			if err := bridge.Start(ctx); err != nil {
				logger.Error("failed to start BAP event bridge", "error", err)
			}
		}
		if svc.BSocial != nil && svc.BSocial.Sync != nil {
			bridge := overlay.NewEventBridge(&overlay.EventBridgeConfig{
				PubSub:   svc.PubSub.PubSub,
				Store:    svc.Store.Store,
				Patterns: []string{"map:type:*"},
				QueueFunc: func(ev pubsub.Event) string {
					return string(txo.KeyQueue("bsocial"))
				},
				Logger:       logger,
				Engine:       svc.BSocial.Engine,
				BeefStorage:  svc.Beef.Storage,
				SubmitBuffer: 64,
			})
			if err := bridge.Start(ctx); err != nil {
				logger.Error("failed to start BSocial event bridge", "error", err)
			}
		}
		if svc.OPNS != nil && svc.OPNS.Sync != nil {
			bridge := overlay.NewEventBridge(&overlay.EventBridgeConfig{
				PubSub:   svc.PubSub.PubSub,
				Store:    svc.Store.Store,
				Patterns: []string{"opns:mine"},
				QueueFunc: func(ev pubsub.Event) string {
					return string(txo.KeyQueue("opns"))
				},
				Logger:       logger,
				Engine:       svc.OPNS.Engine,
				BeefStorage:  svc.Beef.Storage,
				SubmitBuffer: 64,
			})
			if err := bridge.Start(ctx); err != nil {
				logger.Error("failed to start OPNS event bridge", "error", err)
			}
		}
		if svc.OrdLock != nil && svc.OrdLock.Sync != nil {
			bridge := overlay.NewEventBridge(&overlay.EventBridgeConfig{
				PubSub:   svc.PubSub.PubSub,
				Store:    svc.Store.Store,
				Patterns: []string{"ordlock", "spend:ordlock"},
				QueueFunc: func(ev pubsub.Event) string {
					return string(txo.KeyQueue(ordlockpkg.QueueName))
				},
				Logger:       logger,
				Engine:       svc.OrdLock.Engine,
				BeefStorage:  svc.Beef.Storage,
				SubmitBuffer: 64,
			})
			if err := bridge.Start(ctx); err != nil {
				logger.Error("failed to start OrdLock event bridge", "error", err)
			}
		}
		if svc.BSV21 != nil && svc.BSV21.Sync != nil {
			bridge := overlay.NewEventBridge(&overlay.EventBridgeConfig{
				PubSub:   svc.PubSub.PubSub,
				Store:    svc.Store.Store,
				Patterns: []string{"bsv21:*"},
				QueueFunc: func(ev pubsub.Event) string {
					tokenId := strings.TrimPrefix(ev.Topic, "bsv21:")
					if tokenId == "" {
						return ""
					}
					return "q:tm_" + tokenId
				},
				Logger:       logger,
				Engine:       svc.BSV21.Engine,
				BeefStorage:  svc.Beef.Storage,
				SubmitBuffer: 64,
			})
			if err := bridge.Start(ctx); err != nil {
				logger.Error("failed to start BSV21 event bridge", "error", err)
			}
		}
	}

	// Start the always-on arcade SSE consumer (event broker)
	if svc.ArcadeBroker != nil {
		go svc.ArcadeBroker.Run(ctx)
		logger.Info("started arcade event broker")
	}

	// Start BSV21 sync services
	if svc.BSV21 != nil && svc.BSV21.Sync != nil {
		go func() {
			if err := svc.BSV21.Sync.Start(ctx); err != nil {
				logger.Error("BSV21 sync error", "error", err)
			}
		}()
		logger.Info("started BSV21 sync services")
	}

	// Start overlay sync workers (BAP, BSocial, OPNS)
	if svc.BAP != nil && svc.BAP.Sync != nil {
		go func() {
			if err := svc.BAP.Sync.Start(ctx); err != nil {
				logger.Error("BAP sync error", "error", err)
			}
		}()
		logger.Info("started BAP overlay sync")
	}
	if svc.BSocial != nil && svc.BSocial.Sync != nil {
		go func() {
			if err := svc.BSocial.Sync.Start(ctx); err != nil {
				logger.Error("BSocial sync error", "error", err)
			}
		}()
		logger.Info("started BSocial overlay sync")
	}
	if svc.OrdLock != nil && svc.OrdLock.Sync != nil {
		go func() {
			if err := svc.OrdLock.Sync.Start(ctx); err != nil {
				logger.Error("OrdLock sync error", "error", err)
			}
		}()
		logger.Info("started OrdLock overlay sync")
	}
	if svc.OPNS != nil && svc.OPNS.Sync != nil {
		go func() {
			if err := svc.OPNS.Sync.Start(ctx); err != nil {
				logger.Error("OPNS sync error", "error", err)
			}
		}()
		logger.Info("started OPNS overlay sync")
	}
	if svc.OPNS != nil && svc.OPNS.Crawl != nil {
		go func() {
			if err := svc.OPNS.Crawl.Start(ctx); err != nil {
				logger.Error("OPNS crawl error", "error", err)
			}
		}()
		logger.Info("started OPNS genesis crawl")
	}

	// Start arcade event handlers (arcade listener + status handler)
	if svc.Indexer != nil {
		if err := svc.Indexer.StartEventHandlers(ctx); err != nil {
			logger.Error("Failed to start event handlers", "error", err)
		} else {
			logger.Info("started arcade event handlers")
		}
	}

	// Start JungleBus sync (if configured)
	if svc.Indexer != nil && svc.Indexer.Sync != nil {
		go func() {
			if err := svc.Indexer.Start(ctx); err != nil {
				logger.Error("Indexer sync error", "error", err)
			}
		}()
		logger.Info("started indexer sync")
	}
}
