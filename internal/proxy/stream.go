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

	// Determine which item to stream. This comes from the session, which pinned it at
	// play time after the share (and, for a season, the episode) was validated. It must
	// never come from the request: the viewer controls the query string, and taking an
	// item id from there turned every share link into a key to the whole library.
	itemID := share.JellyfinItemID
	if session.JellyfinItemID.Valid && session.JellyfinItemID.String != "" {
		itemID = session.JellyfinItemID.String
	}

	// Build Jellyfin URL
	jellyfinURL := p.buildJellyfinStreamURL(itemID, path, r.URL.RawQuery, session)

	// Proxy the request
	p.proxyRequest(w, r, jellyfinURL)
}

func (p *StreamProxy) buildJellyfinStreamURL(itemID, path, query string, session *models.ShareSession) string {
	baseURL := p.jf.BaseURL()

	// Auth goes in the Authorization header (set in proxyRequest); strip any
	// api_key echoed back from Jellyfin-generated manifests so it never
	// reaches viewers.
	params, _ := url.ParseQuery(query)
	params.Del("api_key")
	// Pin the source to the same item as the path. Jellyfin honours MediaSourceId over
	// the path, so leaving a viewer-supplied value here would re-open the hole that
	// taking itemID from the session closes. itemId is Jellyfin-irrelevant; drop it.
	params.Del("itemId")
	params.Set("MediaSourceId", itemID)

	// Track selection comes from the session too, for the same reason as itemID.
	// Drop whatever the viewer sent before applying the validated choice.
	params.Del("AudioStreamIndex")
	params.Del("SubtitleStreamIndex")
	params.Del("SubtitleMethod")
	if session != nil {
		if session.AudioStreamIndex.Valid {
			params.Set("AudioStreamIndex", strconv.FormatInt(session.AudioStreamIndex.Int64, 10))
		}
		if session.SubtitleStreamIndex.Valid {
			params.Set("SubtitleStreamIndex", strconv.FormatInt(session.SubtitleStreamIndex.Int64, 10))
			// Burn subtitles into the video: HLS side-car tracks would need a second
			// proxied endpoint and would not survive the transcode.
			params.Set("SubtitleMethod", "Encode")
		}
	}

	// Handle different path types
	if strings.HasSuffix(path, ".m3u8") {
		// HLS manifest
		if path == "master.m3u8" {
			params.Set("DeviceId", "jfshare-backend")
			return baseURL + "/Videos/" + itemID + "/master.m3u8?" + params.Encode()
		}
		// Sub-playlist
		return baseURL + "/Videos/" + itemID + "/" + path + "?" + params.Encode()
	}

	if strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".m4s") || strings.HasSuffix(path, ".mp4") {
		// Segment file - remove AudioCodec param as it can confuse FFmpeg
		// (AudioCodec=m3u8 from manifest URLs is not a valid codec)
		params.Del("AudioCodec")
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
	req.Header.Set("Authorization", p.jf.AuthHeader())

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

	// Add query params for sizing
	sizing := url.Values{}
	if maxWidth := r.URL.Query().Get("maxWidth"); maxWidth != "" {
		sizing.Set("maxWidth", maxWidth)
	}
	if maxHeight := r.URL.Query().Get("maxHeight"); maxHeight != "" {
		sizing.Set("maxHeight", maxHeight)
	}
	if quality := r.URL.Query().Get("quality"); quality != "" {
		sizing.Set("quality", quality)
	}
	if len(sizing) > 0 {
		imageURL += "?" + sizing.Encode()
	}

	// Proxy with caching enabled
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, imageURL, nil)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Authorization", p.jf.AuthHeader())

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
