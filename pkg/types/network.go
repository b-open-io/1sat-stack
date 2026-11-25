package types

import "strings"

type Network byte

var (
	Mainnet Network = 0
	Testnet Network = 0x6f
)

// ParseNetwork accepts the spellings used in stack config ("main"/"test")
// and BSV21 config ("mainnet"/"testnet"). Anything else is mainnet.
func ParseNetwork(s string) Network {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "test", "testnet":
		return Testnet
	default:
		return Mainnet
	}
}

func (n Network) Mainnet() bool {
	return n != Testnet
}
