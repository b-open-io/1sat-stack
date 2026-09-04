package ecosystemalias

import (
	"context"
	"database/sql"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/mattn/go-sqlite3"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func storeHash(t *testing.T, digit byte) chainhash.Hash {
	t.Helper()
	hash, err := chainhash.NewHashFromHex(strings.Repeat(string(digit), 64))
	if err != nil {
		t.Fatal(err)
	}
	return *hash
}

func storeClaim(t *testing.T, digit byte, vout uint32, alias, domain string, confirmed bool, height uint32, index uint64) StoredClaim {
	t.Helper()
	return StoredClaim{
		Outpoint: transaction.Outpoint{
			Txid:  storeHash(t, digit),
			Index: vout,
		},
		Alias:       alias,
		Domain:      domain,
		Confirmed:   confirmed,
		BlockHeight: height,
		BlockIndex:  index,
	}
}

func openSQLiteClaimStore(t *testing.T, path string) (*SQLStore, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return NewSQLStore(db, 0), db
}

type claimStoreOpener func(t *testing.T) (*SQLStore, func())

func TestSQLStoreContract(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		runClaimStoreContract(t, func(t *testing.T) (*SQLStore, func()) {
			store, db := openSQLiteClaimStore(t, filepath.Join(t.TempDir(), "claims.db"))
			return store, func() { _ = db.Close() }
		})
	})

	t.Run("postgres", func(t *testing.T) {
		if os.Getenv("SKIP_POSTGRES_TESTS") == "1" {
			t.Skip("SKIP_POSTGRES_TESTS=1")
		}
		db, cleanup := openPostgresClaimTestDB(t)
		defer cleanup()

		topicID := 100
		runClaimStoreContract(t, func(t *testing.T) (*SQLStore, func()) {
			topicID++
			return NewSQLStore(db, topicID), func() {}
		})
		testPostgresTopicIsolation(t, db)
		testPostgresTextCollation(t, db)
	})
}

func openPostgresClaimTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	ctx := context.Background()
	container, err := postgrescontainer.Run(ctx, "postgres:16-alpine",
		postgrescontainer.WithDatabase("test_ecosystem_alias"),
		postgrescontainer.WithUsername("test"),
		postgrescontainer.WithPassword("test"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp")),
	)
	if err != nil {
		t.Skipf("failed to start PostgreSQL container: %v", err)
	}
	connString, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", connString)
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatal(err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		_ = container.Terminate(ctx)
		t.Fatal(err)
	}
	return db, func() {
		_ = db.Close()
		_ = container.Terminate(ctx)
	}
}

func runClaimStoreContract(t *testing.T, open claimStoreOpener) {
	t.Helper()
	t.Run("ordering-conflicts-and-keyset", func(t *testing.T) {
		store, cleanup := open(t)
		defer cleanup()
		testClaimStoreQueryOrderingConflictsAndCursors(t, store)
	})
	t.Run("lifecycle", func(t *testing.T) {
		store, cleanup := open(t)
		defer cleanup()
		testClaimStoreSpendReplayRollbackPlacementAndEviction(t, store)
	})
}

func aliasStoreQuery(value string) Query {
	return Query{Alias: &value}
}

func domainStoreQuery(value string) Query {
	return Query{Domain: &value}
}

func findAllStoreQuery() Query {
	value := true
	return Query{FindAll: &value}
}

func claimOutpointStrings(claims []StoredClaim) []string {
	result := make([]string, len(claims))
	for i := range claims {
		result[i] = claims[i].Outpoint.String()
	}
	return result
}

func testClaimStoreQueryOrderingConflictsAndCursors(t *testing.T, store *SQLStore) {
	t.Helper()
	ctx := t.Context()

	claims := []StoredClaim{
		storeClaim(t, '3', 0, "alice", "example.com", true, 2, 3),
		storeClaim(t, '2', 0, "alice", "example.com", true, 1, 9),
		storeClaim(t, '4', 0, "alice", "example.com", true, 1, 1),
		// Mempool placement coordinates are intentionally non-zero. They must
		// not affect ordering because Confirmed is the source of truth.
		storeClaim(t, '1', 1, "alice", "example.com", false, 999, 99),
		storeClaim(t, '0', 2, "alice", "example.com", false, 1000, 100),
		storeClaim(t, '5', 0, "bob", "example.net", true, 1, 0),
	}
	for i := range claims {
		if err := store.UpsertClaim(ctx, &claims[i]); err != nil {
			t.Fatal(err)
		}
	}

	wantLookup := []string{
		claims[2].Outpoint.String(),
		claims[1].Outpoint.String(),
		claims[0].Outpoint.String(),
		claims[4].Outpoint.String(),
		claims[3].Outpoint.String(),
	}
	for name, query := range map[string]Query{
		"alias":  aliasStoreQuery("ALICE"),
		"domain": domainStoreQuery("EXAMPLE.COM"),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := store.QueryClaims(ctx, query, nil, 20)
			if err != nil {
				t.Fatal(err)
			}
			if outpoints := claimOutpointStrings(got); !reflect.DeepEqual(outpoints, wantLookup) {
				t.Fatalf("outpoints\n got %v\nwant %v", outpoints, wantLookup)
			}
		})
	}

	all, err := store.QueryClaims(ctx, findAllStoreQuery(), nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	wantAll := []string{
		claims[4].Outpoint.String(), claims[3].Outpoint.String(),
		claims[1].Outpoint.String(), claims[0].Outpoint.String(),
		claims[2].Outpoint.String(), claims[5].Outpoint.String(),
	}
	if outpoints := claimOutpointStrings(all); !reflect.DeepEqual(outpoints, wantAll) {
		t.Fatalf("findAll outpoints\n got %v\nwant %v", outpoints, wantAll)
	}

	query := aliasStoreQuery("alice")
	cursorText, err := NewCursor(query, claims[1].Outpoint.Txid.String(), claims[1].Outpoint.Index)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := BindCursor(cursorText, query)
	if err != nil {
		t.Fatal(err)
	}
	after, err := store.QueryClaims(ctx, query, &cursor, 20)
	if err != nil {
		t.Fatal(err)
	}
	if outpoints := claimOutpointStrings(after); !reflect.DeepEqual(outpoints, wantLookup[2:]) {
		t.Fatalf("cursor outpoints\n got %v\nwant %v", outpoints, wantLookup[2:])
	}

	findAll := findAllStoreQuery()
	findAllCursorText, err := NewCursor(findAll, claims[1].Outpoint.Txid.String(), claims[1].Outpoint.Index)
	if err != nil {
		t.Fatal(err)
	}
	findAllCursor, err := BindCursor(findAllCursorText, findAll)
	if err != nil {
		t.Fatal(err)
	}
	after, err = store.QueryClaims(ctx, findAll, &findAllCursor, 20)
	if err != nil {
		t.Fatal(err)
	}
	if outpoints := claimOutpointStrings(after); !reflect.DeepEqual(outpoints, wantAll[3:]) {
		t.Fatalf("findAll cursor outpoints\n got %v\nwant %v", outpoints, wantAll[3:])
	}

	mempoolCursorText, err := NewCursor(query, claims[4].Outpoint.Txid.String(), claims[4].Outpoint.Index)
	if err != nil {
		t.Fatal(err)
	}
	mempoolCursor, err := BindCursor(mempoolCursorText, query)
	if err != nil {
		t.Fatal(err)
	}
	after, err = store.QueryClaims(ctx, query, &mempoolCursor, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := claimOutpointStrings(after), []string{claims[3].Outpoint.String()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("mempool cursor outpoints\n got %v\nwant %v", got, want)
	}
}

func testClaimStoreSpendReplayRollbackPlacementAndEviction(t *testing.T, store *SQLStore) {
	t.Helper()
	ctx := t.Context()

	original := storeClaim(t, 'a', 1, "alice", "example.com", false, 0, 0)
	spendingTxID := storeHash(t, 'b')
	createdBySpender := storeClaim(t, 'b', 0, "bob", "example.net", false, 0, 0)
	if err := store.UpsertClaim(ctx, &original); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkSpent(ctx, &original.Outpoint, &spendingTxID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertClaim(ctx, &createdBySpender); err != nil {
		t.Fatal(err)
	}

	// Admission replay refreshes placement but cannot clear the spender.
	replay := original
	replay.Confirmed = true
	replay.BlockHeight = 800_000
	replay.BlockIndex = 42
	if err := store.UpsertClaim(ctx, &replay); err != nil {
		t.Fatal(err)
	}
	claims, err := store.QueryClaims(ctx, aliasStoreQuery("alice"), nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("replayed spent claim became queryable: %v", claimOutpointStrings(claims))
	}
	placement, err := store.PlacementForOutpoint(ctx, &original.Outpoint)
	if err != nil {
		t.Fatal(err)
	}
	if placement == nil || !placement.Confirmed || placement.BlockHeight != 800_000 || placement.BlockIndex != 42 {
		t.Fatalf("replay placement = %+v", placement)
	}

	if err := store.RollbackTransaction(ctx, &spendingTxID); err != nil {
		t.Fatal(err)
	}
	claims, err = store.QueryClaims(ctx, findAllStoreQuery(), nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := claimOutpointStrings(claims), []string{original.Outpoint.String()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rollback claims got %v want %v", got, want)
	}
	placement, err = store.PlacementForOutpoint(ctx, &createdBySpender.Outpoint)
	if err != nil {
		t.Fatal(err)
	}
	if placement != nil {
		t.Fatalf("claim created by rolled-back transaction remains: %+v", placement)
	}

	if err := store.UpdatePlacementByTxid(ctx, &original.Outpoint.Txid, false, 0, math.MaxUint64); err != nil {
		t.Fatal(err)
	}
	placement, err = store.PlacementForOutpoint(ctx, &original.Outpoint)
	if err != nil {
		t.Fatal(err)
	}
	if placement == nil || placement.Confirmed || placement.BlockIndex != math.MaxUint64 {
		t.Fatalf("updated placement = %+v", placement)
	}

	if err := store.DeleteOutpoint(ctx, &original.Outpoint); err != nil {
		t.Fatal(err)
	}
	placement, err = store.PlacementForOutpoint(ctx, &original.Outpoint)
	if err != nil {
		t.Fatal(err)
	}
	if placement != nil {
		t.Fatalf("evicted placement = %+v", placement)
	}
}

func TestSQLStoreSQLiteCloseAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claims.db")
	store, db := openSQLiteClaimStore(t, path)
	claim := storeClaim(t, 'c', 7, "carol", "example.org", true, 810_000, 23)
	if err := store.UpsertClaim(t.Context(), &claim); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, reopenedDB := openSQLiteClaimStore(t, path)
	defer reopenedDB.Close()
	claims, err := reopened.QueryClaims(t.Context(), domainStoreQuery("example.org"), nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := claimOutpointStrings(claims), []string{claim.Outpoint.String()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reopened claims got %v want %v", got, want)
	}

	var persistedTxid string
	if err := reopenedDB.QueryRow(`SELECT txid FROM ecosystem_alias_claims WHERE vout = 7`).Scan(&persistedTxid); err != nil {
		t.Fatal(err)
	}
	if persistedTxid != claim.Outpoint.Txid.String() || persistedTxid != strings.ToLower(persistedTxid) {
		t.Fatalf("persisted txid %q is not canonical display hex", persistedTxid)
	}
}

func testPostgresTopicIsolation(t *testing.T, db *sql.DB) {
	t.Helper()
	left := NewSQLStore(db, 901)
	right := NewSQLStore(db, 902)
	leftClaim := storeClaim(t, 'd', 3, "dave", "example.dev", true, 820_000, 51)
	rightClaim := leftClaim
	rightClaim.Alias = "erin"
	rightClaim.Domain = "example.net"
	if err := left.UpsertClaim(t.Context(), &leftClaim); err != nil {
		t.Fatal(err)
	}
	if err := right.UpsertClaim(t.Context(), &rightClaim); err != nil {
		t.Fatal(err)
	}

	leftClaims, err := left.QueryClaims(t.Context(), findAllStoreQuery(), nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	rightClaims, err := right.QueryClaims(t.Context(), findAllStoreQuery(), nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(leftClaims) != 1 || leftClaims[0].Alias != "dave" {
		t.Fatalf("topic 901 leaked or lost claims: %+v", leftClaims)
	}
	if len(rightClaims) != 1 || rightClaims[0].Alias != "erin" {
		t.Fatalf("topic 902 leaked or lost claims: %+v", rightClaims)
	}

	spender := storeHash(t, 'e')
	if err := left.MarkSpent(t.Context(), &leftClaim.Outpoint, &spender); err != nil {
		t.Fatal(err)
	}
	rightClaims, err = right.QueryClaims(t.Context(), findAllStoreQuery(), nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rightClaims) != 1 || rightClaims[0].Alias != "erin" {
		t.Fatalf("topic-scoped spend affected topic 902: %+v", rightClaims)
	}
	if err := left.RollbackTransaction(t.Context(), &spender); err != nil {
		t.Fatal(err)
	}
	rightClaims, err = right.QueryClaims(t.Context(), findAllStoreQuery(), nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rightClaims) != 1 || rightClaims[0].Alias != "erin" {
		t.Fatalf("topic-scoped rollback affected topic 902: %+v", rightClaims)
	}
}

func testPostgresTextCollation(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, column := range []string{"txid", "spending_txid", "alias", "domain"} {
		var collation string
		err := db.QueryRowContext(t.Context(), `
			SELECT collation.collname
			FROM pg_attribute attribute
			JOIN pg_class relation ON relation.oid = attribute.attrelid
			JOIN pg_collation collation ON collation.oid = attribute.attcollation
			WHERE relation.relname = 'ecosystem_alias_claims'
				AND attribute.attname = $1
				AND attribute.attnum > 0
				AND NOT attribute.attisdropped`, column).Scan(&collation)
		if err != nil {
			t.Fatalf("read %s collation: %v", column, err)
		}
		if collation != "C" {
			t.Fatalf("%s collation = %q, want C", column, collation)
		}
	}
}

func TestPostgresClaimSchemaIndexesAreTopicFirst(t *testing.T) {
	for _, index := range []string{
		"idx_ecosystem_alias_claims_alias_lookup",
		"idx_ecosystem_alias_claims_domain_lookup",
		"idx_ecosystem_alias_claims_enumeration",
		"idx_ecosystem_alias_claims_spender",
	} {
		pattern := `(?s)` + regexp.QuoteMeta(index) + `\s+ON ecosystem_alias_claims \(\s*topic_id,`
		if !regexp.MustCompile(pattern).MatchString(postgresClaimSchema) {
			t.Fatalf("%s is not topic-first", index)
		}
	}
	for _, column := range []string{"txid", "spending_txid", "alias", "domain"} {
		pattern := regexp.QuoteMeta(column) + `\s+TEXT COLLATE "C"`
		if !regexp.MustCompile(pattern).MatchString(postgresClaimSchema) {
			t.Fatalf("%s does not use deterministic C collation", column)
		}
	}
}
