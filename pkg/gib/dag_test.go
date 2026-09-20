package gib

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gibtpl "github.com/b-open-io/1sat-stack/pkg/template/gib"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

// A commit is a DAG node: it may be published by several heads (a fork
// reuses the forked commit verbatim) and reached from its parents' heads
// regardless of which repository they live in.
func TestCommitDAGAcrossRepos(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	first, err := gibtpl.ParseCommit([]byte(testCommit))
	if err != nil {
		t.Fatal(err)
	}
	second := strings.Replace(testCommit, "author A", "parent "+first.SHA+"\nauthor A", 1)
	secondSha := gibtpl.ObjectID("commit", []byte(second))

	// Upstream: mint (commit 1) then push (commit 2, parent = commit 1).
	mint := f.mintTx(testOrigin, "main", testRoot1)
	f.admit(0, mint)
	push := f.spendTx(mint, f.headScript(testOrigin, "main", testRoot2, second))
	f.admit(0, mint, push)

	// Fork: a new origin whose head republishes commit 2 verbatim.
	fork := transaction.NewTransaction()
	fork.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: f.headScript(testOrigin2, "main", testRoot2, second)})
	f.admit(0, fork)

	byFirst, err := f.store.ListHeads(ctx, HeadFilter{CommitSha: first.SHA, Rev: true})
	if err != nil || len(byFirst) != 1 || byFirst[0].Outpoint != op(mint, 0) {
		t.Fatalf("heads for commit 1 = %+v, %v", byFirst, err)
	}
	bySecond, err := f.store.ListHeads(ctx, HeadFilter{CommitSha: secondSha, Rev: true})
	if err != nil || len(bySecond) != 2 {
		t.Fatalf("heads for commit 2 = %+v, %v", bySecond, err)
	}
	children, err := f.store.ChildrenOfCommit(ctx, first.SHA, 10)
	if err != nil || len(children) != 2 {
		t.Fatalf("children of commit 1 = %+v, %v", children, err)
	}
	origins := map[string]bool{}
	for _, c := range children {
		origins[c.Origin] = true
	}
	if !origins[testOrigin] || !origins[testOrigin2] {
		t.Fatalf("children should span both repos: %v", origins)
	}
	if none, _ := f.store.ChildrenOfCommit(ctx, secondSha, 10); len(none) != 0 {
		t.Fatalf("commit 2 has children %+v", none)
	}

	app := fiber.New()
	NewRoutes(f.store, nil).Register(app.Group("/gib"))
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/gib/commit/"+first.SHA, nil))
	if err != nil {
		t.Fatal(err)
	}
	var body CommitResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || body.Sha != first.SHA || len(body.Heads) != 1 || len(body.Children) != 2 {
		t.Fatalf("commit route: %d %+v", resp.StatusCode, body)
	}
	if r, _ := app.Test(httptest.NewRequest(http.MethodGet, "/gib/commit/"+strings.Repeat("0", 40), nil)); r.StatusCode != 404 {
		t.Fatalf("unknown sha: %d", r.StatusCode)
	}
	if r, _ := app.Test(httptest.NewRequest(http.MethodGet, "/gib/commit/nope", nil)); r.StatusCode != 400 {
		t.Fatalf("bad sha: %d", r.StatusCode)
	}
	if r, _ := app.Test(httptest.NewRequest(http.MethodGet, "/gib/heads?sha="+secondSha, nil)); r.StatusCode != 200 {
		t.Fatalf("heads by sha: %d", r.StatusCode)
	}

	// Eviction drops the parent edges too.
	if err := f.store.DeleteHead(ctx, op(push, 0)); err != nil {
		t.Fatal(err)
	}
	if left, _ := f.store.ChildrenOfCommit(ctx, first.SHA, 10); len(left) != 1 {
		t.Fatalf("children after eviction = %+v", left)
	}
}
