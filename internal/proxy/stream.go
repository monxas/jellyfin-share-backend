package proxy

import (
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jellyfin-share/jellyfin-share-backend/internal/config"
	"github.com/jellyfin-share/jellyfin-share-backend/internal/database"
	"github.com/jellyfin-share/jellyfin-share-backend/internal/jellyfin"
	"github.com/jellyfin-share/jellyfin-share-backend/internal/models"
)

type StreamProxy struct {
	db         *database.DB
	jf         *jellyfin.Client
	cfg        *config.Config
	httpClient *http.Client
}

func NewStreamProxy(db *database.DB, jf *jellyfin.Client, cfg *config.Config) *StreamProxy {
	return &StreamProxy{
		db:  db,
		jf:  jf,
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 0, // No timeout for streaming
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

func (p *StreamProxy) ServeStream(w http.ResponseWriter, r *http.Request) {
	sessionIDStr := chi.URLParam(r, "sessionId")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		http.Error(w, "invalid session", http.StatusBadRequest)
		return
	}

	// Validate session
	session, err := p.db.GetSessionByID(r.Context(), sessionID)
	if err != nil || session == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	if session.FinishedAt.Valid {
		http.Error(w, "session has ended", http.StatusForbidden)
		return
	}

	// Check if session is still active (heartbeat)
	if !session.IsActive(p.cfg.SessionHeartbeatTimeout) {
		p.db.FinishSession(r.Context(), sessionID, models.TerminationReasonTimeout)
		p.db.DecrementConcurrentViewers(r.Context(), session.ShareID)
		http.Error(w, "session timed out", http.StatusForbidden)
		return
	}

	// Treat this segment/playlist fetch as an implicit heartbeat so that
	// external players (VLC/mpv/IINA) that don't call the explicit
	// heartbeat endpoint keep their session alive while actively streaming.
	// Fire-and-forget: an error here shouldn't abort the proxy.
	if err := p.db.UpdateSessionHeartbeat(r.Context(), sessionID, nil); err != nil {
		log.Printf("implicit heartbeat update failed for %s: %v", sessionID, err)
	}

	// Get the share
	share, err := p.db.GetShareByID(r.Context(), session.ShareID)
	if err != nil || share == nil || !share.IsValid() {
		http.Error(w, "share not available", http.StatusForbidden)
		return
	}

	// Get the path after the session ID
	path := chi.URLParam(r, "*")
	if path == "" {
		path = "master.m3u8"
	}

	// Determine which item to stream
	// For Season shares, the itemId or MediaSourceId query param specifies the episode
	itemID := share.JellyfinItemID
	if episodeID := r.URL.Query().Get("itemId"); episodeID != "" {
		itemID = episodeID
	} else if mediaSourceID := r.URL.Query().Get("MediaSourceId"); mediaSourceID != "" {
		itemID = mediaSourceID
	}

	// Build Jellyfin URL (sessionIDStr is used as PlaySessionId so Jellyfin
	// can register the TranscodingJob for this viewer)
	jellyfinURL := p.buildJellyfinStreamURL(itemID, path, r.URL.RawQuery, sessionIDStr)

	// Proxy the request
	p.proxyRequest(w, r, jellyfinURL)
}

func (p *StreamProxy) buildJellyfinStreamURL(itemID, path, query, playSessionID string) string {
	baseURL := p.jf.BaseURL()

	// Parse existing query and ensure api_key is set (don't duplicate)
	params, _ := url.ParseQuery(query)
	if params.Get("api_key") == "" {
		params.Set("api_key", p.jf.APIKey())
	}

	// Handle different path types
	if strings.HasSuffix(path, ".m3u8") {
		// HLS manifest
		if path == "master.m3u8" {
			params.Set("MediaSourceId", itemID)
			params.Set("DeviceId", "jfshare-backend")

			// PlaySessionId is REQUIRED. Without it, Jellyfin returns a
			// playlist but never registers a TranscodingJob, so every
			// segment request hits "no transcode is running".
			params.Set("PlaySessionId", playSessionID)

			// Codec hints for Chrome/Firefox/Safari.
			// Jellyfin will direct-stream when the source already matches
			// these codecs, and transcode otherwise.
			//   AAC listed first  => AAC is used whenever audio must be transcoded.
			//   AC3 listed second => AC3 passthrough when the source is already AC3.
			params.Set("VideoCodec", "h264")
			params.Set("AudioCodec", "aac,ac3")
			params.Set("TranscodingMaxAudioChannels", "2") // stereo downmix when transcoding
			params.Set("SegmentContainer", "ts")

			// Bitrate caps — client-overridable within a safety ceiling.
			// Client picks a preset on the share page, we clamp so nobody
			// can request a 500 Mbps stream by editing the URL.
			const maxAllowedBitrate = 20000000 // 20 Mbps hard ceiling
			if vb := params.Get("VideoBitrate"); vb != "" {
				if n, err := strconv.Atoi(vb); err == nil && n > maxAllowedBitrate {
					params.Set("VideoBitrate", strconv.Itoa(maxAllowedBitrate))
				}
			} else {
				params.Set("VideoBitrate", "8000000")
			}
			if mb := params.Get("MaxStreamingBitrate"); mb != "" {
				if n, err := strconv.Atoi(mb); err == nil && n > maxAllowedBitrate {
					params.Set("MaxStreamingBitrate", strconv.Itoa(maxAllowedBitrate))
				}
			}
			if params.Get("AudioBitrate") == "" {
				params.Set("AudioBitrate", "192000")
			}

			// MaxWidth/MaxHeight and AudioStreamIndex pass through untouched
			// if the client sent them — they're exactly the right names for
			// Jellyfin's master.m3u8 endpoint.

			return baseURL + "/Videos/" + itemID + "/master.m3u8?" + params.Encode()
		}
		// Sub-playlist - forward client params as-is
		return baseURL + "/Videos/" + itemID + "/" + path + "?" + params.Encode()
	}

	if strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".m4s") || strings.HasSuffix(path, ".mp4") {
		// Segment file - remove AudioCodec param only when it's the bogus
		// "m3u8" value (URL extension leaking through). Preserve valid
		// values like "copy", "aac", "ac3" — Jellyfin uses them in the
		// transcode-session hash and stripping them breaks segment lookup.
		if params.Get("AudioCodec") == "m3u8" {
			params.Del("AudioCodec")
		}
		return baseURL + "/Videos/" + itemID + "/" + path + "?" + params.Encode()
	}

	// Generic video stream
	params.Set("Static", "true")
	params.Set("mediaSourceId", itemID)
	return baseURL + "/Videos/" + itemID + "/stream?" + params.Encode()
}

func (p *StreamProxy) proxyRequest(w http.ResponseWriter, r *http.Request, targetURL string) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, nil)
	if err != nil {
		log.Printf("Failed to create proxy request: %v", err)
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}

	// Copy relevant headers
	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	if acceptHeader := r.Header.Get("Accept"); acceptHeader != "" {
		req.Header.Set("Accept", acceptHeader)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		log.Printf("Failed to proxy request to Jellyfin: %v", err)
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		// Skip hop-by-hop headers
		if isHopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Set cache control for streaming content
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	w.WriteHeader(resp.StatusCode)

	// Stream the response
	io.Copy(w, resp.Body)
}

func isHopByHopHeader(header string) bool {
	hopByHopHeaders := map[string]bool{
		"Connection":          true,
		"Keep-Alive":          true,
		"Proxy-Authenticate":  true,
		"Proxy-Authorization": true,
		"Te":                  true,
		"Trailers":            true,
		"Transfer-Encoding":   true,
		"Upgrade":             true,
	}
	return hopByHopHeaders[header]
}

// ServeImage proxies images from Jellyfin
func (p *StreamProxy) ServeImage(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	imageType := chi.URLParam(r, "type")

	share, err := p.db.GetShareByToken(r.Context(), token)
	if err != nil || share == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	var imageURL string
	switch imageType {
	case "poster":
		imageURL = p.jf.GetPosterURL(share.JellyfinItemID)
	case "backdrop":
		imageURL = p.jf.GetBackdropURL(share.JellyfinItemID)
	case "logo":
		imageURL = p.jf.GetLogoURL(share.JellyfinItemID)
	case "thumb":
		imageURL = p.jf.GetThumbURL(share.JellyfinItemID)
	default:
		http.Error(w, "invalid image type", http.StatusBadRequest)
		return
	}

	// Add API key
	imageURL += "?api_key=" + p.jf.APIKey()

	// Add query params for sizing
	if maxWidth := r.URL.Query().Get("maxWidth"); maxWidth != "" {
		imageURL += "&maxWidth=" + maxWidth
	}
	if maxHeight := r.URL.Query().Get("maxHeight"); maxHeight != "" {
		imageURL += "&maxHeight=" + maxHeight
	}
	if quality := r.URL.Query().Get("quality"); quality != "" {
		imageURL += "&quality=" + quality
	}

	// Proxy with caching enabled
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, imageURL, nil)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		http.Error(w, "error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy headers
	for key, values := range resp.Header {
		if isHopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Enable caching for images
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
