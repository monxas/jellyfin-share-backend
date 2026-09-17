package handlers

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jellyfin-share/jellyfin-share-backend/internal/config"
	"github.com/jellyfin-share/jellyfin-share-backend/internal/database"
	"github.com/jellyfin-share/jellyfin-share-backend/internal/jellyfin"
	"github.com/jellyfin-share/jellyfin-share-backend/internal/middleware"
	"github.com/jellyfin-share/jellyfin-share-backend/internal/models"
)

type AdminHandler struct {
	db       *database.DB
	jf       *jellyfin.Client
	cfg      *config.Config
	sessions *middleware.ShareSessionManager
}

func NewAdminHandler(db *database.DB, jf *jellyfin.Client, cfg *config.Config, sessions *middleware.ShareSessionManager) *AdminHandler {
	return &AdminHandler{
		db:       db,
		jf:       jf,
		cfg:      cfg,
		sessions: sessions,
	}
}

func (h *AdminHandler) CreateShare(w http.ResponseWriter, r *http.Request) {
	var req models.CreateShareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.JellyfinItemID == "" || req.JellyfinUserID == "" {
		writeError(w, http.StatusBadRequest, "jellyfinItemId and jellyfinUserId are required")
		return
	}

	if req.ExpiresInMinutes <= 0 {
		req.ExpiresInMinutes = 1440 // Default 24 hours
	}

	// NeverExpires stores a NULL expiry; ExpiresInMinutes is ignored in that case.
	expiresAt := models.NullTime{}
	if !req.NeverExpires {
		expiresAt.Time = time.Now().Add(time.Duration(req.ExpiresInMinutes) * time.Minute)
		expiresAt.Valid = true
	}

	// Fetch item info from Jellyfin
	item, err := h.jf.GetItem(r.Context(), req.JellyfinItemID)
	if err != nil {
		log.Printf("Failed to fetch Jellyfin item: %v", err)
		writeError(w, http.StatusBadGateway, "failed to fetch item from Jellyfin")
		return
	}

	// Generate share token
	publicToken := middleware.GenerateSecureToken(24)

	share := &models.Share{
		ID:             uuid.New(),
		PublicToken:    publicToken,
		JellyfinItemID: req.JellyfinItemID,
		JellyfinUserID: req.JellyfinUserID,
		Title:          item.Name,
		ItemType:       item.Type,
		ExpiresAt:      expiresAt,
		CreatedAt:      time.Now(),
	}

	// Set optional fields
	if item.Overview != "" {
		share.Overview = sql.NullString{String: item.Overview, Valid: true}
	}
	if item.RunTimeTicks > 0 {
		share.RuntimeSeconds = sql.NullInt64{Int64: jellyfin.TicksToSeconds(item.RunTimeTicks), Valid: true}
	}
	if item.ImageTags.Primary != "" {
		share.PosterPath = sql.NullString{String: h.jf.GetPosterURL(item.ID), Valid: true}
	}
	if len(item.BackdropImageTags) > 0 {
		share.BackdropPath = sql.NullString{String: h.jf.GetBackdropURL(item.ID), Valid: true}
	}

	// Handle episode naming
	if item.Type == "Episode" && item.SeriesName != "" {
		share.Title = item.SeriesName
		if item.SeasonName != "" {
			share.Title += " - " + item.SeasonName
		}
		share.Title += " - " + item.Name
	}

	if req.MaxVideoHeight != nil && *req.MaxVideoHeight > 0 {
		share.MaxVideoHeight = sql.NullInt64{Int64: int64(*req.MaxVideoHeight), Valid: true}
	}
	if req.MaxVideoBitrate != nil && *req.MaxVideoBitrate > 0 {
		share.MaxVideoBitrate = sql.NullInt64{Int64: int64(*req.MaxVideoBitrate), Valid: true}
	}

	if req.MaxTotalPlays != nil {
		share.MaxTotalPlays = sql.NullInt64{Int64: int64(*req.MaxTotalPlays), Valid: true}
	}
	if req.MaxConcurrentViewers != nil {
		share.MaxConcurrentViewers = sql.NullInt64{Int64: int64(*req.MaxConcurrentViewers), Valid: true}
	}
	if req.Password != nil && *req.Password != "" {
		hash, err := middleware.HashPassword(*req.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to hash password")
			return
		}
		share.PasswordHash = sql.NullString{String: hash, Valid: true}
	}

	if err := h.db.CreateShare(r.Context(), share); err != nil {
		log.Printf("Failed to create share: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to create share")
		return
	}

	// Log audit event
	h.db.LogAuditEvent(r.Context(), database.AuditEventShareCreated, &share.ID, nil, &req.JellyfinUserID, nil, map[string]interface{}{
		"itemId":   req.JellyfinItemID,
		"itemType": item.Type,
		"title":    share.Title,
	})

	resp := models.CreateShareResponse{
		ShareID:   share.ID,
		PublicURL: h.cfg.PublicBaseURL + "/s/" + publicToken,
		Token:     publicToken,
		ExpiresAt: share.ExpiresAtPtr(),
	}
	if req.MaxTotalPlays != nil {
		resp.MaxTotalPlays = req.MaxTotalPlays
	}
	if req.MaxConcurrentViewers != nil {
		resp.MaxConcurrentViewers = req.MaxConcurrentViewers
	}

	writeJSON(w, http.StatusCreated, resp)
}

func (h *AdminHandler) ListShares(w http.ResponseWriter, r *http.Request) {
	jellyfinUserID := r.URL.Query().Get("jellyfinUserId")

	var shares []models.Share
	var err error

	if jellyfinUserID != "" {
		shares, err = h.db.GetSharesByUser(r.Context(), jellyfinUserID)
	} else {
		shares, err = h.db.GetAllShares(r.Context(), 100, 0)
	}

	if err != nil {
		log.Printf("Failed to list shares: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list shares")
		return
	}

	// Convert to list items
	items := make([]models.ShareListItem, 0, len(shares))
	for _, s := range shares {
		item := models.ShareListItem{
			ID:                       s.ID,
			PublicToken:              s.PublicToken,
			Title:                    s.Title,
			ItemType:                 s.ItemType,
			TotalPlays:               s.TotalPlays,
			CurrentConcurrentViewers: s.CurrentConcurrentViewers,
			ExpiresAt:                s.ExpiresAtPtr(),
			CreatedAt:                s.CreatedAt,
			HasPassword:              s.RequiresPassword(),
			PublicURL:                h.cfg.PublicBaseURL + "/s/" + s.PublicToken,
		}
		if s.MaxTotalPlays.Valid {
			item.MaxTotalPlays = &s.MaxTotalPlays.Int64
		}
		if s.MaxConcurrentViewers.Valid {
			item.MaxConcurrentViewers = &s.MaxConcurrentViewers.Int64
		}
		if s.RevokedAt.Valid {
			item.RevokedAt = &s.RevokedAt.Time
		}
		if s.MaxVideoHeight.Valid {
			item.MaxVideoHeight = &s.MaxVideoHeight.Int64
		}
		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"shares": items,
		"total":  len(items),
	})
}

func (h *AdminHandler) GetShare(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid share ID")
		return
	}

	share, err := h.db.GetShareByID(r.Context(), id)
	if err != nil {
		log.Printf("Failed to get share: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to get share")
		return
	}
	if share == nil {
		writeError(w, http.StatusNotFound, "share not found")
		return
	}

	// Get sessions for this share
	sessions, _ := h.db.GetSessionsByShare(r.Context(), share.ID)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"share":    share,
		"sessions": sessions,
	})
}

func (h *AdminHandler) RevokeShare(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid share ID")
		return
	}

	if err := h.db.RevokeShare(r.Context(), id); err != nil {
		log.Printf("Failed to revoke share: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to revoke share")
		return
	}

	// Terminate all active sessions
	h.db.TerminateAllSessionsForShare(r.Context(), id, models.TerminationReasonRevoked)

	// Log audit event
	h.db.LogAuditEvent(r.Context(), database.AuditEventShareRevoked, &id, nil, nil, nil, nil)

	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (h *AdminHandler) UpdateShare(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid share ID")
		return
	}

	var req models.UpdateShareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var passwordHash *string
	if req.Password != nil && *req.Password != "" {
		hash, err := middleware.HashPassword(*req.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to hash password")
			return
		}
		passwordHash = &hash
	}

	if err := h.db.UpdateShare(r.Context(), id, req.MaxTotalPlays, req.MaxConcurrentViewers, req.ExtendMinutes, passwordHash, req.RemovePassword); err != nil {
		log.Printf("Failed to update share: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to update share")
		return
	}

	// Log audit event
	h.db.LogAuditEvent(r.Context(), database.AuditEventShareUpdated, &id, nil, nil, nil, map[string]interface{}{
		"updates": req,
	})

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *AdminHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	activeShares, _ := h.db.GetActiveSharesCount(r.Context())

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"activeShares": activeShares,
	})
}

func (h *AdminHandler) GetShareAnalytics(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid share ID")
		return
	}

	analytics, err := h.db.GetShareAnalytics(r.Context(), id)
	if err != nil {
		log.Printf("Failed to get analytics: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to get analytics")
		return
	}

	writeJSON(w, http.StatusOK, analytics)
}
