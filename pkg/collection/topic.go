package collection

// Topic name constants and helpers.

const (
	// DiscoveryTopic is the global topic for collection root mints.
	DiscoveryTopic = "tm_1sat_collection"

	// LookupName is the engine lookup service key for this module.
	LookupName = "collection"

	// memberTopicPrefix prefixes per-collection member topics.
	memberTopicPrefix = "tm_col_"
)

// MemberTopic returns the per-collection topic name for a collectionId outpoint.
func MemberTopic(collectionID string) string {
	return memberTopicPrefix + collectionID
}

// CollectionIDFromTopic extracts the collectionId from a member topic name.
// Returns empty string if topic is not a member topic.
func CollectionIDFromTopic(topic string) string {
	if len(topic) <= len(memberTopicPrefix) || topic[:len(memberTopicPrefix)] != memberTopicPrefix {
		return ""
	}
	return topic[len(memberTopicPrefix):]
}

// IsDiscoveryTopic reports whether topic is the collection discovery topic.
func IsDiscoveryTopic(topic string) bool {
	return topic == DiscoveryTopic
}

// IsMemberTopic reports whether topic is a per-collection member topic.
func IsMemberTopic(topic string) bool {
	return CollectionIDFromTopic(topic) != ""
}
