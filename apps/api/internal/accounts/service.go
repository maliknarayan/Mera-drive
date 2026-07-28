// Package accounts orchestrates the connected Google accounts of one user.
//
// It is the only place that fans a request out across several Drives. The rule it
// exists to enforce: one unhealthy account must never take down the view of the
// healthy ones. Every per-account failure is collected and reported alongside the
// results it did get, tagged with the account it belongs to.
package accounts

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/google"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/store"
)

// TokenOpener decrypts stored refresh tokens.
type TokenOpener interface {
	OpenRefreshToken(sealed string) (string, error)
}

// AccessTokens mints and caches Google access tokens.
type AccessTokens interface {
	AccessToken(ctx context.Context, accountID, refreshToken string) (string, error)
	Forget(accountID string)
}

// DriveClient is the slice of the Drive API this package needs.
type DriveClient interface {
	About(ctx context.Context, accessToken string) (*google.About, error)
}

// Revoker asks Google to invalidate a refresh token.
type Revoker interface {
	Revoke(ctx context.Context, token string) error
}

// Service reads and mutates a user's connected accounts.
type Service struct {
	store       store.Store
	opener      TokenOpener
	tokens      AccessTokens
	drive       DriveClient
	revoker     Revoker
	concurrency int
	timeout     time.Duration
	log         *slog.Logger
}

// Config are the tunables from the environment.
type Config struct {
	// Concurrency bounds simultaneous Google calls per fan-out. Raising it makes
	// 429s more likely, not throughput higher.
	Concurrency int
	// Timeout bounds one Google call.
	Timeout time.Duration
}

// NewService builds an accounts service.
func NewService(
	st store.Store,
	opener TokenOpener,
	tokens AccessTokens,
	drive DriveClient,
	revoker Revoker,
	cfg Config,
	log *slog.Logger,
) *Service {
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &Service{
		store:       st,
		opener:      opener,
		tokens:      tokens,
		drive:       drive,
		revoker:     revoker,
		concurrency: cfg.Concurrency,
		timeout:     cfg.Timeout,
		log:         log,
	}
}

// View is one connected account as the client sees it. It carries no credentials.
type View struct {
	ID          string              `json:"id"`
	Email       string              `json:"email"`
	Name        string              `json:"name"`
	AvatarURL   string              `json:"avatar_url"`
	Scope       store.Scope         `json:"scope"`
	Status      store.AccountStatus `json:"status"`
	ConnectedAt time.Time           `json:"connected_at"`
	LastUsedAt  *time.Time          `json:"last_used_at"`
	// Quota is absent when the account is unusable or the live call failed.
	Quota *google.StorageQuota `json:"quota,omitempty"`
	// StatusReason explains a non-connected status in words the user can act on.
	StatusReason string `json:"status_reason,omitempty"`
}

// Summary aggregates every account, for the dashboard's top cards.
type Summary struct {
	// TotalLimit sums only accounts that report a cap; nil when none do.
	TotalLimit *int64 `json:"total_limit"`
	TotalUsage int64  `json:"total_usage"`
	TotalFree  *int64 `json:"total_free"`

	AccountCount   int `json:"account_count"`
	ConnectedCount int `json:"connected_count"`
	// UnlimitedCount is excluded from TotalLimit, so the UI can say so.
	UnlimitedCount int `json:"unlimited_count"`
}

// List returns every connected account with its live storage quota.
//
// The second return value holds per-account failures. A non-empty slice alongside
// a non-empty result set is the normal partial-success case, not an error.
func (s *Service) List(ctx context.Context, userID string) ([]*View, []*apperr.Error, error) {
	stored, err := s.store.ListAccounts(ctx, userID)
	if err != nil {
		return nil, nil, apperr.Internal("Could not read your connected accounts.").WithCause(err)
	}

	views := make([]*View, len(stored))
	// index-aligned so goroutines never contend, compacted after the wait
	failures := make([]*apperr.Error, len(stored))

	var group errgroup.Group
	group.SetLimit(s.concurrency)

	for i, account := range stored {
		views[i] = newView(account)

		if account.Status == store.StatusDisconnected {
			continue
		}

		index, account := i, account
		group.Go(func() error {
			callCtx, cancel := context.WithTimeout(ctx, s.timeout)
			defer cancel()

			quota, err := s.fetchQuota(callCtx, account)
			if err != nil {
				failure := tagged(err, account.ID)
				failures[index] = failure
				s.applyFailure(ctx, views[index], account, failure)
				return nil
			}
			views[index].Quota = quota
			return nil
		})
	}
	// every goroutine returns nil: a partial result beats a blanket failure
	_ = group.Wait()

	return views, compact(failures), nil
}

// Summarise aggregates views into the dashboard totals.
//
// Accounts with unlimited storage are counted separately rather than folded into
// the limit, because adding them in would understate the user's free space.
func Summarise(views []*View) Summary {
	summary := Summary{AccountCount: len(views)}

	var limited int64
	var anyLimited bool

	for _, view := range views {
		if view.Status == store.StatusConnected {
			summary.ConnectedCount++
		}
		if view.Quota == nil {
			continue
		}

		summary.TotalUsage += view.Quota.Usage

		if view.Quota.Limit == nil {
			summary.UnlimitedCount++
			continue
		}
		limited += *view.Quota.Limit
		anyLimited = true
	}

	if anyLimited {
		summary.TotalLimit = &limited

		free := limited - summary.TotalUsage
		if free < 0 {
			free = 0
		}
		summary.TotalFree = &free
	}

	return summary
}

// Disconnect removes an account and asks Google to forget the grant.
func (s *Service) Disconnect(ctx context.Context, userID, accountID string) error {
	account, err := s.store.GetAccount(ctx, userID, accountID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apperr.NotFound("That connected account does not exist.")
		}
		return apperr.Internal("Could not read the connected account.").WithCause(err)
	}

	// best effort: a Google outage must not trap credentials on this server
	if refreshToken, openErr := s.opener.OpenRefreshToken(account.RefreshTokenEnc); openErr == nil {
		revokeCtx, cancel := context.WithTimeout(ctx, s.timeout)
		defer cancel()

		if revokeErr := s.revoker.Revoke(revokeCtx, refreshToken); revokeErr != nil {
			s.log.Warn("could not revoke google grant on disconnect",
				slog.String("account_id", accountID),
				slog.String("error", revokeErr.Error()),
			)
		}
	}

	s.tokens.Forget(accountID)

	if err := s.store.DeleteAccount(ctx, userID, accountID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apperr.NotFound("That connected account does not exist.")
		}
		return apperr.Internal("Could not disconnect the account.").WithCause(err)
	}
	return nil
}

// Reorder sets the display order of a user's account cards.
//
// orderedIDs must be exactly the user's accounts: a partial list would leave
// cards with duplicate positions and no defined order.
func (s *Service) Reorder(ctx context.Context, userID string, orderedIDs []string) error {
	stored, err := s.store.ListAccounts(ctx, userID)
	if err != nil {
		return apperr.Internal("Could not read your connected accounts.").WithCause(err)
	}

	byID := make(map[string]*store.Account, len(stored))
	for _, account := range stored {
		byID[account.ID] = account
	}

	if len(orderedIDs) != len(stored) {
		return apperr.Validation(
			"The order must list all %d connected accounts, got %d.", len(stored), len(orderedIDs),
		)
	}

	seen := make(map[string]bool, len(orderedIDs))
	for _, id := range orderedIDs {
		if _, ok := byID[id]; !ok {
			return apperr.Validation("Unknown connected account %q.", id)
		}
		if seen[id] {
			return apperr.Validation("Connected account %q appears twice.", id)
		}
		seen[id] = true
	}

	// not a transaction: display order is cosmetic, and a half-applied order is
	// corrected by the next drag
	for position, id := range orderedIDs {
		account := byID[id]
		if account.SortOrder == position {
			continue
		}
		account.SortOrder = position
		if err := s.store.UpdateAccount(ctx, account); err != nil {
			return apperr.Internal("Could not save the account order.").WithCause(err)
		}
	}
	return nil
}

// --- internals -------------------------------------------------------------

func (s *Service) fetchQuota(ctx context.Context, account *store.Account) (*google.StorageQuota, error) {
	refreshToken, err := s.opener.OpenRefreshToken(account.RefreshTokenEnc)
	if err != nil {
		return nil, err
	}

	accessToken, err := s.tokens.AccessToken(ctx, account.ID, refreshToken)
	if err != nil {
		return nil, err
	}

	about, err := s.drive.About(ctx, accessToken)
	if err != nil {
		return nil, err
	}
	return &about.Quota, nil
}

// applyFailure reflects a per-account failure in the view, and persists the
// status when the credentials themselves are the problem.
func (s *Service) applyFailure(
	ctx context.Context, view *View, account *store.Account, failure *apperr.Error,
) {
	view.StatusReason = failure.Message

	if failure.Code != apperr.CodeReauthRequired {
		return
	}

	view.Status = store.StatusReauthRequired
	s.tokens.Forget(account.ID)

	if account.Status == store.StatusReauthRequired {
		return
	}
	if err := s.store.SetAccountStatus(ctx, account.ID, store.StatusReauthRequired); err != nil {
		s.log.Warn("could not mark account as needing reconnection",
			slog.String("account_id", account.ID),
			slog.String("error", err.Error()),
		)
	}
}

func newView(account *store.Account) *View {
	return &View{
		ID:          account.ID,
		Email:       account.Email,
		Name:        account.Name,
		AvatarURL:   account.AvatarURL,
		Scope:       account.Scope,
		Status:      account.Status,
		ConnectedAt: account.ConnectedAt,
		LastUsedAt:  account.LastUsedAt,
	}
}

// tagged copies an error and stamps it with the account it came from, so the
// original — which may be shared — is never mutated.
func tagged(err error, accountID string) *apperr.Error {
	source := apperr.From(err)
	copied := *source
	copied.AccountID = accountID
	return &copied
}

func compact(errs []*apperr.Error) []*apperr.Error {
	var out []*apperr.Error
	for _, err := range errs {
		if err != nil {
			out = append(out, err)
		}
	}
	return out
}
