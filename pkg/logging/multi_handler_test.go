// pkg/logging/multi_handler_test.go
package logging

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
)

func TestMultiHandler_FansOut(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	h1 := slog.NewJSONHandler(&buf1, nil)
	h2 := slog.NewJSONHandler(&buf2, nil)

	multi := NewMultiHandler(h1, h2)
	logger := slog.New(multi)
	logger.Info("test message", "key", "value")

	if buf1.Len() == 0 {
		t.Error("handler 1 received no output")
	}
	if buf2.Len() == 0 {
		t.Error("handler 2 received no output")
	}
}

func TestMultiHandler_Enabled(t *testing.T) {
	h1 := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelWarn})
	h2 := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})

	multi := NewMultiHandler(h1, h2)
	if !multi.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("multi should be enabled at debug when one child accepts debug")
	}
}

func TestMultiHandler_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, nil)
	multi := NewMultiHandler(h)

	withAttrs := multi.WithAttrs([]slog.Attr{slog.String("component", "test")})
	logger := slog.New(withAttrs)
	logger.Info("tagged")

	if !bytes.Contains(buf.Bytes(), []byte(`"component"`)) {
		t.Error("attrs not propagated")
	}
}
