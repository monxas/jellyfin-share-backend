package handlers

import (
	"context"
	"database/sql"
	"strconv"
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
	if item != nil || share.ItemType == "Series" || share.ItemType == "Season" {
		info.AudioTracks, info.SubtitleTracks = h.tracksForItem(r.Context(), item, share.ItemType, share.JellyfinItemID)
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
				quality.Resolution = getResolutionLabel(stream.Width, stream.Height)
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

// getResolutionLabel classifies a video stream the way Jellyfin's own clients do:
// primarily by width, with height only as a fallback. Classifying by height alone
// mislabels every letterboxed release - a 2.40:1 scope film is 1920x800, which is
// Full HD but falls below a 1080-height threshold and gets tagged "720p".
func getResolutionLabel(width, height int) string {
	switch {
	case width >= 3800 || height >= 2000:
		return "4K"
	case width >= 2500 || height >= 1400:
		return "1440p"
	case width >= 1900 || height >= 1000:
		return "1080p"
	case width >= 1260 || height >= 700:
		return "720p"
	case width >= 700 || height >= 400:
		return "480p"
	default:
		return ""
	}
}

func extractTracks(ms jellyfin.MediaSource) ([]models.AudioTrack, []models.SubtitleTrack) {
	var audio []models.AudioTrack
	var subs []models.SubtitleTrack
	for _, stream := range ms.MediaStreams {
		switch stream.Type {
		case "Audio":
			audio = append(audio, models.AudioTrack{
				Index:        stream.Index,
				Language:     stream.Language,
				DisplayTitle: stream.DisplayTitle,
				Codec:        stream.Codec,
				Channels:     stream.Channels,
				IsDefault:    stream.IsDefault,
			})
		case "Subtitle":
			subs = append(subs, models.SubtitleTrack{
				Index:        stream.Index,
				Language:     stream.Language,
				DisplayTitle: stream.DisplayTitle,
				Codec:        stream.Codec,
				IsDefault:    stream.IsDefault,
				IsForced:     stream.IsForced,
			})
		}
	}
	return audio, subs
}

// tracksForItem returns the selectable tracks of an item. A Series carries no media
// streams itself, so fall back to its first episode - one traversal, not one per
// track kind.
func (h *PublicHandler) tracksForItem(ctx context.Context, item *jellyfin.ItemInfo, itemType, itemID string) ([]models.AudioTrack, []models.SubtitleTrack) {
	if item != nil && len(item.MediaSources) > 0 {
		return extractTracks(item.MediaSources[0])
	}
	if itemType != "Series" && itemType != "Season" {
		return nil, nil
	}
	episodeID := ""
	if itemType == "Season" {
		if eps, err := h.jf.GetSeasonEpisodes(ctx, itemID); err == nil && len(eps) > 0 {
			episodeID = eps[0].ID
		}
	} else {
		if seasons, err := h.jf.GetSeriesSeasons(ctx, itemID); err == nil && len(seasons) > 0 {
			if eps, err := h.jf.GetSeasonEpisodes(ctx, seasons[0].ID); err == nil && len(eps) > 0 {
				episodeID = eps[0].ID
			}
		}
	}
	if episodeID == "" {
		return nil, nil
	}
	ep, err := h.jf.GetItem(ctx, episodeID)
	if err != nil || ep == nil || len(ep.MediaSources) == 0 {
		return nil, nil
	}
	return extractTracks(ep.MediaSources[0])
}

// resolveTrackSelection validates a requested stream index against what the item
// actually offers. An unknown index is rejected rather than passed to Jellyfin.
func resolveTrackSelection(raw string, audio []models.AudioTrack, subs []models.SubtitleTrack, wantAudio bool) (sql.NullInt64, bool) {
	if raw == "" {
		return sql.NullInt64{}, true
	}
	idx, err := strconv.Atoi(raw)
	if err != nil {
		return sql.NullInt64{}, false
	}
	if wantAudio {
		for _, t := range audio {
			if t.Index == idx {
				return sql.NullInt64{Int64: int64(idx), Valid: true}, true
			}
		}
		return sql.NullInt64{}, false
	}
	for _, t := range subs {
		if t.Index == idx {
			return sql.NullInt64{Int64: int64(idx), Valid: true}, true
		}
	}
	return sql.NullInt64{}, false
}

// pinTrackSelection validates the requested audio/subtitle indices for itemID and
// writes them onto the session. Returns false if the viewer asked for a track the
// item does not have.
func (h *PublicHandler) pinTrackSelection(r *http.Request, session *models.ShareSession, itemType, itemID string) bool {
	wantAudio := r.URL.Query().Get("audioStreamIndex")
	wantSubs := r.URL.Query().Get("subtitleStreamIndex")
	if wantAudio == "" && wantSubs == "" {
		return true
	}

	item, err := h.jf.GetItem(r.Context(), itemID)
	if err != nil {
		log.Printf("Failed to fetch item %s for track selection: %v", itemID, err)
		return false
	}
	audio, subs := h.tracksForItem(r.Context(), item, itemType, itemID)

	a, ok := resolveTrackSelection(wantAudio, audio, subs, true)
	if !ok {
		return false
	}
	b, ok := resolveTrackSelection(wantSubs, audio, subs, false)
	if !ok {
		return false
	}
	session.AudioStreamIndex = a
	session.SubtitleStreamIndex = b
	return true
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
		JellyfinItemID:  sql.NullString{String: share.JellyfinItemID, Valid: true},
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

	if !h.pinTrackSelection(r, session, share.ItemType, share.JellyfinItemID) {
		writeError(w, http.StatusBadRequest, "requested audio or subtitle track is not available")
		return
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

// episodesForShare returns the episodes a share actually grants access to. A Season
// share yields its own episodes; a Series share yields every episode of every season,
// flattened, so the list is playable rather than a list of seasons that cannot be
// started. This is the single source of truth for both listing and play validation.
func (h *PublicHandler) episodesForShare(ctx context.Context, share *models.Share) ([]jellyfin.EpisodeInfo, error) {
	switch share.ItemType {
	case "Season":
		return h.jf.GetSeasonEpisodes(ctx, share.JellyfinItemID)
	case "Series":
		seasons, err := h.jf.GetSeriesSeasons(ctx, share.JellyfinItemID)
		if err != nil {
			return nil, err
		}
		var all []jellyfin.EpisodeInfo
		for _, season := range seasons {
			eps, err := h.jf.GetSeasonEpisodes(ctx, season.ID)
			if err != nil {
				log.Printf("Failed to get episodes of season %s: %v", season.ID, err)
				continue
			}
			all = append(all, eps...)
		}
		return all, nil
	default:
		return nil, nil
	}
}

// GetShareEpisodes returns episodes for a Season or Series share
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

	episodes, err := h.episodesForShare(r.Context(), share)
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

	// Only shares that contain episodes can play one
	if share.ItemType != "Season" && share.ItemType != "Series" {
		writeError(w, http.StatusBadRequest, "this share does not contain episodes")
		return
	}

	// Verify the episode is one this share actually grants
	episodes, err := h.episodesForShare(r.Context(), share)
	if err != nil {
		log.Printf("Failed to get episodes: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to verify episode")
		return
	}

	episodeValid := false
	for _, ep := range episodes {
		if ep.ID == episodeID {
			episodeValid = true
			break
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

	// Create session for the episode. The episode was verified against the season
	// above; pinning it here is what makes that check stick - the stream proxy reads
	// this instead of an item id supplied by the viewer.
	sessionToken := middleware.GenerateSecureToken(32)
	session := &models.ShareSession{
		ID:              uuid.New(),
		ShareID:         share.ID,
		SessionToken:    sessionToken,
		StartedAt:       time.Now(),
		LastHeartbeatAt: time.Now(),
		JellyfinItemID:  sql.NullString{String: episodeID, Valid: true},
	}

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

	if !h.pinTrackSelection(r, session, "Episode", episodeID) {
		writeError(w, http.StatusBadRequest, "requested audio or subtitle track is not available")
		return
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
	playbackURL := h.cfg.PublicBaseURL + "/api/public/stream/" + session.ID.String() + "/master.m3u8"

	writeJSON(w, http.StatusOK, models.PlayResponse{
		SessionID:   session.ID,
		PlaybackURL: playbackURL,
	})
}
