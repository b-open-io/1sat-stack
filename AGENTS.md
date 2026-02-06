# AGENTS.md - 1Sat Stack Development Guide

## Build, Test & Lint Commands

### Build
```bash
# Build the server binary
go build -o server ./cmd/server

# Build with specific output path
go build -o /path/to/output/server ./cmd/server
```

### Run Tests
```bash
# Run all tests in the project
go test ./...

# Run tests for a specific package
go test ./pkg/ordfs

# Run a specific test function
go test ./pkg/ordfs -run TestParseContentPath

# Run with verbose output
go test ./... -v

# Run with coverage
go test ./... -cover

# Run tests with race detection
go test ./... -race
```

### Linting & Formatting
```bash
# Format all Go code (must pass before commit)
gofmt -s -w .

# Run go vet checks
go vet ./...

# Run linter (if configured)
golangci-lint run

# Check for issues without fixing
go fmt ./...
```

## Code Style Guidelines

### Import Ordering
Imports should be grouped and ordered as follows:
1. Standard library packages (context, log/slog, os, time, etc.)
2. Local project packages (github.com/b-open-io/1sat-stack/pkg/...)
3. Third-party dependencies (github.com/spf13/viper, github.com/gofiber/fiber/v2, etc.)

**Example:**
```go
import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/gofiber/fiber/v2"
	"github.com/spf13/viper"
)
```

### Naming Conventions
- **Types/Interfaces**: PascalCase (`Config`, `Ordfs`, `Store`, `Engine`)
- **Functions/Methods**: camelCase (`Load`, `Initialize`, `SetDefaults`, `parseContentPath`)
- **Constants**: UPPER_SNAKE_CASE (`ModeDisabled`, `lockTTL`, `cacheTTL`)
- **Variables**: camelCase (`logger`, `cfg`, `txid`)
- **Private functions**: camelCase with lowercase first letter (`parseContentPath`, `loadByTxid`)
- **Test functions**: camelCase with "Test" prefix (`TestConfigSetDefaults`, `TestParseContentPath`)

### Formatting
- Use `gofmt -s -w .` to format code
- Run `gofmt -l .` to check formatting issues
- Enforce formatting before commits (CI should fail if unformatted)

### Error Handling
- Always check for nil errors
- Wrap errors using `%w` format verb for error chains
- Provide descriptive error messages
- Return wrapped errors when propagating failures

**Examples:**
```go
// Good - wrapping errors
if err := s.BeefStore.SaveBeef(ctx, txid, beef); err != nil {
    return fmt.Errorf("failed to save beef for %s: %w", txid.String(), err)
}

// Good - checking nil
if logger == nil {
    logger = slog.Default()
}

// Good - descriptive error
return nil, fmt.Errorf("transaction not found: %w", err)
```

### Struct Tags
Use appropriate tags for serialization and configuration:
- `mapstructure` for config struct fields
- `json` for API response structs
- `swagger` for API documentation

**Example:**
```go
type Config struct {
    Server ServerConfig `mapstructure:"server"`
    Store  store.Config `mapstructure:"store"`
}

type IndexedOutputResponse struct {
    Outpoint    string         `json:"outpoint"`
    Score       float64        `json:"score"`
    Events      []string       `json:"events,omitempty"`
    Data        map[string]any `json:"data,omitempty"`
}
```

### Documentation
- Exported types and functions should have godoc comments
- Use descriptive comments that explain purpose and behavior
- Include example usage in comments when helpful

**Example:**
```go
// Ordfs handles ordinal file system operations
type Ordfs struct {
    jb     *junglebus.Client
    cache  *redis.Client
    logger *slog.Logger
}

// New creates a new Ordfs service
func New(jb *junglebus.Client, cache *redis.Client, logger *slog.Logger) *Ordfs
```

### Testing Conventions
- Write tests for new functionality
- Use table-driven test patterns
- Test both happy paths and error conditions
- Use `t.Run()` for subtests
- Check for nil errors and expected values
- Test edge cases and invalid inputs

**Example:**
```go
func TestParseContentPath(t *testing.T) {
    tests := []struct {
        name        string
        path        string
        expectTxid  bool
        expectSeq   bool
        expectError bool
    }{
        {
            name:       "valid outpoint",
            path:       "0123456789abcdef...",
            expectTxid: false,
        },
        {
            name:        "invalid format",
            path:        "invalid",
            expectError: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            req, err := parseContentPath(tt.path)
            if tt.expectError {
                if err == nil {
                    t.Error("expected error, got nil")
                }
                return
            }
            // Additional assertions...
        })
    }
}
```

### Context Usage
- Always pass `context.Context` to functions that may need cancellation
- Use context for timeouts and cancellation signals
- Propagate context through method chains
- Use `context.Background()` or `context.WithCancel()` appropriately

**Example:**
```go
func (s *OutputStore) InsertOutputs(ctx context.Context, topic string, txid *chainhash.Hash, ...) error {
    if s.IngestTx != nil {
        if err := s.IngestTx(ctx, tx); err != nil {
            return fmt.Errorf("failed to ingest transaction: %w", err)
        }
    }
    return nil
}
```

### Logging
- Use `log/slog` for structured logging
- Pass logger through function parameters rather than using global logger
- Use appropriate log levels (debug, info, warn, error)
- Include context in log messages (error, request ID, etc.)

**Example:**
```go
func (s *OutputStore) InsertOutputs(ctx context.Context, ...) error {
    s.logger.Info("inserting outputs", "txid", txid.String(), "outputs", len(outputVouts))
    // ...
    s.logger.Error("failed to insert outputs", "error", err, "txid", txid.String())
}
```

### JSON Marshalling
- Use `json.Marshal()` for API responses
- Use custom `MarshalJSON()` methods when needed
- Use `json:"field"` tags for field names
- Use `omitempty` for optional fields

**Example:**
```go
func (o *IndexedOutput) MarshalJSON() ([]byte, error) {
    resp := IndexedOutputResponse{
        Outpoint: o.Outpoint.OrdinalString(),
        Score:    o.Score,
    }
    return json.Marshal(resp)
}
```

## Project Structure

```
1sat-stack/
├── cmd/
│   └── server/          # Main application entry point
│       ├── main.go
│       ├── config.go
│       └── config_test.go
├── pkg/                 # Reusable packages
│   ├── beef/            # BEEF (Blockchain Efficient Exchange Format) handling
│   ├── bsv21/           # BSV21 fungible tokens
│   ├── ordfs/           # Ordinal filesystem operations
│   ├── overlay/         # Overlay services integration
│   ├── txo/             # Transaction output tracking
│   ├── parse/           # Script parsing for various protocols
│   ├── store/           # Storage layer (Redis, BadgerDB)
│   ├── pubsub/          # Publish-subscribe for events
│   ├── wallet/          # Wallet operations
│   └── types/           # Shared type definitions
├── docs/                # Documentation
├── config.yaml          # Configuration file
├── go.mod               # Go module definition
└── go.sum               # Dependency checksums
```

## Configuration

- Configuration is managed using Viper with YAML files
- Default values are set using `SetDefaults()` methods
- Config structs use `mapstructure` tags for field mapping
- Environment variables can override config values

## Development Workflow

1. Create feature branch from main
2. Make changes following code style guidelines
3. Run tests: `go test ./... -v`
4. Run linter: `go fmt ./... && go vet ./...`
5. Commit with descriptive message
6. Push and create PR

## Common Pitfalls

- **Missing nil checks**: Always check for nil before using objects
- **Forgetting context**: Functions that may run long should accept `context.Context`
- **Not wrapping errors**: Use `%w` for proper error chains
- **Untested paths**: Write tests for edge cases and error paths
- **Ignoring formatting**: Run `gofmt` before committing
- **Global state**: Avoid using global variables; pass dependencies explicitly
