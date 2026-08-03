package httpx

import (
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/google/uuid"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
)

// Context keys. Fiber's Locals store is untyped, so keep the keys in one place.
const (
	localRequestID = "sangam.request_id"
	// HeaderRequestID is echoed on every response so users can quote it in bug
	// reports and operators can grep for it.
	HeaderRequestID = "X-Request-ID"
)

// RequestID assigns or adopts a request identifier and exposes it via Locals.
func RequestID() fiber.Handler {
	return func(c *fiber.Ctx) error {
		id := c.Get(HeaderRequestID)
		// only adopt a client-supplied id if it looks like one we generated
		if _, err := uuid.Parse(id); err != nil {
			id = uuid.NewString()
		}
		c.Locals(localRequestID, id)
		c.Set(HeaderRequestID, id)
		return c.Next()
	}
}

// RequestIDOf returns the identifier assigned to the current request.
func RequestIDOf(c *fiber.Ctx) string {
	if id, ok := c.Locals(localRequestID).(string); ok {
		return id
	}
	return ""
}

// Recover converts a panic into a 500 without killing the worker.
func Recover(log *slog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) (err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("recovered from panic",
					slog.String("request_id", RequestIDOf(c)),
					slog.String("path", c.Path()),
					slog.Any("panic", r),
				)
				err = apperr.Internal("Something went wrong on our side.")
			}
		}()
		return c.Next()
	}
}

// AccessLog records one structured line per request.
func AccessLog(log *slog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()

		attrs := []any{
			slog.String("request_id", RequestIDOf(c)),
			slog.String("method", c.Method()),
			slog.String("path", c.Path()),
			slog.Int("status", c.Response().StatusCode()),
			slog.Duration("duration", time.Since(start)),
		}
		switch {
		case err != nil:
			log.Error("request failed", append(attrs, slog.String("error", err.Error()))...)
		case c.Response().StatusCode() >= 500:
			log.Error("request failed", attrs...)
		default:
			log.Info("request", attrs...)
		}
		return err
	}
}

// SecureHeaders sets defensive response headers. The API only ever returns JSON
// or streamed file bytes, so the CSP can be maximally restrictive.
func SecureHeaders() fiber.Handler {
	return func(c *fiber.Ctx) error {
		// by pointer: Response().Header is a struct field, and a copy would
		// swallow every Set below
		h := &c.Response().Header
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "same-site")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		return c.Next()
	}
}

// CORS allows credentialed requests from an explicit origin allowlist.
// Wildcards are rejected at config load because cookies require an exact origin.
func CORS(allowed []string) fiber.Handler {
	set := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		set[strings.ToLower(strings.TrimRight(o, "/"))] = struct{}{}
	}

	return func(c *fiber.Ctx) error {
		origin := strings.ToLower(strings.TrimRight(c.Get(fiber.HeaderOrigin), "/"))
		if origin != "" {
			if _, ok := set[origin]; ok {
				h := &c.Response().Header
				h.Set(fiber.HeaderAccessControlAllowOrigin, c.Get(fiber.HeaderOrigin))
				h.Set(fiber.HeaderAccessControlAllowCredentials, "true")
				h.Set(fiber.HeaderAccessControlAllowHeaders, "Content-Type, X-CSRF-Token, "+HeaderRequestID)
				h.Set(fiber.HeaderAccessControlAllowMethods, "GET, POST, PATCH, DELETE, OPTIONS")
				h.Set(fiber.HeaderAccessControlExposeHeaders, HeaderRequestID+", Content-Disposition")
				h.Set(fiber.HeaderAccessControlMaxAge, "600")
				h.Set(fiber.HeaderVary, fiber.HeaderOrigin)
			}
		}
		if c.Method() == fiber.MethodOptions {
			return c.SendStatus(fiber.StatusNoContent)
		}
		return c.Next()
	}
}

// RateLimit applies a fixed-window per-client limit.
func RateLimit(max int, window time.Duration) fiber.Handler {
	return limiter.New(limiter.Config{
		Max:        max,
		Expiration: window,
		KeyGenerator: func(c *fiber.Ctx) string {
			// authenticated traffic is limited per session, anonymous per IP
			if sid, ok := c.Locals("sangam.session_id").(string); ok && sid != "" {
				return "session:" + sid
			}
			return "ip:" + c.IP()
		},
		LimitReached: func(c *fiber.Ctx) error {
			return apperr.RateLimited("Too many requests. Please slow down and try again shortly.")
		},
	})
}

// ErrorHandler renders any error returned by a handler using the standard
// envelope. Register it on the Fiber app config, not as middleware.
func ErrorHandler(log *slog.Logger, production bool) fiber.ErrorHandler {
	return func(c *fiber.Ctx, err error) error {
		appErr := toAppError(err)

		if appErr.Status >= 500 {
			log.Error("unhandled error",
				slog.String("request_id", RequestIDOf(c)),
				slog.String("path", c.Path()),
				slog.String("code", string(appErr.Code)),
				slog.String("error", appErr.Error()),
			)
			if production {
				// never leak internal detail to the client
				appErr = apperr.Internal("Something went wrong on our side.")
			}
		}

		return c.Status(appErr.Status).JSON(Envelope{
			Error:     appErr,
			RequestID: RequestIDOf(c),
		})
	}
}

// toAppError normalises Fiber's own errors alongside ours.
func toAppError(err error) *apperr.Error {
	var appErr *apperr.Error
	if errors.As(err, &appErr) {
		return appErr
	}

	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		switch fiberErr.Code {
		case fiber.StatusNotFound:
			return apperr.NotFound("That endpoint does not exist.")
		case fiber.StatusMethodNotAllowed:
			return apperr.BadRequest("That method is not allowed on this endpoint.")
		case fiber.StatusRequestEntityTooLarge:
			return apperr.PayloadTooLarge("The request body is too large.")
		default:
			return apperr.Internal("Something went wrong on our side.").WithCause(err)
		}
	}
	return apperr.From(err)
}
