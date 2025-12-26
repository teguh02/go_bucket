# Simple CDN Storage Server

A lightweight file storage server in Go, similar to S3/Google Cloud Storage but much simpler. No database required - uses filesystem only.

## Features

- ✅ **Upload files** with API key authentication
- ✅ **Delete files** with API key authentication  
- ✅ **List files** with optional prefix filtering
- ✅ **Serve files publicly** (no auth required)
- ✅ **Path traversal protection** - secure against `../` attacks
- ✅ **Streaming file serving** - memory efficient
- ✅ **CORS support** - configurable origins
- ✅ **Docker ready** - multi-stage build, non-root user

## Quick Start

```bash
# Clone and enter directory
cd education_storage

# Copy environment file
cp .env.example .env

# Edit .env and set your API key
# STORAGE_API_KEY=your-secret-key-here

# Start with Docker Compose
docker compose up -d --build

# Check health
curl http://localhost:8080/health
```

## API Endpoints

### Health Check

```bash
GET /health
```

Response:
```json
{"ok": true, "time": "2024-01-15T10:30:00Z"}
```

### Upload File (Auth Required)

```bash
POST /api/upload
```

**Headers:**
- `X-API-Key: your-api-key` OR `Authorization: Bearer your-api-key`

**Form Data:**
- `file` (required): The file to upload
- `path` (optional): Destination path, e.g., `avatars/user1.jpg`

**Example:**
```bash
curl -X POST "http://localhost:8080/api/upload" \
  -H "X-API-Key: your-api-key" \
  -F "file=@./photo.jpg" \
  -F "path=avatars/user1.jpg"
```

**Response:**
```json
{
  "ok": true,
  "path": "avatars/user1.jpg",
  "url": "http://localhost:8080/files/avatars/user1.jpg",
  "size": 12345,
  "content_type": "image/jpeg"
}
```

### Access File (Public)

```bash
GET /files/{path}
```

**Example:**
```bash
curl http://localhost:8080/files/avatars/user1.jpg
```

Direct access in browser:
```
http://localhost:8080/files/avatars/user1.jpg
```

### Delete File (Auth Required)

```bash
DELETE /api/files/{path}
```

**Example:**
```bash
curl -X DELETE "http://localhost:8080/api/files/avatars/user1.jpg" \
  -H "X-API-Key: your-api-key"
```

**Response:**
```json
{"ok": true, "deleted": "avatars/user1.jpg"}
```

### List Files (Auth Required)

```bash
GET /api/list?prefix={optional-prefix}
```

**Example:**
```bash
# List all files
curl "http://localhost:8080/api/list" \
  -H "X-API-Key: your-api-key"

# List files in avatars folder
curl "http://localhost:8080/api/list?prefix=avatars" \
  -H "X-API-Key: your-api-key"
```

**Response:**
```json
{
  "ok": true,
  "files": [
    {"path": "avatars/user1.jpg", "size": 12345, "modified": "2024-01-15T10:30:00Z"},
    {"path": "avatars/user2.jpg", "size": 23456, "modified": "2024-01-15T11:00:00Z"}
  ],
  "count": 2
}
```

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `STORAGE_API_KEY` | ✅ Yes | - | API key for upload/delete operations |
| `PORT` | No | `8080` | Server port |
| `STORAGE_DIR` | No | `/data` | Storage directory (container path) |
| `PUBLIC_BASE_URL` | No | auto | Base URL for generated file URLs |
| `MAX_UPLOAD_MB` | No | `50` | Maximum upload size in MB |
| `ALLOW_OVERWRITE` | No | `false` | Allow overwriting existing files |
| `CORS_ALLOWED_ORIGINS` | No | `*` | CORS origins (comma-separated or `*`) |
| `CACHE_MAX_AGE` | No | `31536000` | Cache-Control max-age in seconds |

## Project Structure

```
education_storage/
├── cmd/
│   └── server/
│       └── main.go          # Application entry point
├── internal/
│   ├── config/
│   │   └── config.go        # Configuration loading
│   └── http/
│       ├── handlers.go      # HTTP handlers
│       └── middleware.go    # Auth, CORS, logging middleware
├── storage/                  # File storage (mounted volume)
├── .env.example             # Example environment file
├── .gitignore
├── docker-compose.yml
├── Dockerfile
├── go.mod
└── README.md
```

## Security Features

1. **Path Traversal Protection**: Rejects paths containing `..`, absolute paths, null bytes
2. **API Key Authentication**: Required for upload/delete/list operations
3. **Non-root Docker User**: Container runs as unprivileged user
4. **No Directory Listing**: Returns 404 for directory requests
5. **Atomic File Writes**: Uses temp file + rename for safe writes

## Next.js Integration

**Important:** Never expose the API key in client-side code. Use server-side API routes or server actions.

### Upload from Next.js Server Action

```typescript
// app/actions/upload.ts
'use server'

export async function uploadFile(formData: FormData) {
  const file = formData.get('file') as File
  const path = formData.get('path') as string
  
  const uploadForm = new FormData()
  uploadForm.append('file', file)
  uploadForm.append('path', path)
  
  const response = await fetch(`${process.env.CDN_URL}/api/upload`, {
    method: 'POST',
    headers: {
      'X-API-Key': process.env.CDN_API_KEY!,
    },
    body: uploadForm,
  })
  
  return response.json()
}
```

### Delete from Next.js API Route

```typescript
// app/api/cdn/delete/route.ts
import { NextRequest, NextResponse } from 'next/server'

export async function DELETE(request: NextRequest) {
  const { path } = await request.json()
  
  const response = await fetch(`${process.env.CDN_URL}/api/files/${path}`, {
    method: 'DELETE',
    headers: {
      'X-API-Key': process.env.CDN_API_KEY!,
    },
  })
  
  return NextResponse.json(await response.json())
}
```

## Development

### Run Locally (without Docker)

```bash
# Set environment variables
export STORAGE_API_KEY=dev-key
export STORAGE_DIR=./storage
export PORT=8080

# Run
go run ./cmd/server
```

### Build Binary

```bash
go build -o cdn-server ./cmd/server
./cdn-server
```

## License

MIT
