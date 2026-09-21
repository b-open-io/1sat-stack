package gib

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func routesApp(t *testing.T, f *fixture) *fiber.App {
	t.Helper()
	app := fiber.New()
	NewRoutes(f.store, nil).Register(app.Group("/gib"))
	return app
}

func get(t *testing.T, app *fiber.App, path string, into any) int {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if into != nil && resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, into); err != nil {
			t.Fatalf("decode %s: %v: %s", path, err, body)
		}
	}
	return resp.StatusCode
}

func TestRoutes(t *testing.T) {
	f := newFixture(t)
	first := publish(t, 0x71, []string{testCommit}, nil, testCommit)
	second := commitObject("second", sha(testCommit))
	next := publish(t, 0x72, []string{second}, map[string]string{sha(testCommit): first.objects[sha(testCommit)]}, second)

	mint := f.mintTx(testOrigin, "main", first.root)
	f.admit(0, first.tx, mint)
	push := f.spendTx(mint, f.headScript(testOrigin, "main", next.root, ""))
	f.admit(0, next.tx, mint, push)
	feature := f.mintTx(testOrigin, "feature/x", first.root)
	f.admit(0, first.tx, feature)
	app := routesApp(t, f)

	var repos []RepoRecord
	if code := get(t, app, "/gib/repos", &repos); code != 200 || len(repos) != 1 || repos[0].Branches != 2 {
		t.Fatalf("repos: %d %+v", code, repos)
	}
	if code := get(t, app, "/gib/identity/"+f.identityHex()+"/repos", &repos); code != 200 || len(repos) != 1 {
		t.Fatalf("identity repos: %d %+v", code, repos)
	}
	if code := get(t, app, "/gib/identity/zz/repos", nil); code != 400 {
		t.Fatalf("bad identity: %d", code)
	}

	var repo RepoResponse
	dotOrigin := strings.Replace(testOrigin, "_", ".", 1)
	if code := get(t, app, "/gib/repo/"+dotOrigin, &repo); code != 200 || repo.Origin != testOrigin || len(repo.HeadsList) != 2 {
		t.Fatalf("repo: %d %+v", code, repo)
	}
	if code := get(t, app, "/gib/repo/"+testRoot2, nil); code != 404 {
		t.Fatalf("unknown repo: %d", code)
	}
	if code := get(t, app, "/gib/repo/nope", nil); code != 400 {
		t.Fatalf("bad origin: %d", code)
	}

	var branches []HeadRecord
	if code := get(t, app, "/gib/repo/"+testOrigin+"/branches", &branches); code != 200 || len(branches) != 2 {
		t.Fatalf("branches: %d %+v", code, branches)
	}

	var branch BranchResponse
	if code := get(t, app, "/gib/repo/"+testOrigin+"/branch/feature/x", &branch); code != 200 ||
		branch.Head == nil || branch.Head.Outpoint != op(feature, 0) || len(branch.History) != 1 {
		t.Fatalf("feature branch: %d %+v", code, branch)
	}
	if code := get(t, app, "/gib/repo/"+testOrigin+"/branch/main", &branch); code != 200 ||
		branch.Head == nil || branch.Head.Outpoint != op(push, 0) || len(branch.History) != 2 {
		t.Fatalf("main branch: %d %+v", code, branch)
	}

	var heads []HeadRecord
	if code := get(t, app, "/gib/heads?unspent=true", &heads); code != 200 || len(heads) != 2 {
		t.Fatalf("unspent heads: %d %+v", code, heads)
	}
	if code := get(t, app, "/gib/heads?origin="+testOrigin+"&branch=main", &heads); code != 200 || len(heads) != 2 {
		t.Fatalf("main heads: %d %+v", code, heads)
	}
	if code := get(t, app, "/gib/heads?from=abc", nil); code != 400 {
		t.Fatalf("bad from: %d", code)
	}

	var head HeadRecord
	if code := get(t, app, "/gib/head/"+op(mint, 0), &head); code != 200 || head.Spend == nil || head.Spend.Next != op(push, 0) {
		t.Fatalf("head: %d %+v", code, head)
	}
	if code := get(t, app, "/gib/head/"+testRoot2, nil); code != 404 {
		t.Fatalf("unknown head: %d", code)
	}
}
