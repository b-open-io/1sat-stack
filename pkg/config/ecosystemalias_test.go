package config

import "testing"

func TestEcosystemAliasBounds(t *testing.T) {
	for _, test := range []struct {
		name     string
		validate func(int) error
		min      int
		max      int
	}{
		{name: "concurrency", validate: ValidateEcosystemAliasConcurrency, min: EcosystemAliasMinConcurrency, max: EcosystemAliasMaxConcurrency},
		{name: "batch size", validate: ValidateEcosystemAliasBatchSize, min: EcosystemAliasMinBatchSize, max: EcosystemAliasMaxBatchSize},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, value := range []int{test.min, test.max} {
				if err := test.validate(value); err != nil {
					t.Errorf("valid boundary %d: %v", value, err)
				}
			}
			for _, value := range []int{test.min - 1, test.max + 1} {
				if err := test.validate(value); err == nil {
					t.Errorf("invalid boundary %d accepted", value)
				}
			}
		})
	}
}

func TestParseEcosystemAliasBoundedIntRejectsInvalidInput(t *testing.T) {
	for _, raw := range []string{"", "0", "-1", "1.5", "eight", " 8"} {
		if _, err := ParseEcosystemAliasBoundedInt("concurrency", raw, EcosystemAliasMinConcurrency, EcosystemAliasMaxConcurrency); err == nil {
			t.Errorf("ParseEcosystemAliasBoundedInt(%q) succeeded", raw)
		}
	}
}

func TestNormalizeEcosystemAliasRoutePrefix(t *testing.T) {
	for input, want := range map[string]string{
		"/ecosystemalias":  "/ecosystemalias",
		"/identity/alias/": "/identity/alias",
	} {
		got, err := NormalizeEcosystemAliasRoutePrefix(input)
		if err != nil {
			t.Errorf("NormalizeEcosystemAliasRoutePrefix(%q): %v", input, err)
		} else if got != want {
			t.Errorf("NormalizeEcosystemAliasRoutePrefix(%q) = %q, want %q", input, got, want)
		}
	}

	for _, input := range []string{"", "/", "identity", " /identity", "/iden tity", "/identity?x=1", "/identity#docs", "/identity//alias", "/identity/../alias", "/:alias", "/*", "/%2f", `/alias\path`} {
		if _, err := NormalizeEcosystemAliasRoutePrefix(input); err == nil {
			t.Errorf("NormalizeEcosystemAliasRoutePrefix(%q) succeeded", input)
		}
	}
}

func TestEcosystemAliasLookupPath(t *testing.T) {
	got, err := EcosystemAliasLookupPath("/1sat/", "/identity/")
	if err != nil {
		t.Fatalf("EcosystemAliasLookupPath: %v", err)
	}
	if got != "/1sat/identity/overlay/lookup" {
		t.Fatalf("lookup path = %q", got)
	}
}
