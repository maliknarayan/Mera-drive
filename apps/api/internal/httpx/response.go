// Package httpx holds the HTTP conventions shared by every route: the response
// envelope, the error renderer, and the middleware stack.
package httpx

import (
	"github.com/gofiber/fiber/v2"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
)

// Envelope is the shape of every JSON response body.
//
//	{ "data": {...}, "meta": {...} }
//	{ "error": { "code": "...", "message": "..." }, "request_id": "..." }
//
// Exactly one of Data and Error is present.
type Envelope struct {
	Data      any            `json:"data,omitempty"`
	Meta      any            `json:"meta,omitempty"`
	Error     *apperr.Error  `json:"error,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
}

// Meta carries pagination cursors and partial-failure information. Fan-out
// endpoints use Errors to report that some accounts failed while others
// succeeded — a partial result is far more useful than a blanket 500.
type Meta struct {
	NextPageToken string          `json:"next_page_token,omitempty"`
	Count         int             `json:"count"`
	Errors        []*apperr.Error `json:"errors,omitempty"`
}

// OK writes 200 with a data payload.
func OK(c *fiber.Ctx, data any) error { return JSON(c, fiber.StatusOK, data, nil) }

// OKWithMeta writes 200 with a data payload and metadata.
func OKWithMeta(c *fiber.Ctx, data any, meta any) error {
	return JSON(c, fiber.StatusOK, data, meta)
}

// Created writes 201 with a data payload.
func Created(c *fiber.Ctx, data any) error { return JSON(c, fiber.StatusCreated, data, nil) }

// NoContent writes 204 with an empty body.
func NoContent(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) }

// JSON writes an arbitrary success status with the standard envelope.
func JSON(c *fiber.Ctx, status int, data any, meta any) error {
	return c.Status(status).JSON(Envelope{Data: data, Meta: meta, RequestID: RequestIDOf(c)})
}
