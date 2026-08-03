package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/auth"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/google"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/store"
)

// storedAccounts reads the caller's accounts straight from the store, for tests
// that need a real account id.
func (h *harness) storedAccounts(t *testing.T) []*store.Account {
	t.Helper()

	user, err := h.store.GetUserByEmail(context.Background(), "owner@example.test")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	accounts, err := h.store.ListAccounts(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	return accounts
}

// stubFile is one canned Drive file.
func stubFile(id, name, mimeType string) *google.File {
	return &google.File{ID: id, Name: name, MimeType: mimeType}
}

func filesOf(t *testing.T, data any) []map[string]any {
	t.Helper()

	raw, ok := data.([]any)
	if !ok {
		t.Fatalf("unexpected files payload: %#v", data)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		file, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("unexpected file entry: %#v", item)
		}
		out = append(out, file)
	}
	return out
}

// --- GET /files ------------------------------------------------------------

func TestFilesRequireASession(t *testing.T) {
	h := newHarness(t)

	resp, env := h.sendJSON(t, httptest.NewRequest(http.MethodGet, "/api/v1/files", nil))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got status %d", resp.StatusCode)
	}
	if env.Error == nil || env.Error.Code != apperr.CodeUnauthorized {
		t.Fatalf("unexpected error %#v", env.Error)
	}
}

func TestListFilesMergesEveryAccount(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)
	h.linkAccount(t, token, "google-sub-2", "second@example.test")

	// both stub accounts share one access token, so one canned page serves both
	h.drive.filesFor["access-for-refresh-token"] = []*google.File{
		stubFile("f1", "notes.txt", "text/plain"),
	}

	resp, env := h.sendJSON(t, h.authedRequest(t, http.MethodGet, "/api/v1/files", token, nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d: %#v", resp.StatusCode, env.Error)
	}

	list := filesOf(t, env.Data)
	if len(list) != 2 {
		t.Fatalf("got %d files, want one per account", len(list))
	}
	if list[0]["kind"] != "text" {
		t.Errorf("file kind not derived: %v", list[0]["kind"])
	}
	if list[0]["account_id"] == "" || list[0]["account_email"] == "" {
		t.Errorf("file not stamped with its account: %#v", list[0])
	}
	if meta := metaOf(t, env.Meta); meta["count"] != float64(2) {
		t.Errorf("meta count: %v", meta["count"])
	}
}

func TestListFilesSurvivesOneBrokenAccount(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)
	h.linkAccount(t, token, "google-sub-2", "second@example.test")

	h.drive.filesFor["access-for-refresh-token"] = []*google.File{
		stubFile("f1", "notes.txt", "text/plain"),
	}
	broken := h.storedAccounts(t)[1]
	h.tokens.errFor[broken.ID] = apperr.ReauthRequired("Google rejected the stored credentials.")

	resp, env := h.sendJSON(t, h.authedRequest(t, http.MethodGet, "/api/v1/files", token, nil))
	// one unhealthy Drive must not blank the browser
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d: %#v", resp.StatusCode, env.Error)
	}

	if list := filesOf(t, env.Data); len(list) != 1 {
		t.Fatalf("got %d files, want the healthy account's 1", len(list))
	}

	meta := metaOf(t, env.Meta)
	failures, ok := meta["errors"].([]any)
	if !ok || len(failures) != 1 {
		t.Fatalf("expected one per-account error, got %#v", meta["errors"])
	}
	failure := failures[0].(map[string]any)
	if failure["account_id"] != broken.ID {
		t.Errorf("failure not tagged with its account: %v", failure["account_id"])
	}
}

func TestListFilesReturnsBreadcrumbs(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)
	account := h.storedAccounts(t)[0]

	h.drive.path = []google.PathSegment{{ID: "folder-1", Name: "Work"}}

	path := "/api/v1/files?account_id=" + account.ID + "&parent=folder-1"
	resp, env := h.sendJSON(t, h.authedRequest(t, http.MethodGet, path, token, nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d: %#v", resp.StatusCode, env.Error)
	}

	crumbs, ok := metaOf(t, env.Meta)["path"].([]any)
	if !ok || len(crumbs) != 1 {
		t.Fatalf("unexpected breadcrumbs: %#v", metaOf(t, env.Meta)["path"])
	}
	if crumbs[0].(map[string]any)["name"] != "Work" {
		t.Errorf("unexpected breadcrumb: %#v", crumbs[0])
	}
}

func TestListFilesRejectsAFolderWithoutItsAccount(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)

	resp, env := h.sendJSON(t,
		h.authedRequest(t, http.MethodGet, "/api/v1/files?parent=folder-1", token, nil))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("got status %d", resp.StatusCode)
	}
	if env.Error.Code != apperr.CodeBadRequest {
		t.Errorf("unexpected error: %#v", env.Error)
	}
}

func TestListFilesValidatesQueryParameters(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)

	for _, query := range []string{
		"?scope=everything",
		"?sort=colour",
		"?direction=sideways",
		"?page_size=0",
		"?page_size=99999",
		"?page=!!!",
	} {
		resp, env := h.sendJSON(t,
			h.authedRequest(t, http.MethodGet, "/api/v1/files"+query, token, nil))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: got status %d", query, resp.StatusCode)
			continue
		}
		if env.Error == nil {
			t.Errorf("%s: missing error payload", query)
		}
	}
}

// --- POST /files/folder ----------------------------------------------------

func TestCreateFolder(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)
	account := h.storedAccounts(t)[0]

	body := map[string]any{"account_id": account.ID, "name": " Invoices ", "parent_id": "folder-1"}
	resp, env := h.sendJSON(t,
		h.authedRequest(t, http.MethodPost, "/api/v1/files/folder", token, body))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("got status %d: %#v", resp.StatusCode, env.Error)
	}

	created, ok := env.Data.(map[string]any)
	if !ok || created["kind"] != "folder" {
		t.Fatalf("unexpected payload: %#v", env.Data)
	}
	if len(h.drive.created) != 1 || h.drive.created[0].Name != "Invoices" {
		t.Fatalf("unexpected create: %#v", h.drive.created)
	}
	if h.drive.created[0].ParentID != "folder-1" {
		t.Errorf("parent not forwarded: %#v", h.drive.created[0])
	}
}

func TestCreateFolderRejectsAnEmptyName(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)
	account := h.storedAccounts(t)[0]

	body := map[string]any{"account_id": account.ID, "name": "   "}
	resp, env := h.sendJSON(t,
		h.authedRequest(t, http.MethodPost, "/api/v1/files/folder", token, body))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d: %#v", resp.StatusCode, env.Error)
	}
}

func TestCreateFolderRequiresCSRF(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)

	req := h.authedRequest(t, http.MethodPost, "/api/v1/files/folder", token,
		map[string]any{"account_id": "acc", "name": "x"})
	req.Header.Del(auth.CSRFHeader)

	resp, _ := h.sendJSON(t, req)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("got status %d, want 403", resp.StatusCode)
	}
}

// --- PATCH /files/{account}/{id} -------------------------------------------

func TestUpdateFileRenames(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)
	account := h.storedAccounts(t)[0]

	path := "/api/v1/files/" + account.ID + "/file-1"
	resp, env := h.sendJSON(t,
		h.authedRequest(t, http.MethodPatch, path, token, map[string]any{"name": "renamed.txt"}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d: %#v", resp.StatusCode, env.Error)
	}

	if len(h.drive.updated) != 1 || h.drive.updated[0].Name == nil {
		t.Fatalf("unexpected update: %#v", h.drive.updated)
	}
	if *h.drive.updated[0].Name != "renamed.txt" {
		t.Errorf("name not forwarded: %q", *h.drive.updated[0].Name)
	}
}

func TestUpdateFileMovesBetweenFolders(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)
	account := h.storedAccounts(t)[0]

	path := "/api/v1/files/" + account.ID + "/file-1"
	resp, env := h.sendJSON(t,
		h.authedRequest(t, http.MethodPatch, path, token, map[string]any{"parent_id": "new-parent"}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d: %#v", resp.StatusCode, env.Error)
	}

	if len(h.drive.updated) != 1 {
		t.Fatalf("got %d updates, want 1", len(h.drive.updated))
	}
	if h.drive.updated[0].AddParent != "new-parent" {
		t.Errorf("new parent not added: %#v", h.drive.updated[0])
	}
	if h.drive.updated[0].RemoveParent != "old-parent" {
		t.Errorf("old parent not removed: %q", h.drive.updated[0].RemoveParent)
	}
}

func TestUpdateFileRejectsAnEmptyChange(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)
	account := h.storedAccounts(t)[0]

	path := "/api/v1/files/" + account.ID + "/file-1"
	resp, _ := h.sendJSON(t, h.authedRequest(t, http.MethodPatch, path, token, map[string]any{}))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422", resp.StatusCode)
	}
}

func TestUpdateFileRefusesAnotherUsersAccount(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)

	resp, env := h.sendJSON(t, h.authedRequest(
		t, http.MethodPatch, "/api/v1/files/acc-not-mine/file-1", token,
		map[string]any{"name": "x.txt"},
	))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got status %d: %#v", resp.StatusCode, env.Error)
	}
}

// --- DELETE /files/{account}/{id} ------------------------------------------

func TestDeleteFileTrashesByDefault(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)
	account := h.storedAccounts(t)[0]

	path := "/api/v1/files/" + account.ID + "/file-1"
	resp, _ := h.sendJSON(t, h.authedRequest(t, http.MethodDelete, path, token, nil))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("got status %d, want 204", resp.StatusCode)
	}

	if len(h.drive.deleted) != 0 {
		t.Fatalf("a plain delete erased the file: %#v", h.drive.deleted)
	}
	if len(h.drive.updated) != 1 || h.drive.updated[0].Trashed == nil || !*h.drive.updated[0].Trashed {
		t.Fatalf("file was not trashed: %#v", h.drive.updated)
	}
}

func TestDeleteFilePermanently(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)
	account := h.storedAccounts(t)[0]

	path := "/api/v1/files/" + account.ID + "/file-1?permanent=true"
	resp, _ := h.sendJSON(t, h.authedRequest(t, http.MethodDelete, path, token, nil))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("got status %d, want 204", resp.StatusCode)
	}

	if len(h.drive.deleted) != 1 || h.drive.deleted[0] != "file-1" {
		t.Fatalf("file was not erased: %#v", h.drive.deleted)
	}
}

func TestDeleteFileRejectsABadPermanentFlag(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)
	account := h.storedAccounts(t)[0]

	path := "/api/v1/files/" + account.ID + "/file-1?permanent=maybe"
	resp, _ := h.sendJSON(t, h.authedRequest(t, http.MethodDelete, path, token, nil))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400", resp.StatusCode)
	}
}
