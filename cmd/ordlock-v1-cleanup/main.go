// ordlock-v1-cleanup removes the leftover public index of DEPRECATED OrdLock
// v1 listings. Run ONCE, with the server stopped when the txo store is Badger.
//
// What it deletes:
//   - txo store sorted sets ev:ordlock, ev:ordlock:spnd, tp:tm_ordlock,
//     tp:tm_ordlock:spnd (the public v1 listing enumeration). New v1
//     outputs no longer get the ordlock event, but rows indexed before
//     the deprecation still carry it, so ev:ordlock:spnd may regrow as
//     those old listings are spent. Nothing reads it; spend tracking for
//     wallets and sweep runs off the per-output spend record and the
//     owner (ev:own:*) index, which are untouched.
//   - the v1 overlay topic storage: <overlay dir>/tm_ordlock.db (SQLite) or
//     every tm_ordlock row in the shared Postgres overlay tables.
//
// What it keeps (on purpose): the per-output dt:ordlock data field and the
// ev:own:{addr} owner index, which the legacy sweep tool uses to find and
// cancel a user's old listings.
//
//	ordlock-v1-cleanup -badger ~/.1sat/badger [-overlay-sqlite ~/.1sat/overlay] [-dry-run]
//	ordlock-v1-cleanup -redis redis://localhost:6379/0 -overlay-pg postgres://... [-dry-run]
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const v1Topic = "tm_ordlock"

func main() {
	badgerPath := flag.String("badger", "", "Badger txo store path (server must be stopped)")
	redisURL := flag.String("redis", "", "Redis txo store URL")
	overlaySQLite := flag.String("overlay-sqlite", "", "overlay storage directory (SQLite); drops tm_ordlock.db")
	overlayPG := flag.String("overlay-pg", "", "overlay Postgres connection string; drops tm_ordlock rows")
	dryRun := flag.Bool("dry-run", false, "report what would be deleted without deleting")
	flag.Parse()

	if (*badgerPath == "") == (*redisURL == "") {
		fmt.Fprintln(os.Stderr, "exactly one of -badger or -redis is required")
		flag.Usage()
		os.Exit(2)
	}

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	var st store.Store
	var err error
	if *badgerPath != "" {
		st, err = store.NewBadgerStoreFromConfig(&store.BadgerConfig{Path: *badgerPath}, logger)
	} else {
		st, err = store.NewRedisStore(&store.RedisConfig{URL: *redisURL}, logger)
	}
	if err != nil {
		fatal("open txo store: %v", err)
	}
	defer st.Close()

	keys := [][]byte{
		txo.KeyEvent("ordlock"),
		txo.KeyEventSpent("ordlock"),
		[]byte(txo.PfxTopic + v1Topic),
		[]byte(txo.PfxTopic + v1Topic + ":spnd"),
		txo.KeyQueue("ordlock"), // v1 overlay work queue; v2 uses q:ordlock2
	}
	for _, k := range keys {
		n, err := st.ZCard(ctx, k)
		if err != nil {
			fatal("zcard %s: %v", k, err)
		}
		if *dryRun {
			fmt.Printf("would delete %s (%d members)\n", k, n)
			continue
		}
		if err := st.Del(ctx, k); err != nil {
			fatal("del %s: %v", k, err)
		}
		fmt.Printf("deleted %s (%d members)\n", k, n)
	}

	if *overlaySQLite != "" {
		for _, suffix := range []string{".db", ".db-wal", ".db-shm"} {
			path := filepath.Join(*overlaySQLite, v1Topic+suffix)
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				continue
			}
			if *dryRun {
				fmt.Printf("would remove %s\n", path)
				continue
			}
			if err := os.Remove(path); err != nil {
				fatal("remove %s: %v", path, err)
			}
			fmt.Printf("removed %s\n", path)
		}
	}

	if *overlayPG != "" {
		db, err := sql.Open("pgx", *overlayPG)
		if err != nil {
			fatal("open overlay postgres: %v", err)
		}
		defer db.Close()
		var topicID int
		err = db.QueryRowContext(ctx, `SELECT id FROM topics WHERE name = $1`, v1Topic).Scan(&topicID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			fmt.Printf("overlay postgres: no %s topic\n", v1Topic)
		case err != nil:
			fatal("lookup %s topic: %v", v1Topic, err)
		default:
			for _, table := range []string{"listings", "events", "applied_txs", "txid_topics", "outputs"} {
				q := fmt.Sprintf(`DELETE FROM %s WHERE topic_id = $1`, table)
				if *dryRun {
					var n int64
					_ = db.QueryRowContext(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE topic_id = $1`, table), topicID).Scan(&n)
					fmt.Printf("would delete %d rows from %s\n", n, table)
					continue
				}
				res, err := db.ExecContext(ctx, q, topicID)
				if err != nil {
					fatal("%s: %v", q, err)
				}
				n, _ := res.RowsAffected()
				fmt.Printf("deleted %d rows from %s\n", n, table)
			}
			if !*dryRun {
				if _, err := db.ExecContext(ctx, `DELETE FROM topics WHERE id = $1`, topicID); err != nil {
					fatal("delete topic row: %v", err)
				}
				fmt.Printf("deleted topic %s (id %d)\n", v1Topic, topicID)
			}
		}
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
