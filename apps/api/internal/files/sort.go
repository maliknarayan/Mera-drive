package files

import (
	"sort"
	"strings"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/google"
)

// SortField is a client-facing sort key.
type SortField string

const (
	SortName       SortField = "name"
	SortModifiedAt SortField = "modified_at"
	SortSize       SortField = "size"
	SortAccount    SortField = "account_email"
)

// Direction is a sort direction.
type Direction string

const (
	Ascending  Direction = "asc"
	Descending Direction = "desc"
)

// Sort is a validated sort specification.
type Sort struct {
	Field     SortField
	Direction Direction
}

// DefaultSort matches what a file browser is expected to open with.
func DefaultSort() Sort { return Sort{Field: SortName, Direction: Ascending} }

// ParseSort validates client input.
func ParseSort(field, direction string) (Sort, error) {
	sortSpec := DefaultSort()

	if field != "" {
		switch SortField(field) {
		case SortName, SortModifiedAt, SortSize, SortAccount:
			sortSpec.Field = SortField(field)
		default:
			return Sort{}, apperr.BadRequest("Cannot sort by %q.", field)
		}
	}

	if direction != "" {
		switch Direction(direction) {
		case Ascending, Descending:
			sortSpec.Direction = Direction(direction)
		default:
			return Sort{}, apperr.BadRequest("Sort direction must be asc or desc, got %q.", direction)
		}
	}
	return sortSpec, nil
}

// driveOrderBy translates a sort into Drive's orderBy clause.
//
// `folder` first keeps folders above files, which is what every file browser
// does. Drive cannot sort by owning account, so that case falls back to name and
// is ordered locally after the merge.
func (s Sort) driveOrderBy() string {
	var key string
	switch s.Field {
	case SortModifiedAt:
		key = "modifiedTime"
	case SortSize:
		// Drive has no `size` sort key; quotaBytesUsed is the equivalent
		key = "quotaBytesUsed"
	default:
		key = "name"
	}

	if s.Direction == Descending {
		return "folder," + key + " desc"
	}
	return "folder," + key
}

// sortFiles orders a merged batch the same way Drive ordered each source page, so
// a merge of several accounts reads as one list.
func (s Sort) sortFiles(views []*FileView) {
	descending := s.Direction == Descending

	sort.SliceStable(views, func(i, j int) bool {
		left, right := views[i], views[j]

		// folders always lead, in both directions
		leftIsFolder, rightIsFolder := left.Kind == kindFolder, right.Kind == kindFolder
		if leftIsFolder != rightIsFolder {
			return leftIsFolder
		}

		if cmp := s.compare(left, right); cmp != 0 {
			if descending {
				return cmp > 0
			}
			return cmp < 0
		}

		// stable tie-breaks so repeated requests agree
		if cmp := strings.Compare(strings.ToLower(left.Name), strings.ToLower(right.Name)); cmp != 0 {
			return cmp < 0
		}
		if cmp := strings.Compare(left.AccountEmail, right.AccountEmail); cmp != 0 {
			return cmp < 0
		}
		return left.ID < right.ID
	})
}

func (s Sort) compare(left, right *FileView) int {
	switch s.Field {
	case SortModifiedAt:
		switch {
		case left.ModifiedAt.Before(right.ModifiedAt):
			return -1
		case left.ModifiedAt.After(right.ModifiedAt):
			return 1
		default:
			return 0
		}

	case SortSize:
		return compareInt64(sizeOrZero(left), sizeOrZero(right))

	case SortAccount:
		return strings.Compare(left.AccountEmail, right.AccountEmail)

	default:
		return strings.Compare(strings.ToLower(left.Name), strings.ToLower(right.Name))
	}
}

// sizeOrZero treats a missing size as zero. Folders and Google-native files
// report none, and they sort together at one end rather than randomly.
func sizeOrZero(view *FileView) int64 {
	if view.Size == nil {
		return 0
	}
	return *view.Size
}

func compareInt64(left, right int64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

// fileKind maps a MIME type to the coarse category the UI uses for icons and
// previews. Mirrors fileKindFromMimeType in packages/shared.
func fileKind(mimeType string) string {
	switch mimeType {
	case google.FolderMimeType:
		return kindFolder
	case "application/vnd.google-apps.document":
		return "gdoc"
	case "application/vnd.google-apps.spreadsheet":
		return "gsheet"
	case "application/vnd.google-apps.presentation":
		return "gslide"
	case "application/pdf":
		return "pdf"
	}

	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	case strings.HasPrefix(mimeType, "text/"):
		return "text"
	case archiveMimeTypes[mimeType]:
		return "archive"
	default:
		return "other"
	}
}

var archiveMimeTypes = map[string]bool{
	"application/zip":             true,
	"application/x-tar":           true,
	"application/gzip":            true,
	"application/x-7z-compressed": true,
	"application/vnd.rar":         true,
}
