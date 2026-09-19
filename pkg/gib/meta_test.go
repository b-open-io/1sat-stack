package gib

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestParseRepoMeta(t *testing.T) {
	m, err := ParseRepoMeta([]byte(`{"name":" gib-test ","description":"d","defaultBranch":"main","extra":1}`))
	if err != nil || m.Name != "gib-test" || m.Description != "d" || m.DefaultBranch != "main" {
		t.Fatalf("meta = %+v, %v", m, err)
	}
	for _, bad := range []string{"", "{}", "not json", `{"name":""}`} {
		if _, err := ParseRepoMeta([]byte(bad)); err == nil {
			t.Fatalf("%q parsed", bad)
		}
	}
	long, _ := ParseRepoMeta([]byte(`{"name":"` + strings.Repeat("x", 500) + `"}`))
	if len(long.Name) != maxMetaName {
		t.Fatalf("name not clipped: %d", len(long.Name))
	}
}

// Admission enriches heads with .gib from the ORIGIN tree (genesis), so a
// later commit cannot rename a repository; the repo route backfills heads
// that were indexed before their .gib was fetchable.
func TestMetaEnrichment(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	served := map[string]string{} // root -> .gib body
	f.svc.SetMetaFetcher(func(_ context.Context, root string) ([]byte, error) {
		if body, ok := served[root]; ok {
			return []byte(body), nil
		}
		return nil, errNoMeta
	})

	// The genesis tree names the repository; a later root claiming another
	// name is ignored.
	served[testOrigin] = `{"name":"gib-test","description":"desc","defaultBranch":"dev"}`
	served[testRoot2] = `{"name":"renamed"}`
	mint := f.mintTx(testOrigin, "main", testRoot1)
	f.admit(0, mint)
	head, _ := f.store.GetHead(ctx, op(mint, 0))
	if head.Meta == nil || head.Meta.Name != "gib-test" {
		t.Fatalf("genesis meta = %+v", head.Meta)
	}
	push := f.spendTx(mint, f.headScript(testOrigin, "main", testRoot2, testCommit))
	f.admit(0, mint, push)
	head, _ = f.store.GetHead(ctx, op(push, 0))
	if head.Meta == nil || head.Meta.Name != "gib-test" || head.Meta.DefaultBranch != "dev" {
		t.Fatalf("meta = %+v", head.Meta)
	}
	repo, _ = f.store.GetRepo(ctx, testOrigin)
	if repo.Name != "gib-test" || repo.Description != "desc" || repo.DefaultBranch != "dev" {
		t.Fatalf("repo = %+v", repo)
	}
	repos, _ := f.store.ListRepos(ctx, "", 0, 10, true)
	if len(repos) != 1 || repos[0].Name != "gib-test" {
		t.Fatalf("repos = %+v", repos)
	}

	// Backfill: a repo whose only head predates enrichment.
	other := f.mintTx(testOrigin2, "main", testRoot1)
	f.admit(0, other)
	served[testOrigin2] = `{"name":"late"}`
	routes := NewRoutes(f.store, nil)
	routes.SetMetaFiller(f.svc.FillMeta)
	app := fiber.New()
	routes.Register(app.Group("/gib"))
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/gib/repo/"+testOrigin2, nil))
	if err != nil {
		t.Fatal(err)
	}
	var body RepoResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Name != "late" || len(body.HeadsList) != 1 || body.HeadsList[0].Meta == nil {
		t.Fatalf("backfill: %+v", body)
	}
	stored, _ := f.store.GetHead(ctx, op(other, 0))
	if stored.Meta == nil || stored.Meta.Name != "late" {
		t.Fatalf("backfill not persisted: %+v", stored.Meta)
	}
}

func TestHTTPMetaFetcher(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/content/" + testRoot1 + "/.gib":
			_, _ = w.Write([]byte(`{"name":"x"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	fetch := HTTPMetaFetcher(srv.URL, nil)
	b, err := fetch(t.Context(), testRoot1)
	if err != nil || string(b) != `{"name":"x"}` {
		t.Fatalf("fetch = %q, %v", b, err)
	}
	if _, err := fetch(t.Context(), testRoot2); !errors.Is(err, errNoMeta) {
		t.Fatalf("missing .gib err = %v", err)
	}
}
