package server

import (
	"github.com/gofiber/fiber/v2"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/httpx"
)

type healthResponse struct {
	Status string `json:"status"`
}

type metaResponse struct {
	Name        string    `json:"name"`
	Environment string    `json:"environment"`
	Build       BuildInfo `json:"build"`
}

// handleHealth reports process liveness. It never touches dependencies, so a
// failing database does not cause an orchestrator to restart a healthy process.
func (s *Server) handleHealth(c *fiber.Ctx) error {
	return httpx.OK(c, healthResponse{Status: "ok"})
}

// handleReady reports whether the process can serve traffic.
func (s *Server) handleReady(c *fiber.Ctx) error {
	if err := s.deps.Store.Ping(c.Context()); err != nil {
		return apperr.UpstreamUnavailable("The database is not reachable.").WithCause(err)
	}
	return httpx.OK(c, healthResponse{Status: "ready"})
}

// handleMeta exposes build information for the About dialog and bug reports.
func (s *Server) handleMeta(c *fiber.Ctx) error {
	return httpx.OK(c, metaResponse{
		Name:        "SangamDrive",
		Environment: string(s.deps.Config.Env),
		Build:       s.deps.Build,
	})
}
