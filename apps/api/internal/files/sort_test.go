package files

import (
	"testing"
	"time"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/google"
)

func TestParseSortDefaultsToNameAscending(t *testing.T) {
	got, err := ParseSort("", "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got != DefaultSort() {
		t.Errorf("got %#v, want %#v", got, DefaultSort())
	}
}

func TestParseSortRejectsUnknownInput(t *testing.T) {
	if _, err := ParseSort("colour", ""); apperr.From(err).Code != apperr.CodeBadRequest {
		t.Errorf("unknown field: %v", err)
	}
	if _, err := ParseSort("name", "sideways"); apperr.From(err).Code != apperr.CodeBadRequest {
		t.Errorf("unknown direction: %v", err)
	}
}

func TestDriveOrderByAlwaysLeadsWithFolders(t *testing.T) {
	cases := map[Sort]string{
		{Field: SortName, Direction: Ascending}:       "folder,name",
		{Field: SortName, Direction: Descending}:      "folder,name desc",
		{Field: SortModifiedAt, Direction: Ascending}: "folder,modifiedTime",
		{Field: SortSize, Direction: Descending}:      "folder,quotaBytesUsed desc",
		{Field: SortAccount, Direction: Ascending}:    "folder,name",
	}
	for spec, want := range cases {
		if got := spec.driveOrderBy(); got != want {
			t.Errorf("%#v: got %q want %q", spec, got, want)
		}
	}
}

func viewOf(name, email string, size *int64, modified time.Time, mimeType string) *FileView {
	return &FileView{
		ID:           name + email,
		Name:         name,
		Kind:         fileKind(mimeType),
		Size:         size,
		ModifiedAt:   modified,
		AccountEmail: email,
	}
}

func namesOf(views []*FileView) []string {
	out := make([]string, len(views))
	for i, view := range views {
		out[i] = view.Name
	}
	return out
}

func assertOrder(t *testing.T, views []*FileView, want ...string) {
	t.Helper()

	got := namesOf(views)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestSortByNameIsCaseInsensitive(t *testing.T) {
	epoch := time.Unix(0, 0)
	views := []*FileView{
		viewOf("banana", "a@x", nil, epoch, "text/plain"),
		viewOf("Apple", "a@x", nil, epoch, "text/plain"),
	}

	Sort{Field: SortName, Direction: Ascending}.sortFiles(views)
	assertOrder(t, views, "Apple", "banana")
}

func TestSortKeepsFoldersFirstInBothDirections(t *testing.T) {
	epoch := time.Unix(0, 0)
	build := func() []*FileView {
		return []*FileView{
			viewOf("zzz.txt", "a@x", nil, epoch, "text/plain"),
			viewOf("aaa", "a@x", nil, epoch, google.FolderMimeType),
		}
	}

	ascending := build()
	Sort{Field: SortName, Direction: Ascending}.sortFiles(ascending)
	assertOrder(t, ascending, "aaa", "zzz.txt")

	descending := build()
	Sort{Field: SortName, Direction: Descending}.sortFiles(descending)
	assertOrder(t, descending, "aaa", "zzz.txt")
}

func TestSortBySizeTreatsMissingSizeAsZero(t *testing.T) {
	epoch := time.Unix(0, 0)
	big, small := int64(900), int64(10)

	views := []*FileView{
		viewOf("big.bin", "a@x", &big, epoch, "application/octet-stream"),
		viewOf("native.gdoc", "a@x", nil, epoch, "text/plain"),
		viewOf("small.bin", "a@x", &small, epoch, "application/octet-stream"),
	}

	Sort{Field: SortSize, Direction: Ascending}.sortFiles(views)
	assertOrder(t, views, "native.gdoc", "small.bin", "big.bin")
}

func TestSortByModifiedAt(t *testing.T) {
	older := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	views := []*FileView{
		viewOf("older.txt", "a@x", nil, older, "text/plain"),
		viewOf("newer.txt", "a@x", nil, newer, "text/plain"),
	}

	Sort{Field: SortModifiedAt, Direction: Descending}.sortFiles(views)
	assertOrder(t, views, "newer.txt", "older.txt")
}

func TestSortByAccountGroupsDrivesTogether(t *testing.T) {
	epoch := time.Unix(0, 0)
	views := []*FileView{
		viewOf("one.txt", "zoe@x", nil, epoch, "text/plain"),
		viewOf("two.txt", "amy@x", nil, epoch, "text/plain"),
	}

	Sort{Field: SortAccount, Direction: Ascending}.sortFiles(views)
	assertOrder(t, views, "two.txt", "one.txt")
}

func TestSortTieBreaksAreStable(t *testing.T) {
	epoch := time.Unix(0, 0)
	views := []*FileView{
		viewOf("same.txt", "zoe@x", nil, epoch, "text/plain"),
		viewOf("same.txt", "amy@x", nil, epoch, "text/plain"),
	}

	Sort{Field: SortModifiedAt, Direction: Ascending}.sortFiles(views)
	if views[0].AccountEmail != "amy@x" {
		t.Fatalf("tie was not broken by account: %#v", namesOf(views))
	}
}

func TestFileKindCoversTheUICategories(t *testing.T) {
	cases := map[string]string{
		google.FolderMimeType:                      kindFolder,
		"application/vnd.google-apps.document":     "gdoc",
		"application/vnd.google-apps.spreadsheet":  "gsheet",
		"application/vnd.google-apps.presentation": "gslide",
		"application/pdf":                          "pdf",
		"image/png":                                "image",
		"video/mp4":                                "video",
		"audio/mpeg":                               "audio",
		"text/csv":                                 "text",
		"application/zip":                          "archive",
		"application/octet-stream":                 "other",
	}
	for mimeType, want := range cases {
		if got := fileKind(mimeType); got != want {
			t.Errorf("%s: got %q want %q", mimeType, got, want)
		}
	}
}
