package architecture

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestEcosystemAliasDocsDescribeTheShippedContract(t *testing.T) {
	files := []string{
		"ECOSYSTEM_ALIAS_OVERLAY.md",
		filepath.Join("..", "..", "pkg", "ecosystemalias", "README.md"),
	}
	required := []string{
		"tm_ecosystemalias",
		"ls_ecosystemalias",
		"POST /1sat/ecosystemalias/overlay/lookup",
		`"type": "output-list"`,
		"does not fetch",
		"SHIP/SLAP",
	}

	for _, path := range files {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(contents)
		for _, term := range required {
			if !strings.Contains(text, term) {
				t.Errorf("%s does not document %q", path, term)
			}
		}
	}
}

func TestEcosystemAliasDocsLocalMarkdownLinksResolve(t *testing.T) {
	files := []string{
		"ECOSYSTEM_ALIAS_OVERLAY.md",
		filepath.Join("..", "..", "pkg", "ecosystemalias", "README.md"),
	}
	linkPattern := regexp.MustCompile(`\[[^]]+\]\(([^)#]+\.md)(?:#[^)]+)?\)`)

	for _, path := range files {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, match := range linkPattern.FindAllStringSubmatch(string(contents), -1) {
			if strings.Contains(match[1], "://") {
				continue
			}
			target := filepath.Clean(filepath.Join(filepath.Dir(path), match[1]))
			if _, err := os.Stat(target); err != nil {
				t.Errorf("%s link %q does not resolve: %v", path, match[1], err)
			}
		}
	}
}
