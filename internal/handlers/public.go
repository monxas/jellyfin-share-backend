package handlers

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"encoding/json"
	"errors"
	"fmt"
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
				IsText:       stream.IsText,
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

// errTrackNotAvailable means the viewer asked for a track this item does not have.
// Anything else returned by pinPlaybackParams is an upstream failure and must not
// be reported to the viewer as a bad track.
var errTrackNotAvailable = errors.New("requested audio or subtitle track is not available")

// pinPlaybackParams resolves everything the stream proxy must not take from the
// viewer: the audio/subtitle selection, the codec and the transcode target bitrate.
//
// lenient applies to an episode inside a season or series share. There the track
// list the viewer chose from was probed on a different episode, so an index that
// does not exist here is a stale guess, not a bad request - fall back to the
// default track instead of refusing to play.
func (h *PublicHandler) pinPlaybackParams(r *http.Request, session *models.ShareSession, itemType, itemID string, lenient bool) error {
	item, err := h.jf.GetItem(r.Context(), itemID)
	if err != nil {
		// Playback itself can still work; without a bitrate Jellyfin would fall back
		// to 128 kbit/s on a transcode, so use the configured cap rather than nothing.
		log.Printf("Failed to fetch item %s for playback params: %v", itemID, err)
		session.VideoBitrate = sql.NullInt64{Int64: int64(h.cfg.MaxTranscodeBitrate), Valid: true}
		if r.URL.Query().Get("audioStreamIndex") == "" && r.URL.Query().Get("subtitleStreamIndex") == "" {
			return nil
		}
		return fmt.Errorf("cannot verify track selection: %w", err)
	}

	// Pin the source the indices below are read from, so the proxy addresses the
	// same one rather than assuming it equals the item.
	if len(item.MediaSources) > 0 && item.MediaSources[0].ID != "" {
		session.MediaSourceID = sql.NullString{String: item.MediaSources[0].ID, Valid: true}
	}

	// Codec first: the bitrate target depends on what we are re-encoding into.
	codec := negotiateVideoCodec(item, r.URL.Query().Get("videoCodecs"), h.cfg.StreamVideoCodec)
	if codec != "" {
		session.VideoCodec = sql.NullString{String: codec, Valid: true}
	}
	bitrate := transcodeBitrate(item, codec, h.cfg.MaxTranscodeBitrate)
	// A share may cap its own quality; a cap only ever lowers what the source gives.
	if share := h.shareForSession(r.Context(), session); share != nil {
		if share.MaxVideoBitrate.Valid && int(share.MaxVideoBitrate.Int64) < bitrate {
			bitrate = int(share.MaxVideoBitrate.Int64)
		}
		if share.MaxVideoHeight.Valid {
			session.MaxVideoHeight = share.MaxVideoHeight
		}
	}
	session.VideoBitrate = sql.NullInt64{Int64: int64(bitrate), Valid: true}

	wantAudio := r.URL.Query().Get("audioStreamIndex")
	wantSubs := r.URL.Query().Get("subtitleStreamIndex")
	if wantAudio == "" && wantSubs == "" {
		return nil
	}

	audio, subs := h.tracksForItem(r.Context(), item, itemType, itemID)

	// In lenient mode the index came from a different episode, where stream N is
	// often a different language. Match on language first so the viewer gets the
	// track they picked rather than whatever happens to sit at that index here.
	if lenient {
		if lang := r.URL.Query().Get("audioLanguage"); lang != "" {
			if idx, found := indexForLanguage(audioLanguages(audio), lang); found {
				wantAudio = strconv.Itoa(idx)
			}
		}
		if lang := r.URL.Query().Get("subtitleLanguage"); lang != "" {
			if idx, found := indexForLanguage(subtitleLanguages(subs), lang); found {
				wantSubs = strconv.Itoa(idx)
			}
		}
	}

	a, ok := resolveTrackSelection(wantAudio, audio, subs, true)
	if !ok {
		if !lenient {
			return errTrackNotAvailable
		}
		a = sql.NullInt64{}
	}
	b, ok := resolveTrackSelection(wantSubs, audio, subs, false)
	if !ok {
		if !lenient {
			return errTrackNotAvailable
		}
		b = sql.NullInt64{}
	}
	session.AudioStreamIndex = a

	// A text subtitle travels as a WebVTT sidecar: the viewer can toggle it and the
	// video needs no re-encode. Only image formats have to be rendered into the
	// picture, which is what SubtitleStreamIndex triggers.
	if b.Valid && isTextSubtitle(subs, b.Int64) {
		session.VTTSubtitleIndex = b
	} else {
		session.SubtitleStreamIndex = b
	}
	return nil
}

func audioLanguages(tracks []models.AudioTrack) map[int]string {
	out := make(map[int]string, len(tracks))
	for _, t := range tracks {
		out[t.Index] = strings.ToLower(t.Language)
	}
	return out
}

func subtitleLanguages(tracks []models.SubtitleTrack) map[int]string {
	out := make(map[int]string, len(tracks))
	for _, t := range tracks {
		out[t.Index] = strings.ToLower(t.Language)
	}
	return out
}

// indexForLanguage finds the lowest stream index carrying a language, so the
// choice stays stable when an episode offers the same language more than once.
func indexForLanguage(byIndex map[int]string, want string) (int, bool) {
	want = strings.ToLower(strings.TrimSpace(want))
	if want == "" {
		return 0, false
	}
	best, found := 0, false
	for idx, lang := range byIndex {
		if lang == want && (!found || idx < best) {
			best, found = idx, true
		}
	}
	return best, found
}

func isTextSubtitle(subs []models.SubtitleTrack, index int64) bool {
	for _, t := range subs {
		if int64(t.Index) == index {
			return t.IsText
		}
	}
	return false
}

// negotiableVideoCodecs are the codecs a stream may be *copied* as. AV1 is
// deliberately absent even when a browser reports decoding it: this HLS path
// packages segments as mpegts, and AV1 in mpegts is what produced the black
// picture with audio-only playback in the first place. It always gets re-encoded.
var negotiableVideoCodecs = map[string]bool{"h264": true, "hevc": true}

// negotiateVideoCodec picks the codec to ask Jellyfin for. If the source codec is
// one the viewer's browser reported and one we trust in this container, name it so
// Jellyfin stream-copies. Otherwise fall back, which means a re-encode.
func negotiateVideoCodec(item *jellyfin.ItemInfo, clientCodecs string, fallback string) string {
	if clientCodecs == "" || item == nil || len(item.MediaSources) == 0 {
		return fallback
	}
	source := ""
	for _, st := range item.MediaSources[0].MediaStreams {
		if st.Type == "Video" {
			source = strings.ToLower(st.Codec)
			break
		}
	}
	if source == "" || !negotiableVideoCodecs[source] {
		return fallback
	}
	for _, c := range strings.Split(clientCodecs, ",") {
		if strings.ToLower(strings.TrimSpace(c)) == source {
			return source
		}
	}
	return fallback
}

// minTranscodeBitrate keeps a pathologically small source from setting a target so
// low that the encoder throws away detail the viewer would notice.
const minTranscodeBitrate = 1000000

// codecBitrateFactor is roughly the bitrate a codec needs for a given quality,
// relative to h264. HEVC and AV1 reach the same picture with fewer bits, so
// re-encoding one of them to h264 at the source's own bitrate loses quality
// visibly - the target has to be scaled up. Older codecs go the other way.
var codecBitrateFactor = map[string]float64{
	"h264": 1.0, "avc": 1.0,
	"hevc": 0.6, "h265": 0.6,
	"av1":  0.5,
	"vp9":  0.65,
	"vp8":  1.1,
	"vc1":  1.2,
	"mpeg4": 1.6, "msmpeg4v3": 1.6,
	"mpeg2video": 2.0,
}

// sourceVideo returns the source's video bitrate and codec. The video stream's own
// rate is preferred over the container's, which also counts audio - Jellyfin's
// VideoBitrate is video only.
func sourceVideo(item *jellyfin.ItemInfo) (bitrate int, codec string) {
	if item == nil || len(item.MediaSources) == 0 {
		return 0, ""
	}
	ms := item.MediaSources[0]
	for _, st := range ms.MediaStreams {
		if st.Type == "Video" {
			return st.BitRate, strings.ToLower(st.Codec)
		}
	}
	return ms.Bitrate, ""
}

// transcodeBitrate picks the target bitrate for a possible transcode. Jellyfin has
// no "auto" for this - omitting the parameter makes it encode at 128 kbit/s and
// downscale to 416x234 regardless of the source. The source's own rate is the
// starting point, adjusted for the efficiency gap between the source codec and the
// one we are asking for, then clamped.
func transcodeBitrate(item *jellyfin.ItemInfo, targetCodec string, max int) int {
	bitrate, sourceCodec := sourceVideo(item)
	if bitrate <= 0 {
		if item != nil && len(item.MediaSources) > 0 && item.MediaSources[0].Bitrate > 0 {
			bitrate = item.MediaSources[0].Bitrate
		} else {
			return max
		}
	}

	// A stream copy ignores the bitrate; only a re-encode needs the adjustment and
	// the floor. Raising it for a copy would just put a misleading number on record.
	target := strings.ToLower(targetCodec)
	if target != "" && sourceCodec != "" && target != sourceCodec {
		from, okFrom := codecBitrateFactor[sourceCodec]
		to, okTo := codecBitrateFactor[target]
		if okFrom && okTo && from > 0 {
			bitrate = int(float64(bitrate) * (to / from))
		}
		if bitrate < minTranscodeBitrate {
			bitrate = minTranscodeBitrate
		}
	}
	if bitrate > max {
		bitrate = max
	}
	return bitrate
}

// shareForSession loads the share a session belongs to. Errors are not fatal here:
// the quality cap is a preference, and playback should not fail over it.
func (h *PublicHandler) shareForSession(ctx context.Context, session *models.ShareSession) *models.Share {
	share, err := h.db.GetShareByID(ctx, session.ShareID)
	if err != nil {
		log.Printf("Failed to load share for quality cap: %v", err)
		return nil
	}
	return share
}

// subtitleURL points at our own proxy, never at Jellyfin. It is deliberately
// relative: a <track> with a cross-origin src is only fetched when the media
// element is in CORS mode, so an absolute URL built from PublicBaseURL silently
// yields no subtitles whenever the viewer reached the page on another hostname -
// including the repo's own Vite dev setup.
func (h *PublicHandler) subtitleURL(session *models.ShareSession) string {
	if !session.VTTSubtitleIndex.Valid {
		return ""
	}
	return fmt.Sprintf("/api/public/subtitles/%s/%d.vtt",
		session.ID.String(), session.VTTSubtitleIndex.Int64)
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

	if err := h.pinPlaybackParams(r, session, share.ItemType, share.JellyfinItemID, false); err != nil {
		if errors.Is(err, errTrackNotAvailable) {
			writeError(w, http.StatusBadRequest, err.Error())
		} else {
			log.Printf("Failed to prepare playback: %v", err)
			writeError(w, http.StatusBadGateway, "could not reach the media server")
		}
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
		SubtitleURL: h.subtitleURL(session),
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
		// One recursive query rather than one per season: this runs on two
		// unauthenticated endpoints, and a 20-season show would otherwise cost 21
		// upstream calls per request.
		return h.jf.GetSeriesEpisodes(ctx, share.JellyfinItemID)
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

	// lenient: the track list came from a different episode of this season/series.
	if err := h.pinPlaybackParams(r, session, "Episode", episodeID, true); err != nil {
		log.Printf("Failed to prepare episode playback: %v", err)
		writeError(w, http.StatusBadGateway, "could not reach the media server")
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
		SubtitleURL: h.subtitleURL(session),
	})
}
