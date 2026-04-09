# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.1.0] - 2026-04-09

### Added
- **Smart Upload Detection** (`POST /api/upload`): The upload endpoint now automatically detects the type of upload from the request:
  - **Multipart file** (existing): standard HTML form / curl `-F file=@...`
  - **URL upload**: pass a remote URL as the `file` form field — the server downloads and stores the file
  - **Base64 upload**: pass a plain base64 string or a `data:<mime>;base64,<data>` URI as the `file` form field
  - **Raw binary body**: send the file bytes directly as the request body (useful for Ajax / axios / jQuery XHR); supply the destination path via `?path=` query param or `X-File-Path` header
  - **JSON body**: `Content-Type: application/json` with `{"file": "<url or base64>", "path": "..."}`
- **File integrity hashes**: Upload response and list entries now include a `hash` object with `md5` and `sha1` checksums for integrity verification
- **`ALLOW_DELETE` environment variable**: Controls whether file deletion is permitted via the API (default: `true`). Setting it to `false` returns HTTP 403 for all delete requests even with a valid API key
- **Pagination for `/api/list`**: The list endpoint now supports `page` (default: `1`) and `per_page` (default: `10`) query parameters. The response includes `page`, `per_page`, `total`, and `total_pages` fields
- **Test suite** (`internal/http/handlers_test.go`): Comprehensive unit and integration tests covering all endpoints and features
- **GitHub Actions CI** (`.github/workflows/test.yml`): Automated build and test workflow that runs on every push and pull request

### Changed
- `GET /api/list` response now includes `page`, `per_page`, `total`, `total_pages` fields alongside the existing `ok`, `files`, and `count` fields
- `POST /api/upload` response now includes a `hash` object (`md5`, `sha1`) alongside existing fields

## [1.0.0] - 2025-12-26

### Added
- Initial release of Simple CDN Storage Server
- **Health Check Endpoint**: `GET /health` - Server health status
- **File Upload**: `POST /api/upload` - Upload files with multipart/form-data
  - Support for custom destination path
  - Atomic file writes (temp file + rename)
  - Configurable max upload size
  - Overwrite protection (configurable)
- **File Serving**: `GET /files/{path}` - Public file access
  - Streaming file serving (memory efficient)
  - Cache-Control headers
  - No directory listing
- **File Deletion**: `DELETE /api/files/{path}` - Remove files
- **File Listing**: `GET /api/list?prefix=` - List files with optional prefix filter
- **Authentication**: API key via `X-API-Key` header or `Authorization: Bearer`
- **Security Features**:
  - Path traversal protection (blocks `..`, absolute paths, null bytes)
  - Non-root Docker user
  - Safe file permissions (0644 files, 0755 directories)
- **CORS Support**: Configurable allowed origins
- **Docker Support**:
  - Multi-stage Dockerfile with Alpine
  - Docker Compose configuration
  - Health check integration
- **Documentation**:
  - Comprehensive README with examples
  - Postman collection for API testing
  - Next.js integration examples

