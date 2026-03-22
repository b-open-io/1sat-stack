# 1sat-engine Work

The distributed compute / Redis protocol migration work lives in a git worktree:

- **Worktree**: `/Users/davidcase/Source/1sat/1sat-engine/`
- **Branch**: `engine` (branched from `admin` at commit 04987c3)
- **Vision doc**: `docs/research/distributed-compute-vision.md`
- **Migration plan**: `docs/plans/engine-migration.md`
- **Linear project**: 1sat-engine

## What It Is

Replacing the custom `Store` interface with the Redis protocol (redcon + Badger) as the canonical data interface. Foundation for distributable WASM compute modules.

## Current State

Phase 1 (package migration): 10 of 13 packages migrated to go-redis client.
Remaining: pkg/bsv21, pkg/indexer, pkg/txo.
