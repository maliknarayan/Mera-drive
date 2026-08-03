package files

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/google"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/store"
)

const testUserID = "user-1"

// fakeAccounts stands in for the accounts service.
type fakeAccounts struct {
	mu       sync.Mutex
	accounts []*store.Account
	tokenErr map[string]error
	touched  []string
	noted    []*apperr.Error
}

func newFakeAccounts(ids ...string) *fakeAccounts {
	fake := &fakeAccounts{tokenErr: map[string]error{}}
	for _, id := range ids {
		fake.accounts = append(fake.accounts, &store.Account{
			ID:     id,
			UserID: testUserID,
			Email:  id + "@example.test",
			Status: store.StatusConnected,
		})
	}
	return fake
}

func (f *fakeAccounts) Connected(context.Context, string) ([]*store.Account, error) {
	return f.accounts, nil
}

func (f *fakeAccounts) AccessTokenFor(
	_ context.Context, _, accountID string,
) (*store.Account, string, error) {
	f.mu.Lock()
	err := f.tokenErr[accountID]
	f.mu.Unlock()

	if err != nil {
		return nil, "", err
	}
	for _, account := range f.accounts {
		if account.ID == accountID {
			return account, "token-" + accountID, nil
		}
	}
	return nil, "", apperr.NotFound("That connected account does not exist.")
}

func (f *fakeAccounts) NoteFailure(_ context.Context, _ *store.Account, failure *apperr.Error) {
	f.mu.Lock()
	f.noted = append(f.noted, failure)
	f.mu.Unlock()
}

func (f *fakeAccounts) Touch(_ context.Context, accountID string) {
	f.mu.Lock()
	f.touched = append(f.touched, accountID)
	f.mu.Unlock()
}

func (f *fakeAccounts) CallTimeout() time.Duration { return time.Second }
func (f *fakeAccounts) Concurrency() int           { return 4 }

// fakeDrive records calls and replays canned pages keyed by access token.
type fakeDrive struct {
	mu sync.Mutex

	pages   map[string][]*google.FileList
	listErr map[string]error
	path    []google.PathSegment

	created  []google.CreateFolderRequest
	updated  []google.UpdateFileRequest
	deleted  []string
	getFile  *google.File
	mutateEr error

	listCalls []google.ListOptions
}

func newFakeDrive() *fakeDrive {
	return &fakeDrive{
		pages:   map[string][]*google.FileList{},
		listErr: map[string]error{},
	}
}

func (f *fakeDrive) ListFiles(
	_ context.Context, accessToken string, opts google.ListOptions,
) (*google.FileList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.listCalls = append(f.listCalls, opts)

	if err := f.listErr[accessToken]; err != nil {
		return nil, err
	}

	queue := f.pages[accessToken]
	if len(queue) == 0 {
		return &google.FileList{}, nil
	}
	page := queue[0]
	f.pages[accessToken] = queue[1:]
	return page, nil
}

func (f *fakeDrive) GetFile(_ context.Context, _, fileID string) (*google.File, error) {
	if f.mutateEr != nil {
		return nil, f.mutateEr
	}
	if f.getFile != nil {
		return f.getFile, nil
	}
	return &google.File{ID: fileID, Parents: []string{"old-parent"}}, nil
}

func (f *fakeDrive) ResolvePath(_ context.Context, _, _ string) ([]google.PathSegment, error) {
	return f.path, nil
}

func (f *fakeDrive) CreateFolder(
	_ context.Context, _ string, req google.CreateFolderRequest,
) (*google.File, error) {
	if f.mutateEr != nil {
		return nil, f.mutateEr
	}
	f.created = append(f.created, req)
	return &google.File{ID: "new-folder", Name: req.Name, MimeType: google.FolderMimeType}, nil
}

func (f *fakeDrive) UpdateFile(
	_ context.Context, _, fileID string, req google.UpdateFileRequest,
) (*google.File, error) {
	if f.mutateEr != nil {
		return nil, f.mutateEr
	}
	f.updated = append(f.updated, req)

	file := &google.File{ID: fileID, Name: "file", MimeType: "text/plain"}
	if req.Name != nil {
		file.Name = *req.Name
	}
	if req.Trashed != nil {
		file.Trashed = *req.Trashed
	}
	return file, nil
}

func (f *fakeDrive) DeleteFile(_ context.Context, _, fileID string) error {
	if f.mutateEr != nil {
		return f.mutateEr
	}
	f.deleted = append(f.deleted, fileID)
	return nil
}

func newTestService(accounts *fakeAccounts, drive *fakeDrive) *Service {
	return NewService(accounts, drive, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func fileNamed(id, name, mimeType string) *google.File {
	return &google.File{ID: id, Name: name, MimeType: mimeType}
}

// --- listing ---------------------------------------------------------------

func TestListMergesEveryAccount(t *testing.T) {
	accounts := newFakeAccounts("acc-a", "acc-b")
	drive := newFakeDrive()
	drive.pages["token-acc-a"] = []*google.FileList{{
		Files: []*google.File{fileNamed("1", "alpha.txt", "text/plain")},
	}}
	drive.pages["token-acc-b"] = []*google.FileList{{
		Files: []*google.File{fileNamed("2", "beta.txt", "text/plain")},
	}}

	listing, err := newTestService(accounts, drive).List(context.Background(), ListRequest{
		UserID: testUserID,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(listing.Files) != 2 {
		t.Fatalf("got %d files, want 2", len(listing.Files))
	}
	if listing.Files[0].Name != "alpha.txt" || listing.Files[1].Name != "beta.txt" {
		t.Errorf("unexpected order: %s, %s", listing.Files[0].Name, listing.Files[1].Name)
	}
	if listing.Files[0].AccountEmail != "acc-a@example.test" {
		t.Errorf("file not stamped with its account: %#v", listing.Files[0])
	}
	if listing.NextPage != "" {
		t.Errorf("expected no next page, got %q", listing.NextPage)
	}
}

func TestListFoldersLeadRegardlessOfDirection(t *testing.T) {
	accounts := newFakeAccounts("acc-a")
	drive := newFakeDrive()
	drive.pages["token-acc-a"] = []*google.FileList{{
		Files: []*google.File{
			fileNamed("1", "zebra.txt", "text/plain"),
			fileNamed("2", "apple", google.FolderMimeType),
		},
	}}

	listing, err := newTestService(accounts, drive).List(context.Background(), ListRequest{
		UserID: testUserID,
		Sort:   Sort{Field: SortName, Direction: Descending},
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if listing.Files[0].Kind != kindFolder {
		t.Errorf("folder did not lead: %#v", listing.Files[0])
	}
}

func TestListSurvivesOneBrokenAccount(t *testing.T) {
	accounts := newFakeAccounts("acc-a", "acc-b")
	drive := newFakeDrive()
	drive.pages["token-acc-a"] = []*google.FileList{{
		Files: []*google.File{fileNamed("1", "alpha.txt", "text/plain")},
	}}
	drive.listErr["token-acc-b"] = apperr.UpstreamUnavailable("Google Drive is having trouble.")

	listing, err := newTestService(accounts, drive).List(context.Background(), ListRequest{
		UserID: testUserID,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(listing.Files) != 1 {
		t.Fatalf("got %d files, want the healthy account's 1", len(listing.Files))
	}
	if len(listing.Failures) != 1 {
		t.Fatalf("got %d failures, want 1", len(listing.Failures))
	}
	if listing.Failures[0].AccountID != "acc-b" {
		t.Errorf("failure not tagged with its account: %#v", listing.Failures[0])
	}
}

func TestListRecordsCredentialFailureAgainstTheAccount(t *testing.T) {
	accounts := newFakeAccounts("acc-a")
	drive := newFakeDrive()
	drive.listErr["token-acc-a"] = apperr.ReauthRequired("Google rejected the stored credentials.")

	if _, err := newTestService(accounts, drive).List(context.Background(), ListRequest{
		UserID: testUserID,
	}); err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(accounts.noted) != 1 || accounts.noted[0].Code != apperr.CodeReauthRequired {
		t.Fatalf("credential failure was not recorded: %#v", accounts.noted)
	}
}

func TestListPagesOnlyTheAccountsWithMore(t *testing.T) {
	accounts := newFakeAccounts("acc-a", "acc-b")
	drive := newFakeDrive()
	drive.pages["token-acc-a"] = []*google.FileList{
		{Files: []*google.File{fileNamed("1", "a1", "text/plain")}, NextPageToken: "a-next"},
		{Files: []*google.File{fileNamed("2", "a2", "text/plain")}},
	}
	drive.pages["token-acc-b"] = []*google.FileList{
		{Files: []*google.File{fileNamed("3", "b1", "text/plain")}},
	}

	service := newTestService(accounts, drive)

	first, err := service.List(context.Background(), ListRequest{UserID: testUserID})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if first.NextPage == "" {
		t.Fatal("expected a cursor while one account has more pages")
	}

	second, err := service.List(context.Background(), ListRequest{
		UserID: testUserID,
		Page:   first.NextPage,
	})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}

	if len(second.Files) != 1 || second.Files[0].AccountID != "acc-a" {
		t.Fatalf("second page should hold only the unfinished account: %#v", second.Files)
	}
	if second.NextPage != "" {
		t.Errorf("expected the listing to end, got cursor %q", second.NextPage)
	}
}

func TestListIgnoresForeignAccountsInACursor(t *testing.T) {
	accounts := newFakeAccounts("acc-a")
	drive := newFakeDrive()
	drive.pages["token-acc-a"] = []*google.FileList{{
		Files: []*google.File{fileNamed("1", "a1", "text/plain")},
	}}

	forged := encodeCursor(cursor{"someone-elses-account": "token"})

	listing, err := newTestService(accounts, drive).List(context.Background(), ListRequest{
		UserID: testUserID,
		Page:   forged,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listing.Files) != 0 {
		t.Fatalf("a forged cursor reached %d files", len(listing.Files))
	}
}

func TestListRejectsAMalformedCursor(t *testing.T) {
	service := newTestService(newFakeAccounts("acc-a"), newFakeDrive())

	_, err := service.List(context.Background(), ListRequest{UserID: testUserID, Page: "!!!"})
	if apperr.From(err).Code != apperr.CodeBadRequest {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListRequiresAnAccountToOpenAFolder(t *testing.T) {
	service := newTestService(newFakeAccounts("acc-a"), newFakeDrive())

	_, err := service.List(context.Background(), ListRequest{
		UserID:   testUserID,
		ParentID: "folder-1",
	})
	if apperr.From(err).Code != apperr.CodeBadRequest {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListReturnsBreadcrumbsForAFolder(t *testing.T) {
	accounts := newFakeAccounts("acc-a")
	drive := newFakeDrive()
	drive.path = []google.PathSegment{{ID: "root-child", Name: "Work"}}
	drive.pages["token-acc-a"] = []*google.FileList{{}}

	listing, err := newTestService(accounts, drive).List(context.Background(), ListRequest{
		UserID:    testUserID,
		AccountID: "acc-a",
		ParentID:  "root-child",
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(listing.Path) != 1 || listing.Path[0].Name != "Work" {
		t.Fatalf("unexpected breadcrumbs: %#v", listing.Path)
	}
}

func TestListRejectsAnUnknownAccount(t *testing.T) {
	service := newTestService(newFakeAccounts("acc-a"), newFakeDrive())

	_, err := service.List(context.Background(), ListRequest{
		UserID:    testUserID,
		AccountID: "acc-nope",
	})
	if apperr.From(err).Code != apperr.CodeNotFound {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListScopeReachesDrive(t *testing.T) {
	accounts := newFakeAccounts("acc-a")
	drive := newFakeDrive()
	drive.pages["token-acc-a"] = []*google.FileList{{}}

	if _, err := newTestService(accounts, drive).List(context.Background(), ListRequest{
		UserID: testUserID,
		Scope:  google.ScopeStarred,
	}); err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(drive.listCalls) != 1 || drive.listCalls[0].Scope != google.ScopeStarred {
		t.Fatalf("scope did not reach Drive: %#v", drive.listCalls)
	}
}

func TestPerAccountPageSizeSplitsTheRequest(t *testing.T) {
	cases := []struct {
		requested, accounts, want int
	}{
		{100, 1, 100},
		{100, 4, 25},
		{100, 50, minPerAccountPageSize},
		{5, 4, minPerAccountPageSize},
	}
	for _, tc := range cases {
		if got := perAccountPageSize(tc.requested, tc.accounts); got != tc.want {
			t.Errorf("perAccountPageSize(%d, %d) = %d, want %d",
				tc.requested, tc.accounts, got, tc.want)
		}
	}
}

// --- mutations -------------------------------------------------------------

func TestCreateFolderTrimsAndForwardsTheName(t *testing.T) {
	accounts := newFakeAccounts("acc-a")
	drive := newFakeDrive()

	view, err := newTestService(accounts, drive).CreateFolder(context.Background(), CreateFolderRequest{
		UserID:    testUserID,
		AccountID: "acc-a",
		Name:      "  Invoices  ",
		ParentID:  "parent-1",
	})
	if err != nil {
		t.Fatalf("create folder: %v", err)
	}

	if len(drive.created) != 1 || drive.created[0].Name != "Invoices" {
		t.Fatalf("unexpected create: %#v", drive.created)
	}
	if view.Kind != kindFolder || view.AccountID != "acc-a" {
		t.Errorf("unexpected view: %#v", view)
	}
}

func TestCreateFolderRejectsBadNames(t *testing.T) {
	service := newTestService(newFakeAccounts("acc-a"), newFakeDrive())

	for _, name := range []string{"", "   ", "a/b"} {
		_, err := service.CreateFolder(context.Background(), CreateFolderRequest{
			UserID:    testUserID,
			AccountID: "acc-a",
			Name:      name,
		})
		if apperr.From(err).Code != apperr.CodeValidation {
			t.Errorf("name %q: unexpected error %v", name, err)
		}
	}
}

func TestUpdateRejectsAnEmptyChange(t *testing.T) {
	service := newTestService(newFakeAccounts("acc-a"), newFakeDrive())

	_, err := service.Update(context.Background(), UpdateRequest{
		UserID:    testUserID,
		AccountID: "acc-a",
		FileID:    "file-1",
	})
	if apperr.From(err).Code != apperr.CodeValidation {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateMoveSwapsTheParents(t *testing.T) {
	accounts := newFakeAccounts("acc-a")
	drive := newFakeDrive()
	drive.getFile = &google.File{ID: "file-1", Parents: []string{"old-a", "old-b"}}

	if _, err := newTestService(accounts, drive).Update(context.Background(), UpdateRequest{
		UserID:    testUserID,
		AccountID: "acc-a",
		FileID:    "file-1",
		MoveTo:    "new-parent",
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	if len(drive.updated) != 1 {
		t.Fatalf("got %d updates, want 1", len(drive.updated))
	}
	if drive.updated[0].AddParent != "new-parent" {
		t.Errorf("new parent not added: %#v", drive.updated[0])
	}
	if drive.updated[0].RemoveParent != "old-a,old-b" {
		t.Errorf("old parents not removed: %q", drive.updated[0].RemoveParent)
	}
}

func TestDeleteTrashesByDefault(t *testing.T) {
	accounts := newFakeAccounts("acc-a")
	drive := newFakeDrive()

	if err := newTestService(accounts, drive).
		Delete(context.Background(), testUserID, "acc-a", "file-1", false); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if len(drive.deleted) != 0 {
		t.Fatalf("a plain delete erased the file: %#v", drive.deleted)
	}
	if len(drive.updated) != 1 || drive.updated[0].Trashed == nil || !*drive.updated[0].Trashed {
		t.Fatalf("file was not trashed: %#v", drive.updated)
	}
}

func TestDeletePermanentlyErasesTheFile(t *testing.T) {
	accounts := newFakeAccounts("acc-a")
	drive := newFakeDrive()

	if err := newTestService(accounts, drive).
		Delete(context.Background(), testUserID, "acc-a", "file-1", true); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if len(drive.deleted) != 1 || drive.deleted[0] != "file-1" {
		t.Fatalf("file was not erased: %#v", drive.deleted)
	}
}

func TestMutationsRefuseAnUnknownAccount(t *testing.T) {
	accounts := newFakeAccounts("acc-a")
	accounts.tokenErr["acc-a"] = apperr.NotFound("That connected account does not exist.")

	err := newTestService(accounts, newFakeDrive()).
		Delete(context.Background(), testUserID, "acc-a", "file-1", true)
	if apperr.From(err).Code != apperr.CodeNotFound {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMutationFailureIsTaggedWithItsAccount(t *testing.T) {
	accounts := newFakeAccounts("acc-a")
	drive := newFakeDrive()
	drive.mutateEr = errors.New("boom")

	_, err := newTestService(accounts, drive).CreateFolder(context.Background(), CreateFolderRequest{
		UserID:    testUserID,
		AccountID: "acc-a",
		Name:      "Invoices",
	})
	if got := apperr.From(err); got.AccountID != "acc-a" {
		t.Fatalf("failure not tagged with its account: %#v", got)
	}
}

func TestSuccessfulCallsTouchTheAccount(t *testing.T) {
	accounts := newFakeAccounts("acc-a")
	drive := newFakeDrive()
	drive.pages["token-acc-a"] = []*google.FileList{{
		Files: []*google.File{fileNamed("1", "a1", "text/plain")},
	}}

	if _, err := newTestService(accounts, drive).List(context.Background(), ListRequest{
		UserID: testUserID,
	}); err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(accounts.touched) != 1 || accounts.touched[0] != "acc-a" {
		t.Fatalf("account usage was not recorded: %#v", accounts.touched)
	}
}
