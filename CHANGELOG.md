# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
