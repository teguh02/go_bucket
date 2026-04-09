package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/teguh02/go_bucket/internal/config"
)

// newTestHandler creates a Handler backed by a temporary storage directory.
func newTestHandler(t *testing.T, opts ...func(*config.Config)) (*Handler, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		StorageAPIKey: "test-key",
		Port:          "8080",
		StorageDir:    dir,
		MaxUploadMB:   50,
		AllowOverwrite: true,
		AllowDelete:   true,
		CORSAllowedOrigins: []string{"*"},
		CacheMaxAge:   31536000,
	}
	for _, o := range opts {
		o(cfg)
	}
	return NewHandler(cfg), dir
}

// doRequest is a small helper to execute a request against a handler and decode the JSON body.
func doRequest(t *testing.T, h http.Handler, req *http.Request) (int, map[string]interface{}) {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var body map[string]interface{}
	_ = json.NewDecoder(rr.Body).Decode(&body)
	return rr.Code, body
}

// ---------- Health ----------

func TestHealthHandler(t *testing.T) {
	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	code, body := doRequest(t, http.HandlerFunc(h.HealthHandler), req)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if body["ok"] != true {
		t.Errorf("expected ok=true, got %v", body["ok"])
	}
	if body["time"] == nil {
		t.Errorf("expected time field")
	}
}

func TestHealthHandler_MethodNotAllowed(t *testing.T) {
	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	code, _ := doRequest(t, http.HandlerFunc(h.HealthHandler), req)
	if code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", code)
	}
}

// ---------- Upload (multipart file) ----------

func TestUploadHandler_MultipartFile(t *testing.T) {
	h, _ := newTestHandler(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "hello.txt")
	fmt.Fprint(fw, "hello world")
	_ = mw.WriteField("path", "test/hello.txt")
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	code, body := doRequest(t, http.HandlerFunc(h.UploadHandler), req)

	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, body)
	}
	if body["ok"] != true {
		t.Errorf("expected ok=true, got %v", body)
	}
	if body["path"] != "test/hello.txt" {
		t.Errorf("unexpected path: %v", body["path"])
	}
	// Check hash present
	hash, ok := body["hash"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected hash object, got %T: %v", body["hash"], body["hash"])
	}
	if hash["md5"] == "" || hash["sha1"] == "" {
		t.Errorf("expected non-empty hash values, got %v", hash)
	}
}

func TestUploadHandler_MultipartFile_NoPath(t *testing.T) {
	h, _ := newTestHandler(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "auto.txt")
	fmt.Fprint(fw, "auto filename")
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	code, body := doRequest(t, http.HandlerFunc(h.UploadHandler), req)

	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, body)
	}
	// path defaults to sanitized filename
	if body["path"] != "auto.txt" {
		t.Errorf("expected path=auto.txt, got %v", body["path"])
	}
}

// ---------- Upload (base64) ----------

func TestUploadHandler_Base64Plain(t *testing.T) {
	h, _ := newTestHandler(t)

	content := []byte("base64 content")
	encoded := base64.StdEncoding.EncodeToString(content)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("file", encoded)
	_ = mw.WriteField("path", "base64/plain.txt")
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	code, body := doRequest(t, http.HandlerFunc(h.UploadHandler), req)

	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, body)
	}
	if body["ok"] != true {
		t.Errorf("expected ok=true")
	}
}

func TestUploadHandler_Base64DataURI(t *testing.T) {
	h, _ := newTestHandler(t)

	content := []byte("data uri content")
	encoded := "data:text/plain;base64," + base64.StdEncoding.EncodeToString(content)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("file", encoded)
	_ = mw.WriteField("path", "base64/datauri.txt")
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	code, body := doRequest(t, http.HandlerFunc(h.UploadHandler), req)

	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, body)
	}
	if body["content_type"] != "text/plain" {
		t.Errorf("expected content_type text/plain, got %v", body["content_type"])
	}
}

// ---------- Upload (URL) ----------

func TestUploadHandler_URL(t *testing.T) {
	// Serve a small file via a local test server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "content from url")
	}))
	defer ts.Close()

	h, _ := newTestHandler(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("file", ts.URL+"/remote.txt")
	_ = mw.WriteField("path", "url/remote.txt")
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	code, body := doRequest(t, http.HandlerFunc(h.UploadHandler), req)

	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, body)
	}
	if body["ok"] != true {
		t.Errorf("expected ok=true, got %v", body)
	}
}

func TestUploadHandler_URL_AutoPath(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		fmt.Fprint(w, "fake png")
	}))
	defer ts.Close()

	h, _ := newTestHandler(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("file", ts.URL+"/photo.png")
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	code, body := doRequest(t, http.HandlerFunc(h.UploadHandler), req)

	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, body)
	}
	// path should be derived from URL filename
	if body["path"] != "photo.png" {
		t.Errorf("expected path=photo.png, got %v", body["path"])
	}
}

// ---------- Upload (raw binary body) ----------

func TestUploadHandler_BinaryBody(t *testing.T) {
	h, _ := newTestHandler(t)

	content := []byte("binary content here")
	req := httptest.NewRequest(http.MethodPost, "/api/upload?path=binary/data.bin", bytes.NewReader(content))
	req.Header.Set("Content-Type", "application/octet-stream")
	code, body := doRequest(t, http.HandlerFunc(h.UploadHandler), req)

	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, body)
	}
	if body["path"] != "binary/data.bin" {
		t.Errorf("unexpected path: %v", body["path"])
	}
}

func TestUploadHandler_BinaryBody_XFilePathHeader(t *testing.T) {
	h, _ := newTestHandler(t)

	content := []byte("binary via header")
	req := httptest.NewRequest(http.MethodPost, "/api/upload", bytes.NewReader(content))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-File-Path", "binary/header.bin")
	code, body := doRequest(t, http.HandlerFunc(h.UploadHandler), req)

	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, body)
	}
	if body["path"] != "binary/header.bin" {
		t.Errorf("unexpected path: %v", body["path"])
	}
}

func TestUploadHandler_BinaryBody_NoPath(t *testing.T) {
	h, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/upload", bytes.NewReader([]byte("data")))
	req.Header.Set("Content-Type", "application/octet-stream")
	code, _ := doRequest(t, http.HandlerFunc(h.UploadHandler), req)

	if code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", code)
	}
}

// ---------- Upload (JSON body) ----------

func TestUploadHandler_JSON_Base64(t *testing.T) {
	h, _ := newTestHandler(t)

	content := base64.StdEncoding.EncodeToString([]byte("json base64"))
	body := fmt.Sprintf(`{"file":"%s","path":"json/b64.txt"}`, content)
	req := httptest.NewRequest(http.MethodPost, "/api/upload", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	code, resp := doRequest(t, http.HandlerFunc(h.UploadHandler), req)

	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, resp)
	}
	if resp["path"] != "json/b64.txt" {
		t.Errorf("unexpected path: %v", resp["path"])
	}
}

func TestUploadHandler_JSON_URL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "from json url")
	}))
	defer ts.Close()

	h, _ := newTestHandler(t)

	payload := fmt.Sprintf(`{"file":"%s/doc.txt","path":"json/url.txt"}`, ts.URL)
	req := httptest.NewRequest(http.MethodPost, "/api/upload", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	code, resp := doRequest(t, http.HandlerFunc(h.UploadHandler), req)

	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, resp)
	}
}

// ---------- Upload: overwrite protection ----------

func TestUploadHandler_OverwriteDisabled(t *testing.T) {
	h, dir := newTestHandler(t, func(c *config.Config) {
		c.AllowOverwrite = false
	})
	// Create file
	_ = os.MkdirAll(filepath.Join(dir, "ow"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "ow", "file.txt"), []byte("existing"), 0644)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "file.txt")
	fmt.Fprint(fw, "new content")
	_ = mw.WriteField("path", "ow/file.txt")
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	code, _ := doRequest(t, http.HandlerFunc(h.UploadHandler), req)

	if code != http.StatusConflict {
		t.Fatalf("expected 409 conflict, got %d", code)
	}
}

// ---------- Upload: method check ----------

func TestUploadHandler_MethodNotAllowed(t *testing.T) {
	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/upload", nil)
	code, _ := doRequest(t, http.HandlerFunc(h.UploadHandler), req)
	if code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", code)
	}
}

// ---------- Delete ----------

func uploadFile(t *testing.T, h *Handler, dir, relPath, content string) {
	t.Helper()
	abs := filepath.Join(dir, relPath)
	_ = os.MkdirAll(filepath.Dir(abs), 0755)
	_ = os.WriteFile(abs, []byte(content), 0644)
}

func TestDeleteHandler(t *testing.T) {
	h, dir := newTestHandler(t)
	uploadFile(t, h, dir, "del/file.txt", "delete me")

	req := httptest.NewRequest(http.MethodDelete, "/api/files/del/file.txt", nil)
	req.RequestURI = "/api/files/del/file.txt"
	code, body := doRequest(t, http.HandlerFunc(h.DeleteHandler), req)

	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, body)
	}
	if body["ok"] != true {
		t.Errorf("expected ok=true")
	}
	if _, err := os.Stat(filepath.Join(dir, "del/file.txt")); !os.IsNotExist(err) {
		t.Error("file should have been deleted")
	}
}

func TestDeleteHandler_AllowDeleteFalse(t *testing.T) {
	h, dir := newTestHandler(t, func(c *config.Config) {
		c.AllowDelete = false
	})
	uploadFile(t, h, dir, "nd/file.txt", "keep me")

	req := httptest.NewRequest(http.MethodDelete, "/api/files/nd/file.txt", nil)
	code, body := doRequest(t, http.HandlerFunc(h.DeleteHandler), req)

	if code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %v", code, body)
	}
	// file must still exist
	if _, err := os.Stat(filepath.Join(dir, "nd/file.txt")); err != nil {
		t.Error("file should NOT have been deleted")
	}
}

func TestDeleteHandler_NotFound(t *testing.T) {
	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/files/nonexistent.txt", nil)
	code, _ := doRequest(t, http.HandlerFunc(h.DeleteHandler), req)
	if code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", code)
	}
}

func TestDeleteHandler_MethodNotAllowed(t *testing.T) {
	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/files/file.txt", nil)
	code, _ := doRequest(t, http.HandlerFunc(h.DeleteHandler), req)
	if code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", code)
	}
}

// ---------- List ----------

func seedFiles(t *testing.T, dir string, names []string) {
	t.Helper()
	for _, n := range names {
		abs := filepath.Join(dir, n)
		_ = os.MkdirAll(filepath.Dir(abs), 0755)
		_ = os.WriteFile(abs, []byte("content of "+n), 0644)
	}
}

func TestListHandler_Pagination(t *testing.T) {
	h, dir := newTestHandler(t)
	var names []string
	for i := 1; i <= 25; i++ {
		names = append(names, fmt.Sprintf("file%02d.txt", i))
	}
	seedFiles(t, dir, names)

	// Page 1 (default per_page=10)
	req := httptest.NewRequest(http.MethodGet, "/api/list", nil)
	code, body := doRequest(t, http.HandlerFunc(h.ListHandler), req)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	files := body["files"].([]interface{})
	if len(files) != 10 {
		t.Errorf("expected 10 files on page 1, got %d", len(files))
	}
	if int(body["total"].(float64)) != 25 {
		t.Errorf("expected total=25, got %v", body["total"])
	}
	if int(body["total_pages"].(float64)) != 3 {
		t.Errorf("expected total_pages=3, got %v", body["total_pages"])
	}
	if int(body["page"].(float64)) != 1 {
		t.Errorf("expected page=1, got %v", body["page"])
	}

	// Page 3 (last page, 5 files)
	req2 := httptest.NewRequest(http.MethodGet, "/api/list?page=3&per_page=10", nil)
	_, body2 := doRequest(t, http.HandlerFunc(h.ListHandler), req2)
	files2 := body2["files"].([]interface{})
	if len(files2) != 5 {
		t.Errorf("expected 5 files on page 3, got %d", len(files2))
	}
}

func TestListHandler_Hashes(t *testing.T) {
	h, dir := newTestHandler(t)
	seedFiles(t, dir, []string{"hash_test.txt"})

	req := httptest.NewRequest(http.MethodGet, "/api/list", nil)
	_, body := doRequest(t, http.HandlerFunc(h.ListHandler), req)

	files := body["files"].([]interface{})
	if len(files) == 0 {
		t.Fatal("no files returned")
	}
	entry := files[0].(map[string]interface{})
	hash, ok := entry["hash"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected hash object in list entry, got %T: %v", entry["hash"], entry["hash"])
	}
	if hash["md5"] == "" || hash["sha1"] == "" {
		t.Errorf("expected non-empty hash values in list")
	}
}

func TestListHandler_EmptyDir(t *testing.T) {
	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/list", nil)
	code, body := doRequest(t, http.HandlerFunc(h.ListHandler), req)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	files := body["files"].([]interface{})
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
	if int(body["total"].(float64)) != 0 {
		t.Errorf("expected total=0")
	}
}

func TestListHandler_MethodNotAllowed(t *testing.T) {
	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/list", nil)
	code, _ := doRequest(t, http.HandlerFunc(h.ListHandler), req)
	if code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", code)
	}
}

// ---------- Serve file ----------

func TestServeFileHandler(t *testing.T) {
	h, dir := newTestHandler(t)
	_ = os.WriteFile(filepath.Join(dir, "serve.txt"), []byte("served content"), 0644)

	req := httptest.NewRequest(http.MethodGet, "/files/serve.txt", nil)
	rr := httptest.NewRecorder()
	h.ServeFileHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if body, _ := io.ReadAll(rr.Body); string(body) != "served content" {
		t.Errorf("unexpected body: %s", body)
	}
}

func TestServeFileHandler_NotFound(t *testing.T) {
	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/files/missing.txt", nil)
	rr := httptest.NewRecorder()
	h.ServeFileHandler(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// ---------- Auth middleware ----------

func TestAuthMiddleware_ValidKey(t *testing.T) {
	cfg := &config.Config{StorageAPIKey: "secret"}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := Chain(inner, AuthMiddleware(cfg))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "secret")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestAuthMiddleware_BearerToken(t *testing.T) {
	cfg := &config.Config{StorageAPIKey: "secret"}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := Chain(inner, AuthMiddleware(cfg))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestAuthMiddleware_InvalidKey(t *testing.T) {
	cfg := &config.Config{StorageAPIKey: "secret"}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := Chain(inner, AuthMiddleware(cfg))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "wrong")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestAuthMiddleware_MissingKey(t *testing.T) {
	cfg := &config.Config{StorageAPIKey: "secret"}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := Chain(inner, AuthMiddleware(cfg))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// ---------- isPathSafe ----------

func TestIsPathSafe(t *testing.T) {
	cases := []struct {
		path string
		safe bool
	}{
		{"file.txt", true},
		{"dir/file.txt", true},
		{"", false},
		{"/etc/passwd", false},
		{"../escape", false},
		{"a/../b", true}, // cleans to "b" which is safe
		{"C:/windows", false},
		{"null\x00byte", false},
	}
	for _, c := range cases {
		got := isPathSafe(c.path)
		if got != c.safe {
			t.Errorf("isPathSafe(%q) = %v, want %v", c.path, got, c.safe)
		}
	}
}

// ---------- computeFileHashes ----------

func TestComputeFileHashes(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "hash.txt")
	_ = os.WriteFile(f, []byte("hello"), 0644)

	md5h, sha1h, err := computeFileHashes(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// known MD5 and SHA1 of "hello"
	if md5h != "5d41402abc4b2a76b9719d911017c592" {
		t.Errorf("wrong MD5: %s", md5h)
	}
	if sha1h != "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d" {
		t.Errorf("wrong SHA1: %s", sha1h)
	}
}

// ---------- decodeBase64File ----------

func TestDecodeBase64File_Plain(t *testing.T) {
	data := []byte("test data")
	encoded := base64.StdEncoding.EncodeToString(data)
	r, mime, err := decodeBase64File(encoded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mime != "" {
		t.Errorf("expected empty mime, got %q", mime)
	}
	got, _ := io.ReadAll(r)
	if string(got) != "test data" {
		t.Errorf("unexpected decoded content: %s", got)
	}
}

func TestDecodeBase64File_DataURI(t *testing.T) {
	data := []byte("image data")
	encoded := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	r, mimeType, err := decodeBase64File(encoded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mimeType != "image/png" {
		t.Errorf("expected image/png, got %q", mimeType)
	}
	got, _ := io.ReadAll(r)
	if string(got) != "image data" {
		t.Errorf("unexpected content: %s", got)
	}
}

func TestDecodeBase64File_Invalid(t *testing.T) {
	_, _, err := decodeBase64File("not-valid-base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}
