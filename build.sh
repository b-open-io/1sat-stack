#!/bin/bash
set -e

cd "$(dirname "$0")"

echo "Building admin UI..."
(cd admin/ui && bun install && bun run build)

echo "Building sweep UI..."
(cd sweep/ui && bun install && bun run build)

echo "Building server..."
go build -o server ./cmd/server

echo "Done."
