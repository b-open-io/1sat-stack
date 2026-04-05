FROM oven/bun:1-alpine AS ui-builder

RUN apk add --no-cache python3 make g++

# Build admin UI
WORKDIR /app/admin/ui
COPY admin/ui/package.json admin/ui/bun.lock* ./
RUN bun install --no-save
COPY admin/ui/ ./
RUN bun run build

# Build sweep UI
WORKDIR /app/sweep/ui
COPY sweep/ui/package.json sweep/ui/bun.lock* ./
RUN bun install --no-save
COPY sweep/ui/ ./
RUN bun run build

# Build landing UI
WORKDIR /app/landing/ui
COPY landing/ui/package.json landing/ui/bun.lock* ./
RUN bun install --no-save
COPY landing/ui/ ./
RUN bun run build

FROM golang:1.26-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Copy built UIs from previous stage
COPY --from=ui-builder /app/admin/ui/dist/ admin/ui/dist/
COPY --from=ui-builder /app/sweep/ui/dist/ sweep/ui/dist/
COPY --from=ui-builder /app/landing/ui/dist/ landing/ui/dist/

RUN CGO_ENABLED=1 go build -o server ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/server .

EXPOSE 8080

CMD ["./server", "--data-dir", "/data"]
