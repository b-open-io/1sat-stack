package main

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: fixproofs <wallet.sqlite> [arcade.db]\n")
		os.Exit(1)
	}

	walletDB := os.Args[1]
	arcadeDB := ""
	if len(os.Args) > 2 {
		arcadeDB = os.Args[2]
	}

	db, err := sql.Open("sqlite3", walletDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open wallet db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	rows, err := db.Query("SELECT tx_id FROM bsv_known_txes WHERE merkle_path IS NOT NULL AND length(merkle_path) > 0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to query: %v\n", err)
		os.Exit(1)
	}

	var txids []string
	for rows.Next() {
		var txid string
		rows.Scan(&txid)
		txids = append(txids, txid)
	}
	rows.Close()

	fmt.Printf("Found %d txids with merkle paths\n", len(txids))

	client := &http.Client{Timeout: 30 * time.Second}
	updated := 0
	failed := 0

	for _, txid := range txids {
		resp, err := client.Get(fmt.Sprintf("https://junglebus.gorillapool.io/v1/transaction/proof/%s/bin", txid))
		if err != nil {
			fmt.Printf("SKIP %s: %v\n", txid, err)
			failed++
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != 200 || len(body) < 10 {
			fmt.Printf("SKIP %s: status %d, %d bytes\n", txid, resp.StatusCode, len(body))
			failed++
			continue
		}

		_, err = db.Exec("UPDATE bsv_known_txes SET merkle_path = ? WHERE tx_id = ?", body, txid)
		if err != nil {
			fmt.Printf("FAIL %s: %v\n", txid, err)
			failed++
			continue
		}

		fmt.Printf("OK   %s: %d bytes\n", txid, len(body))
		updated++
		time.Sleep(100 * time.Millisecond)
	}

	fmt.Printf("\nWallet DB: updated %d, failed %d\n", updated, failed)

	if arcadeDB != "" {
		adb, err := sql.Open("sqlite3", arcadeDB)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to open arcade db: %v\n", err)
			os.Exit(1)
		}
		defer adb.Close()

		res, err := adb.Exec("DELETE FROM merkle_paths")
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to clear arcade merkle_paths: %v\n", err)
			os.Exit(1)
		}
		n, _ := res.RowsAffected()
		fmt.Printf("Arcade DB: cleared %d merkle_paths\n", n)
	}
}
