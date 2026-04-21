package models

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type Share struct {
	ID                       uuid.UUID      `db:"id" json:"id"`
	PublicToken              string         `db:"public_token" json:"publicToken"`
	JellyfinItemID           string         `db:"jellyfin_item_id" json:"jellyfinItemId"`
	JellyfinUserID           string         `db:"jellyfin_user_id" json:"jellyfinUserId"`
	Title                    string         `db:"title" json:"title"`
	Overview                 sql.NullString `db:"overview" json:"overview,omitempty"`
	RuntimeSeconds           sql.NullInt64  `db:"runtime_seconds" json:"runtimeSeconds,omitempty"`
	PosterPath               sql.NullString `db:"poster_path" json:"posterPath,omitempty"`
	BackdropPath             sql.NullString `db:"backdrop_path" json:"backdropPath,omitempty"`
	ItemType                 string         `db:"item_type" json:"itemType"`
	MaxTotalPlays            sql.NullInt64  `db:"max_total_plays" json:"maxTotalPlays,omitempty"`
	MaxConcurrentViewers     sql.NullInt64  `db:"max_concurrent_viewers" json:"maxConcurrentViewers,omitempty"`
	TotalPlays               int            `db:"total_plays" json:"totalPlays"`
	CurrentConcurrentViewers int            `db:"current_concurrent_viewers" json:"currentConcurrentViewers"`
	ExpiresAt                time.Time      `db:"expires_at" json:"expiresAt"`
	PasswordHash             sql.NullString `db:"password_hash" json:"-"`
	CreatedAt                time.Time      `db:"created_at" json:"createdAt"`
	RevokedAt                sql.NullTime   `db:"revoked_at" json:"revokedAt,omitempty"`
	LastActivityAt           sql.NullTime   `db:"last_activity_at" json:"lastActivityAt,omitempty"`
}

func (s *Share) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

func (s *Share) IsRevoked() bool {
	return s.RevokedAt.Valid
}

func (s *Share) IsValid() bool {
	return !s.IsExpired() && !s.IsRevoked()
}

func (s *Share) RequiresPassword() bool {
	return s.PasswordHash.Valid && s.PasswordHash.String != ""
}

func (s *Share) CanStartNewPlay() bool {
	if !s.IsValid() {
		return false
	}
	if s.MaxTotalPlays.Valid && int64(s.TotalPlays) >= s.MaxTotalPlays.Int64 {
		return false
	}
	if s.MaxConcurrentViewers.Valid && int64(s.CurrentConcurrentViewers) >= s.MaxConcurrentViewers.Int64 {
		return false
	}
	return true
}

type CreateShareRequest struct {
	JellyfinItemID       string  `json:"jellyfinItemId"`
	JellyfinUserID       string  `json:"jellyfinUserId"`
	MaxTotalPlays        *int    `json:"maxTotalPlays,omitempty"`
	MaxConcurrentViewers *int    `json:"maxConcurrentViewers,omitempty"`
	ExpiresInMinutes     int     `json:"expiresInMinutes"`
	Password             *string `json:"password,omitempty"`
}

type CreateShareResponse struct {
	ShareID              uuid.UUID `json:"shareId"`
	PublicURL            string    `json:"publicUrl"`
	Token                string    `json:"token"`
	ExpiresAt            time.Time `json:"expiresAt"`
	MaxTotalPlays        *int      `json:"maxTotalPlays,omitempty"`
	MaxConcurrentViewers *int      `json:"maxConcurrentViewers,omitempty"`
}

type SharePublicInfo struct {
	Title                    string    `json:"title"`
	Overview                 string    `json:"overview,omitempty"`
	Tagline                  string    `json:"tagline,omitempty"`
	RuntimeSeconds           int64     `json:"runtimeSeconds,omitempty"`
	PosterURL                string    `json:"posterUrl,omitempty"`
	BackdropURL              string    `json:"backdropUrl,omitempty"`
	LogoURL                  string    `json:"logoUrl,omitempty"`
	ItemType                 string    `json:"itemType"`
	ExpiresAt                time.Time `json:"expiresAt"`
	RequiresPassword         bool      `json:"requiresPassword"`
	MaxTotalPlays            *int64    `json:"maxTotalPlays,omitempty"`
	TotalPlays               int       `json:"totalPlays"`
	MaxConcurrentViewers     *int64    `json:"maxConcurrentViewers,omitempty"`
	CurrentConcurrentViewers int       `json:"currentConcurrentViewers"`

	// Extended metadata (fetched live from Jellyfin)
	Year            int               `json:"year,omitempty"`
	OfficialRating  string            `json:"officialRating,omitempty"`
	CommunityRating float64           `json:"communityRating,omitempty"`
	CriticRating    int               `json:"criticRating,omitempty"`
	Genres          []string          `json:"genres,omitempty"`
	Studios         []string          `json:"studios,omitempty"`
	Directors       []string          `json:"directors,omitempty"`
	Actors          []ActorInfo       `json:"actors,omitempty"`
	VideoQuality    *VideoQualityInfo `json:"videoQuality,omitempty"`
	AudioTracks     []AudioTrackInfo  `json:"audioTracks,omitempty"`
}

// AudioTrackInfo describes one selectable audio stream for the public share view.
type AudioTrackInfo struct {
	Index        int    `json:"index"`            // Jellyfin MediaStream.Index — pass to AudioStreamIndex
	Codec        string `json:"codec,omitempty"`  // e.g. "ac3", "aac", "dts"
	Language     string `json:"language,omitempty"`
	Title        string `json:"title,omitempty"`
	DisplayTitle string `json:"displayTitle,omitempty"`
	Channels     int    `json:"channels,omitempty"`
	IsDefault    bool   `json:"isDefault,omitempty"`
}

type ActorInfo struct {
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

type VideoQualityInfo struct {
	Resolution string `json:"resolution"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Codec      string `json:"codec,omitempty"`
	Container  string `json:"container,omitempty"`
	Bitrate    int    `json:"bitrate,omitempty"`
	AudioCodec string `json:"audioCodec,omitempty"`
}

func (s *Share) ToPublicInfo(baseURL string) SharePublicInfo {
	info := SharePublicInfo{
		Title:                    s.Title,
		ItemType:                 s.ItemType,
		ExpiresAt:                s.ExpiresAt,
		RequiresPassword:         s.RequiresPassword(),
		TotalPlays:               s.TotalPlays,
		CurrentConcurrentViewers: s.CurrentConcurrentViewers,
	}

	if s.Overview.Valid {
		info.Overview = s.Overview.String
	}
	if s.RuntimeSeconds.Valid {
		info.RuntimeSeconds = s.RuntimeSeconds.Int64
	}
	if s.PosterPath.Valid && s.PosterPath.String != "" {
		info.PosterURL = baseURL + "/api/public/images/" + s.PublicToken + "/poster"
	}
	if s.BackdropPath.Valid && s.BackdropPath.String != "" {
		info.BackdropURL = baseURL + "/api/public/images/" + s.PublicToken + "/backdrop"
	}
	if s.MaxTotalPlays.Valid {
		info.MaxTotalPlays = &s.MaxTotalPlays.Int64
	}
	if s.MaxConcurrentViewers.Valid {
		info.MaxConcurrentViewers = &s.MaxConcurrentViewers.Int64
	}

	return info
}

type ShareListItem struct {
	ID                       uuid.UUID  `db:"id" json:"id"`
	PublicToken              string     `db:"public_token" json:"publicToken"`
	Title                    string     `db:"title" json:"title"`
	ItemType                 string     `db:"item_type" json:"itemType"`
	TotalPlays               int        `db:"total_plays" json:"totalPlays"`
	CurrentConcurrentViewers int        `db:"current_concurrent_viewers" json:"currentConcurrentViewers"`
	MaxTotalPlays            *int64     `db:"max_total_plays" json:"maxTotalPlays,omitempty"`
	MaxConcurrentViewers     *int64     `db:"max_concurrent_viewers" json:"maxConcurrentViewers,omitempty"`
	ExpiresAt                time.Time  `db:"expires_at" json:"expiresAt"`
	CreatedAt                time.Time  `db:"created_at" json:"createdAt"`
	RevokedAt                *time.Time `db:"revoked_at" json:"revokedAt,omitempty"`
	HasPassword              bool       `json:"hasPassword"`
}

type UpdateShareRequest struct {
	MaxTotalPlays        *int   `json:"maxTotalPlays,omitempty"`
	MaxConcurrentViewers *int   `json:"maxConcurrentViewers,omitempty"`
	ExtendMinutes        *int   `json:"extendMinutes,omitempty"`
	Password             *string `json:"password,omitempty"`
	RemovePassword       bool   `json:"removePassword,omitempty"`
}
