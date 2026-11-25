package gib

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	gibtpl "github.com/b-open-io/1sat-stack/pkg/template/gib"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

// Routes serves the gib REST API. Paths are prefix-relative; the server
// mounts them at {base}/gib.
type Routes struct {
	store  *Store
	logger *slog.Logger
	fill   func(ctx context.Context, rec *HeadRecord) bool
}

// SetMetaFiller lets repository views backfill `.gib` metadata for heads
// indexed before enrichment existed (or before the content was fetchable).
func (r *Routes) SetMetaFiller(f func(ctx context.Context, rec *HeadRecord) bool) { r.fill = f }

// NewRoutes creates the gib REST routes.
func NewRoutes(store *Store, logger *slog.Logger) *Routes {
	if logger == nil {
		logger = slog.Default()
	}
	return &Routes{store: store, logger: logger}
}

// Register mounts the routes on the given router.
func (r *Routes) Register(router fiber.Router) {
	router.Get("/repos", r.ListRepos)
	router.Get("/repo/:origin", r.GetRepo)
	router.Get("/repo/:origin/branches", r.ListBranches)
	router.Get("/repo/:origin/branch/*", r.BranchHistory)
	router.Get("/identity/:identity/repos", r.IdentityRepos)
	router.Get("/heads", r.ListHeads)
	router.Get("/head/:outpoint", r.GetHead)
	router.Get("/commit/:sha", r.GetCommit)
}

// RepoResponse is a repository summary with its current branch heads.
type RepoResponse struct {
	RepoRecord
	HeadsList []HeadRecord `json:"branchHeads"`
}

// CommitResponse is one git commit as a node in the DAG: the commit object
// itself when a push published it, every head whose tip it is (across
// branches, repositories, and forks), and every head whose tip commit names
// it as a parent.
//
// Commit comes from the `.git` object store of whichever root first
// published it, so a commit deep in a repository's history is here even
// though no head's tip it ever was. Held is false when every store that
// named it cited an outpoint the overlay does not hold: the hop is named,
// not kept, and Ref says where it lives.
type CommitResponse struct {
	Sha      string         `json:"sha"`
	Commit   *gibtpl.Commit `json:"commit,omitempty"`
	Ref      string         `json:"ref,omitempty"`
	Held     bool           `json:"held"`
	Heads    []HeadRecord   `json:"heads"`
	Children []HeadRecord   `json:"children"`
}

// BranchResponse is a branch's current head plus its push history.
type BranchResponse struct {
	Origin  string       `json:"origin"`
	Branch  string       `json:"branch"`
	Head    *HeadRecord  `json:"head,omitempty"`
	History []HeadRecord `json:"history"`
}

type paging struct {
	from  float64
	limit int
	rev   bool
}

func parsePaging(c *fiber.Ctx) (paging, error) {
	p := paging{limit: c.QueryInt("limit", DefaultLimit), rev: c.Query("rev", "true") == "true"}
	if from := c.Query("from"); from != "" {
		v, err := strconv.ParseFloat(from, 64)
		if err != nil {
			return p, errors.New("invalid 'from' parameter")
		}
		p.from = v
	}
	return p, nil
}

func parseOutpointParam(s string) (string, error) {
	op, err := transaction.OutpointFromString(s)
	if err != nil {
		return "", err
	}
	return op.OrdinalString(), nil
}

func parseIdentityParam(s string) (string, error) {
	if len(s) != gibtpl.PubKeyLen*2 {
		return "", errors.New("identity must be a 33-byte compressed public key in hex")
	}
	if _, err := hex.DecodeString(s); err != nil {
		return "", errors.New("identity must be hex")
	}
	return s, nil
}

func badRequest(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": msg})
}

func (r *Routes) internalError(c *fiber.Ctx, what string, err error) error {
	r.logger.Error("gib route failed", "what", what, "error", err)
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": what + ": " + err.Error()})
}

// ListRepos lists repositories by most recent push.
// @Summary List repositories
// @Tags gib
// @Produce json
// @Param identity query string false "Only repositories this identity (hex pubkey) has pushed to"
// @Param limit query int false "Results limit" default(20)
// @Param from query number false "Pagination score cursor"
// @Param rev query bool false "Newest first" default(true)
// @Success 200 {array} RepoRecord
// @Failure 400 {object} object{message=string}
// @Failure 500 {object} object{message=string}
// @Router /repos [get]
func (r *Routes) ListRepos(c *fiber.Ctx) error {
	p, err := parsePaging(c)
	if err != nil {
		return badRequest(c, err.Error())
	}
	identity := c.Query("identity")
	if identity != "" {
		if identity, err = parseIdentityParam(identity); err != nil {
			return badRequest(c, err.Error())
		}
	}
	repos, err := r.store.ListRepos(c.Context(), identity, p.from, p.limit, p.rev)
	if err != nil {
		return r.internalError(c, "failed to list repositories", err)
	}
	return c.JSON(repos)
}

// IdentityRepos lists repositories an identity has pushed to.
// @Summary List an identity's repositories
// @Tags gib
// @Produce json
// @Param identity path string true "Identity public key (hex)"
// @Param limit query int false "Results limit" default(20)
// @Param from query number false "Pagination score cursor"
// @Param rev query bool false "Newest first" default(true)
// @Success 200 {array} RepoRecord
// @Failure 400 {object} object{message=string}
// @Failure 500 {object} object{message=string}
// @Router /identity/{identity}/repos [get]
func (r *Routes) IdentityRepos(c *fiber.Ctx) error {
	identity, err := parseIdentityParam(c.Params("identity"))
	if err != nil {
		return badRequest(c, err.Error())
	}
	p, err := parsePaging(c)
	if err != nil {
		return badRequest(c, err.Error())
	}
	repos, err := r.store.ListRepos(c.Context(), identity, p.from, p.limit, p.rev)
	if err != nil {
		return r.internalError(c, "failed to list repositories", err)
	}
	return c.JSON(repos)
}

// GetRepo returns a repository summary and its current branch heads.
// @Summary Get repository
// @Tags gib
// @Produce json
// @Param origin path string true "Origin (txid_vout or txid.vout)"
// @Success 200 {object} RepoResponse
// @Failure 400 {object} object{message=string}
// @Failure 404 {object} object{message=string}
// @Failure 500 {object} object{message=string}
// @Router /repo/{origin} [get]
func (r *Routes) GetRepo(c *fiber.Ctx) error {
	origin, err := parseOutpointParam(c.Params("origin"))
	if err != nil {
		return badRequest(c, "invalid origin")
	}
	repo, err := r.store.GetRepo(c.Context(), origin)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "repository not found"})
		}
		return r.internalError(c, "failed to get repository", err)
	}
	heads, err := r.store.ListHeads(c.Context(), HeadFilter{Origin: origin, Unspent: true, Limit: MaxLimit, Rev: true})
	if err != nil {
		return r.internalError(c, "failed to list branches", err)
	}
	if repo.Name == "" && r.fill != nil {
		for i := range heads {
			if r.fill(c.Context(), &heads[i]) {
				repo.Name, repo.Description, repo.DefaultBranch = heads[i].Meta.Name, heads[i].Meta.Description, heads[i].Meta.DefaultBranch
				break
			}
		}
	}
	return c.JSON(RepoResponse{RepoRecord: *repo, HeadsList: heads})
}

// ListBranches returns the current (unspent) heads of a repository.
// @Summary List branches
// @Tags gib
// @Produce json
// @Param origin path string true "Origin (txid_vout or txid.vout)"
// @Param identity query string false "Only heads published by this identity"
// @Success 200 {array} HeadRecord
// @Failure 400 {object} object{message=string}
// @Failure 500 {object} object{message=string}
// @Router /repo/{origin}/branches [get]
func (r *Routes) ListBranches(c *fiber.Ctx) error {
	origin, err := parseOutpointParam(c.Params("origin"))
	if err != nil {
		return badRequest(c, "invalid origin")
	}
	identity := c.Query("identity")
	if identity != "" {
		if identity, err = parseIdentityParam(identity); err != nil {
			return badRequest(c, err.Error())
		}
	}
	heads, err := r.store.ListHeads(c.Context(), HeadFilter{Origin: origin, Identity: identity, Unspent: true, Limit: MaxLimit, Rev: true})
	if err != nil {
		return r.internalError(c, "failed to list branches", err)
	}
	return c.JSON(heads)
}

// BranchHistory returns a branch's current head and its push history.
// @Summary Branch history
// @Tags gib
// @Produce json
// @Param origin path string true "Origin (txid_vout or txid.vout)"
// @Param branch path string true "Branch name (may contain slashes)"
// @Param identity query string false "Only heads published by this identity"
// @Param limit query int false "History limit" default(20)
// @Param from query number false "Pagination score cursor"
// @Param rev query bool false "Newest first" default(true)
// @Success 200 {object} BranchResponse
// @Failure 400 {object} object{message=string}
// @Failure 500 {object} object{message=string}
// @Router /repo/{origin}/branch/{branch} [get]
func (r *Routes) BranchHistory(c *fiber.Ctx) error {
	origin, err := parseOutpointParam(c.Params("origin"))
	if err != nil {
		return badRequest(c, "invalid origin")
	}
	branch := c.Params("*")
	if branch == "" {
		return badRequest(c, "branch is required")
	}
	identity := c.Query("identity")
	if identity != "" {
		if identity, err = parseIdentityParam(identity); err != nil {
			return badRequest(c, err.Error())
		}
	}
	p, err := parsePaging(c)
	if err != nil {
		return badRequest(c, err.Error())
	}
	history, err := r.store.ListHeads(c.Context(), HeadFilter{
		Origin: origin, Branch: branch, Identity: identity, From: p.from, Limit: p.limit, Rev: p.rev,
	})
	if err != nil {
		return r.internalError(c, "failed to load branch history", err)
	}
	current, err := r.store.ListHeads(c.Context(), HeadFilter{Origin: origin, Branch: branch, Identity: identity, Unspent: true, Limit: 1, Rev: true})
	if err != nil {
		return r.internalError(c, "failed to load branch head", err)
	}
	resp := BranchResponse{Origin: origin, Branch: branch, History: history}
	if len(current) > 0 {
		resp.Head = &current[0]
	}
	return c.JSON(resp)
}

// ListHeads lists commit heads with optional filters.
// @Summary List commit heads
// @Tags gib
// @Produce json
// @Param origin query string false "Origin (txid_vout or txid.vout)"
// @Param branch query string false "Branch name"
// @Param sha query string false "Git commit object id"
// @Param identity query string false "Identity public key (hex)"
// @Param unspent query bool false "Only current heads" default(false)
// @Param limit query int false "Results limit" default(20)
// @Param from query number false "Pagination score cursor"
// @Param rev query bool false "Newest first" default(true)
// @Success 200 {array} HeadRecord
// @Failure 400 {object} object{message=string}
// @Failure 500 {object} object{message=string}
// @Router /heads [get]
func (r *Routes) ListHeads(c *fiber.Ctx) error {
	p, err := parsePaging(c)
	if err != nil {
		return badRequest(c, err.Error())
	}
	f := HeadFilter{Branch: c.Query("branch"), CommitSha: strings.ToLower(c.Query("sha")), From: p.from, Limit: p.limit, Rev: p.rev, Unspent: c.QueryBool("unspent", false)}
	if origin := c.Query("origin"); origin != "" {
		if f.Origin, err = parseOutpointParam(origin); err != nil {
			return badRequest(c, "invalid origin")
		}
	}
	if identity := c.Query("identity"); identity != "" {
		if f.Identity, err = parseIdentityParam(identity); err != nil {
			return badRequest(c, err.Error())
		}
	}
	heads, err := r.store.ListHeads(c.Context(), f)
	if err != nil {
		return r.internalError(c, "failed to list heads", err)
	}
	return c.JSON(heads)
}

// GetHead returns one commit head.
// @Summary Get commit head
// @Tags gib
// @Produce json
// @Param outpoint path string true "Head outpoint (txid_vout or txid.vout)"
// @Success 200 {object} HeadRecord
// @Failure 400 {object} object{message=string}
// @Failure 404 {object} object{message=string}
// @Failure 500 {object} object{message=string}
// @Router /head/{outpoint} [get]
func (r *Routes) GetHead(c *fiber.Ctx) error {
	outpoint, err := parseOutpointParam(c.Params("outpoint"))
	if err != nil {
		return badRequest(c, "invalid outpoint")
	}
	head, err := r.store.GetHead(c.Context(), outpoint)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "head not found"})
		}
		return r.internalError(c, "failed to get head", err)
	}
	return c.JSON(head)
}

var shaRe = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// GetCommit returns a git commit's DAG neighborhood.
// @Summary Get commit by git sha
// @Tags gib
// @Produce json
// @Param sha path string true "Git commit object id (40 or 64 hex)"
// @Success 200 {object} CommitResponse
// @Failure 400 {object} object{message=string}
// @Failure 404 {object} object{message=string}
// @Failure 500 {object} object{message=string}
// @Router /commit/{sha} [get]
func (r *Routes) GetCommit(c *fiber.Ctx) error {
	sha := strings.ToLower(c.Params("sha"))
	if !shaRe.MatchString(sha) {
		return badRequest(c, "sha must be a 40 or 64 character hex git object id")
	}
	heads, err := r.store.ListHeads(c.Context(), HeadFilter{CommitSha: sha, Limit: MaxLimit, Rev: true})
	if err != nil {
		return r.internalError(c, "failed to list heads for commit", err)
	}
	children, err := r.store.ChildrenOfCommit(c.Context(), sha, MaxLimit)
	if err != nil {
		return r.internalError(c, "failed to list commit children", err)
	}
	commit, err := r.store.GetCommit(c.Context(), sha)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return r.internalError(c, "failed to get commit", err)
	}
	if len(heads) == 0 && len(children) == 0 && commit == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "commit not found"})
	}
	resp := CommitResponse{Sha: sha, Heads: heads, Children: children}
	if commit != nil {
		resp.Commit, resp.Ref, resp.Held = commit.Commit, commit.Ref, commit.Held
	}
	return c.JSON(resp)
}
