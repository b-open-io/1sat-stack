package collection

// Topic name constants and helpers.

const (
	// DiscoveryTopic is the global topic for collection mints.
	DiscoveryTopic = "tm_1sat_collection"

	// LookupName is the engine lookup service key for this module.
	LookupName = "collection"

	// itemTopicPrefix prefixes per-collection item topics.
	itemTopicPrefix = "tm_col_"
)

// ItemTopic returns the per-collection topic name for a collectionId outpoint.
func ItemTopic(collectionID string) string {
	return itemTopicPrefix + collectionID
}

// CollectionIDFromTopic extracts the collectionId from an item topic name.
// Returns empty string if topic is not an item topic.
func CollectionIDFromTopic(topic string) string {
	if len(topic) <= len(itemTopicPrefix) || topic[:len(itemTopicPrefix)] != itemTopicPrefix {
		return ""
	}
	return topic[len(itemTopicPrefix):]
}

// IsDiscoveryTopic reports whether topic is the collection discovery topic.
func IsDiscoveryTopic(topic string) bool {
	return topic == DiscoveryTopic
}

// IsItemTopic reports whether topic is a per-collection item topic.
func IsItemTopic(topic string) bool {
	return CollectionIDFromTopic(topic) != ""
}
