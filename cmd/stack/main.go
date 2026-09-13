// Command stack is the 1sat-stack operator command. It loads exactly what the
// server loads (config file, ONESAT_* environment, data dir), so nothing has
// to be specified for it to find the same config store and the same admin
// API. Today it wires up configuration and restart; serving and the rest of
// the operator surface are meant to move under it over time.
//
//	stack config list [prefix]
//	stack config get <key>
//	stack config set <key> <value>
//	stack config unset <key>
//	stack restart
//	stack queue add <queue> <txid|txid_vout>
//
// Reads go straight to the config store, read-only. Writes go through the
// running server's admin API so the server stays the only writer of
// config.db; when the server is not running they fall back to a direct
// write and take effect on the next start.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	configpkg "github.com/b-open-io/1sat-stack/pkg/config"
	_ "github.com/mattn/go-sqlite3"
)

func usage() {
	fmt.Fprint(os.Stderr, `usage: stack [--config file] [--data-dir dir] [--json] <command>

commands:
  config list [prefix]       print keys (and values) in the config store
  config get <key>           print one value
  config set <key> <value>   set a value (via the running server, or directly if it is down)
  config unset <key>         delete a key
  restart                    ask the running server to restart
  queue add <queue> <member> put a txid or txid_vout on a store queue for reprocessing

The same config file, ONESAT_* environment and data dir the server uses are
picked up automatically; --config and --data-dir override them exactly as
they do for the server.
`)
}

func main() {
	fs := flag.NewFlagSet("stack", flag.ExitOnError)
	fs.Usage = usage
	configPath := fs.String("config", "", "Path to config file")
	dataDir := fs.String("data-dir", "", "Base directory for all data files (default: ~/.1sat)")
	asJSON := fs.Bool("json", false, "JSON output")
	// Accept flags before or after the subcommand words.
	var flags, args []string
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
		} else {
			args = append(args, a)
		}
	}
	_ = fs.Parse(flags)
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	ctx := context.Background()
	env, err := LoadEnv(ctx, *configPath, *dataDir)
	if err != nil {
		fatal(err)
	}

	switch args[0] {
	case "config":
		if err := runConfig(ctx, env, args[1:], *asJSON); err != nil {
			fatal(err)
		}
	case "restart":
		if err := runRestart(ctx, env); err != nil {
			fatal(err)
		}
	case "queue":
		if err := runQueue(ctx, env, args[1:]); err != nil {
			fatal(err)
		}
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage()
		os.Exit(2)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func runConfig(ctx context.Context, env *Env, args []string, asJSON bool) error {
	if len(args) == 0 {
		return errors.New("config: expected list, get, set or unset")
	}
	switch args[0] {
	case "list":
		prefix := ""
		if len(args) > 1 {
			prefix = args[1]
		}
		r, err := OpenReader(env.ConfigDB)
		if errors.Is(err, ErrNoStore) {
			if asJSON {
				fmt.Println("{}")
			}
			return nil
		}
		if err != nil {
			return err
		}
		defer r.Close()
		entries, err := r.List(ctx, prefix)
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(os.Stdout).Encode(entries)
		}
		keys := make([]string, 0, len(entries))
		for k := range entries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("%s=%s\n", k, entries[k])
		}
		return nil
	case "get":
		if len(args) != 2 {
			return errors.New("config get: expected <key>")
		}
		r, err := OpenReader(env.ConfigDB)
		if err != nil {
			return err
		}
		defer r.Close()
		v, err := r.Get(ctx, args[1])
		if errors.Is(err, configpkg.ErrNotFound) {
			return fmt.Errorf("%s: not set", args[1])
		}
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{args[1]: v})
		}
		fmt.Println(v)
		return nil
	case "set":
		if len(args) != 3 {
			return errors.New("config set: expected <key> <value>")
		}
		if args[2] == "" {
			return errors.New("config set: empty value deletes the key; use unset")
		}
		return write(ctx, env, map[string]string{args[1]: args[2]}, fmt.Sprintf("%s=%s", args[1], args[2]))
	case "unset":
		if len(args) != 2 {
			return errors.New("config unset: expected <key>")
		}
		return write(ctx, env, map[string]string{args[1]: ""}, fmt.Sprintf("%s unset", args[1]))
	default:
		return fmt.Errorf("config: unknown subcommand %q", args[0])
	}
}

// write goes through the admin API when the server answers, else directly
// to the store. Protected keys are rejected by the server; the direct path
// refuses the same ones so the two behave alike.
func write(ctx context.Context, env *Env, updates map[string]string, summary string) error {
	for k := range updates {
		if k == "setup.complete" || strings.HasPrefix(k, "user:") {
			return fmt.Errorf("%s is a protected key", k)
		}
	}
	apiKey := ""
	if r, err := OpenReader(env.ConfigDB); err == nil {
		apiKey, _ = r.Get(ctx, "auth.api_key")
		r.Close()
	}
	client := NewAdminClient(env.AdminAPI(), apiKey)
	err := client.UpdateConfig(ctx, updates)
	if err == nil {
		fmt.Printf("%s (via admin api %s)\n", summary, env.AdminAPI())
		return nil
	}
	if !errors.Is(err, ErrServerDown) {
		return err
	}
	store, serr := configpkg.NewSQLiteStore(env.ConfigDB)
	if serr != nil {
		return fmt.Errorf("server not reachable (%v) and config store could not be opened: %w", err, serr)
	}
	defer store.Close()
	for k, v := range updates {
		var werr error
		if v == "" {
			werr = store.Delete(ctx, k)
		} else {
			werr = store.Set(ctx, k, v)
		}
		if werr != nil {
			return werr
		}
	}
	fmt.Printf("%s (server not running; written to %s, applies on next start)\n", summary, env.ConfigDB)
	return nil
}

func runRestart(ctx context.Context, env *Env) error {
	apiKey := ""
	if r, err := OpenReader(env.ConfigDB); err == nil {
		apiKey, _ = r.Get(ctx, "auth.api_key")
		r.Close()
	}
	if err := NewAdminClient(env.AdminAPI(), apiKey).Restart(ctx); err != nil {
		return err
	}
	fmt.Printf("restart requested via %s\n", env.AdminAPI())
	return nil
}

func runQueue(ctx context.Context, env *Env, args []string) error {
	if len(args) != 3 || args[0] != "add" {
		return errors.New("queue: expected add <queue> <txid|txid_vout>")
	}
	apiKey := ""
	if r, err := OpenReader(env.ConfigDB); err == nil {
		apiKey, _ = r.Get(ctx, "auth.api_key")
		r.Close()
	}
	out, err := NewAdminClient(env.AdminAPI(), apiKey).Enqueue(ctx, args[1], args[2])
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", strings.TrimSpace(string(out)))
	return nil
}
