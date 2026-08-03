// Package files serves the unified file browser.
//
// It is the second fan-out in the system, after accounts: one listing request
// becomes one Drive call per connected account, merged into a single ordered
// page. The same rule applies here — one unhealthy Drive must never blank the
// browser for the healthy ones, so per-account failures travel alongside the
// files they did not stop.
//
// Nothing here is cached or persisted. Every listing is live from Google.
package files

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/google"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/store"
)

const (
	// maxAccountsPerListing bounds one fan-out. It matches the per-user account
	// ceiling, so in practice it only guards against a forged cursor.
	maxAccountsPerListing = 50

	// DefaultPageSize is the page a client gets when it asks for nothing.
	DefaultPageSize = 100
	// MaxPageSize caps what a client may ask for.
	MaxPageSize = 500
	// minPerAccountPageSize keeps a wide fan-out from degenerating into a
	// couple of files per Drive, which would page forever.
	minPerAccountPageSize = 10

	// maxNameLength is the longest name we accept. Drive itself allows far more,
	// but a name longer than this breaks as a filename on download.
	maxNameLength = 255

	kindFolder = "folder"
)

// Accounts is the slice of the accounts service this package needs. It is the
// only route to Drive credentials, so ownership is enforced in one place.
type Accounts interface {
	Connected(ctx context.Context, userID string) ([]*store.Account, error)
	AccessTokenFor(ctx context.Context, userID, accountID string) (*store.Account, string, error)
	NoteFailure(ctx context.Context, account *store.Account, failure *apperr.Error)
	Touch(ctx context.Context, accountID string)
	CallTimeout() time.Duration
	Concurrency() int
}

// DriveClient is the slice of the Drive API this package needs.
type DriveClient interface {
	ListFiles(ctx context.Context, accessToken string, opts google.ListOptions) (*google.FileList, error)
	GetFile(ctx context.Context, accessToken, fileID string) (*google.File, error)
	ResolvePath(ctx context.Context, accessToken, folderID string) ([]google.PathSegment, error)
	CreateFolder(ctx context.Context, accessToken string, req google.CreateFolderRequest) (*google.File, error)
	UpdateFile(ctx context.Context, accessToken, fileID string, req google.UpdateFileRequest) (*google.File, error)
	DeleteFile(ctx context.Context, accessToken, fileID string) error
}

// Service browses and mutates files across a user's connected Drives.
type Service struct {
	accounts Accounts
	drive    DriveClient
	log      *slog.Logger
}

// NewService builds a files service.
func NewService(accounts Accounts, drive DriveClient, log *slog.Logger) *Service {
	return &Service{accounts: accounts, drive: drive, log: log}
}

// FileView is one Drive file, stamped with the account it came from. The account
// fields are what make a merged listing navigable — without them the client
// cannot tell which Drive to act against. Mirrors DriveFile in packages/shared.
type FileView struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	MimeType      string              `json:"mime_type"`
	Kind          string              `json:"kind"`
	Size          *int64              `json:"size"`
	ModifiedAt    time.Time           `json:"modified_at"`
	CreatedAt     time.Time           `json:"created_at"`
	Starred       bool                `json:"starred"`
	Trashed       bool                `json:"trashed"`
	Shared        bool                `json:"shared"`
	Parents       []string            `json:"parents"`
	WebViewLink   string              `json:"web_view_link"`
	IconLink      string              `json:"icon_link"`
	ThumbnailLink string              `json:"thumbnail_link"`
	Owner         *google.FileOwner   `json:"owner"`
	Capabilities  google.Capabilities `json:"capabilities"`

	AccountID    string `json:"account_id"`
	AccountEmail string `json:"account_email"`
}

// ListRequest is one page of the browser.
type ListRequest struct {
	UserID string
	// AccountID restricts the listing to one Drive. Required to browse into a
	// folder, because a folder id only means something inside its own Drive.
	AccountID string
	Scope     google.ListScope
	ParentID  string
	Sort      Sort
	PageSize  int
	// Page is the opaque cursor from a previous response.
	Page string
}

// Listing is one merged page.
type Listing struct {
	Files []*FileView
	// Path is the breadcrumb trail, root-first. Only a single-account folder
	// listing has one: a merged listing spans several roots.
	Path []google.PathSegment
	// NextPage is empty when every account has been exhausted.
	NextPage string
	// Failures are per-account, each tagged with its account id.
	Failures []*apperr.Error
}

// List returns one merged page across the requested accounts.
//
// Ordering is per page, not global: each Drive is asked for its own slice in the
// same order, and the slices are merged. Google paginates per account and gives
// no cross-account cursor, so a globally sorted stream is not purchasable at any
// reasonable number of calls.
func (s *Service) List(ctx context.Context, req ListRequest) (*Listing, error) {
	if req.Scope == "" {
		req.Scope = google.ScopeChildren
	}
	if req.ParentID != "" && req.AccountID == "" {
		return nil, apperr.BadRequest("Opening a folder needs the account it belongs to.")
	}
	if req.Sort == (Sort{}) {
		req.Sort = DefaultSort()
	}

	resume, err := decodeCursor(req.Page)
	if err != nil {
		return nil, err
	}

	targets, err := s.targets(ctx, req, resume)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return &Listing{Files: []*FileView{}}, nil
	}

	opts := google.ListOptions{
		Scope:    req.Scope,
		ParentID: req.ParentID,
		OrderBy:  req.Sort.driveOrderBy(),
		PageSize: perAccountPageSize(clampPageSize(req.PageSize), len(targets)),
	}

	// index-aligned so the goroutines never contend
	pages := make([][]*FileView, len(targets))
	tokens := make([]string, len(targets))
	failures := make([]*apperr.Error, len(targets))

	var path []google.PathSegment
	var pathErr error

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(s.accounts.Concurrency())

	for i, account := range targets {
		index, account := i, account
		accountOpts := opts
		accountOpts.PageToken = resume[account.ID]

		group.Go(func() error {
			files, next, err := s.listOne(groupCtx, req.UserID, account, accountOpts)
			if err != nil {
				failures[index] = err
				return nil
			}
			pages[index], tokens[index] = files, next
			return nil
		})
	}

	// breadcrumbs run alongside the listing rather than after it
	if req.Scope == google.ScopeChildren && req.ParentID != "" {
		account := targets[0]
		group.Go(func() error {
			path, pathErr = s.resolvePath(groupCtx, req.UserID, account, req.ParentID)
			return nil
		})
	}
	// every goroutine returns nil: a partial page beats a blanket failure
	_ = group.Wait()

	if pathErr != nil {
		s.log.Warn("could not resolve folder path",
			slog.String("folder_id", req.ParentID),
			slog.String("error", pathErr.Error()),
		)
	}

	listing := &Listing{
		Files:    merge(pages),
		Path:     path,
		NextPage: encodeCursor(nextCursor(targets, tokens)),
		Failures: compact(failures),
	}
	req.Sort.sortFiles(listing.Files)
	return listing, nil
}

// CreateFolderRequest describes a new folder in one Drive.
type CreateFolderRequest struct {
	UserID    string
	AccountID string
	Name      string
	// ParentID is the containing folder. Empty means that Drive's root.
	ParentID string
}

// CreateFolder creates a folder and returns it as the browser sees it.
func (s *Service) CreateFolder(ctx context.Context, req CreateFolderRequest) (*FileView, error) {
	name, err := cleanName(req.Name)
	if err != nil {
		return nil, err
	}

	account, token, err := s.accounts.AccessTokenFor(ctx, req.UserID, req.AccountID)
	if err != nil {
		return nil, err
	}

	callCtx, cancel := context.WithTimeout(ctx, s.accounts.CallTimeout())
	defer cancel()

	file, err := s.drive.CreateFolder(callCtx, token, google.CreateFolderRequest{
		Name:     name,
		ParentID: req.ParentID,
	})
	if err != nil {
		return nil, s.fail(ctx, account, err)
	}

	s.accounts.Touch(ctx, account.ID)
	return newFileView(file, account), nil
}

// UpdateRequest is a partial update to one file. Nil fields are left untouched.
type UpdateRequest struct {
	UserID    string
	AccountID string
	FileID    string

	Name    *string
	Starred *bool
	Trashed *bool
	// MoveTo relocates the file into another folder in the same Drive. Drive has
	// no move operation, so this becomes add-parent plus remove-current-parents.
	MoveTo string
}

// Update renames, stars, trashes or moves a file.
func (s *Service) Update(ctx context.Context, req UpdateRequest) (*FileView, error) {
	update := google.UpdateFileRequest{Starred: req.Starred, Trashed: req.Trashed}

	if req.Name != nil {
		name, err := cleanName(*req.Name)
		if err != nil {
			return nil, err
		}
		update.Name = &name
	}
	if update.Name == nil && update.Starred == nil && update.Trashed == nil && req.MoveTo == "" {
		return nil, apperr.Validation("Nothing to update. Send a name, starred, trashed or parent.")
	}

	account, token, err := s.accounts.AccessTokenFor(ctx, req.UserID, req.AccountID)
	if err != nil {
		return nil, err
	}

	callCtx, cancel := context.WithTimeout(ctx, s.accounts.CallTimeout())
	defer cancel()

	if req.MoveTo != "" {
		current, err := s.drive.GetFile(callCtx, token, req.FileID)
		if err != nil {
			return nil, s.fail(ctx, account, err)
		}
		update.AddParent = req.MoveTo
		update.RemoveParent = strings.Join(current.Parents, ",")
	}

	file, err := s.drive.UpdateFile(callCtx, token, req.FileID, update)
	if err != nil {
		return nil, s.fail(ctx, account, err)
	}

	s.accounts.Touch(ctx, account.ID)
	return newFileView(file, account), nil
}

// Delete trashes a file, or erases it when permanent is set.
//
// Trashing is the default because it is recoverable from Drive's own UI;
// permanent deletion is not, and Google offers no undo.
func (s *Service) Delete(ctx context.Context, userID, accountID, fileID string, permanent bool) error {
	if !permanent {
		trashed := true
		_, err := s.Update(ctx, UpdateRequest{
			UserID:    userID,
			AccountID: accountID,
			FileID:    fileID,
			Trashed:   &trashed,
		})
		return err
	}

	account, token, err := s.accounts.AccessTokenFor(ctx, userID, accountID)
	if err != nil {
		return err
	}

	callCtx, cancel := context.WithTimeout(ctx, s.accounts.CallTimeout())
	defer cancel()

	if err := s.drive.DeleteFile(callCtx, token, fileID); err != nil {
		return s.fail(ctx, account, err)
	}

	s.accounts.Touch(ctx, account.ID)
	return nil
}

// --- internals -------------------------------------------------------------

// targets picks the accounts this request fans out over.
//
// A cursor narrows the set to the accounts that still have pages left. Ids in it
// are intersected with the caller's own accounts, so a forged cursor reaches
// nothing a plain request could not.
func (s *Service) targets(
	ctx context.Context, req ListRequest, resume cursor,
) ([]*store.Account, error) {
	connected, err := s.accounts.Connected(ctx, req.UserID)
	if err != nil {
		return nil, err
	}

	targets := make([]*store.Account, 0, len(connected))
	for _, account := range connected {
		if req.AccountID != "" && account.ID != req.AccountID {
			continue
		}
		if len(resume) > 0 {
			if _, hasMore := resume[account.ID]; !hasMore {
				continue
			}
		}
		targets = append(targets, account)
	}

	if req.AccountID != "" && len(targets) == 0 && len(resume) == 0 {
		return nil, apperr.NotFound("That connected account does not exist.")
	}
	if len(targets) > maxAccountsPerListing {
		targets = targets[:maxAccountsPerListing]
	}
	return targets, nil
}

// listOne fetches one account's slice of the page.
func (s *Service) listOne(
	ctx context.Context, userID string, account *store.Account, opts google.ListOptions,
) ([]*FileView, string, *apperr.Error) {
	callCtx, cancel := context.WithTimeout(ctx, s.accounts.CallTimeout())
	defer cancel()

	_, token, err := s.accounts.AccessTokenFor(callCtx, userID, account.ID)
	if err != nil {
		return nil, "", tagged(err, account.ID)
	}

	page, err := s.drive.ListFiles(callCtx, token, opts)
	if err != nil {
		return nil, "", s.fail(ctx, account, err)
	}

	views := make([]*FileView, 0, len(page.Files))
	for _, file := range page.Files {
		views = append(views, newFileView(file, account))
	}

	s.accounts.Touch(ctx, account.ID)
	return views, page.NextPageToken, nil
}

// resolvePath fetches breadcrumbs for a folder.
func (s *Service) resolvePath(
	ctx context.Context, userID string, account *store.Account, folderID string,
) ([]google.PathSegment, error) {
	callCtx, cancel := context.WithTimeout(ctx, s.accounts.CallTimeout())
	defer cancel()

	_, token, err := s.accounts.AccessTokenFor(callCtx, userID, account.ID)
	if err != nil {
		return nil, err
	}
	return s.drive.ResolvePath(callCtx, token, folderID)
}

// fail tags an error with its account and records it against the account when
// the credentials themselves are the problem.
func (s *Service) fail(ctx context.Context, account *store.Account, err error) *apperr.Error {
	failure := tagged(err, account.ID)
	s.accounts.NoteFailure(ctx, account, failure)
	return failure
}

// perAccountPageSize splits a requested page across the fan-out, so a merged
// page stays near what the client asked for. The floor means a very wide fan-out
// can overshoot; a page of ten per Drive is the smallest useful slice.
func perAccountPageSize(requested, accounts int) int {
	if accounts <= 1 {
		return requested
	}
	size := requested / accounts
	if size < minPerAccountPageSize {
		return minPerAccountPageSize
	}
	return size
}

func clampPageSize(requested int) int {
	switch {
	case requested <= 0:
		return DefaultPageSize
	case requested > MaxPageSize:
		return MaxPageSize
	default:
		return requested
	}
}

// nextCursor keeps only the accounts Google says have more pages.
func nextCursor(targets []*store.Account, tokens []string) cursor {
	next := cursor{}
	for i, account := range targets {
		if tokens[i] != "" {
			next[account.ID] = tokens[i]
		}
	}
	return next
}

func merge(pages [][]*FileView) []*FileView {
	total := 0
	for _, page := range pages {
		total += len(page)
	}

	merged := make([]*FileView, 0, total)
	for _, page := range pages {
		merged = append(merged, page...)
	}
	return merged
}

// cleanName validates a user-supplied file or folder name.
func cleanName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	switch {
	case name == "":
		return "", apperr.Validation("A name is required.")
	case len(name) > maxNameLength:
		return "", apperr.Validation("That name is longer than %d characters.", maxNameLength)
	case strings.ContainsAny(name, "/\\"):
		return "", apperr.Validation("A name cannot contain a slash.")
	}
	return name, nil
}

func newFileView(file *google.File, account *store.Account) *FileView {
	return &FileView{
		ID:            file.ID,
		Name:          file.Name,
		MimeType:      file.MimeType,
		Kind:          fileKind(file.MimeType),
		Size:          file.Size,
		ModifiedAt:    file.ModifiedAt,
		CreatedAt:     file.CreatedAt,
		Starred:       file.Starred,
		Trashed:       file.Trashed,
		Shared:        file.Shared,
		Parents:       file.Parents,
		WebViewLink:   file.WebViewLink,
		IconLink:      file.IconLink,
		ThumbnailLink: file.ThumbnailLink,
		Owner:         file.Owner,
		Capabilities:  file.Capabilities,
		AccountID:     account.ID,
		AccountEmail:  account.Email,
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
