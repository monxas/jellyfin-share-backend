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

type PublicHandler struct {
	db       *database.DB
	jf       *jellyfin.Client
	cfg      *config.Config
	sessions *middleware.ShareSessionManager
}

func NewPublicHandler(db *database.DB, jf *jellyfin.Client, cfg *config.Config, sessions *middleware.ShareSessionManager) *PublicHandler {
	return &PublicHandler{
		db:       db,
		jf:       jf,
		cfg:      cfg,
		sessions: sessions,
	}
}

func (h *PublicHandler) GetShareInfo(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")

	share, err := h.db.GetShareByToken(r.Context(), token)
	if err != nil {
		log.Printf("Failed to get share: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to get share")
		return
	}
	if share == nil {
		writeError(w, http.StatusNotFound, "share not found")
		return
	}

	if share.IsExpired() {
		writeError(w, http.StatusGone, "share has expired")
		return
	}
	if share.IsRevoked() {
		writeError(w, http.StatusGone, "share is no longer available")
		return
	}

	// Log access
	ipHash := middleware.GetIPHash(r.Context())
	h.db.LogAuditEvent(r.Context(), database.AuditEventShareAccessed, &share.ID, nil, nil, &ipHash, nil)

	info := share.ToPublicInfo(h.cfg.PublicBaseURL)

	// Fetch extended metadata from Jellyfin
	item, err := h.jf.GetItem(r.Context(), share.JellyfinItemID)
	if err != nil {
		log.Printf("Failed to fetch Jellyfin item %s: %v", share.JellyfinItemID, err)
	} else if item != nil {
		h.enrichShareInfo(&info, item, token)
	}

	writeJSON(w, http.StatusOK, info)
}

func (h *PublicHandler) enrichShareInfo(info *models.SharePublicInfo, item *jellyfin.ItemInfo, token string) {
	// Year
	if item.ProductionYear > 0 {
		info.Year = item.ProductionYear
	}

	// Tagline
	if len(item.Taglines) > 0 {
		info.Tagline = item.Taglines[0]
	}

	// Ratings
	info.OfficialRating = item.OfficialRating
	info.CommunityRating = item.CommunityRating
	info.CriticRating = item.CriticRating

	// Genres
	info.Genres = item.Genres

	// Studios
	if len(item.Studios) > 0 {
		studios := make([]string, 0, len(item.Studios))
		for _, s := range item.Studios {
			studios = append(studios, s.Name)
		}
		info.Studios = studios
	}

	// People (Directors and Actors)
	if len(item.People) > 0 {
		directors := []string{}
		actors := []models.ActorInfo{}

		for _, p := range item.People {
			switch p.Type {
			case "Director":
				directors = append(directors, p.Name)
			case "Actor":
				if len(actors) < 8 { // Limit to 8 actors
					actors = append(actors, models.ActorInfo{
						Name: p.Name,
						Role: p.Role,
					})
				}
			}
		}
		info.Directors = directors
		info.Actors = actors
	}

	// Logo URL (if available)
	if item.ImageTags.Logo != "" {
		info.LogoURL = h.cfg.PublicBaseURL + "/api/public/images/" + token + "/logo"
	}

	// Video quality info
	if len(item.MediaSources) > 0 {
		ms := item.MediaSources[0]
		quality := &models.VideoQualityInfo{
			Container: ms.Container,
			Bitrate:   ms.Bitrate / 1000, // Convert to kbps
		}

		// Find video and audio streams
		for _, stream := range ms.MediaStreams {
			switch stream.Type {
			case "Video":
				quality.Width = stream.Width
				quality.Height = stream.Height
				quality.Codec = stream.Codec
				quality.Resolution = getResolutionLabel(stream.Height)
			case "Audio":
				if quality.AudioCodec == "" {
					quality.AudioCodec = stream.Codec
				}
			}
		}

		if quality.Resolution != "" {
			info.VideoQuality = quality
		}
	}
}

func getResolutionLabel(height int) string {
	switch {
	case height >= 2160:
		return "4K"
	case height >= 1440:
		return "1440p"
	case height >= 1080:
		return "1080p"
	case height >= 720:
		return "720p"
	case height >= 480:
		return "480p"
	default:
		return ""
	}
}

func (h *PublicHandler) ValidatePassword(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")

	share, err := h.db.GetShareByToken(r.Context(), token)
	if err != nil || share == nil {
		writeError(w, http.StatusNotFound, "share not found")
		return
	}

	if !share.RequiresPassword() {
		writeJSON(w, http.StatusOK, map[string]string{"status": "no password required"})
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ipHash := middleware.GetIPHash(r.Context())

	if !middleware.CheckPassword(req.Password, share.PasswordHash.String) {
		h.db.LogAuditEvent(r.Context(), database.AuditEventPasswordAttempt, &share.ID, nil, nil, &ipHash, map[string]interface{}{
			"success": false,
		})
		writeError(w, http.StatusUnauthorized, "incorrect password")
		return
	}

	// Set session cookie
	h.sessions.SetSessionCookie(w, token)

	h.db.LogAuditEvent(r.Context(), database.AuditEventPasswordAttempt, &share.ID, nil, nil, &ipHash, map[string]interface{}{
		"success": true,
	})

	writeJSON(w, http.StatusOK, map[string]string{"status": "authenticated"})
}

func (h *PublicHandler) StartPlayback(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")

	share, err := h.db.GetShareByToken(r.Context(), token)
	if err != nil || share == nil {
		writeError(w, http.StatusNotFound, "share not found")
		return
	}

	// Validate share state
	if !share.IsValid() {
		if share.IsExpired() {
			writeError(w, http.StatusGone, "share has expired")
		} else {
			writeError(w, http.StatusGone, "share is no longer available")
		}
		return
	}

	// Check password if required
	if share.RequiresPassword() && !h.sessions.GetSessionFromCookie(r, token) {
		writeError(w, http.StatusUnauthorized, "password required")
		return
	}

	// Clean up stale sessions first
	staleCount, _ := h.db.TerminateStaleSessionsForShare(r.Context(), share.ID, h.cfg.SessionHeartbeatTimeout)
	if staleCount > 0 {
		// Reconcile the concurrent viewer count
		h.db.ReconcileConcurrentViewers(r.Context(), share.ID, h.cfg.SessionHeartbeatTimeout)
		// Refresh share data
		share, _ = h.db.GetShareByToken(r.Context(), token)
	}

	// Check limits
	if !share.CanStartNewPlay() {
		ipHash := middleware.GetIPHash(r.Context())
		h.db.LogAuditEvent(r.Context(), database.AuditEventPlaybackDenied, &share.ID, nil, nil, &ipHash, map[string]interface{}{
			"reason":           "limit_reached",
			"totalPlays":       share.TotalPlays,
			"maxTotalPlays":    share.MaxTotalPlays,
			"concurrentViewers": share.CurrentConcurrentViewers,
			"maxConcurrent":    share.MaxConcurrentViewers,
		})

		if share.MaxTotalPlays.Valid && int64(share.TotalPlays) >= share.MaxTotalPlays.Int64 {
			writeError(w, http.StatusForbidden, "maximum plays reached")
		} else {
			writeError(w, http.StatusForbidden, "maximum concurrent viewers reached")
		}
		return
	}

	// Create session
	sessionToken := middleware.GenerateSecureToken(32)
	session := &models.ShareSession{
		ID:              uuid.New(),
		ShareID:         share.ID,
		SessionToken:    sessionToken,
		StartedAt:       time.Now(),
		LastHeartbeatAt: time.Now(),
	}

	// Add client info
	ipHash := middleware.GetIPHash(r.Context())
	if ipHash != "" {
		session.ClientIPHash = sql.NullString{String: ipHash, Valid: true}
	}
	userAgent := r.Header.Get("User-Agent")
	if userAgent != "" {
		if len(userAgent) > 256 {
			userAgent = userAgent[:256]
		}
		session.UserAgent = sql.NullString{String: userAgent, Valid: true}
	}

	if err := h.db.CreateSession(r.Context(), session); err != nil {
		log.Printf("Failed to create session: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to start playback")
		return
	}

	// Increment counters
	if err := h.db.IncrementPlayCount(r.Context(), share.ID); err != nil {
		log.Printf("Failed to increment play count: %v", err)
	}

	// Log audit event
	h.db.LogAuditEvent(r.Context(), database.AuditEventPlaybackStarted, &share.ID, &session.ID, nil, &ipHash, nil)

	// Generate playback URL
	playbackURL := h.cfg.PublicBaseURL + "/api/public/stream/" + session.ID.String() + "/master.m3u8"

	writeJSON(w, http.StatusOK, models.PlayResponse{
		SessionID:   session.ID,
		PlaybackURL: playbackURL,
	})
}

func (h *PublicHandler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	sessionIDStr := chi.URLParam(r, "sessionId")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid session ID")
		return
	}

	session, err := h.db.GetSessionByID(r.Context(), sessionID)
	if err != nil || session == nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	if session.FinishedAt.Valid {
		writeJSON(w, http.StatusOK, models.HeartbeatResponse{
			Status:  "terminated",
			Message: "session has ended",
		})
		return
	}

	// Get the share to check if it's still valid
	share, _ := h.db.GetShareByID(r.Context(), session.ShareID)
	if share == nil || !share.IsValid() {
		// Terminate this session
		var reason models.TerminationReason
		var status string
		if share == nil || share.IsRevoked() {
			reason = models.TerminationReasonRevoked
			status = "revoked"
		} else {
			reason = models.TerminationReasonExpired
			status = "expired"
		}
		h.db.FinishSession(r.Context(), sessionID, reason)
		h.db.DecrementConcurrentViewers(r.Context(), session.ShareID)

		writeJSON(w, http.StatusOK, models.HeartbeatResponse{
			Status:  status,
			Message: "share is no longer available",
		})
		return
	}

	// Parse request
	var req models.HeartbeatRequest
	json.NewDecoder(r.Body).Decode(&req)

	// Update heartbeat
	if err := h.db.UpdateSessionHeartbeat(r.Context(), sessionID, req.PositionSeconds); err != nil {
		log.Printf("Failed to update heartbeat: %v", err)
	}

	// Update share activity
	h.db.UpdateLastActivity(r.Context(), session.ShareID)

	writeJSON(w, http.StatusOK, models.HeartbeatResponse{
		Status: "ok",
	})
}

func (h *PublicHandler) FinishPlayback(w http.ResponseWriter, r *http.Request) {
	sessionIDStr := chi.URLParam(r, "sessionId")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid session ID")
		return
	}

	session, err := h.db.GetSessionByID(r.Context(), sessionID)
	if err != nil || session == nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	if !session.FinishedAt.Valid {
		if err := h.db.FinishSession(r.Context(), sessionID, models.TerminationReasonNormal); err != nil {
			log.Printf("Failed to finish session: %v", err)
		}

		if err := h.db.DecrementConcurrentViewers(r.Context(), session.ShareID); err != nil {
			log.Printf("Failed to decrement concurrent viewers: %v", err)
		}

		// Log audit event
		ipHash := middleware.GetIPHash(r.Context())
		h.db.LogAuditEvent(r.Context(), database.AuditEventPlaybackEnded, &session.ShareID, &session.ID, nil, &ipHash, nil)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "finished"})
}

// GetShareEpisodes returns episodes for a Season share
func (h *PublicHandler) GetShareEpisodes(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")

	share, err := h.db.GetShareByToken(r.Context(), token)
	if err != nil || share == nil {
		writeError(w, http.StatusNotFound, "share not found")
		return
	}

	if !share.IsValid() {
		writeError(w, http.StatusGone, "share is no longer available")
		return
	}

	// Check password if required
	if share.RequiresPassword() && !h.sessions.GetSessionFromCookie(r, token) {
		writeError(w, http.StatusUnauthorized, "password required")
		return
	}

	// Only Season and Series types have episodes/children
	if share.ItemType != "Season" && share.ItemType != "Series" {
		writeError(w, http.StatusBadRequest, "this share does not contain episodes")
		return
	}

	var episodes []jellyfin.EpisodeInfo

	if share.ItemType == "Season" {
		episodes, err = h.jf.GetSeasonEpisodes(r.Context(), share.JellyfinItemID)
	} else {
		// Series share
		seasonID := r.URL.Query().Get("seasonId")
		if seasonID != "" {
			// Validate the season belongs to this series
			seasons, sErr := h.jf.GetSeriesSeasons(r.Context(), share.JellyfinItemID)
			if sErr != nil {
				log.Printf("Failed to get seasons: %v", sErr)
				writeError(w, http.StatusInternalServerError, "failed to verify season")
				return
			}
			seasonValid := false
			for _, s := range seasons {
				if s.ID == seasonID {
					seasonValid = true
					break
				}
			}
			if !seasonValid {
				writeError(w, http.StatusForbidden, "season not part of this series")
				return
			}
			episodes, err = h.jf.GetSeasonEpisodes(r.Context(), seasonID)
		} else {
			episodes, err = h.jf.GetSeriesSeasons(r.Context(), share.JellyfinItemID)
		}
	}

	if err != nil {
		log.Printf("Failed to get episodes: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to get episodes")
		return
	}

	// Add poster URLs
	type EpisodeWithPoster struct {
		jellyfin.EpisodeInfo
		PosterURL string `json:"posterUrl,omitempty"`
	}

	result := make([]EpisodeWithPoster, 0, len(episodes))
	for _, ep := range episodes {
		ewp := EpisodeWithPoster{EpisodeInfo: ep}
		if ep.HasPoster {
			ewp.PosterURL = h.cfg.PublicBaseURL + "/api/public/images/" + token + "/episode/" + ep.ID
		}
		result = append(result, ewp)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"episodes": result,
		"total":    len(result),
	})
}

// StartEpisodePlayback starts playback for a specific episode within a season share
func (h *PublicHandler) StartEpisodePlayback(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	episodeID := chi.URLParam(r, "episodeId")

	share, err := h.db.GetShareByToken(r.Context(), token)
	if err != nil || share == nil {
		writeError(w, http.StatusNotFound, "share not found")
		return
	}

	// Validate share state
	if !share.IsValid() {
		writeError(w, http.StatusGone, "share is no longer available")
		return
	}

	// Check password if required
	if share.RequiresPassword() && !h.sessions.GetSessionFromCookie(r, token) {
		writeError(w, http.StatusUnauthorized, "password required")
		return
	}

	// Verify this is a Season or Series share
	if share.ItemType != "Season" && share.ItemType != "Series" {
		writeError(w, http.StatusBadRequest, "episode playback only available for season or series shares")
		return
	}

	// Verify the episode belongs to this share
	episodeValid := false
	if share.ItemType == "Season" {
		episodes, err := h.jf.GetSeasonEpisodes(r.Context(), share.JellyfinItemID)
		if err != nil {
			log.Printf("Failed to get episodes: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to verify episode")
			return
		}
		for _, ep := range episodes {
			if ep.ID == episodeID {
				episodeValid = true
				break
			}
		}
	} else {
		// Series share — check all seasons
		seasons, err := h.jf.GetSeriesSeasons(r.Context(), share.JellyfinItemID)
		if err != nil {
			log.Printf("Failed to get seasons: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to verify episode")
			return
		}
		for _, season := range seasons {
			eps, err := h.jf.GetSeasonEpisodes(r.Context(), season.ID)
			if err != nil {
				continue
			}
			for _, ep := range eps {
				if ep.ID == episodeID {
					episodeValid = true
					break
				}
			}
			if episodeValid {
				break
			}
		}
	}

	if !episodeValid {
		writeError(w, http.StatusForbidden, "episode not part of this share")
		return
	}

	// Clean up stale sessions first
	staleCount, _ := h.db.TerminateStaleSessionsForShare(r.Context(), share.ID, h.cfg.SessionHeartbeatTimeout)
	if staleCount > 0 {
		h.db.ReconcileConcurrentViewers(r.Context(), share.ID, h.cfg.SessionHeartbeatTimeout)
		share, _ = h.db.GetShareByToken(r.Context(), token)
	}

	// Check limits
	if !share.CanStartNewPlay() {
		ipHash := middleware.GetIPHash(r.Context())
		h.db.LogAuditEvent(r.Context(), database.AuditEventPlaybackDenied, &share.ID, nil, nil, &ipHash, map[string]interface{}{
			"reason":    "limit_reached",
			"episodeId": episodeID,
		})

		if share.MaxTotalPlays.Valid && int64(share.TotalPlays) >= share.MaxTotalPlays.Int64 {
			writeError(w, http.StatusForbidden, "maximum plays reached")
		} else {
			writeError(w, http.StatusForbidden, "maximum concurrent viewers reached")
		}
		return
	}

	// Create session for the episode
	sessionToken := middleware.GenerateSecureToken(32)
	session := &models.ShareSession{
		ID:              uuid.New(),
		ShareID:         share.ID,
		SessionToken:    sessionToken,
		StartedAt:       time.Now(),
		LastHeartbeatAt: time.Now(),
	}

	// Store episode ID in session (we'll use client IP hash field for now, or add a note)
	ipHash := middleware.GetIPHash(r.Context())
	if ipHash != "" {
		session.ClientIPHash = sql.NullString{String: ipHash, Valid: true}
	}
	userAgent := r.Header.Get("User-Agent")
	if userAgent != "" {
		if len(userAgent) > 256 {
			userAgent = userAgent[:256]
		}
		session.UserAgent = sql.NullString{String: userAgent, Valid: true}
	}

	if err := h.db.CreateSession(r.Context(), session); err != nil {
		log.Printf("Failed to create session: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to start playback")
		return
	}

	// Increment counters
	if err := h.db.IncrementPlayCount(r.Context(), share.ID); err != nil {
		log.Printf("Failed to increment play count: %v", err)
	}

	// Log audit event
	h.db.LogAuditEvent(r.Context(), database.AuditEventPlaybackStarted, &share.ID, &session.ID, nil, &ipHash, map[string]interface{}{
		"episodeId": episodeID,
	})

	// Generate playback URL for the specific episode
	playbackURL := h.cfg.PublicBaseURL + "/api/public/stream/" + session.ID.String() + "/master.m3u8?itemId=" + episodeID

	writeJSON(w, http.StatusOK, models.PlayResponse{
		SessionID:   session.ID,
		PlaybackURL: playbackURL,
	})
}
