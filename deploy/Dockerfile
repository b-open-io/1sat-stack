# Server-only image for local collection testing.
# The repo Dockerfile also builds the admin UI, which this test does not need.
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=1 go build -o server ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/server .

EXPOSE 8080

CMD ["./server", "--data-dir", "/data"]
