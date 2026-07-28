// Package server wires configuration, dependencies and routes into a Fiber app.
package server

import (
	"log/slog"

	"github.com/gofiber/fiber/v2"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/config"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/cryptobox"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/httpx"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/store"
)

// BuildInfo is stamped in at link time.
type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Built   string `json:"built"`
}

// Deps are everything a Server needs. Constructed in main, never global.
type Deps struct {
	Config *config.Config
	Logger *slog.Logger
	Store  store.Store
	Crypto *cryptobox.Box
	Build  BuildInfo
}

// Server owns the Fiber app and its dependencies.
type Server struct {
	app  *fiber.App
	deps Deps
}

// New builds the HTTP server with the full middleware stack and routes.
func New(deps Deps) *Server {
	cfg := deps.Config

	app := fiber.New(fiber.Config{
		AppName:               "SangamDrive API",
		ErrorHandler:          httpx.ErrorHandler(deps.Logger, cfg.Env.IsProduction()),
		DisableStartupMessage: true,

		// with TRUST_PROXY the operator has put a proxy they control in front,
		// so X-Forwarded-For is authoritative for c.IP(); without it, only the
		// socket address is ever used
		ProxyHeader: proxyHeader(cfg.TrustProxy),

		// file bytes stream through the API without being buffered, so the body
		// limit only needs to cover JSON payloads
		BodyLimit:         4 * 1024 * 1024,
		StreamRequestBody: true,

		// a multi-gigabyte transfer is a single long-lived request
		ReadTimeout:  0,
		WriteTimeout: 0,
	})

	s := &Server{app: app, deps: deps}

	app.Use(httpx.RequestID())
	app.Use(httpx.Recover(deps.Logger))
	app.Use(httpx.SecureHeaders())
	app.Use(httpx.CORS(cfg.CORSOrigins))
	app.Use(httpx.AccessLog(deps.Logger))

	s.registerRoutes()
	return s
}

func proxyHeader(trust bool) string {
	if trust {
		return fiber.HeaderXForwardedFor
	}
	return ""
}

// registerRoutes mounts every route. Later phases extend the v1 group.
func (s *Server) registerRoutes() {
	// unauthenticated, unrate-limited probes for orchestrators
	s.app.Get("/healthz", s.handleHealth)
	s.app.Get("/readyz", s.handleReady)

	v1 := s.app.Group("/api/v1",
		httpx.RateLimit(s.deps.Config.RateLimitMax, s.deps.Config.RateLimitWindow))

	v1.Get("/meta", s.handleMeta)

	// phase 2+ mounts /auth, /accounts, /files, /search, /upload, /storage here

	s.app.Use(func(c *fiber.Ctx) error {
		return apperr.NotFound("That endpoint does not exist.")
	})
}

// App exposes the Fiber app for tests and for the listener in main.
func (s *Server) App() *fiber.App { return s.app }
