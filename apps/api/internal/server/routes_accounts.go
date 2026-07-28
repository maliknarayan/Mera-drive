package server

import (
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/accounts"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/httpx"
)

// maxAccountsPerUser caps how many Drives one user may connect.
//
// "Unlimited" is the product promise, but an unbounded fan-out is a foot-gun: a
// single request would issue one Google call per account. This is a sanity
// ceiling, not a product limit — raise it if anyone genuinely needs more.
const maxAccountsPerUser = 50

// handleListAccounts returns every connected account with live storage figures.
//
// Responds 200 with partial data when some accounts fail; the failures are in
// meta.errors, each tagged with its account_id.
func (s *Server) handleListAccounts(c *fiber.Ctx) error {
	views, failures, err := s.deps.Accounts.List(c.Context(), currentUser(c).ID)
	if err != nil {
		return err
	}

	return httpx.OKWithMeta(c, views, httpx.Meta{
		Count:  len(views),
		Errors: failures,
	})
}

// handleStorage returns the aggregate storage summary.
//
// This performs the same fan-out as GET /accounts. The web app deliberately does
// not call it — it derives the summary from the accounts payload it already has,
// rather than doubling the number of Google calls to render one card. The endpoint
// exists for scripts and other API consumers.
func (s *Server) handleStorage(c *fiber.Ctx) error {
	views, failures, err := s.deps.Accounts.List(c.Context(), currentUser(c).ID)
	if err != nil {
		return err
	}

	return httpx.OKWithMeta(c, accounts.Summarise(views), httpx.Meta{
		Count:  len(views),
		Errors: failures,
	})
}

// handleDisconnectAccount removes an account and revokes it at Google.
func (s *Server) handleDisconnectAccount(c *fiber.Ctx) error {
	accountID := strings.TrimSpace(c.Params("id"))
	if accountID == "" {
		return apperr.BadRequest("An account id is required.")
	}

	if err := s.deps.Accounts.Disconnect(c.Context(), currentUser(c).ID, accountID); err != nil {
		return err
	}
	return httpx.NoContent(c)
}

type reorderAccountsRequest struct {
	AccountIDs []string `json:"account_ids"`
}

// handleReorderAccounts sets the display order of the account cards.
func (s *Server) handleReorderAccounts(c *fiber.Ctx) error {
	var body reorderAccountsRequest
	if err := c.BodyParser(&body); err != nil {
		return apperr.BadRequest("The request body must be JSON with an account_ids array.").WithCause(err)
	}
	if len(body.AccountIDs) == 0 {
		return apperr.Validation("account_ids must list at least one account.")
	}
	if len(body.AccountIDs) > maxAccountsPerUser {
		return apperr.Validation("account_ids lists more than %d accounts.", maxAccountsPerUser)
	}

	if err := s.deps.Accounts.Reorder(c.Context(), currentUser(c).ID, body.AccountIDs); err != nil {
		return err
	}
	return httpx.NoContent(c)
}
