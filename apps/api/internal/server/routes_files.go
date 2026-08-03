package server

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/files"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/google"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/httpx"
)

// filesMeta extends the shared envelope metadata with the breadcrumb trail, so a
// folder listing carries its own path without a second round trip.
type filesMeta struct {
	httpx.Meta
	Path []google.PathSegment `json:"path,omitempty"`
}

// handleListFiles returns one merged page of files across the caller's Drives.
//
// Responds 200 with partial data when some accounts fail; the failures are in
// meta.errors, each tagged with its account_id.
func (s *Server) handleListFiles(c *fiber.Ctx) error {
	scope, err := parseScope(c.Query("scope"))
	if err != nil {
		return err
	}

	sortSpec, err := files.ParseSort(c.Query("sort"), c.Query("direction"))
	if err != nil {
		return err
	}

	pageSize, err := parsePageSize(c.Query("page_size"))
	if err != nil {
		return err
	}

	listing, err := s.deps.Files.List(c.Context(), files.ListRequest{
		UserID:    currentUser(c).ID,
		AccountID: strings.TrimSpace(c.Query("account_id")),
		Scope:     scope,
		ParentID:  strings.TrimSpace(c.Query("parent")),
		Sort:      sortSpec,
		PageSize:  pageSize,
		Page:      c.Query("page"),
	})
	if err != nil {
		return err
	}

	return httpx.OKWithMeta(c, listing.Files, filesMeta{
		Meta: httpx.Meta{
			NextPageToken: listing.NextPage,
			Count:         len(listing.Files),
			Errors:        listing.Failures,
		},
		Path: listing.Path,
	})
}

type createFolderRequest struct {
	AccountID string `json:"account_id"`
	Name      string `json:"name"`
	ParentID  string `json:"parent_id"`
}

// handleCreateFolder creates a folder in one Drive.
func (s *Server) handleCreateFolder(c *fiber.Ctx) error {
	var body createFolderRequest
	if err := c.BodyParser(&body); err != nil {
		return apperr.BadRequest("The request body must be JSON with an account_id and a name.").WithCause(err)
	}
	if strings.TrimSpace(body.AccountID) == "" {
		return apperr.Validation("account_id is required.")
	}

	view, err := s.deps.Files.CreateFolder(c.Context(), files.CreateFolderRequest{
		UserID:    currentUser(c).ID,
		AccountID: strings.TrimSpace(body.AccountID),
		Name:      body.Name,
		ParentID:  strings.TrimSpace(body.ParentID),
	})
	if err != nil {
		return err
	}
	return httpx.Created(c, view)
}

// updateFileRequest is a partial update. Absent fields are left untouched, which
// is why every one of them is a pointer.
type updateFileRequest struct {
	Name    *string `json:"name"`
	Starred *bool   `json:"starred"`
	Trashed *bool   `json:"trashed"`
	// ParentID moves the file into another folder in the same Drive.
	ParentID *string `json:"parent_id"`
}

// handleUpdateFile renames, stars, trashes or moves a file.
func (s *Server) handleUpdateFile(c *fiber.Ctx) error {
	accountID, fileID, err := fileTarget(c)
	if err != nil {
		return err
	}

	var body updateFileRequest
	if err := c.BodyParser(&body); err != nil {
		return apperr.BadRequest("The request body must be JSON.").WithCause(err)
	}

	req := files.UpdateRequest{
		UserID:    currentUser(c).ID,
		AccountID: accountID,
		FileID:    fileID,
		Name:      body.Name,
		Starred:   body.Starred,
		Trashed:   body.Trashed,
	}
	if body.ParentID != nil {
		req.MoveTo = strings.TrimSpace(*body.ParentID)
		if req.MoveTo == "" {
			return apperr.Validation("parent_id cannot be empty. Omit it to leave the file where it is.")
		}
	}

	view, err := s.deps.Files.Update(c.Context(), req)
	if err != nil {
		return err
	}
	return httpx.OK(c, view)
}

// handleDeleteFile trashes a file, or erases it with ?permanent=true.
func (s *Server) handleDeleteFile(c *fiber.Ctx) error {
	accountID, fileID, err := fileTarget(c)
	if err != nil {
		return err
	}

	permanent, err := parseBool(c.Query("permanent"))
	if err != nil {
		return err
	}

	if err := s.deps.Files.Delete(
		c.Context(), currentUser(c).ID, accountID, fileID, permanent,
	); err != nil {
		return err
	}
	return httpx.NoContent(c)
}

// fileTarget reads the account and file a route acts on.
func fileTarget(c *fiber.Ctx) (string, string, error) {
	accountID := strings.TrimSpace(c.Params("account"))
	fileID := strings.TrimSpace(c.Params("id"))

	if accountID == "" || fileID == "" {
		return "", "", apperr.BadRequest("An account id and a file id are required.")
	}
	return accountID, fileID, nil
}

func parseScope(raw string) (google.ListScope, error) {
	switch google.ListScope(raw) {
	case "":
		return google.ScopeChildren, nil
	case google.ScopeChildren, google.ScopeStarred, google.ScopeRecent, google.ScopeTrash:
		return google.ListScope(raw), nil
	default:
		return "", apperr.BadRequest("Unknown scope %q.", raw)
	}
}

func parsePageSize(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}

	size, err := strconv.Atoi(raw)
	if err != nil || size < 1 {
		return 0, apperr.BadRequest("page_size must be a positive number, got %q.", raw)
	}
	if size > files.MaxPageSize {
		return 0, apperr.BadRequest("page_size may not exceed %d.", files.MaxPageSize)
	}
	return size, nil
}

func parseBool(raw string) (bool, error) {
	if raw == "" {
		return false, nil
	}

	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, apperr.BadRequest("Expected true or false, got %q.", raw)
	}
	return value, nil
}
