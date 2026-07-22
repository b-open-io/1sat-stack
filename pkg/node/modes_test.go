package node

import "testing"

func TestApplyModesAll(t *testing.T) {
	c := &Config{Mode: "all"}
	// mode=all must not touch any service flags
	before := c.OPNS.Mode
	c.applyModes()
	if c.OPNS.Mode != before {
		t.Fatal("mode=all must not modify service config")
	}
}

func TestApplyModesExplicitList(t *testing.T) {
	c := &Config{Mode: "index,opns"}
	c.applyModes()
	if !c.runsService("index") || !c.runsService("opns") {
		t.Fatal("named services must run")
	}
	if c.runsService("bsv21") || c.runsService("gateway") {
		t.Fatal("unnamed services must not run")
	}
	// naming a service implies enablement
	if c.OPNS.Mode == "disabled" || c.OPNS.Mode == "" {
		t.Fatal("naming opns must imply enablement")
	}
}

func TestRunsServiceAllHonorsEnabledFlags(t *testing.T) {
	c := &Config{Mode: "all"}
	c.BSV21.Mode = "disabled"
	c.applyModes()
	if c.runsService("bsv21") {
		t.Fatal("mode=all runs only enabled services")
	}
}
