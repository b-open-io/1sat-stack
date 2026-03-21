package store

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/tidwall/redcon"
)

// RedconServer wraps a BadgerStore and exposes it over the Redis RESP protocol.
// External clients (redis-cli, go-redis, ioredis, etc.) connect to it like a real Redis server.
// In-process Go callers can use the BadgerStore directly — no protocol overhead.
type RedconServer struct {
	store  *BadgerStore
	addr   string
	server *redcon.Server
	ps     *redcon.PubSub
	logger *slog.Logger
}

// NewRedconServer creates a RESP-protocol server backed by the given BadgerStore.
func NewRedconServer(store *BadgerStore, addr string, logger *slog.Logger) *RedconServer {
	if logger == nil {
		logger = slog.Default()
	}
	return &RedconServer{
		store:  store,
		addr:   addr,
		ps:     &redcon.PubSub{},
		logger: logger,
	}
}

// ListenAndServe starts the RESP server. Blocks until closed.
func (rs *RedconServer) ListenAndServe() error {
	rs.logger.Info("starting redcon server", "addr", rs.addr)

	mux := redcon.NewServeMux()
	rs.registerCommands(mux)

	rs.server = redcon.NewServer(rs.addr,
		mux.ServeRESP,
		func(conn redcon.Conn) bool {
			rs.logger.Debug("client connected", "addr", conn.RemoteAddr())
			return true
		},
		func(conn redcon.Conn, err error) {
			rs.logger.Debug("client disconnected", "addr", conn.RemoteAddr())
		},
	)

	return rs.server.ListenAndServe()
}

// Close stops the RESP server.
func (rs *RedconServer) Close() error {
	if rs.server != nil {
		return rs.server.Close()
	}
	return nil
}

// Store returns the underlying BadgerStore for direct in-process access.
func (rs *RedconServer) Store() *BadgerStore {
	return rs.store
}

func (rs *RedconServer) registerCommands(mux *redcon.ServeMux) {
	// Connection
	mux.HandleFunc("ping", rs.handlePing)
	mux.HandleFunc("command", rs.handleCommand)

	// KV
	mux.HandleFunc("get", rs.handleGet)
	mux.HandleFunc("set", rs.handleSet)
	mux.HandleFunc("del", rs.handleDel)
	mux.HandleFunc("scan", rs.handleScan)

	// Hash
	mux.HandleFunc("hset", rs.handleHSet)
	mux.HandleFunc("hget", rs.handleHGet)
	mux.HandleFunc("hgetall", rs.handleHGetAll)
	mux.HandleFunc("hdel", rs.handleHDel)
	mux.HandleFunc("hmset", rs.handleHMSet)
	mux.HandleFunc("hmget", rs.handleHMGet)

	// Set
	mux.HandleFunc("sadd", rs.handleSAdd)
	mux.HandleFunc("smembers", rs.handleSMembers)
	mux.HandleFunc("srem", rs.handleSRem)
	mux.HandleFunc("sismember", rs.handleSIsMember)

	// Sorted Set
	mux.HandleFunc("zadd", rs.handleZAdd)
	mux.HandleFunc("zrem", rs.handleZRem)
	mux.HandleFunc("zrangebyscore", rs.handleZRangeByScore)
	mux.HandleFunc("zrevrangebyscore", rs.handleZRevRangeByScore)
	mux.HandleFunc("zscore", rs.handleZScore)
	mux.HandleFunc("zcard", rs.handleZCard)
	mux.HandleFunc("zincrby", rs.handleZIncrBy)

	// Custom commands (not standard Redis)
	mux.HandleFunc("zsum", rs.handleZSum)

	// PubSub
	mux.HandleFunc("subscribe", rs.handleSubscribe)
	mux.HandleFunc("publish", rs.handlePublish)
}

// Connection handlers

func (rs *RedconServer) handlePing(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) > 1 {
		conn.WriteBulk(cmd.Args[1])
	} else {
		conn.WriteString("PONG")
	}
}

func (rs *RedconServer) handleCommand(conn redcon.Conn, cmd redcon.Command) {
	conn.WriteArray(0)
}

// KV handlers

func (rs *RedconServer) handleGet(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) != 2 {
		conn.WriteError("ERR wrong number of arguments for 'get' command")
		return
	}
	val, err := rs.store.Get(bgCtx, cmd.Args[1])
	if err != nil {
		if err == ErrKeyNotFound {
			conn.WriteNull()
			return
		}
		conn.WriteError("ERR " + err.Error())
		return
	}
	conn.WriteBulk(val)
}

func (rs *RedconServer) handleSet(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) < 3 {
		conn.WriteError("ERR wrong number of arguments for 'set' command")
		return
	}
	if err := rs.store.Set(bgCtx, cmd.Args[1], cmd.Args[2]); err != nil {
		conn.WriteError("ERR " + err.Error())
		return
	}
	conn.WriteString("OK")
}

func (rs *RedconServer) handleDel(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) < 2 {
		conn.WriteError("ERR wrong number of arguments for 'del' command")
		return
	}
	var count int
	for _, key := range cmd.Args[1:] {
		_, err := rs.store.Get(bgCtx, key)
		if err == nil {
			if err := rs.store.Del(bgCtx, key); err == nil {
				count++
			}
		}
	}
	conn.WriteInt(count)
}

// Hash handlers

func (rs *RedconServer) handleHSet(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) < 4 || len(cmd.Args)%2 != 0 {
		conn.WriteError("ERR wrong number of arguments for 'hset' command")
		return
	}
	key := cmd.Args[1]
	var count int
	for i := 2; i < len(cmd.Args); i += 2 {
		if err := rs.store.HSet(bgCtx, key, cmd.Args[i], cmd.Args[i+1]); err != nil {
			conn.WriteError("ERR " + err.Error())
			return
		}
		count++
	}
	conn.WriteInt(count)
}

func (rs *RedconServer) handleHGet(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) != 3 {
		conn.WriteError("ERR wrong number of arguments for 'hget' command")
		return
	}
	val, err := rs.store.HGet(bgCtx, cmd.Args[1], cmd.Args[2])
	if err != nil {
		if err == ErrKeyNotFound {
			conn.WriteNull()
			return
		}
		conn.WriteError("ERR " + err.Error())
		return
	}
	conn.WriteBulk(val)
}

func (rs *RedconServer) handleHGetAll(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) != 2 {
		conn.WriteError("ERR wrong number of arguments for 'hgetall' command")
		return
	}
	vals, err := rs.store.HGetAll(bgCtx, cmd.Args[1])
	if err != nil {
		conn.WriteError("ERR " + err.Error())
		return
	}
	conn.WriteArray(len(vals) * 2)
	for k, v := range vals {
		conn.WriteBulk([]byte(k))
		conn.WriteBulk(v)
	}
}

func (rs *RedconServer) handleHDel(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) < 3 {
		conn.WriteError("ERR wrong number of arguments for 'hdel' command")
		return
	}
	if err := rs.store.HDel(bgCtx, cmd.Args[1], cmd.Args[2:]...); err != nil {
		conn.WriteError("ERR " + err.Error())
		return
	}
	conn.WriteInt(len(cmd.Args) - 2)
}

func (rs *RedconServer) handleHMSet(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) < 4 || len(cmd.Args)%2 != 0 {
		conn.WriteError("ERR wrong number of arguments for 'hmset' command")
		return
	}
	fields := make(map[string][]byte)
	for i := 2; i < len(cmd.Args); i += 2 {
		fields[string(cmd.Args[i])] = cmd.Args[i+1]
	}
	if err := rs.store.HMSet(bgCtx, cmd.Args[1], fields); err != nil {
		conn.WriteError("ERR " + err.Error())
		return
	}
	conn.WriteString("OK")
}

func (rs *RedconServer) handleHMGet(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) < 3 {
		conn.WriteError("ERR wrong number of arguments for 'hmget' command")
		return
	}
	vals, err := rs.store.HMGet(bgCtx, cmd.Args[1], cmd.Args[2:]...)
	if err != nil {
		conn.WriteError("ERR " + err.Error())
		return
	}
	conn.WriteArray(len(vals))
	for _, v := range vals {
		if v == nil {
			conn.WriteNull()
		} else {
			conn.WriteBulk(v)
		}
	}
}

// Set handlers

func (rs *RedconServer) handleSAdd(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) < 3 {
		conn.WriteError("ERR wrong number of arguments for 'sadd' command")
		return
	}
	if err := rs.store.SAdd(bgCtx, cmd.Args[1], cmd.Args[2:]...); err != nil {
		conn.WriteError("ERR " + err.Error())
		return
	}
	conn.WriteInt(len(cmd.Args) - 2)
}

func (rs *RedconServer) handleSMembers(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) != 2 {
		conn.WriteError("ERR wrong number of arguments for 'smembers' command")
		return
	}
	members, err := rs.store.SMembers(bgCtx, cmd.Args[1])
	if err != nil {
		conn.WriteError("ERR " + err.Error())
		return
	}
	conn.WriteArray(len(members))
	for _, m := range members {
		conn.WriteBulk(m)
	}
}

func (rs *RedconServer) handleSRem(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) < 3 {
		conn.WriteError("ERR wrong number of arguments for 'srem' command")
		return
	}
	if err := rs.store.SRem(bgCtx, cmd.Args[1], cmd.Args[2:]...); err != nil {
		conn.WriteError("ERR " + err.Error())
		return
	}
	conn.WriteInt(len(cmd.Args) - 2)
}

func (rs *RedconServer) handleSIsMember(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) != 3 {
		conn.WriteError("ERR wrong number of arguments for 'sismember' command")
		return
	}
	exists, err := rs.store.SIsMember(bgCtx, cmd.Args[1], cmd.Args[2])
	if err != nil {
		conn.WriteError("ERR " + err.Error())
		return
	}
	if exists {
		conn.WriteInt(1)
	} else {
		conn.WriteInt(0)
	}
}

// Sorted Set handlers

func (rs *RedconServer) handleZAdd(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) < 4 || (len(cmd.Args)-2)%2 != 0 {
		conn.WriteError("ERR wrong number of arguments for 'zadd' command")
		return
	}
	key := cmd.Args[1]
	var members []ScoredMember
	for i := 2; i < len(cmd.Args); i += 2 {
		score, err := strconv.ParseFloat(string(cmd.Args[i]), 64)
		if err != nil {
			conn.WriteError("ERR value is not a valid float")
			return
		}
		members = append(members, ScoredMember{Score: score, Member: cmd.Args[i+1]})
	}
	if err := rs.store.ZAdd(bgCtx, key, members...); err != nil {
		conn.WriteError("ERR " + err.Error())
		return
	}
	conn.WriteInt(len(members))
}

func (rs *RedconServer) handleZRem(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) < 3 {
		conn.WriteError("ERR wrong number of arguments for 'zrem' command")
		return
	}
	if err := rs.store.ZRem(bgCtx, cmd.Args[1], cmd.Args[2:]...); err != nil {
		conn.WriteError("ERR " + err.Error())
		return
	}
	conn.WriteInt(len(cmd.Args) - 2)
}

func (rs *RedconServer) handleZRangeByScore(conn redcon.Conn, cmd redcon.Command) {
	rs.zRangeByScore(conn, cmd, false)
}

func (rs *RedconServer) handleZRevRangeByScore(conn redcon.Conn, cmd redcon.Command) {
	rs.zRangeByScore(conn, cmd, true)
}

func (rs *RedconServer) zRangeByScore(conn redcon.Conn, cmd redcon.Command, reverse bool) {
	if len(cmd.Args) < 4 {
		conn.WriteError("ERR wrong number of arguments")
		return
	}

	key := cmd.Args[1]
	minStr := string(cmd.Args[2])
	maxStr := string(cmd.Args[3])
	if reverse {
		minStr, maxStr = maxStr, minStr
	}

	sr := parseScoreRange(minStr, maxStr)

	// Parse optional LIMIT offset count and WITHSCORES
	withScores := false
	for i := 4; i < len(cmd.Args); i++ {
		arg := strings.ToUpper(string(cmd.Args[i]))
		if arg == "WITHSCORES" {
			withScores = true
		} else if arg == "LIMIT" && i+2 < len(cmd.Args) {
			offset, _ := strconv.ParseInt(string(cmd.Args[i+1]), 10, 64)
			count, _ := strconv.ParseInt(string(cmd.Args[i+2]), 10, 64)
			sr.Offset = offset
			sr.Count = count
			i += 2
		}
	}

	var members []ScoredMember
	var err error
	if reverse {
		members, err = rs.store.ZRevRange(bgCtx, key, sr)
	} else {
		members, err = rs.store.ZRange(bgCtx, key, sr)
	}
	if err != nil {
		conn.WriteError("ERR " + err.Error())
		return
	}

	if withScores {
		conn.WriteArray(len(members) * 2)
		for _, m := range members {
			conn.WriteBulk(m.Member)
			conn.WriteBulkString(strconv.FormatFloat(m.Score, 'f', -1, 64))
		}
	} else {
		conn.WriteArray(len(members))
		for _, m := range members {
			conn.WriteBulk(m.Member)
		}
	}
}

func (rs *RedconServer) handleZScore(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) != 3 {
		conn.WriteError("ERR wrong number of arguments for 'zscore' command")
		return
	}
	score, err := rs.store.ZScore(bgCtx, cmd.Args[1], cmd.Args[2])
	if err != nil {
		if err == ErrKeyNotFound {
			conn.WriteNull()
			return
		}
		conn.WriteError("ERR " + err.Error())
		return
	}
	conn.WriteBulkString(strconv.FormatFloat(score, 'f', -1, 64))
}

func (rs *RedconServer) handleZCard(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) != 2 {
		conn.WriteError("ERR wrong number of arguments for 'zcard' command")
		return
	}
	count, err := rs.store.ZCard(bgCtx, cmd.Args[1])
	if err != nil {
		conn.WriteError("ERR " + err.Error())
		return
	}
	conn.WriteInt64(count)
}

func (rs *RedconServer) handleZIncrBy(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) != 4 {
		conn.WriteError("ERR wrong number of arguments for 'zincrby' command")
		return
	}
	increment, err := strconv.ParseFloat(string(cmd.Args[2]), 64)
	if err != nil {
		conn.WriteError("ERR value is not a valid float")
		return
	}
	newScore, err := rs.store.ZIncrBy(bgCtx, cmd.Args[1], cmd.Args[3], increment)
	if err != nil {
		conn.WriteError("ERR " + err.Error())
		return
	}
	conn.WriteBulkString(strconv.FormatFloat(newScore, 'f', -1, 64))
}

// SCAN handler — Redis SCAN returns cursor + keys, we implement prefix-based scanning
// SCAN cursor MATCH pattern COUNT count

func (rs *RedconServer) handleScan(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) < 2 {
		conn.WriteError("ERR wrong number of arguments for 'scan' command")
		return
	}

	var pattern string
	var count int

	for i := 2; i < len(cmd.Args); i++ {
		arg := strings.ToUpper(string(cmd.Args[i]))
		if arg == "MATCH" && i+1 < len(cmd.Args) {
			pattern = string(cmd.Args[i+1])
			i++
		} else if arg == "COUNT" && i+1 < len(cmd.Args) {
			c, _ := strconv.Atoi(string(cmd.Args[i+1]))
			if c > 0 {
				count = c
			}
			i++
		}
	}

	// Strip trailing * from pattern for prefix scan
	prefix := strings.TrimSuffix(pattern, "*")
	if count == 0 {
		count = 100
	}

	results, err := rs.store.Scan(bgCtx, []byte(prefix), count)
	if err != nil {
		conn.WriteError("ERR " + err.Error())
		return
	}

	// Return [cursor, [keys...]] — cursor "0" means done
	conn.WriteArray(2)
	conn.WriteBulkString("0")
	conn.WriteArray(len(results))
	for _, kv := range results {
		conn.WriteBulk(kv.Key)
	}
}

// ZSUM — custom command, sum all scores in a sorted set

func (rs *RedconServer) handleZSum(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) != 2 {
		conn.WriteError("ERR wrong number of arguments for 'zsum' command")
		return
	}
	sum, err := rs.store.ZSum(bgCtx, cmd.Args[1])
	if err != nil {
		conn.WriteError("ERR " + err.Error())
		return
	}
	conn.WriteBulkString(strconv.FormatFloat(sum, 'f', -1, 64))
}

// PubSub handlers

func (rs *RedconServer) handleSubscribe(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) < 2 {
		conn.WriteError("ERR wrong number of arguments for 'subscribe' command")
		return
	}
	for _, channel := range cmd.Args[1:] {
		rs.ps.Subscribe(conn, string(channel))
	}
}

func (rs *RedconServer) handlePublish(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) != 3 {
		conn.WriteError("ERR wrong number of arguments for 'publish' command")
		return
	}
	count := rs.ps.Publish(string(cmd.Args[1]), string(cmd.Args[2]))
	conn.WriteInt(count)
}

// Helpers

var bgCtx = context.Background()

func parseScoreRange(minStr, maxStr string) ScoreRange {
	sr := ScoreRange{}

	if minStr != "-inf" {
		if strings.HasPrefix(minStr, "(") {
			sr.MinExclusive = true
			minStr = minStr[1:]
		}
		v, err := strconv.ParseFloat(minStr, 64)
		if err == nil {
			sr.Min = &v
		}
	}

	if maxStr != "+inf" {
		if strings.HasPrefix(maxStr, "(") {
			sr.MaxExclusive = true
			maxStr = maxStr[1:]
		}
		v, err := strconv.ParseFloat(maxStr, 64)
		if err == nil {
			sr.Max = &v
		}
	}

	return sr
}

// Addr returns the address the server listens on (for testing).
func (rs *RedconServer) Addr() string {
	return rs.addr
}

// Publish sends a message on a PubSub channel. Can be called in-process.
func (rs *RedconServer) Publish(channel, message string) int {
	return rs.ps.Publish(channel, message)
}

