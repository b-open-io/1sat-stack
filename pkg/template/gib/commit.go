package gib

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // git object ids are SHA-1 by definition
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrNotCommit is returned when the bytes are not a git commit object.
var ErrNotCommit = errors.New("gib: content is not a git commit object")

// Signature is a git author/committer line.
type Signature struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Time  int64  `json:"time"`
	TZ    string `json:"tz"`
}

// Commit is a parsed git commit object. SHA is the git object id computed
// over the verbatim bytes, so it matches the local repository's hash.
type Commit struct {
	SHA       string     `json:"sha"`
	Tree      string     `json:"tree"`
	Parents   []string   `json:"parents"`
	Author    *Signature `json:"author,omitempty"`
	Committer *Signature `json:"committer,omitempty"`
	Message   string     `json:"message"`
}

// ParseCommit parses the raw (uncompressed, header-less) bytes of a git
// commit object, as published in a tree's `.git` object store.
func ParseCommit(raw []byte) (*Commit, error) {
	if !bytes.HasPrefix(raw, []byte("tree ")) {
		return nil, ErrNotCommit
	}
	commit := &Commit{Parents: []string{}}

	header := raw
	var message []byte
	if end := bytes.Index(raw, []byte("\n\n")); end >= 0 {
		header = raw[:end]
		message = raw[end+2:]
	}

	for _, line := range bytes.Split(header, []byte("\n")) {
		if len(line) == 0 || line[0] == ' ' {
			// Continuation lines belong to multi-line headers (gpgsig).
			continue
		}
		key, value, _ := bytes.Cut(line, []byte(" "))
		switch string(key) {
		case "tree":
			commit.Tree = string(value)
		case "parent":
			commit.Parents = append(commit.Parents, string(value))
		case "author":
			commit.Author = parseSignature(string(value))
		case "committer":
			commit.Committer = parseSignature(string(value))
		}
	}
	if !isObjectID(commit.Tree) {
		return nil, fmt.Errorf("%w: bad tree id %q", ErrNotCommit, commit.Tree)
	}
	for _, parent := range commit.Parents {
		if !isObjectID(parent) {
			return nil, fmt.Errorf("%w: bad parent id %q", ErrNotCommit, parent)
		}
	}
	commit.Message = string(message)
	commit.SHA = ObjectID("commit", raw)
	return commit, nil
}

// ObjectID computes the git object id for a loose object of the given type.
func ObjectID(objType string, raw []byte) string {
	h := sha1.New() //nolint:gosec // git object ids are SHA-1 by definition
	fmt.Fprintf(h, "%s %d\x00", objType, len(raw))
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}

// IsObjectID reports whether s is a git object id: 40 hex characters
// (SHA-1) or 64 (SHA-256). `.git` entries are named by one, so a name is
// proof of content.
func IsObjectID(s string) bool {
	return isObjectID(s)
}

func isObjectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// parseSignature parses "Name <email> 1700000000 +0000". Missing pieces
// are left empty rather than failing the whole commit.
func parseSignature(s string) *Signature {
	sig := &Signature{}
	lt := strings.LastIndex(s, "<")
	gt := strings.LastIndex(s, ">")
	if lt < 0 || gt < lt {
		sig.Name = strings.TrimSpace(s)
		return sig
	}
	sig.Name = strings.TrimSpace(s[:lt])
	sig.Email = s[lt+1 : gt]
	rest := strings.Fields(s[gt+1:])
	if len(rest) > 0 {
		if ts, err := strconv.ParseInt(rest[0], 10, 64); err == nil {
			sig.Time = ts
		}
	}
	if len(rest) > 1 {
		sig.TZ = rest[1]
	}
	return sig
}
