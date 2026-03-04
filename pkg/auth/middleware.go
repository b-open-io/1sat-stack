package auth

import (
	"bufio"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/bsv-blockchain/go-bsv-middleware/pkg/middleware"
	"github.com/bsv-blockchain/go-sdk/auth"
	"github.com/bsv-blockchain/go-sdk/wallet"
	"github.com/gofiber/fiber/v2"
	"github.com/valyala/fasthttp/fasthttputil"
)

// Middleware is a Fiber middleware that performs BRC-103/104 mutual authentication
// using go-bsv-middleware under the hood. Supports optional API key bypass for
// development and agent access.
type Middleware struct {
	authFactory *middleware.AuthMiddlewareFactory
	logger      *slog.Logger
	apiKey      string
}

// NewMiddleware creates a new auth middleware.
// The wallet is used for the server side of the BRC-103/104 handshake.
// The session manager is shared so that all routes share one auth state.
func NewMiddleware(
	w wallet.Interface,
	sessionManager auth.SessionManager,
	logger *slog.Logger,
	allowUnauthenticated bool,
	apiKey string,
) *Middleware {
	if logger == nil {
		logger = slog.Default()
	}

	authFactory := middleware.NewAuth(w,
		middleware.WithAuthSessionManager(sessionManager),
		middleware.WithAuthLogger(logger),
		middleware.WithAuthAllowUnauthenticatedValue(allowUnauthenticated),
	)

	return &Middleware{
		authFactory: authFactory,
		logger:      logger,
		apiKey:      apiKey,
	}
}

// Handler returns the Fiber handler function.
func (m *Middleware) Handler() fiber.Handler {
	return func(c *fiber.Ctx) error {
		// API key bypass: skip BRC-103/104 if a valid key is provided.
		if m.apiKey != "" {
			if key := c.Get("X-Api-Key"); key == m.apiKey {
				c.SetUserContext(withApiKeyAuth(c.UserContext()))
				return c.Next()
			}
		}

		// Build a net/http request from the Fiber context.
		httpReq, err := buildHTTPRequest(c)
		if err != nil {
			m.logger.Error("failed to build http request for auth", "error", err)
			return c.SendStatus(fiber.StatusInternalServerError)
		}

		// captureHandler is the "next" handler in the net/http chain.
		// When the auth middleware decides the request is authenticated (or allowed),
		// it calls next.ServeHTTP — our captureHandler fires and grabs the identity
		// from the request context.
		var (
			captured    bool
			capturedReq *http.Request
		)
		captureHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			captured = true
			capturedReq = r
		})

		// Wrap our capture handler with the auth middleware.
		authHandler := m.authFactory.HTTPHandler(captureHandler)

		// Run it through a pipe so we can capture the response if the auth
		// middleware writes one directly (handshake responses, 401s, etc.).
		rec := &responseRecorder{
			header: make(http.Header),
			body:   &strings.Builder{},
		}
		authHandler.ServeHTTP(rec, httpReq)

		if captured {
			// Auth succeeded and called next — extract identity and continue Fiber chain.
			identity, _ := middleware.ShouldGetIdentity(capturedReq.Context())
			if identity != nil && !middleware.IsUnknownIdentity(identity) {
				c.SetUserContext(withIdentity(c.UserContext(), identity))
			}
			return c.Next()
		}

		// Auth middleware handled the response itself (handshake, 401, etc.).
		// Copy it back to Fiber.
		for key, vals := range rec.header {
			for _, v := range vals {
				c.Response().Header.Add(key, v)
			}
		}
		c.Status(rec.statusCode)
		return c.SendString(rec.body.String())
	}
}

// HTTPHandler wraps an http.Handler with auth middleware.
// Use this when you want to compose at the HTTP layer before adapting to Fiber.
// The auth context will flow directly to the wrapped handler without conversion.
func (m *Middleware) HTTPHandler(next http.Handler) http.Handler {
	return m.authFactory.HTTPHandler(next)
}

// buildHTTPRequest converts a Fiber context into a net/http.Request.
func buildHTTPRequest(c *fiber.Ctx) (*http.Request, error) {
	reqURI := string(c.Request().RequestURI())

	u, err := url.ParseRequestURI(reqURI)
	if err != nil {
		return nil, err
	}

	body := io.NopCloser(strings.NewReader(string(c.Body())))

	req := &http.Request{
		Method:        c.Method(),
		URL:           u,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        make(http.Header),
		Body:          body,
		ContentLength: int64(len(c.Body())),
		Host:          string(c.Request().Host()),
		RequestURI:    reqURI,
	}

	// Copy headers
	c.Request().Header.VisitAll(func(key, value []byte) {
		req.Header.Add(string(key), string(value))
	})

	// Set remote address
	req.RemoteAddr = c.IP()

	// Carry over Fiber's context so cancellation propagates
	req = req.WithContext(c.UserContext())

	return req, nil
}

// responseRecorder captures a net/http response in memory.
type responseRecorder struct {
	header     http.Header
	body       *strings.Builder
	statusCode int
	written    bool
	mu         sync.Mutex
}

func (r *responseRecorder) Header() http.Header {
	return r.header
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.written {
		r.written = true
		if r.statusCode == 0 {
			r.statusCode = http.StatusOK
		}
	}
	return r.body.Write(b)
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.written {
		r.statusCode = statusCode
		r.written = true
	}
}

// Flush implements http.Flusher (no-op for buffered recorder).
func (r *responseRecorder) Flush() {}

// Hijack implements http.Hijacker to satisfy interfaces some middleware may check.
func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return fasthttputil.NewPipeConns().Conn1(), nil, nil
}
