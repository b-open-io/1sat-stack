package jbsync

import "github.com/spf13/viper"

// SubscriberConfig holds configuration for a JungleBus topic subscriber
type SubscriberConfig struct {
	// SubscriptionID is the JungleBus subscription/topic ID (used for progress tracking)
	SubscriptionID string `mapstructure:"subscription_id"`

	// QueueName is the name of the queue to populate (e.g., "bsv21" → q:bsv21)
	// If empty, defaults to SubscriptionID
	QueueName string `mapstructure:"queue_name"`

	// FromBlock is the minimum starting block height for this subscription
	// If stored progress is ahead, resume from progress; otherwise start from FromBlock
	FromBlock uint64 `mapstructure:"from_block"`

	// BatchSize is the number of transactions per batch write (default: 1000)
	BatchSize int `mapstructure:"batch_size"`

	// ReorgDepth is the number of blocks to wait before confirming progress (default: 6)
	ReorgDepth uint32 `mapstructure:"reorg_depth"`

	// EnableMempool enables subscription to mempool transactions
	EnableMempool bool `mapstructure:"enable_mempool"`
}

// SetDefaults sets default values for SubscriberConfig
func (c *SubscriberConfig) SetDefaults(v *viper.Viper, prefix string) {
	v.SetDefault(prefix+".batch_size", 1000)
	v.SetDefault(prefix+".reorg_depth", 6)
	v.SetDefault(prefix+".enable_mempool", false)
}

// GetQueueName returns the queue name, defaulting to subscription ID if empty
func (c *SubscriberConfig) GetQueueName() string {
	if c.QueueName != "" {
		return c.QueueName
	}
	return c.SubscriptionID
}
