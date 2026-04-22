package database

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

type AuditEventType string

const (
	AuditEventShareCreated     AuditEventType = "share_created"
	AuditEventShareRevoked     AuditEventType = "share_revoked"
	AuditEventShareUpdated     AuditEventType = "share_updated"
	AuditEventShareAccessed    AuditEventType = "share_accessed"
	AuditEventPasswordAttempt  AuditEventType = "password_attempt"
	AuditEventPlaybackStarted  AuditEventType = "playback_started"
	AuditEventPlaybackEnded    AuditEventType = "playback_ended"
	AuditEventPlaybackDenied   AuditEventType = "playback_denied"
	AuditEventSessionTimeout   AuditEventType = "session_timeout"
)

type AuditLog struct {
	ID             uuid.UUID              `db:"id" json:"id"`
	EventType      string                 `db:"event_type" json:"eventType"`
	ShareID        *uuid.UUID             `db:"share_id" json:"shareId,omitempty"`
	SessionID      *uuid.UUID             `db:"session_id" json:"sessionId,omitempty"`
	JellyfinUserID *string                `db:"jellyfin_user_id" json:"jellyfinUserId,omitempty"`
	ClientIPHash   *string                `db:"client_ip_hash" json:"clientIpHash,omitempty"`
	Details        map[string]interface{} `json:"details,omitempty"`
	CreatedAt      string                 `db:"created_at" json:"createdAt"`
}

func (db *DB) LogAuditEvent(ctx context.Context, eventType AuditEventType, shareID, sessionID *uuid.UUID, jellyfinUserID, clientIPHash *string, details map[string]interface{}) error {
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		detailsJSON = []byte("{}")
	}

	query := `
		INSERT INTO audit_logs (event_type, share_id, session_id, jellyfin_user_id, client_ip_hash, details)
		VALUES ($1, $2, $3, $4, $5, $6)`

	_, err = db.ExecContext(ctx, query, string(eventType), shareID, sessionID, jellyfinUserID, clientIPHash, detailsJSON)
	if err != nil {
		return fmt.Errorf("failed to log audit event: %w", err)
	}
	return nil
}

func (db *DB) GetAuditLogs(ctx context.Context, shareID *uuid.UUID, limit, offset int) ([]AuditLog, error) {
	var logs []AuditLog
	var query string
	var args []interface{}

	if shareID != nil {
		query = `SELECT id, event_type, share_id, session_id, jellyfin_user_id, client_ip_hash, created_at
		         FROM audit_logs WHERE share_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
		args = []interface{}{shareID, limit, offset}
	} else {
		query = `SELECT id, event_type, share_id, session_id, jellyfin_user_id, client_ip_hash, created_at
		         FROM audit_logs ORDER BY created_at DESC LIMIT $1 OFFSET $2`
		args = []interface{}{limit, offset}
	}

	err := db.SelectContext(ctx, &logs, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get audit logs: %w", err)
	}
	return logs, nil
}

// ShareAnalytics holds analytics data for a share
type ShareAnalytics struct {
	TotalViews          int          `json:"totalViews"`
	UniqueViewers       int          `json:"uniqueViewers"`
	AvgWatchTimeSeconds int          `json:"avgWatchTimeSeconds"`
	ViewsByDay          []DailyViews `json:"viewsByDay"`
}

// DailyViews holds views per day
type DailyViews struct {
	Date  string `json:"date"`
	Views int    `json:"views"`
}

// GetShareAnalytics retrieves analytics for a specific share
func (db *DB) GetShareAnalytics(ctx context.Context, shareID uuid.UUID) (*ShareAnalytics, error) {
	analytics := &ShareAnalytics{
		ViewsByDay: []DailyViews{},
	}

	// Get total views (playback_started events)
	var totalViews int
	err := db.GetContext(ctx, &totalViews, `
		SELECT COUNT(*) FROM audit_logs
		WHERE share_id = $1 AND event_type = 'playback_started'
	`, shareID)
	if err != nil {
		return nil, fmt.Errorf("failed to get total views: %w", err)
	}
	analytics.TotalViews = totalViews

	// Get unique viewers (distinct IP hashes)
	var uniqueViewers int
	err = db.GetContext(ctx, &uniqueViewers, `
		SELECT COUNT(DISTINCT client_ip_hash) FROM audit_logs
		WHERE share_id = $1 AND event_type = 'playback_started' AND client_ip_hash IS NOT NULL
	`, shareID)
	if err != nil {
		return nil, fmt.Errorf("failed to get unique viewers: %w", err)
	}
	analytics.UniqueViewers = uniqueViewers

	// Get average watch time from sessions
	var avgWatchTime float64
	err = db.GetContext(ctx, &avgWatchTime, `
		SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (COALESCE(finished_at, NOW()) - started_at))), 0)
		FROM share_sessions WHERE share_id = $1
	`, shareID)
	if err != nil {
		return nil, fmt.Errorf("failed to get avg watch time: %w", err)
	}
	analytics.AvgWatchTimeSeconds = int(avgWatchTime)

	// Get views by day (last 30 days)
	type dailyRow struct {
		Date  string `db:"date"`
		Views int    `db:"views"`
	}
	var dailyRows []dailyRow
	err = db.SelectContext(ctx, &dailyRows, `
		SELECT d::date as date, COUNT(a.id) as views
		FROM generate_series(
			(NOW() - INTERVAL '29 days')::date,
			NOW()::date,
			'1 day'::interval
		) d
		LEFT JOIN audit_logs a ON DATE(a.created_at) = d::date
			AND a.share_id = $1
			AND a.event_type = 'playback_started'
		GROUP BY d::date
		ORDER BY d::date DESC
	`, shareID)
	if err != nil {
		return nil, fmt.Errorf("failed to get daily views: %w", err)
	}

	for _, row := range dailyRows {
		analytics.ViewsByDay = append(analytics.ViewsByDay, DailyViews{
			Date:  row.Date,
			Views: row.Views,
		})
	}

	return analytics, nil
}
