package node

import (
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/bap"
	"github.com/b-open-io/1sat-stack/pkg/bsocial"
	"github.com/b-open-io/1sat-stack/pkg/bsv21"
	"github.com/b-open-io/1sat-stack/pkg/indexer"
	"github.com/b-open-io/1sat-stack/pkg/opns"
	ordlockpkg "github.com/b-open-io/1sat-stack/pkg/ordlock"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/paymail"
	"github.com/b-open-io/1sat-stack/pkg/txo"
)

var serviceNames = []string{"index", "beef", "ordfs", "paymail", "bsv21", "opns", "bap", "bsocial", "ordlock", "gateway"}

func (c *Config) modeSet() map[string]bool {
	set := map[string]bool{}
	for _, m := range strings.Split(c.Mode, ",") {
		if m = strings.TrimSpace(m); m != "" {
			set[m] = true
		}
	}
	return set
}

// modeAll reports whether the config runs every enabled service. An empty
// mode is treated as "all" so programmatically-constructed configs keep the
// default behavior.
func (c *Config) modeAll() bool {
	if strings.TrimSpace(c.Mode) == "" {
		return true
	}
	return c.modeSet()["all"]
}

// applyModes runs after applyRuntimeConfig. mode=all leaves per-service
// enabled flags authoritative. An explicit list implies enablement for the
// named services and disables everything else. Substrate sections (store,
// pubsub, beef storage, chaintracks, junglebus, arcade) are untouched.
func (c *Config) applyModes() {
	if c.modeAll() {
		return
	}
	set := c.modeSet()

	if set["index"] {
		if c.Indexer.Mode == indexer.ModeDisabled || c.Indexer.Mode == "" {
			c.Indexer.Mode = indexer.ModeEmbedded
		}
		if c.TXO.Mode == txo.ModeDisabled || c.TXO.Mode == "" {
			c.TXO.Mode = txo.ModeEmbedded
		}
	} else {
		c.Indexer.Mode = indexer.ModeDisabled
	}

	c.Beef.Routes.Enabled = set["beef"]
	c.ORDFS.Enabled = set["ordfs"]

	if set["paymail"] {
		c.Paymail.Mode = paymail.ModeEnabled
	} else {
		c.Paymail.Mode = paymail.ModeDisabled
	}

	overlayNamed := false
	for name, mode := range map[string]*string{
		"bsv21":   &c.BSV21.Mode,
		"opns":    &c.OPNS.Mode,
		"bap":     &c.BAP.Mode,
		"bsocial": &c.BSocial.Mode,
		"ordlock": &c.OrdLock.Mode,
	} {
		if set[name] {
			*mode = overlay.ModeEmbedded
			overlayNamed = true
		} else {
			*mode = overlay.ModeDisabled
		}
	}
	if overlayNamed && c.Overlay.Mode != overlay.ModeEmbedded {
		c.Overlay.Mode = overlay.ModeEmbedded
	}
}

// runsService reports whether the named service runs in this process:
// mode=all defers to the service's own enabled flag; an explicit mode list
// is authoritative membership.
func (c *Config) runsService(name string) bool {
	if !c.modeAll() {
		return c.modeSet()[name]
	}
	switch name {
	case "index":
		return c.TXO.Mode != txo.ModeDisabled || c.Indexer.Mode != indexer.ModeDisabled
	case "beef":
		return c.Beef.Routes.Enabled
	case "ordfs":
		return c.ORDFS.Enabled
	case "paymail":
		return c.Paymail.Mode != paymail.ModeDisabled && c.Paymail.Mode != ""
	case "bsv21":
		return c.BSV21.Mode != bsv21.ModeDisabled
	case "opns":
		return c.OPNS.Mode != opns.ModeDisabled
	case "bap":
		return c.BAP.Mode != bap.ModeDisabled
	case "bsocial":
		return c.BSocial.Mode != bsocial.ModeDisabled
	case "ordlock":
		return c.OrdLock.Mode != ordlockpkg.ModeDisabled
	case "gateway":
		return false
	default:
		return false
	}
}
