package gib

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RepoMeta is the committed `.gib` file in the repository's GENESIS tree:
// display metadata and the default branch. It is read from the origin
// outpoint only, so it is fixed for the life of the repository (renaming
// means a new origin); edits in later commits are ignored by indexers.
type RepoMeta struct {
	Name          string `json:"name,omitempty"`
	Description   string `json:"description,omitempty"`
	DefaultBranch string `json:"defaultBranch,omitempty"`
}

// MetaFile is the filename at the tree root.
const MetaFile = ".gib"

const (
	maxMetaBytes   = 64 * 1024
	maxMetaName    = 200
	maxMetaDesc    = 2000
	maxMetaBranch  = 255
	metaFetchLimit = 5 * time.Second
)

var errNoMeta = errors.New("gib: no .gib file")

// ParseRepoMeta decodes a `.gib` file. Unknown keys are ignored; over-long
// values are truncated rather than rejected so a sloppy file still names
// the repository.
func ParseRepoMeta(b []byte) (*RepoMeta, error) {
	if len(b) == 0 {
		return nil, errNoMeta
	}
	if len(b) > maxMetaBytes {
		return nil, fmt.Errorf("gib: .gib larger than %d bytes", maxMetaBytes)
	}
	var m RepoMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("gib: .gib is not valid JSON: %w", err)
	}
	m.Name = clip(strings.TrimSpace(m.Name), maxMetaName)
	m.Description = clip(strings.TrimSpace(m.Description), maxMetaDesc)
	m.DefaultBranch = clip(strings.TrimSpace(m.DefaultBranch), maxMetaBranch)
	if m.Name == "" && m.Description == "" && m.DefaultBranch == "" {
		return nil, errNoMeta
	}
	return &m, nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// MetaFetcher returns the raw `.gib` bytes for a tree root (txid_vout), or
// an error when the tree has none or it cannot be read yet. Callers pass
// the repository origin, never a later root.
type MetaFetcher func(ctx context.Context, root string) ([]byte, error)

// HTTPMetaFetcher reads `.gib` through an ORDFS gateway's content route,
// which walks the manifest and applies any patch chain. baseURL is the
// gateway origin (e.g. http://127.0.0.1:8080).
func HTTPMetaFetcher(baseURL string, client *http.Client) MetaFetcher {
	if client == nil {
		client = &http.Client{Timeout: metaFetchLimit}
	}
	base := strings.TrimRight(baseURL, "/")
	return func(ctx context.Context, root string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, metaFetchLimit)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/content/"+root+"/"+MetaFile, nil)
		if err != nil {
			return nil, err
		}
		res, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		if res.StatusCode == http.StatusNotFound {
			return nil, errNoMeta
		}
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("gib: fetch .gib: %s", res.Status)
		}
		return io.ReadAll(io.LimitReader(res.Body, maxMetaBytes+1))
	}
}

// fetchMeta is best-effort: any failure yields nil.
func (l *LookupService) fetchMeta(ctx context.Context, root string) *RepoMeta {
	if l.meta == nil {
		return nil
	}
	b, err := l.meta(ctx, root)
	if err != nil {
		if !errors.Is(err, errNoMeta) {
			l.logger.Debug("gib: .gib not available", "root", root, "error", err)
		}
		return nil
	}
	m, err := ParseRepoMeta(b)
	if err != nil {
		l.logger.Debug("gib: .gib unreadable", "root", root, "error", err)
		return nil
	}
	return m
}
