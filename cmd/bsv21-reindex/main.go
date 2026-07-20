// bsv21-reindex rebuilds missing token_outputs rows in BSV21 topic databases.
//
// The engine's outputs table is the record of admittance; token_outputs is the
// BSV21 lookup's projection of it. This tool finds admitted outputs with no
// token_outputs row, loads each transaction from JungleBus, and replays it
// through the lookup's OutputAdmittedByTopic handler. Spend state is copied
// from the engine's outputs row. Safe to re-run; existing rows are untouched.
//
// Usage:
//
//	bsv21-reindex [-overlay ~/.1sat/overlay] [-junglebus https://junglebus.gorillapool.io] [-topic tm_bsv21] [-dry-run]
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/lookup"
	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/b-open-io/go-junglebus"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	_ "github.com/mattn/go-sqlite3"
)

var topicPattern = regexp.MustCompile(`^tm_(bsv21|[0-9a-f]{64}_\d+)\.db$`)

type missingOutput struct {
	outpoint  *transaction.Outpoint
	spendTxid []byte
}

func main() {
	overlayDir := flag.String("overlay", defaultOverlayDir(), "overlay storage directory")
	jbURL := flag.String("junglebus", "https://junglebus.gorillapool.io", "JungleBus URL for transaction loading")
	topic := flag.String("topic", "", "restrict to a single topic (default: all BSV21 topics)")
	dryRun := flag.Bool("dry-run", false, "report missing rows without reindexing")
	flag.Parse()

	topics, err := listTopics(*overlayDir, *topic)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list topics: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Scanning %d topics in %s\n", len(topics), *overlayDir)

	jbClient, err := junglebus.New(junglebus.WithHTTP(*jbURL))
	if err != nil {
		fmt.Fprintf(os.Stderr, "junglebus client: %v\n", err)
		os.Exit(1)
	}
	beefStore := beef.NewStorageFromProviders(
		[]beef.BaseBeefStorage{beef.NewJunglebusBeefStorageWithClient(jbClient)}, nil)

	ctx := context.Background()
	var totalMissing, totalReindexed, totalSkipped, totalErrors int

	for _, t := range topics {
		missing, err := scanTopic(filepath.Join(*overlayDir, t+".db"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: scan failed: %v\n", t, err)
			totalErrors++
			continue
		}
		if len(missing) == 0 {
			continue
		}
		totalMissing += len(missing)
		fmt.Printf("%s: %d missing\n", t, len(missing))
		if *dryRun {
			continue
		}

		reindexed, skipped, errored := reindexTopic(ctx, *overlayDir, t, missing, beefStore)
		totalReindexed += reindexed
		totalSkipped += skipped
		totalErrors += errored
	}

	fmt.Printf("\nMissing %d, reindexed %d, skipped %d, errors %d\n",
		totalMissing, totalReindexed, totalSkipped, totalErrors)
	if *dryRun {
		fmt.Println("(dry run: no changes made)")
	}
}

func defaultOverlayDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "./overlay"
	}
	return filepath.Join(home, ".1sat", "overlay")
}

func listTopics(dir, only string) ([]string, error) {
	if only != "" {
		return []string{only}, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var topics []string
	for _, e := range entries {
		if topicPattern.MatchString(e.Name()) {
			topics = append(topics, strings.TrimSuffix(e.Name(), ".db"))
		}
	}
	return topics, nil
}

// scanTopic finds admitted outputs with no token_outputs row, using a direct
// read-only connection so the sweep doesn't hold thousands of pooled handles.
func scanTopic(path string) ([]*missingOutput, error) {
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(
		`SELECT o.outpoint, o.spend_txid FROM outputs o
		 LEFT JOIN token_outputs t ON o.outpoint = t.outpoint
		 WHERE t.outpoint IS NULL`)
	if err != nil {
		// A topic the lookup never touched has no token_outputs table yet;
		// every admitted output is missing.
		if strings.Contains(err.Error(), "no such table: token_outputs") {
			rows, err = db.Query(`SELECT outpoint, spend_txid FROM outputs`)
		}
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()

	var missing []*missingOutput
	for rows.Next() {
		var outpointBytes, spendTxid []byte
		if err := rows.Scan(&outpointBytes, &spendTxid); err != nil {
			return nil, err
		}
		outpoint := transaction.NewOutpointFromBytes(outpointBytes)
		if outpoint == nil {
			return nil, fmt.Errorf("invalid outpoint %x", outpointBytes)
		}
		missing = append(missing, &missingOutput{outpoint: outpoint, spendTxid: spendTxid})
	}
	return missing, rows.Err()
}

// reindexTopic replays missing outputs through the BSV21 lookup. Each topic
// gets its own factory so handles are released before moving on.
func reindexTopic(ctx context.Context, overlayDir, topic string, missing []*missingOutput, beefStore *beef.Storage) (reindexed, skipped, errored int) {
	factory, err := overlaystorage.NewSQLiteFactory(overlayDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: storage factory: %v\n", topic, err)
		return 0, 0, len(missing)
	}
	defer factory.Close()
	lkp := lookup.NewBSV21Lookup(factory.Factory())

	ts, err := lkp.TopicDB(topic)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: open topic: %v\n", topic, err)
		return 0, 0, len(missing)
	}

	byTxid := map[chainhash.Hash][]*missingOutput{}
	for _, m := range missing {
		byTxid[m.outpoint.Txid] = append(byTxid[m.outpoint.Txid], m)
	}

	for txid, outputs := range byTxid {
		tx, err := beefStore.LoadTx(ctx, &txid)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: load tx %s: %v\n", topic, txid, err)
			errored += len(outputs)
			continue
		}
		atomicBeef, err := tx.AtomicBEEF(true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: beef for %s: %v\n", topic, txid, err)
			errored += len(outputs)
			continue
		}

		for _, m := range outputs {
			if err := lkp.OutputAdmittedByTopic(ctx, &engine.OutputAdmittedByTopic{
				Topic:       topic,
				OutputIndex: m.outpoint.Index,
				AtomicBEEF:  atomicBeef,
			}); err != nil {
				fmt.Fprintf(os.Stderr, "%s: admit %s: %v\n", topic, m.outpoint.OrdinalString(), err)
				errored++
				continue
			}

			// The handler silently ignores outputs that don't decode as BSV21;
			// confirm the row landed before counting it.
			var exists int
			if err := ts.DB().QueryRowContext(ctx,
				`SELECT COUNT(*) FROM token_outputs WHERE outpoint = ?`, m.outpoint.Bytes(),
			).Scan(&exists); err != nil {
				fmt.Fprintf(os.Stderr, "%s: verify %s: %v\n", topic, m.outpoint.OrdinalString(), err)
				errored++
				continue
			}
			if exists == 0 {
				skipped++
				continue
			}

			if m.spendTxid != nil {
				if _, err := ts.DB().ExecContext(ctx,
					`UPDATE token_outputs SET spend_txid = ? WHERE outpoint = ?`,
					m.spendTxid, m.outpoint.Bytes(),
				); err != nil {
					fmt.Fprintf(os.Stderr, "%s: spend %s: %v\n", topic, m.outpoint.OrdinalString(), err)
					errored++
					continue
				}
			}
			reindexed++
			if reindexed%100 == 0 {
				fmt.Printf("%s: %d/%d\n", topic, reindexed, len(missing))
			}
		}
	}
	return reindexed, skipped, errored
}
