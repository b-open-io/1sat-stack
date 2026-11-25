package admin

import (
	"strconv"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/config"
)

func TestValidateEcosystemAliasConfigUpdatesBoundaries(t *testing.T) {
	for _, value := range []int{config.EcosystemAliasMinConcurrency, config.EcosystemAliasMaxConcurrency} {
		if err := validateEcosystemAliasConfigUpdates(map[string]string{
			"overlay.ecosystemalias.concurrency": strconv.Itoa(value),
		}); err != nil {
			t.Fatalf("concurrency %d: %v", value, err)
		}
	}
	for _, value := range []int{config.EcosystemAliasMinBatchSize, config.EcosystemAliasMaxBatchSize} {
		if err := validateEcosystemAliasConfigUpdates(map[string]string{
			"overlay.ecosystemalias.batch_size": strconv.Itoa(value),
		}); err != nil {
			t.Fatalf("batch size %d: %v", value, err)
		}
	}
	for _, updates := range []map[string]string{
		{"overlay.ecosystemalias.enabled": "yes"},
		{"overlay.ecosystemalias.sync_enabled": ""},
		{"overlay.ecosystemalias.routes_enabled": "FALSE"},
		{"overlay.ecosystemalias.concurrency": "0"},
		{"overlay.ecosystemalias.concurrency": "1.5"},
		{"overlay.ecosystemalias.batch_size": "10001"},
		{"overlay.ecosystemalias.batch_size": ""},
	} {
		if err := validateEcosystemAliasConfigUpdates(updates); err == nil {
			t.Fatalf("accepted invalid updates: %#v", updates)
		}
	}
}

func TestValidateEcosystemAliasConfigUpdatesNormalizesRoutePrefix(t *testing.T) {
	updates := map[string]string{"overlay.ecosystemalias.route_prefix": "/identity/"}
	if err := validateEcosystemAliasConfigUpdates(updates); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := updates["overlay.ecosystemalias.route_prefix"]; got != "/identity" {
		t.Fatalf("normalized route = %q, want /identity", got)
	}

	for _, prefix := range []string{"", "/", "identity", "/identity path", "/identity?x=1", "/identity#fragment"} {
		if err := validateEcosystemAliasConfigUpdates(map[string]string{
			"overlay.ecosystemalias.route_prefix": prefix,
		}); err == nil {
			t.Fatalf("accepted invalid route prefix %q", prefix)
		}
	}
}
