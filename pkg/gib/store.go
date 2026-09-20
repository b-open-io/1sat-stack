package gib

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"

	gibtpl "github.com/b-open-io/1sat-stack/pkg/template/gib"
)

const (
	// DefaultLimit is the default page size for list queries.
	DefaultLimit = 20
	// MaxLimit is the maximum page size for list queries.
	MaxLimit = 100
	// mempoolScoreFloor separates block-height scores from the unix-timestamp
	// scores types.HeightScore assigns to unconfirmed transactions.
	mempoolScoreFloor = 1e9
)

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS gib_heads (
    outpoint        TEXT PRIMARY KEY,
    txid            TEXT NOT NULL,
    vout            INTEGER NOT NULL,
    origin          TEXT NOT NULL,
    branch          TEXT NOT NULL,
    root            TEXT NOT NULL,
    identity        TEXT NOT NULL,
    commit_sha      TEXT,
    tree_sha        TEXT,
    parents         TEXT,
    author_name     TEXT,
    author_email    TEXT,
    author_time     INTEGER,
    author_tz       TEXT,
    committer_name  TEXT,
    committer_email TEXT,
    committer_time  INTEGER,
    committer_tz    TEXT,
    message         TEXT,
    prev_outpoint   TEXT,
    spend_txid      TEXT,
    next_outpoint   TEXT,
    spend_score     REAL,
    score           REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_gib_heads_branch ON gib_heads(origin, branch, score);
CREATE INDEX IF NOT EXISTS idx_gib_heads_identity ON gib_heads(identity, score);
CREATE INDEX IF NOT EXISTS idx_gib_heads_score ON gib_heads(score);
CREATE INDEX IF NOT EXISTS idx_gib_heads_txid ON gib_heads(txid);
CREATE INDEX IF NOT EXISTS idx_gib_heads_spend ON gib_heads(spend_txid);
CREATE INDEX IF NOT EXISTS idx_gib_heads_sha ON gib_heads(commit_sha);
CREATE TABLE IF NOT EXISTS gib_commit_parents (
    outpoint        TEXT NOT NULL,
    parent          TEXT NOT NULL,
    PRIMARY KEY (outpoint, parent)
);
CREATE INDEX IF NOT EXISTS idx_gib_parents_parent ON gib_commit_parents(parent);
`

const postgresSchema = `
CREATE TABLE IF NOT EXISTS gib_heads (
    topic_id        INTEGER NOT NULL,
    outpoint        TEXT NOT NULL,
    txid            TEXT NOT NULL,
    vout            INTEGER NOT NULL,
    origin          TEXT NOT NULL,
    branch          TEXT NOT NULL,
    root            TEXT NOT NULL,
    identity        TEXT NOT NULL,
    commit_sha      TEXT,
    tree_sha        TEXT,
    parents         TEXT,
    author_name     TEXT,
    author_email    TEXT,
    author_time     BIGINT,
    author_tz       TEXT,
    committer_name  TEXT,
    committer_email TEXT,
    committer_time  BIGINT,
    committer_tz    TEXT,
    message         TEXT,
    prev_outpoint   TEXT,
    spend_txid      TEXT,
    next_outpoint   TEXT,
    spend_score     DOUBLE PRECISION,
    score           DOUBLE PRECISION NOT NULL,
    PRIMARY KEY (topic_id, outpoint)
);
CREATE INDEX IF NOT EXISTS idx_gib_heads_branch ON gib_heads(topic_id, origin, branch, score);
CREATE INDEX IF NOT EXISTS idx_gib_heads_identity ON gib_heads(topic_id, identity, score);
CREATE INDEX IF NOT EXISTS idx_gib_heads_score ON gib_heads(topic_id, score);
CREATE INDEX IF NOT EXISTS idx_gib_heads_txid ON gib_heads(topic_id, txid);
CREATE INDEX IF NOT EXISTS idx_gib_heads_spend ON gib_heads(topic_id, spend_txid);
CREATE INDEX IF NOT EXISTS idx_gib_heads_sha ON gib_heads(topic_id, commit_sha);
CREATE TABLE IF NOT EXISTS gib_commit_parents (
    topic_id        INTEGER NOT NULL,
    outpoint        TEXT NOT NULL,
    parent          TEXT NOT NULL,
    PRIMARY KEY (topic_id, outpoint, parent)
);
CREATE INDEX IF NOT EXISTS idx_gib_parents_parent ON gib_commit_parents(topic_id, parent);
`

// Spend records how a head was spent: the spending txid and, for a push,
// the successor head in that transaction. A spend with no successor is a
// branch deletion (burn).
type Spend struct {
	Txid  string  `json:"txid"`
	Next  string  `json:"next,omitempty"`
	Score float64 `json:"score"`
}

// HeadRecord is one indexed commit head: a branch pointer at one moment.
// Outpoints are txid_vout strings; identity is a compressed pubkey in hex.
type HeadRecord struct {
	Outpoint string         `json:"outpoint"`
	Txid     string         `json:"txid"`
	Vout     uint32         `json:"vout"`
	Origin   string         `json:"origin"`
	Branch   string         `json:"branch"`
	Root     string         `json:"root"`
	Identity string         `json:"identity"`
	Commit   *gibtpl.Commit `json:"commit,omitempty"`
	Prev     string         `json:"prev,omitempty"`
	Spend    *Spend         `json:"spend,omitempty"`
	Meta     *RepoMeta      `json:"meta,omitempty"`
	Score    float64        `json:"score"`
	Height   uint32         `json:"height"`
}

// RepoRecord summarizes one repository (origin) from its indexed heads.
// Owner is the identity that minted the earliest head for the origin.
type RepoRecord struct {
	Origin        string  `json:"origin"`
	Owner         string  `json:"owner"`
	FirstOutpoint string  `json:"firstOutpoint"`
	FirstScore    float64 `json:"firstScore"`
	LastScore     float64 `json:"lastScore"`
	Name          string  `json:"name,omitempty"`
	Description   string  `json:"description,omitempty"`
	DefaultBranch string  `json:"defaultBranch,omitempty"`
	Heads         int     `json:"heads"`
	Branches      int     `json:"branches"`
}

// HeadFilter selects heads for ListHeads.
type HeadFilter struct {
	Origin   string
	Branch   string
	Identity string
	// CommitSha selects heads publishing this git commit (forks and
	// multi-branch pushes share one sha).
	CommitSha string
	Unspent   bool    // only current heads
	From      float64 // paging cursor on score; 0 = start
	Limit     int
	Rev       bool // newest first
}

// Store persists commit heads in the module's topic database.
type Store struct {
	db      *sql.DB
	topicID int
	logger  *slog.Logger
	once    sync.Once
	initErr error
}

// NewStore wraps the topic database. topicID > 0 selects the Postgres
// schema (shared table scoped by topic_id); 0 selects SQLite.
func NewStore(db *sql.DB, topicID int, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{db: db, topicID: topicID, logger: logger}
}

func (s *Store) ensureSchema() error {
	s.once.Do(func() {
		schema := sqliteSchema
		if s.topicID > 0 {
			schema = postgresSchema
		}
		_, s.initErr = s.db.Exec(schema)
		if s.initErr != nil {
			return
		}
		// Columns added after the first deploy; SQLite has no IF NOT EXISTS
		// for columns, so a "duplicate column" error is the expected no-op.
		for _, col := range []string{"name TEXT", "description TEXT", "default_branch TEXT"} {
			_, _ = s.db.Exec("ALTER TABLE gib_heads ADD COLUMN " + col)
		}
	})
	return s.initErr
}

// qb numbers placeholders and scopes queries by topic_id on Postgres.
type qb struct {
	topicID int
	args    []any
	n       int
}

func (s *Store) newQB() *qb {
	q := &qb{topicID: s.topicID}
	if s.topicID > 0 {
		q.args = append(q.args, s.topicID)
		q.n = 1
	}
	return q
}

func (q *qb) ph(val any) string {
	q.n++
	q.args = append(q.args, val)
	if q.topicID > 0 {
		return fmt.Sprintf("$%d", q.n)
	}
	return "?"
}

// topicWhere returns "alias.topic_id = $1 AND " on Postgres, "" on SQLite.
func (q *qb) topicWhere(alias string) string {
	if q.topicID > 0 {
		if alias != "" {
			return alias + ".topic_id = $1 AND "
		}
		return "topic_id = $1 AND "
	}
	return ""
}

func (q *qb) topicCols() string {
	if q.topicID > 0 {
		return "topic_id, "
	}
	return ""
}

func (q *qb) topicVals() string {
	if q.topicID > 0 {
		return "$1, "
	}
	return ""
}

func (q *qb) conflictTarget() string {
	if q.topicID > 0 {
		return "(topic_id, outpoint)"
	}
	return "(outpoint)"
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullMeta(m *RepoMeta, pick func(*RepoMeta) string) any {
	if m == nil {
		return nil
	}
	return nullStr(pick(m))
}

func nullSpendScore(sp *Spend) any {
	if sp == nil {
		return nil
	}
	return sp.Score
}

func nullSpendField(sp *Spend, pick func(*Spend) string) any {
	if sp == nil {
		return nil
	}
	return nullStr(pick(sp))
}

// UpsertHead inserts or refreshes a head. Spend and predecessor fields are
// only ever filled in, never cleared, so the admission and spend paths can
// arrive in either order. The lower score wins: a mined height replaces a
// mempool timestamp (see types.HeightScore) and the first arrival is kept.
func (s *Store) UpsertHead(ctx context.Context, rec *HeadRecord) error {
	if err := s.ensureSchema(); err != nil {
		return err
	}
	var (
		sha, tree, parents        any
		aName, aEmail, aTime, aTZ any
		cName, cEmail, cTime, cTZ any
		message                   any
	)
	if c := rec.Commit; c != nil {
		sha, tree, message = c.SHA, c.Tree, c.Message
		if b, err := json.Marshal(c.Parents); err == nil {
			parents = string(b)
		}
		if c.Author != nil {
			aName, aEmail, aTime, aTZ = c.Author.Name, c.Author.Email, c.Author.Time, c.Author.TZ
		}
		if c.Committer != nil {
			cName, cEmail, cTime, cTZ = c.Committer.Name, c.Committer.Email, c.Committer.Time, c.Committer.TZ
		}
	}

	q := s.newQB()
	query := fmt.Sprintf(`INSERT INTO gib_heads (%soutpoint, txid, vout, origin, branch, root, identity,
		commit_sha, tree_sha, parents, author_name, author_email, author_time, author_tz,
		committer_name, committer_email, committer_time, committer_tz, message,
		prev_outpoint, spend_txid, next_outpoint, spend_score, score, name, description, default_branch)
		VALUES (%s%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
		ON CONFLICT %s DO UPDATE SET
			origin = EXCLUDED.origin,
			branch = EXCLUDED.branch,
			root = EXCLUDED.root,
			identity = EXCLUDED.identity,
			commit_sha = COALESCE(EXCLUDED.commit_sha, gib_heads.commit_sha),
			tree_sha = COALESCE(EXCLUDED.tree_sha, gib_heads.tree_sha),
			parents = COALESCE(EXCLUDED.parents, gib_heads.parents),
			author_name = COALESCE(EXCLUDED.author_name, gib_heads.author_name),
			author_email = COALESCE(EXCLUDED.author_email, gib_heads.author_email),
			author_time = COALESCE(EXCLUDED.author_time, gib_heads.author_time),
			author_tz = COALESCE(EXCLUDED.author_tz, gib_heads.author_tz),
			committer_name = COALESCE(EXCLUDED.committer_name, gib_heads.committer_name),
			committer_email = COALESCE(EXCLUDED.committer_email, gib_heads.committer_email),
			committer_time = COALESCE(EXCLUDED.committer_time, gib_heads.committer_time),
			committer_tz = COALESCE(EXCLUDED.committer_tz, gib_heads.committer_tz),
			message = COALESCE(EXCLUDED.message, gib_heads.message),
			prev_outpoint = COALESCE(EXCLUDED.prev_outpoint, gib_heads.prev_outpoint),
			spend_txid = COALESCE(EXCLUDED.spend_txid, gib_heads.spend_txid),
			next_outpoint = COALESCE(EXCLUDED.next_outpoint, gib_heads.next_outpoint),
			spend_score = COALESCE(EXCLUDED.spend_score, gib_heads.spend_score),
			score = CASE WHEN EXCLUDED.score < gib_heads.score THEN EXCLUDED.score ELSE gib_heads.score END,
			name = COALESCE(EXCLUDED.name, gib_heads.name),
			description = COALESCE(EXCLUDED.description, gib_heads.description),
			default_branch = COALESCE(EXCLUDED.default_branch, gib_heads.default_branch)`,
		q.topicCols(), q.topicVals(),
		q.ph(rec.Outpoint), q.ph(rec.Txid), q.ph(rec.Vout), q.ph(rec.Origin), q.ph(rec.Branch), q.ph(rec.Root), q.ph(rec.Identity),
		q.ph(sha), q.ph(tree), q.ph(parents), q.ph(aName), q.ph(aEmail), q.ph(aTime), q.ph(aTZ),
		q.ph(cName), q.ph(cEmail), q.ph(cTime), q.ph(cTZ), q.ph(message),
		q.ph(nullStr(rec.Prev)),
		q.ph(nullSpendField(rec.Spend, func(sp *Spend) string { return sp.Txid })),
		q.ph(nullSpendField(rec.Spend, func(sp *Spend) string { return sp.Next })),
		q.ph(nullSpendScore(rec.Spend)),
		q.ph(rec.Score),
		q.ph(nullMeta(rec.Meta, func(m *RepoMeta) string { return m.Name })),
		q.ph(nullMeta(rec.Meta, func(m *RepoMeta) string { return m.Description })),
		q.ph(nullMeta(rec.Meta, func(m *RepoMeta) string { return m.DefaultBranch })),
		q.conflictTarget())
	if _, err := s.db.ExecContext(ctx, query, q.args...); err != nil {
		return err
	}
	if rec.Commit == nil {
		return nil
	}
	// Parent edges make the DAG walkable across repositories: a fork's
	// first commit names parents that live on another origin's heads.
	for _, parent := range rec.Commit.Parents {
		pq := s.newQB()
		ins := fmt.Sprintf(`INSERT INTO gib_commit_parents (%soutpoint, parent) VALUES (%s%s, %s) ON CONFLICT DO NOTHING`,
			pq.topicCols(), pq.topicVals(), pq.ph(rec.Outpoint), pq.ph(parent))
		if _, err := s.db.ExecContext(ctx, ins, pq.args...); err != nil {
			return err
		}
	}
	return nil
}

// MarkSpent records the spend of a head. It returns the number of rows
// updated; zero means the head has not been indexed yet.
func (s *Store) MarkSpent(ctx context.Context, outpoint, spendTxid, next string, spendScore float64) (int64, error) {
	if err := s.ensureSchema(); err != nil {
		return 0, err
	}
	q := s.newQB()
	query := fmt.Sprintf(`UPDATE gib_heads SET
			spend_txid = %s,
			next_outpoint = COALESCE(%s, next_outpoint),
			spend_score = %s
		WHERE %soutpoint = %s`,
		q.ph(spendTxid), q.ph(nullStr(next)), q.ph(spendScore), q.topicWhere(""), q.ph(outpoint))
	res, err := s.db.ExecContext(ctx, query, q.args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// UpdateScoreForTxid restamps heads created by txid and spends made by txid
// once the transaction's block position is known.
func (s *Store) UpdateScoreForTxid(ctx context.Context, txid string, score float64) error {
	if err := s.ensureSchema(); err != nil {
		return err
	}
	q := s.newQB()
	query := fmt.Sprintf(`UPDATE gib_heads SET score = %s WHERE %stxid = %s`,
		q.ph(score), q.topicWhere(""), q.ph(txid))
	if _, err := s.db.ExecContext(ctx, query, q.args...); err != nil {
		return err
	}
	q = s.newQB()
	query = fmt.Sprintf(`UPDATE gib_heads SET spend_score = %s WHERE %sspend_txid = %s`,
		q.ph(score), q.topicWhere(""), q.ph(txid))
	_, err := s.db.ExecContext(ctx, query, q.args...)
	return err
}

// DeleteHead removes a head (engine eviction, e.g. reorg).
func (s *Store) DeleteHead(ctx context.Context, outpoint string) error {
	if err := s.ensureSchema(); err != nil {
		return err
	}
	q := s.newQB()
	query := fmt.Sprintf(`DELETE FROM gib_heads WHERE %soutpoint = %s`, q.topicWhere(""), q.ph(outpoint))
	if _, err := s.db.ExecContext(ctx, query, q.args...); err != nil {
		return err
	}
	pq := s.newQB()
	del := fmt.Sprintf(`DELETE FROM gib_commit_parents WHERE %soutpoint = %s`, pq.topicWhere(""), pq.ph(outpoint))
	_, err := s.db.ExecContext(ctx, del, pq.args...)
	return err
}

// ChildrenOfCommit returns heads whose commit names sha as a parent: the
// next step along every branch, fork, or repository that built on it.
func (s *Store) ChildrenOfCommit(ctx context.Context, sha string, limit int) ([]HeadRecord, error) {
	if err := s.ensureSchema(); err != nil {
		return nil, err
	}
	q := s.newQB()
	query := fmt.Sprintf(`SELECT %s FROM gib_heads h
		WHERE %sh.outpoint IN (SELECT p.outpoint FROM gib_commit_parents p WHERE %sp.parent = %s)
		ORDER BY h.score ASC, h.vout ASC LIMIT %s`,
		prefixedHeadColumns("h."), q.topicWhere("h"), q.topicWhere("p"), q.ph(sha), q.ph(clampLimit(limit)))
	rows, err := s.db.QueryContext(ctx, query, q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HeadRecord{}
	for rows.Next() {
		rec, err := scanHead(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

func prefixedHeadColumns(prefix string) string {
	cols := strings.Split(headColumns, ",")
	for i, c := range cols {
		cols[i] = prefix + strings.TrimSpace(c)
	}
	return strings.Join(cols, ", ")
}

const headColumns = `outpoint, txid, vout, origin, branch, root, identity,
	commit_sha, tree_sha, parents, author_name, author_email, author_time, author_tz,
	committer_name, committer_email, committer_time, committer_tz, message,
	prev_outpoint, spend_txid, next_outpoint, spend_score, score,
	name, description, default_branch`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanHead(r rowScanner) (*HeadRecord, error) {
	var (
		rec                   HeadRecord
		sha, tree, parents    sql.NullString
		aName, aEmail, aTZ    sql.NullString
		cName, cEmail, cTZ    sql.NullString
		aTime, cTime          sql.NullInt64
		message               sql.NullString
		prev, spendTxid, next sql.NullString
		spendScore            sql.NullFloat64
		name, desc, branch    sql.NullString
	)
	if err := r.Scan(&rec.Outpoint, &rec.Txid, &rec.Vout, &rec.Origin, &rec.Branch, &rec.Root, &rec.Identity,
		&sha, &tree, &parents, &aName, &aEmail, &aTime, &aTZ,
		&cName, &cEmail, &cTime, &cTZ, &message,
		&prev, &spendTxid, &next, &spendScore, &rec.Score, &name, &desc, &branch); err != nil {
		return nil, err
	}
	if name.Valid || desc.Valid || branch.Valid {
		rec.Meta = &RepoMeta{Name: name.String, Description: desc.String, DefaultBranch: branch.String}
	}
	// Mined scores are block heights; mempool scores are unix timestamps
	// (see types.HeightScore), which carry no height.
	if rec.Score < mempoolScoreFloor {
		rec.Height = uint32(math.Floor(rec.Score))
	}
	rec.Prev = prev.String
	if sha.Valid {
		commit := &gibtpl.Commit{SHA: sha.String, Tree: tree.String, Message: message.String, Parents: []string{}}
		if parents.Valid && parents.String != "" {
			_ = json.Unmarshal([]byte(parents.String), &commit.Parents)
		}
		if aName.Valid || aEmail.Valid {
			commit.Author = &gibtpl.Signature{Name: aName.String, Email: aEmail.String, Time: aTime.Int64, TZ: aTZ.String}
		}
		if cName.Valid || cEmail.Valid {
			commit.Committer = &gibtpl.Signature{Name: cName.String, Email: cEmail.String, Time: cTime.Int64, TZ: cTZ.String}
		}
		rec.Commit = commit
	}
	if spendTxid.Valid {
		rec.Spend = &Spend{Txid: spendTxid.String, Next: next.String, Score: spendScore.Float64}
	}
	return &rec, nil
}

// GetHead returns one head or sql.ErrNoRows.
func (s *Store) GetHead(ctx context.Context, outpoint string) (*HeadRecord, error) {
	if err := s.ensureSchema(); err != nil {
		return nil, err
	}
	q := s.newQB()
	query := fmt.Sprintf(`SELECT %s FROM gib_heads WHERE %soutpoint = %s`, headColumns, q.topicWhere(""), q.ph(outpoint))
	return scanHead(s.db.QueryRowContext(ctx, query, q.args...))
}

// ListHeads returns heads matching the filter, ordered by score then vout.
func (s *Store) ListHeads(ctx context.Context, f HeadFilter) ([]HeadRecord, error) {
	if err := s.ensureSchema(); err != nil {
		return nil, err
	}
	q := s.newQB()
	where := []string{}
	if tw := q.topicWhere(""); tw != "" {
		where = append(where, strings.TrimSuffix(tw, " AND "))
	}
	if f.Origin != "" {
		where = append(where, "origin = "+q.ph(f.Origin))
	}
	if f.Branch != "" {
		where = append(where, "branch = "+q.ph(f.Branch))
	}
	if f.Identity != "" {
		where = append(where, "identity = "+q.ph(f.Identity))
	}
	if f.CommitSha != "" {
		where = append(where, "commit_sha = "+q.ph(f.CommitSha))
	}
	if f.Unspent {
		where = append(where, "spend_txid IS NULL")
	}
	if f.From > 0 {
		if f.Rev {
			where = append(where, "score < "+q.ph(f.From))
		} else {
			where = append(where, "score > "+q.ph(f.From))
		}
	}
	query := fmt.Sprintf(`SELECT %s FROM gib_heads`, headColumns)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	if f.Rev {
		query += " ORDER BY score DESC, vout DESC"
	} else {
		query += " ORDER BY score ASC, vout ASC"
	}
	query += " LIMIT " + q.ph(clampLimit(f.Limit))

	rows, err := s.db.QueryContext(ctx, query, q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HeadRecord{}
	for rows.Next() {
		rec, err := scanHead(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

const repoSelect = `SELECT h.origin,
	(SELECT h2.identity FROM gib_heads h2 WHERE %sh2.origin = h.origin ORDER BY h2.score ASC, h2.vout ASC LIMIT 1) AS owner,
	(SELECT h3.outpoint FROM gib_heads h3 WHERE %sh3.origin = h.origin ORDER BY h3.score ASC, h3.vout ASC LIMIT 1) AS first_outpoint,
	(SELECT h5.name FROM gib_heads h5 WHERE %sh5.origin = h.origin AND h5.name IS NOT NULL ORDER BY h5.score DESC, h5.vout DESC LIMIT 1) AS name,
	(SELECT h6.description FROM gib_heads h6 WHERE %sh6.origin = h.origin AND h6.description IS NOT NULL ORDER BY h6.score DESC, h6.vout DESC LIMIT 1) AS description,
	(SELECT h7.default_branch FROM gib_heads h7 WHERE %sh7.origin = h.origin AND h7.default_branch IS NOT NULL ORDER BY h7.score DESC, h7.vout DESC LIMIT 1) AS default_branch,
	MIN(h.score), MAX(h.score), COUNT(*), COUNT(DISTINCT h.branch)
	FROM gib_heads h`

func scanRepo(r rowScanner) (*RepoRecord, error) {
	var rec RepoRecord
	var owner, first, name, desc, branch sql.NullString
	if err := r.Scan(&rec.Origin, &owner, &first, &name, &desc, &branch, &rec.FirstScore, &rec.LastScore, &rec.Heads, &rec.Branches); err != nil {
		return nil, err
	}
	rec.Owner = owner.String
	rec.FirstOutpoint = first.String
	rec.Name, rec.Description, rec.DefaultBranch = name.String, desc.String, branch.String
	return &rec, nil
}

// GetRepo summarizes one origin or returns sql.ErrNoRows.
func (s *Store) GetRepo(ctx context.Context, origin string) (*RepoRecord, error) {
	if err := s.ensureSchema(); err != nil {
		return nil, err
	}
	q := s.newQB()
	query := fmt.Sprintf(repoSelect, q.topicWhere("h2"), q.topicWhere("h3"), q.topicWhere("h5"), q.topicWhere("h6"), q.topicWhere("h7")) +
		fmt.Sprintf(` WHERE %sh.origin = %s GROUP BY h.origin`, q.topicWhere("h"), q.ph(origin))
	return scanRepo(s.db.QueryRowContext(ctx, query, q.args...))
}

// ListRepos pages repositories by most recent activity. When identity is
// set, only repositories that identity has pushed to are returned.
func (s *Store) ListRepos(ctx context.Context, identity string, from float64, limit int, rev bool) ([]RepoRecord, error) {
	if err := s.ensureSchema(); err != nil {
		return nil, err
	}
	q := s.newQB()
	query := fmt.Sprintf(repoSelect, q.topicWhere("h2"), q.topicWhere("h3"), q.topicWhere("h5"), q.topicWhere("h6"), q.topicWhere("h7"))
	where := []string{}
	if tw := q.topicWhere("h"); tw != "" {
		where = append(where, strings.TrimSuffix(tw, " AND "))
	}
	if identity != "" {
		where = append(where, fmt.Sprintf(`EXISTS (SELECT 1 FROM gib_heads h4 WHERE %sh4.origin = h.origin AND h4.identity = %s)`,
			q.topicWhere("h4"), q.ph(identity)))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " GROUP BY h.origin"
	if from > 0 {
		if rev {
			query += " HAVING MAX(h.score) < " + q.ph(from)
		} else {
			query += " HAVING MAX(h.score) > " + q.ph(from)
		}
	}
	if rev {
		query += " ORDER BY MAX(h.score) DESC"
	} else {
		query += " ORDER BY MAX(h.score) ASC"
	}
	query += " LIMIT " + q.ph(clampLimit(limit))

	rows, err := s.db.QueryContext(ctx, query, q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RepoRecord{}
	for rows.Next() {
		rec, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return DefaultLimit
	}
	if limit > MaxLimit {
		return MaxLimit
	}
	return limit
}
