package google

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
)

// FolderMimeType is the MIME type Drive uses for folders.
const FolderMimeType = "application/vnd.google-apps.folder"

// RootFolderAlias is Drive's alias for an account's top-level folder.
const RootFolderAlias = "root"

// maxPathDepth bounds the ancestor walk when building breadcrumbs. Drive allows
// deep nesting; a runaway loop on a cyclic parent chain would be worse than a
// truncated breadcrumb.
const maxPathDepth = 20

// fileFields is the field mask for every file we return. Requesting everything
// would multiply response size across every account on every page.
const fileFields = "id,name,mimeType,size,modifiedTime,createdTime,starred,trashed," +
	"shared,parents,webViewLink,iconLink,thumbnailLink," +
	"owners(displayName,emailAddress,photoLink)," +
	"capabilities(canEdit,canRename,canDelete,canTrash,canShare,canCopy,canAddChildren)"

// FileOwner is a file's owner as Drive reports it.
type FileOwner struct {
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	PhotoURL    string `json:"photo_url"`
}

// Capabilities is what the signed-in account may do with a file. Forwarded so the
// UI can disable actions Google would reject, rather than guessing from scope.
type Capabilities struct {
	CanEdit        bool `json:"can_edit"`
	CanRename      bool `json:"can_rename"`
	CanDelete      bool `json:"can_delete"`
	CanTrash       bool `json:"can_trash"`
	CanShare       bool `json:"can_share"`
	CanCopy        bool `json:"can_copy"`
	CanAddChildren bool `json:"can_add_children"`
}

// File is a Drive file or folder.
type File struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	// Size is nil for folders and Google-native files, which report no size.
	Size          *int64       `json:"size"`
	ModifiedAt    time.Time    `json:"modified_at"`
	CreatedAt     time.Time    `json:"created_at"`
	Starred       bool         `json:"starred"`
	Trashed       bool         `json:"trashed"`
	Shared        bool         `json:"shared"`
	Parents       []string     `json:"parents"`
	WebViewLink   string       `json:"web_view_link"`
	IconLink      string       `json:"icon_link"`
	ThumbnailLink string       `json:"thumbnail_link"`
	Owner         *FileOwner   `json:"owner"`
	Capabilities  Capabilities `json:"capabilities"`
}

// IsFolder reports whether the file is a folder.
func (f *File) IsFolder() bool { return f.MimeType == FolderMimeType }

// IsGoogleNative reports whether the file is a Docs/Sheets/Slides style document,
// which has no byte content to download directly.
func (f *File) IsGoogleNative() bool {
	return strings.HasPrefix(f.MimeType, "application/vnd.google-apps.")
}

// FileList is one page of files from one account.
type FileList struct {
	Files         []*File
	NextPageToken string
}

// ListScope selects which slice of a Drive to list.
type ListScope string

const (
	// ScopeChildren lists the contents of one folder.
	ScopeChildren ListScope = "children"
	// ScopeStarred lists starred files anywhere in the Drive.
	ScopeStarred ListScope = "starred"
	// ScopeRecent lists recently modified files anywhere in the Drive.
	ScopeRecent ListScope = "recent"
	// ScopeTrash lists trashed files.
	ScopeTrash ListScope = "trash"
)

// ListOptions describes one page request against one account.
type ListOptions struct {
	Scope ListScope
	// ParentID applies to ScopeChildren. Empty means the account's root.
	ParentID string
	// OrderBy is a Drive orderBy clause, already including any " desc" suffix.
	OrderBy   string
	PageSize  int
	PageToken string
}

// ListFiles fetches one page of files.
func (d *Drive) ListFiles(ctx context.Context, accessToken string, opts ListOptions) (*FileList, error) {
	query := url.Values{
		"fields":                    {"nextPageToken,files(" + fileFields + ")"},
		"q":                         {buildListQuery(opts)},
		"pageSize":                  {strconv.Itoa(opts.PageSize)},
		"supportsAllDrives":         {"true"},
		"includeItemsFromAllDrives": {"true"},
		// spamming the whole corpus is what makes shared drives work here
		"corpora": {"allDrives"},
	}
	if opts.OrderBy != "" {
		query.Set("orderBy", opts.OrderBy)
	}
	if opts.PageToken != "" {
		query.Set("pageToken", opts.PageToken)
	}

	var raw struct {
		NextPageToken string      `json:"nextPageToken"`
		Files         []*fileWire `json:"files"`
	}
	if err := d.getJSON(ctx, accessToken, "/files", query, &raw); err != nil {
		return nil, err
	}

	files := make([]*File, 0, len(raw.Files))
	for _, wire := range raw.Files {
		files = append(files, wire.toFile())
	}
	return &FileList{Files: files, NextPageToken: raw.NextPageToken}, nil
}

// buildListQuery assembles Drive's `q` parameter for a scope.
func buildListQuery(opts ListOptions) string {
	var clauses []string

	switch opts.Scope {
	case ScopeStarred:
		clauses = append(clauses, "starred = true", "trashed = false")
	case ScopeTrash:
		clauses = append(clauses, "trashed = true")
	case ScopeRecent:
		clauses = append(clauses,
			"trashed = false",
			// folders are containers, not recent activity
			"mimeType != '"+FolderMimeType+"'",
		)
	default:
		parent := opts.ParentID
		if parent == "" {
			parent = RootFolderAlias
		}
		clauses = append(clauses, "'"+escapeQueryValue(parent)+"' in parents", "trashed = false")
	}

	return strings.Join(clauses, " and ")
}

// GetFile fetches one file's metadata.
func (d *Drive) GetFile(ctx context.Context, accessToken, fileID string) (*File, error) {
	query := url.Values{
		"fields":            {fileFields},
		"supportsAllDrives": {"true"},
	}

	var wire fileWire
	if err := d.getJSON(ctx, accessToken, "/files/"+url.PathEscape(fileID), query, &wire); err != nil {
		return nil, err
	}
	return wire.toFile(), nil
}

// PathSegment is one ancestor in a folder's path.
type PathSegment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ResolvePath walks a folder's ancestors and returns them root-first.
//
// The walk is sequential because each step depends on the previous parent, so it
// costs one Google call per level. Depth is bounded by maxPathDepth.
func (d *Drive) ResolvePath(ctx context.Context, accessToken, folderID string) ([]PathSegment, error) {
	if folderID == "" || folderID == RootFolderAlias {
		return nil, nil
	}

	query := url.Values{"fields": {"id,name,parents"}, "supportsAllDrives": {"true"}}

	var reversed []PathSegment
	seen := make(map[string]bool, maxPathDepth)
	current := folderID

	for depth := 0; depth < maxPathDepth && current != "" && current != RootFolderAlias; depth++ {
		if seen[current] {
			// a cyclic parent chain should not hang the request
			break
		}
		seen[current] = true

		var node struct {
			ID      string   `json:"id"`
			Name    string   `json:"name"`
			Parents []string `json:"parents"`
		}
		if err := d.getJSON(ctx, accessToken, "/files/"+url.PathEscape(current), query, &node); err != nil {
			// a partial breadcrumb beats failing the whole listing
			if len(reversed) > 0 {
				break
			}
			return nil, err
		}

		reversed = append(reversed, PathSegment{ID: node.ID, Name: node.Name})
		if len(node.Parents) == 0 {
			break
		}
		current = node.Parents[0]
	}

	// collected leaf-first, returned root-first
	path := make([]PathSegment, 0, len(reversed))
	for i := len(reversed) - 1; i >= 0; i-- {
		path = append(path, reversed[i])
	}
	return path, nil
}

// CreateFolderRequest describes a new folder.
type CreateFolderRequest struct {
	Name     string
	ParentID string
}

// CreateFolder creates a folder and returns it.
//
// Not retried: a retried create would leave two folders behind.
func (d *Drive) CreateFolder(ctx context.Context, accessToken string, req CreateFolderRequest) (*File, error) {
	body := map[string]any{
		"name":     req.Name,
		"mimeType": FolderMimeType,
	}
	if req.ParentID != "" {
		body["parents"] = []string{req.ParentID}
	}

	var wire fileWire
	err := d.call(ctx, accessToken, callSpec{
		method: "POST",
		path:   "/files",
		query:  url.Values{"fields": {fileFields}, "supportsAllDrives": {"true"}},
		body:   body,
	}, &wire)
	if err != nil {
		return nil, err
	}
	return wire.toFile(), nil
}

// UpdateFileRequest is a partial update. Nil fields are left untouched.
type UpdateFileRequest struct {
	Name    *string
	Starred *bool
	Trashed *bool
	// AddParent and RemoveParent move the file between folders.
	AddParent    string
	RemoveParent string
}

// empty reports whether the request would change nothing.
func (r UpdateFileRequest) empty() bool {
	return r.Name == nil && r.Starred == nil && r.Trashed == nil &&
		r.AddParent == "" && r.RemoveParent == ""
}

// UpdateFile applies a partial update. Idempotent, so it is retried.
func (d *Drive) UpdateFile(
	ctx context.Context, accessToken, fileID string, req UpdateFileRequest,
) (*File, error) {
	if req.empty() {
		return nil, apperr.Validation("Nothing to update.")
	}

	body := map[string]any{}
	if req.Name != nil {
		body["name"] = *req.Name
	}
	if req.Starred != nil {
		body["starred"] = *req.Starred
	}
	if req.Trashed != nil {
		body["trashed"] = *req.Trashed
	}

	query := url.Values{"fields": {fileFields}, "supportsAllDrives": {"true"}}
	if req.AddParent != "" {
		query.Set("addParents", req.AddParent)
	}
	if req.RemoveParent != "" {
		query.Set("removeParents", req.RemoveParent)
	}

	var wire fileWire
	err := d.call(ctx, accessToken, callSpec{
		method:     "PATCH",
		path:       "/files/" + url.PathEscape(fileID),
		query:      query,
		body:       body,
		idempotent: true,
	}, &wire)
	if err != nil {
		return nil, err
	}
	return wire.toFile(), nil
}

// CopyFileRequest describes a copy.
type CopyFileRequest struct {
	Name     string
	ParentID string
}

// CopyFile duplicates a file inside the same Drive. Not retried.
func (d *Drive) CopyFile(
	ctx context.Context, accessToken, fileID string, req CopyFileRequest,
) (*File, error) {
	body := map[string]any{}
	if req.Name != "" {
		body["name"] = req.Name
	}
	if req.ParentID != "" {
		body["parents"] = []string{req.ParentID}
	}

	var wire fileWire
	err := d.call(ctx, accessToken, callSpec{
		method: "POST",
		path:   "/files/" + url.PathEscape(fileID) + "/copy",
		query:  url.Values{"fields": {fileFields}, "supportsAllDrives": {"true"}},
		body:   body,
	}, &wire)
	if err != nil {
		return nil, err
	}
	return wire.toFile(), nil
}

// DeleteFile permanently deletes a file, bypassing the trash.
func (d *Drive) DeleteFile(ctx context.Context, accessToken, fileID string) error {
	return d.call(ctx, accessToken, callSpec{
		method:     "DELETE",
		path:       "/files/" + url.PathEscape(fileID),
		query:      url.Values{"supportsAllDrives": {"true"}},
		idempotent: true,
	}, nil)
}

// --- wire format -----------------------------------------------------------

// fileWire mirrors Drive's JSON, where sizes are strings and times are RFC 3339.
type fileWire struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	MimeType      string   `json:"mimeType"`
	Size          *string  `json:"size"`
	ModifiedTime  string   `json:"modifiedTime"`
	CreatedTime   string   `json:"createdTime"`
	Starred       bool     `json:"starred"`
	Trashed       bool     `json:"trashed"`
	Shared        bool     `json:"shared"`
	Parents       []string `json:"parents"`
	WebViewLink   string   `json:"webViewLink"`
	IconLink      string   `json:"iconLink"`
	ThumbnailLink string   `json:"thumbnailLink"`
	Owners        []struct {
		DisplayName  string `json:"displayName"`
		EmailAddress string `json:"emailAddress"`
		PhotoLink    string `json:"photoLink"`
	} `json:"owners"`
	Capabilities struct {
		CanEdit        bool `json:"canEdit"`
		CanRename      bool `json:"canRename"`
		CanDelete      bool `json:"canDelete"`
		CanTrash       bool `json:"canTrash"`
		CanShare       bool `json:"canShare"`
		CanCopy        bool `json:"canCopy"`
		CanAddChildren bool `json:"canAddChildren"`
	} `json:"capabilities"`
}

func (w *fileWire) toFile() *File {
	file := &File{
		ID:            w.ID,
		Name:          w.Name,
		MimeType:      w.MimeType,
		Size:          parseSize(w.Size),
		ModifiedAt:    parseDriveTime(w.ModifiedTime),
		CreatedAt:     parseDriveTime(w.CreatedTime),
		Starred:       w.Starred,
		Trashed:       w.Trashed,
		Shared:        w.Shared,
		Parents:       w.Parents,
		WebViewLink:   w.WebViewLink,
		IconLink:      w.IconLink,
		ThumbnailLink: w.ThumbnailLink,
		Capabilities: Capabilities{
			CanEdit:        w.Capabilities.CanEdit,
			CanRename:      w.Capabilities.CanRename,
			CanDelete:      w.Capabilities.CanDelete,
			CanTrash:       w.Capabilities.CanTrash,
			CanShare:       w.Capabilities.CanShare,
			CanCopy:        w.Capabilities.CanCopy,
			CanAddChildren: w.Capabilities.CanAddChildren,
		},
	}
	if len(w.Owners) > 0 {
		file.Owner = &FileOwner{
			DisplayName: w.Owners[0].DisplayName,
			Email:       w.Owners[0].EmailAddress,
			PhotoURL:    w.Owners[0].PhotoLink,
		}
	}
	if file.Parents == nil {
		file.Parents = []string{}
	}
	return file
}

func parseSize(raw *string) *int64 {
	if raw == nil || *raw == "" {
		return nil
	}
	value, err := strconv.ParseInt(*raw, 10, 64)
	if err != nil {
		return nil
	}
	return &value
}

func parseDriveTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

// escapeQueryValue escapes a value for interpolation into Drive's `q` syntax,
// where strings are single-quoted and backslash is the escape character.
func escapeQueryValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `'`, `\'`)
}
