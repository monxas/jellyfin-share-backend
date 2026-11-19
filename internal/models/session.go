package models

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type TerminationReason string

const (
	TerminationReasonNormal       TerminationReason = "normal"
	TerminationReasonExpired      TerminationReason = "expired"
	TerminationReasonRevoked      TerminationReason = "revoked"
	TerminationReasonLimitReached TerminationReason = "limit_reached"
	TerminationReasonTimeout      TerminationReason = "timeout"
)

type ShareSession struct {
	ID                uuid.UUID         `db:"id" json:"id"`
	ShareID           uuid.UUID         `db:"share_id" json:"shareId"`
	SessionToken      string            `db:"session_token" json:"sessionToken"`
	ClientIPHash      sql.NullString    `db:"client_ip_hash" json:"-"`
	UserAgent         sql.NullString    `db:"user_agent" json:"userAgent,omitempty"`
	StartedAt         time.Time         `db:"started_at" json:"startedAt"`
	LastHeartbeatAt   time.Time         `db:"last_heartbeat_at" json:"lastHeartbeatAt"`
	FinishedAt        sql.NullTime      `db:"finished_at" json:"finishedAt,omitempty"`
	TerminatedReason  sql.NullString    `db:"terminated_reason" json:"terminatedReason,omitempty"`
	LastPositionSecs  sql.NullInt64     `db:"last_position_secs" json:"lastPositionSecs,omitempty"`
	// JellyfinItemID pins what this session may stream. The stream proxy uses it
	// instead of trusting an item id from the viewer's query string.
	JellyfinItemID    sql.NullString    `db:"jellyfin_item_id" json:"-"`
	// Audio/subtitle selection, validated once at play time and pinned here for the
	// same reason as JellyfinItemID: the proxy must not read it from the request.
	AudioStreamIndex    sql.NullInt64 `db:"audio_stream_index" json:"-"`
	SubtitleStreamIndex sql.NullInt64 `db:"subtitle_stream_index" json:"-"`
}

func (s *ShareSession) IsActive(heartbeatTimeout time.Duration) bool {
	if s.FinishedAt.Valid {
		return false
	}
	return time.Since(s.LastHeartbeatAt) < heartbeatTimeout
}

type PlayRequest struct {
	// No body needed currently, auth is via cookie/token
}

type PlayResponse struct {
	SessionID   uuid.UUID `json:"sessionId"`
	PlaybackURL string    `json:"playbackUrl"`
}

type HeartbeatRequest struct {
	PositionSeconds *int64 `json:"positionSeconds,omitempty"`
}

type HeartbeatResponse struct {
	Status  string `json:"status"` // "ok", "expired", "revoked", "limit_reached"
	Message string `json:"message,omitempty"`
}

type SessionInfo struct {
	ID              uuid.UUID  `json:"id"`
	StartedAt       time.Time  `json:"startedAt"`
	LastHeartbeatAt time.Time  `json:"lastHeartbeatAt"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
	IsActive        bool       `json:"isActive"`
	UserAgent       string     `json:"userAgent,omitempty"`
}
