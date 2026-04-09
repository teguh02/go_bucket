package http

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/teguh02/go_bucket/internal/config"
)

// Handler holds dependencies for HTTP handlers
type Handler struct {
	cfg *config.Config
}

// NewHandler creates a new Handler instance
func NewHandler(cfg *config.Config) *Handler {
	return &Handler{cfg: cfg}
}

// jsonResponse writes a JSON response
func jsonResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

// jsonError writes a JSON error response
func jsonError(w http.ResponseWriter, statusCode int, message string) {
	jsonResponse(w, statusCode, map[string]interface{}{
		"ok":    false,
		"error": message,
	})
}

// HealthHandler handles GET /health
func (h *Handler) HealthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"time": time.Now().UTC().Format(time.RFC3339),
	})
}

// isPathSafe validates that a path is safe (no traversal attacks)
func isPathSafe(path string) bool {
	// Reject empty paths
	if path == "" {
		return false
	}

	// Reject absolute paths
	if filepath.IsAbs(path) {
		return false
	}

	// Reject paths with null bytes
	if strings.ContainsRune(path, 0) {
		return false
	}

	// Reject paths with Windows drive letters
	if len(path) >= 2 && path[1] == ':' {
		return false
	}

	// Clean the path and check for traversal
	cleaned := filepath.Clean(path)

	// Reject if path starts with .. or contains ..
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || strings.Contains(cleaned, string(filepath.Separator)+".."+string(filepath.Separator)) || strings.HasSuffix(cleaned, string(filepath.Separator)+"..") {
		return false
	}

	// Reject hidden files starting with .
	parts := strings.Split(cleaned, string(filepath.Separator))
	for _, part := range parts {
		if part == ".." {
			return false
		}
	}

	return true
}

// getSafePath returns a safe absolute path within storage dir, or error
func (h *Handler) getSafePath(requestPath string) (string, error) {
	// Clean and validate the path
	cleaned := filepath.Clean(requestPath)

	if !isPathSafe(cleaned) {
		return "", fmt.Errorf("invalid path")
	}

	// Build absolute path
	absPath := filepath.Join(h.cfg.StorageDir, cleaned)

	// Verify the path is still within storage dir after resolution
	absStorageDir, err := filepath.Abs(h.cfg.StorageDir)
	if err != nil {
		return "", fmt.Errorf("storage dir error")
	}

	absPath, err = filepath.Abs(absPath)
	if err != nil {
		return "", fmt.Errorf("path resolution error")
	}

	// Ensure the resolved path is within storage dir
	if !strings.HasPrefix(absPath, absStorageDir+string(filepath.Separator)) && absPath != absStorageDir {
		return "", fmt.Errorf("path traversal detected")
	}

	return absPath, nil
}

// buildPublicURL builds the public URL for a file path
func (h *Handler) buildPublicURL(r *http.Request, filePath string) string {
	var baseURL string

	if h.cfg.PublicBaseURL != "" {
		baseURL = h.cfg.PublicBaseURL
	} else {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
			scheme = fwd
		}
		host := r.Host
		baseURL = fmt.Sprintf("%s://%s", scheme, host)
	}

	return fmt.Sprintf("%s/files/%s", baseURL, filePath)
}

// ServeFileHandler handles GET /files/{path...}
func (h *Handler) ServeFileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract path from URL (remove /files/ prefix)
	path := strings.TrimPrefix(r.URL.Path, "/files/")
	if path == "" || path == "/" {
		jsonError(w, http.StatusBadRequest, "file path required")
		return
	}

	// Get safe path
	absPath, err := h.getSafePath(path)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Check if file exists
	info, err := os.Stat(absPath)
	if os.IsNotExist(err) {
		jsonError(w, http.StatusNotFound, "file not found")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to access file")
		return
	}

	// Reject directory listing
	if info.IsDir() {
		jsonError(w, http.StatusNotFound, "file not found")
		return
	}

	// Set cache control header
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", h.cfg.CacheMaxAge))

	// Serve file (http.ServeFile handles streaming, range requests, etc.)
	http.ServeFile(w, r, absPath)
}

// sanitizeFilename sanitizes a filename for safe storage
func sanitizeFilename(name string) string {
	// Remove path separators
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")

	// Remove null bytes
	name = strings.ReplaceAll(name, "\x00", "")

	// Remove leading/trailing dots and spaces
	name = strings.Trim(name, ". ")

	if name == "" {
		name = "unnamed"
	}

	return name
}

// computeFileHashes computes MD5 and SHA1 hashes for a file at the given path
func computeFileHashes(filePath string) (md5Hash string, sha1Hash string, err error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	md5Hasher := md5.New()
	sha1Hasher := sha1.New()
	if _, err = io.Copy(io.MultiWriter(md5Hasher, sha1Hasher), f); err != nil {
		return "", "", err
	}

	return fmt.Sprintf("%x", md5Hasher.Sum(nil)), fmt.Sprintf("%x", sha1Hasher.Sum(nil)), nil
}

// decodeBase64File decodes a base64-encoded string (plain or data URI) into a reader.
// It also returns the detected MIME type when a data URI prefix is present.
func decodeBase64File(value string) (io.Reader, string, error) {
	mimeType := ""
	b64data := value

	if strings.HasPrefix(value, "data:") {
		// data:<mimetype>;base64,<data>
		comma := strings.Index(value, ",")
		if comma < 0 {
			return nil, "", fmt.Errorf("invalid data URI")
		}
		meta := value[5:comma] // strip "data:"
		b64data = value[comma+1:]
		if idx := strings.Index(meta, ";"); idx >= 0 {
			mimeType = meta[:idx]
		} else {
			mimeType = meta
		}
	}

	// Try StdEncoding first, then URLEncoding, then RawStdEncoding
	var decoded []byte
	var err error
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		decoded, err = enc.DecodeString(b64data)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, "", fmt.Errorf("invalid base64 data")
	}

	return bytes.NewReader(decoded), mimeType, nil
}

// filenameFromURL extracts the base filename from a URL
func filenameFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "file"
	}
	base := path.Base(u.Path)
	if base == "" || base == "." || base == "/" {
		return "file"
	}
	return base
}

// writeFileAtomic writes content from reader to absPath using a temp file and rename.
// It also computes MD5 and SHA1 hashes during writing.
func writeFileAtomic(absPath string, reader io.Reader) (written int64, md5Hash string, sha1Hash string, err error) {
	tmpPath := absPath + ".tmp"

	destFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return 0, "", "", fmt.Errorf("failed to create file: %w", err)
	}

	md5Hasher := md5.New()
	sha1Hasher := sha1.New()
	multiWriter := io.MultiWriter(destFile, md5Hasher, sha1Hasher)

	written, err = io.Copy(multiWriter, reader)
	destFile.Close()
	if err != nil {
		os.Remove(tmpPath)
		return 0, "", "", fmt.Errorf("failed to write file: %w", err)
	}

	if err = os.Rename(tmpPath, absPath); err != nil {
		os.Remove(tmpPath)
		return 0, "", "", fmt.Errorf("failed to save file: %w", err)
	}

	return written, fmt.Sprintf("%x", md5Hasher.Sum(nil)), fmt.Sprintf("%x", sha1Hasher.Sum(nil)), nil
}

// finishUpload handles the common end of the upload pipeline: path validation,
// overwrite check, directory creation, atomic write, and JSON response.
func (h *Handler) finishUpload(w http.ResponseWriter, r *http.Request, destPath string, fileReader io.Reader, contentType string) {
	// Clean the path (for safety)
	destPath = filepath.ToSlash(destPath)
	destPath = strings.TrimPrefix(destPath, "/")

	// Get safe absolute path
	absPath, err := h.getSafePath(destPath)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid path: "+err.Error())
		return
	}

	// Check if file exists and handle overwrite
	if _, err := os.Stat(absPath); err == nil {
		if !h.cfg.AllowOverwrite {
			jsonError(w, http.StatusConflict, "file already exists and overwrite is disabled")
			return
		}
	}

	// Create parent directories if needed
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to create directory")
		return
	}

	// Write atomically and compute hashes
	written, md5Hash, sha1Hash, err := writeFileAtomic(absPath, fileReader)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Detect content type if not already set
	if contentType == "" || contentType == "application/octet-stream" {
		ext := filepath.Ext(destPath)
		if detected := mime.TypeByExtension(ext); detected != "" {
			contentType = detected
		} else {
			contentType = "application/octet-stream"
		}
	}

	log.Printf("UPLOAD: %s (%d bytes)", destPath, written)

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"ok":           true,
		"path":         destPath,
		"url":          h.buildPublicURL(r, destPath),
		"size":         written,
		"content_type": contentType,
		"hash": map[string]string{
			"md5":  md5Hash,
			"sha1": sha1Hash,
		},
	})
}

// UploadHandler handles POST /api/upload
// Supports: multipart file, base64 (plain or data URI), URL, raw binary body.
func (h *Handler) UploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ct := r.Header.Get("Content-Type")

	switch {
	case strings.Contains(ct, "multipart/form-data"):
		h.handleMultipartUpload(w, r)
	case strings.Contains(ct, "application/json"):
		h.handleJSONUpload(w, r)
	default:
		// Raw binary body (application/octet-stream, image/*, video/*, audio/*, etc.)
		h.handleBinaryUpload(w, r)
	}
}

// handleMultipartUpload processes multipart/form-data uploads.
// The "file" field can be an actual file attachment, a URL string, or a base64 string.
func (h *Handler) handleMultipartUpload(w http.ResponseWriter, r *http.Request) {
	maxBytes := h.cfg.MaxUploadBytes() + (10 * 1024 * 1024)
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		if strings.Contains(err.Error(), "too large") {
			jsonError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file too large, max size is %d MB", h.cfg.MaxUploadMB))
		} else {
			jsonError(w, http.StatusBadRequest, "failed to parse form: "+err.Error())
		}
		return
	}
	defer r.MultipartForm.RemoveAll()

	destPath := r.FormValue("path")

	// Try as file attachment first
	file, header, err := r.FormFile("file")
	if err == nil {
		defer file.Close()

		if header.Size > h.cfg.MaxUploadBytes() {
			jsonError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file too large, max size is %d MB", h.cfg.MaxUploadMB))
			return
		}

		if destPath == "" {
			destPath = sanitizeFilename(header.Filename)
		}
		contentType := header.Header.Get("Content-Type")
		h.finishUpload(w, r, destPath, file, contentType)
		return
	}

	// Fall back to text field (URL or base64)
	fileValue := r.FormValue("file")
	if fileValue == "" {
		jsonError(w, http.StatusBadRequest, "file field is required")
		return
	}

	h.handleStringFile(w, r, fileValue, destPath)
}

// handleJSONUpload processes application/json uploads.
// Expects {"file": "<url or base64>", "path": "<dest>"}
func (h *Handler) handleJSONUpload(w http.ResponseWriter, r *http.Request) {
	var body struct {
		File string `json:"file"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.File == "" {
		jsonError(w, http.StatusBadRequest, "file field is required")
		return
	}
	h.handleStringFile(w, r, body.File, body.Path)
}

// handleBinaryUpload processes raw binary body uploads.
// The destination path must be supplied via ?path= query param or X-File-Path header.
func (h *Handler) handleBinaryUpload(w http.ResponseWriter, r *http.Request) {
	destPath := r.URL.Query().Get("path")
	if destPath == "" {
		destPath = r.Header.Get("X-File-Path")
	}
	if destPath == "" {
		jsonError(w, http.StatusBadRequest, "path is required for binary upload (use ?path= query param or X-File-Path header)")
		return
	}

	contentType := r.Header.Get("Content-Type")
	h.finishUpload(w, r, destPath, r.Body, contentType)
}

// handleStringFile processes a "file" value that is either a URL or base64 data.
func (h *Handler) handleStringFile(w http.ResponseWriter, r *http.Request, fileValue string, destPath string) {
	if strings.HasPrefix(fileValue, "http://") || strings.HasPrefix(fileValue, "https://") {
		// Download from URL using the request context with a 30-second timeout
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileValue, nil)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid URL: "+err.Error())
			return
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "failed to download file from URL: "+err.Error())
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			jsonError(w, http.StatusBadRequest, fmt.Sprintf("URL returned HTTP %d", resp.StatusCode))
			return
		}

		if destPath == "" {
			destPath = sanitizeFilename(filenameFromURL(fileValue))
		}

		contentType := resp.Header.Get("Content-Type")
		// Strip parameters from content-type (e.g. "image/png; charset=utf-8")
		if idx := strings.Index(contentType, ";"); idx >= 0 {
			contentType = strings.TrimSpace(contentType[:idx])
		}

		h.finishUpload(w, r, destPath, resp.Body, contentType)
		return
	}

	// Treat as base64 (plain or data URI)
	reader, mimeType, err := decodeBase64File(fileValue)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "file must be a valid URL or base64-encoded data: "+err.Error())
		return
	}

	if destPath == "" {
		destPath = "file"
		// Try to guess extension from MIME type
		if mimeType != "" {
			exts, _ := mime.ExtensionsByType(mimeType)
			if len(exts) > 0 {
				destPath = "file" + exts[0]
			}
		}
	}

	h.finishUpload(w, r, destPath, reader, mimeType)
}

// DeleteHandler handles DELETE /api/files/{path...}
func (h *Handler) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Check if delete is allowed by configuration
	if !h.cfg.AllowDelete {
		jsonError(w, http.StatusForbidden, "file deletion is disabled")
		return
	}

	// Extract path from URL (remove /api/files/ prefix)
	path := strings.TrimPrefix(r.URL.Path, "/api/files/")
	if path == "" || path == "/" {
		jsonError(w, http.StatusBadRequest, "file path required")
		return
	}

	// Get safe path
	absPath, err := h.getSafePath(path)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Check if file exists
	info, err := os.Stat(absPath)
	if os.IsNotExist(err) {
		jsonError(w, http.StatusNotFound, "file not found")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to access file")
		return
	}

	// Don't allow deleting directories
	if info.IsDir() {
		jsonError(w, http.StatusBadRequest, "cannot delete directory")
		return
	}

	// Delete the file
	if err := os.Remove(absPath); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to delete file")
		return
	}

	log.Printf("DELETE: %s", path)

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"deleted": path,
	})
}

// ListHandler handles GET /api/list?prefix=&page=&per_page=
func (h *Handler) ListHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	prefix := r.URL.Query().Get("prefix")

	// Pagination parameters
	page := 1
	perPage := 10
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := r.URL.Query().Get("per_page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			perPage = n
		}
	}

	// Start from storage dir or subdirectory
	searchPath := h.cfg.StorageDir
	if prefix != "" {
		safePath, err := h.getSafePath(prefix)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid prefix")
			return
		}
		searchPath = safePath
	}

	// Check if path exists
	info, err := os.Stat(searchPath)
	if os.IsNotExist(err) {
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"ok":          true,
			"files":       []interface{}{},
			"count":       0,
			"page":        page,
			"per_page":    perPage,
			"total":       0,
			"total_pages": 0,
		})
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to access path")
		return
	}

	// Collect all file metadata (without hashes yet)
	type fileEntry struct {
		relPath  string
		absPath  string
		size     int64
		modified string
	}
	var allFiles []fileEntry
	const maxFiles = 10000

	if info.IsDir() {
		err = filepath.Walk(searchPath, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if fi.IsDir() {
				return nil
			}
			if len(allFiles) >= maxFiles {
				return filepath.SkipAll
			}
			relPath, err := filepath.Rel(h.cfg.StorageDir, p)
			if err != nil {
				return nil
			}
			allFiles = append(allFiles, fileEntry{
				relPath:  filepath.ToSlash(relPath),
				absPath:  p,
				size:     fi.Size(),
				modified: fi.ModTime().UTC().Format(time.RFC3339),
			})
			return nil
		})
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to list files")
			return
		}
	} else {
		relPath, _ := filepath.Rel(h.cfg.StorageDir, searchPath)
		allFiles = append(allFiles, fileEntry{
			relPath:  filepath.ToSlash(relPath),
			absPath:  searchPath,
			size:     info.Size(),
			modified: info.ModTime().UTC().Format(time.RFC3339),
		})
	}

	total := len(allFiles)
	totalPages := 0
	if perPage > 0 {
		totalPages = (total + perPage - 1) / perPage
	}

	// Paginate
	start := (page - 1) * perPage
	end := start + perPage
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	pageEntries := allFiles[start:end]

	// Build response with hashes only for the current page
	files := make([]map[string]interface{}, 0, len(pageEntries))
	for _, fe := range pageEntries {
		entry := map[string]interface{}{
			"path":     fe.relPath,
			"size":     fe.size,
			"modified": fe.modified,
		}
		md5Hash, sha1Hash, err := computeFileHashes(fe.absPath)
		if err == nil {
			entry["hash"] = map[string]string{
				"md5":  md5Hash,
				"sha1": sha1Hash,
			}
		}
		files = append(files, entry)
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"ok":          true,
		"files":       files,
		"count":       len(files),
		"page":        page,
		"per_page":    perPage,
		"total":       total,
		"total_pages": totalPages,
	})
}

