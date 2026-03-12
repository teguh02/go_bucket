package http

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
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

// UploadHandler handles POST /api/upload
func (h *Handler) UploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Parse multipart form with size limit
	maxBytes := h.cfg.MaxUploadBytes() + (10 * 1024 * 1024) // Add 10MB buffer for form overhead
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		if strings.Contains(err.Error(), "too large") {
			jsonError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file too large, max size is %d MB", h.cfg.MaxUploadMB))
		} else {
			jsonError(w, http.StatusBadRequest, "failed to parse form: "+err.Error())
		}
		return
	}
	defer r.MultipartForm.RemoveAll()

	// Get file from form
	file, header, err := r.FormFile("file")
	if err != nil {
		jsonError(w, http.StatusBadRequest, "file field is required")
		return
	}
	defer file.Close()

	// Check file size
	if header.Size > h.cfg.MaxUploadBytes() {
		jsonError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file too large, max size is %d MB", h.cfg.MaxUploadMB))
		return
	}

	// Determine destination path
	destPath := r.FormValue("path")
	if destPath == "" {
		destPath = sanitizeFilename(header.Filename)
	}

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
	parentDir := filepath.Dir(absPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to create directory")
		return
	}

	// Write to temp file first (atomic write)
	tmpPath := absPath + ".tmp"
	destFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to create file")
		return
	}

	written, err := io.Copy(destFile, file)
	destFile.Close()
	if err != nil {
		os.Remove(tmpPath)
		jsonError(w, http.StatusInternalServerError, "failed to write file")
		return
	}

	// Rename temp file to final name (atomic on most filesystems)
	if err := os.Rename(tmpPath, absPath); err != nil {
		os.Remove(tmpPath)
		jsonError(w, http.StatusInternalServerError, "failed to save file")
		return
	}

	// Detect content type
	contentType := header.Header.Get("Content-Type")
	if contentType == "" || contentType == "application/octet-stream" {
		ext := filepath.Ext(destPath)
		contentType = mime.TypeByExtension(ext)
		if contentType == "" {
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
	})
}

// DeleteHandler handles DELETE /api/files/{path...}
func (h *Handler) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
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

// ListHandler handles GET /api/list?prefix=
func (h *Handler) ListHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	prefix := r.URL.Query().Get("prefix")

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
			"ok":    true,
			"files": []interface{}{},
		})
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to access path")
		return
	}

	var files []map[string]interface{}
	maxFiles := 1000

	if info.IsDir() {
		// Walk directory
		err = filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // Skip errors
			}

			// Skip directories
			if info.IsDir() {
				return nil
			}

			// Limit number of files
			if len(files) >= maxFiles {
				return filepath.SkipAll
			}

			// Get relative path from storage dir
			relPath, err := filepath.Rel(h.cfg.StorageDir, path)
			if err != nil {
				return nil
			}

			files = append(files, map[string]interface{}{
				"path":     filepath.ToSlash(relPath),
				"size":     info.Size(),
				"modified": info.ModTime().UTC().Format(time.RFC3339),
			})

			return nil
		})
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to list files")
			return
		}
	} else {
		// Single file
		relPath, _ := filepath.Rel(h.cfg.StorageDir, searchPath)
		files = append(files, map[string]interface{}{
			"path":     filepath.ToSlash(relPath),
			"size":     info.Size(),
			"modified": info.ModTime().UTC().Format(time.RFC3339),
		})
	}

	if files == nil {
		files = []map[string]interface{}{}
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"ok":    true,
		"files": files,
		"count": len(files),
	})
}
