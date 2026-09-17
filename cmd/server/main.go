package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"
	"github.com/jellyfin-share/jellyfin-share-backend/internal/config"
	"github.com/jellyfin-share/jellyfin-share-backend/internal/database"
	"github.com/jellyfin-share/jellyfin-share-backend/internal/handlers"
	"github.com/jellyfin-share/jellyfin-share-backend/internal/jellyfin"
	"github.com/jellyfin-share/jellyfin-share-backend/internal/middleware"
	"github.com/jellyfin-share/jellyfin-share-backend/internal/proxy"
)

func main() {
	cfg := config.Load()

	// Validate required config
	if cfg.JellyfinAPIKey == "" {
		log.Fatal("JFSHARE_JELLYFIN_API_KEY is required")
	}
	if cfg.BackendAPIKey == "" {
		log.Fatal("JFSHARE_BACKEND_API_KEY is required")
	}

	// Initialize database
	db, err := database.New(cfg.DatabaseDSN)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Run migrations from filesystem
	if err := db.RunMigrationsFromPath("migrations"); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Initialize Jellyfin client
	jf := jellyfin.NewClient(cfg.JellyfinBaseURL, cfg.JellyfinAPIKey)
	if err := jf.VerifyConnection(context.Background()); err != nil {
		log.Printf("Warning: Could not verify Jellyfin connection: %v", err)
	} else {
		log.Printf("Successfully connected to Jellyfin at %s", cfg.JellyfinBaseURL)
	}

	// Fetch Jellyfin user ID for API calls
	if err := jf.FetchAndSetUserID(context.Background()); err != nil {
		log.Fatalf("Failed to fetch Jellyfin user: %v", err)
	}

	// Initialize session manager
	sessionManager := middleware.NewShareSessionManager(cfg.BackendAPIKey)

	// Initialize handlers
	adminHandler := handlers.NewAdminHandler(db, jf, cfg, sessionManager)
	publicHandler := handlers.NewPublicHandler(db, jf, cfg, sessionManager)
	streamProxy := proxy.NewStreamProxy(db, jf, cfg)

	// Create router
	r := chi.NewRouter()

	// Global middleware
	r.Use(chimiddleware.Logger)
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.RealIP)
	r.Use(middleware.ClientIPMiddleware)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Backend-Key"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Admin API (requires backend API key)
	r.Route("/api/admin", func(r chi.Router) {
		r.Use(middleware.AdminAuth(cfg.BackendAPIKey))

		r.Post("/shares", adminHandler.CreateShare)
		r.Get("/shares", adminHandler.ListShares)
		r.Get("/shares/{id}", adminHandler.GetShare)
		r.Post("/shares/{id}/revoke", adminHandler.RevokeShare)
		r.Patch("/shares/{id}", adminHandler.UpdateShare)
		r.Get("/shares/{id}/analytics", adminHandler.GetShareAnalytics)
		r.Get("/stats", adminHandler.GetStats)
	})

	// Public API (rate limited)
	r.Route("/api/public", func(r chi.Router) {
		r.Use(httprate.LimitByIP(cfg.RateLimitRequests, cfg.RateLimitWindow))

		r.Get("/shares/{token}", publicHandler.GetShareInfo)
		r.Post("/shares/{token}/password", publicHandler.ValidatePassword)
		r.Post("/shares/{token}/play", publicHandler.StartPlayback)
		r.Get("/shares/{token}/episodes", publicHandler.GetShareEpisodes)
		r.Post("/shares/{token}/episodes/{episodeId}/play", publicHandler.StartEpisodePlayback)
		r.Post("/sessions/{sessionId}/heartbeat", publicHandler.Heartbeat)
		r.Post("/sessions/{sessionId}/finish", publicHandler.FinishPlayback)

		// Image proxy
		r.Get("/images/{token}/{type}", streamProxy.ServeImage)

		// WebVTT sidecar for text subtitles
		r.Get("/subtitles/{sessionId}/{index}", streamProxy.ServeSubtitle)

		// Stream proxy (no rate limit for streaming)
		r.Get("/stream/{sessionId}/*", streamProxy.ServeStream)
	})

	// Serve static frontend
	r.Get("/s/{token}", serveIndex)
	r.Get("/s/{token}/*", serveIndex)

	// Admin UI
	r.Get("/admin", serveIndex)
	r.Get("/admin/*", serveIndex)

	// Serve static assets from filesystem
	r.Handle("/assets/*", http.StripPrefix("/assets/", http.FileServer(http.Dir("web/dist/assets"))))

	// Start background cleanup task
	go startCleanupTask(db, cfg)

	// Create server
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // No timeout for streaming
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		log.Println("Shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			log.Printf("Server shutdown error: %v", err)
		}
	}()

	log.Printf("Server starting on port %d", cfg.Port)
	log.Printf("Public URL: %s", cfg.PublicBaseURL)

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}

	log.Println("Server stopped")
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "web/dist/index.html")
}

func startCleanupTask(db *database.DB, cfg *config.Config) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		ctx := context.Background()

		// Cleanup old sessions
		deleted, err := db.CleanupOldSessions(ctx, 7*24*time.Hour)
		if err != nil {
			log.Printf("Failed to cleanup old sessions: %v", err)
		} else if deleted > 0 {
			log.Printf("Cleaned up %d old sessions", deleted)
		}
	}
}
