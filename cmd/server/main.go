package main

import (
	"log"
	"net/http"
	"os"

	"github.com/teguh02/education_storage/internal/config"
	handler "github.com/teguh02/education_storage/internal/http"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Ensure storage directory exists
	if err := os.MkdirAll(cfg.StorageDir, 0755); err != nil {
		log.Fatalf("Failed to create storage directory: %v", err)
	}

	// Create handler
	h := handler.NewHandler(cfg)

	// Create router (using standard library)
	mux := http.NewServeMux()

	// Health check (no auth)
	mux.HandleFunc("/health", h.HealthHandler)

	// Public file serving (no auth)
	mux.HandleFunc("/files/", h.ServeFileHandler)

	// Protected API routes
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/api/upload", h.UploadHandler)
	apiMux.HandleFunc("/api/files/", h.DeleteHandler)
	apiMux.HandleFunc("/api/list", h.ListHandler)

	// Apply auth middleware to API routes
	protectedAPI := handler.Chain(
		apiMux,
		handler.MaxBytesMiddleware(cfg.MaxUploadBytes()+(10*1024*1024)),
		handler.AuthMiddleware(cfg),
	)

	// Mount protected API routes
	mux.Handle("/api/", protectedAPI)

	// Apply global middlewares
	finalHandler := handler.Chain(
		mux,
		handler.LoggingMiddleware,
		handler.CORSMiddleware(cfg),
	)

	// Start server
	addr := ":" + cfg.Port
	log.Printf("Starting CDN Storage Server on %s", addr)
	log.Printf("Storage directory: %s", cfg.StorageDir)
	log.Printf("Max upload size: %d MB", cfg.MaxUploadMB)
	log.Printf("Allow overwrite: %v", cfg.AllowOverwrite)

	if err := http.ListenAndServe(addr, finalHandler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
