package gib

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

// A commit is a DAG node: it may be published by several heads (a fork
// reuses the forked commit verbatim) and reached from its parents' heads
// regardless of which repository they live in. The commits themselves come
// from each root's `.git` object store, not from the heads.
func TestCommitDAGAcrossRepos(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	second := commitObject("second", sha(testCommit))

	// Upstream: mint (commit 1) then push (commit 2, parent = commit 1).
	first := publish(t, 0x31, []string{testCommit}, nil, testCommit)
	mint := f.mintTx(testOrigin, "main", first.root)
	f.admit(0, first.tx, mint)

	next := publish(t, 0x32, []string{second}, map[string]string{sha(testCommit): first.objects[sha(testCommit)]}, second)
	push := f.spendTx(mint, f.headScript(testOrigin, "main", next.root, ""))
	f.admit(0, next.tx, mint, push)

	// Fork: a new origin whose head republishes commit 2 verbatim, naming
	// the head it forked from.
	forkContent := publish(t, 0x33, []string{testCommit, second}, nil, second)
	fork := transaction.NewTransaction()
	fork.AddOutput(&transaction.TransactionOutput{
		Satoshis:      1,
		LockingScript: f.headScript(testOrigin2, "main", forkContent.root, op(push, 0)),
	})
	f.admit(0, forkContent.tx, next.tx, mint, push, fork)

	forked, err := f.store.GetHead(ctx, op(fork, 0))
	if err != nil {
		t.Fatal(err)
	}
	if forked.BranchedFrom != op(push, 0) {
		t.Fatalf("fork branched from %q, want %s", forked.BranchedFrom, op(push, 0))
	}

	byFirst, err := f.store.ListHeads(ctx, HeadFilter{CommitSha: sha(testCommit), Rev: true})
	if err != nil || len(byFirst) != 1 || byFirst[0].Outpoint != op(mint, 0) {
		t.Fatalf("heads for commit 1 = %+v, %v", byFirst, err)
	}
	bySecond, err := f.store.ListHeads(ctx, HeadFilter{CommitSha: sha(second), Rev: true})
	if err != nil || len(bySecond) != 2 {
		t.Fatalf("heads for commit 2 = %+v, %v", bySecond, err)
	}
	children, err := f.store.ChildrenOfCommit(ctx, sha(testCommit), 10)
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
	if none, _ := f.store.ChildrenOfCommit(ctx, sha(second), 10); len(none) != 0 {
		t.Fatalf("commit 2 has children %+v", none)
	}

	app := fiber.New()
	NewRoutes(f.store, nil).Register(app.Group("/gib"))
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/gib/commit/"+sha(testCommit), nil))
	if err != nil {
		t.Fatal(err)
	}
	var body CommitResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || body.Sha != sha(testCommit) || len(body.Heads) != 1 || len(body.Children) != 2 {
		t.Fatalf("commit route: %d %+v", resp.StatusCode, body)
	}
	// The commit object itself is served from the object store that
	// published it, whether or not a head's tip it ever was.
	if body.Commit == nil || body.Commit.Message != "first\n" || !body.Held ||
		body.Ref != first.objects[sha(testCommit)] {
		t.Fatalf("commit route body = %+v ref=%q held=%v", body.Commit, body.Ref, body.Held)
	}
	if r, _ := app.Test(httptest.NewRequest(http.MethodGet, "/gib/commit/"+strings.Repeat("0", 40), nil)); r.StatusCode != 404 {
		t.Fatalf("unknown sha: %d", r.StatusCode)
	}
	if r, _ := app.Test(httptest.NewRequest(http.MethodGet, "/gib/commit/nope", nil)); r.StatusCode != 400 {
		t.Fatalf("bad sha: %d", r.StatusCode)
	}
	if r, _ := app.Test(httptest.NewRequest(http.MethodGet, "/gib/heads?sha="+sha(second), nil)); r.StatusCode != 200 {
		t.Fatalf("heads by sha: %d", r.StatusCode)
	}

	// Eviction drops the parent edges too.
	if err := f.store.DeleteHead(ctx, op(push, 0)); err != nil {
		t.Fatal(err)
	}
	if left, _ := f.store.ChildrenOfCommit(ctx, sha(testCommit), 10); len(left) != 1 {
		t.Fatalf("children after eviction = %+v", left)
	}
}
