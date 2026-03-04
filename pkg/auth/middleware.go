package auth

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/bsv-blockchain/go-bsv-middleware/pkg/middleware"
	"github.com/bsv-blockchain/go-sdk/auth"
	"github.com/bsv-blockchain/go-sdk/wallet"
	"github.com/gofiber/fiber/v2"
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
//
// The auth middleware wraps a "next" http.Handler that executes the Fiber chain.
// When auth succeeds, "next" runs the downstream Fiber handlers and writes their
// response into the http.ResponseWriter the auth middleware provides. The auth
// middleware then signs the buffered response, adds x-bsv-auth-* headers, and
// flushes. We copy the final signed response back to Fiber.
//
// When auth handles the request itself (handshake, 401), "next" is never called
// and the auth middleware writes directly to the recorder.
func (m *Middleware) Handler() fiber.Handler {
	return func(c *fiber.Ctx) error {
		// API key bypass: skip BRC-103/104 if a valid key is provided.
		if m.apiKey != "" {
			if key := c.Get("X-Api-Key"); key == m.apiKey {
				c.SetUserContext(withApiKeyAuth(c.UserContext()))
				return c.Next()
			}
		}

		httpReq, err := buildHTTPRequest(c)
		if err != nil {
			m.logger.Error("failed to build http request for auth", "error", err)
			return c.SendStatus(fiber.StatusInternalServerError)
		}

		// "next" is called by the auth middleware after successful authentication.
		// It runs the Fiber chain and writes the response into w so the auth
		// middleware can sign it.
		var fiberErr error
		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract identity from the auth-enriched request context.
			identity, _ := middleware.ShouldGetIdentity(r.Context())
			if identity != nil && !middleware.IsUnknownIdentity(identity) {
				c.SetUserContext(withIdentity(c.UserContext(), identity))
			}

			// Run the rest of the Fiber chain.
			fiberErr = c.Next()

			// Copy Fiber's response into the http.ResponseWriter so the auth
			// middleware can read it back, sign it, and flush with auth headers.
			c.Response().Header.VisitAll(func(key, value []byte) {
				w.Header().Set(string(key), string(value))
			})
			w.WriteHeader(c.Response().StatusCode())
			w.Write(c.Response().Body()) //nolint:errcheck
		})

		// Wrap nextHandler with the auth middleware and run it.
		authHandler := m.authFactory.HTTPHandler(nextHandler)

		rec := &responseRecorder{
			header: make(http.Header),
			body:   &strings.Builder{},
		}
		authHandler.ServeHTTP(rec, httpReq)

		// Copy the final response (including auth signature headers) to Fiber.
		// Reset Fiber's response first to avoid duplicating what "next" wrote.
		c.Response().Reset()
		for key, vals := range rec.header {
			for _, v := range vals {
				c.Response().Header.Add(key, v)
			}
		}
		statusCode := rec.statusCode
		if statusCode == 0 {
			statusCode = http.StatusOK
		}
		c.Status(statusCode)
		if fiberErr != nil {
			return fiberErr
		}
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
