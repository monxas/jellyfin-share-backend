package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jellyfin-share/jellyfin-share-backend/internal/models"
)

func (db *DB) CreateSession(ctx context.Context, session *models.ShareSession) error {
	query := `
		INSERT INTO share_sessions (
			id, share_id, session_token, client_ip_hash, user_agent, started_at, last_heartbeat_at,
			jellyfin_item_id, audio_stream_index, subtitle_stream_index, video_bitrate,
			video_codec, vtt_subtitle_index, media_source_id, max_video_height
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`

	_, err := db.ExecContext(ctx, query,
		session.ID, session.ShareID, session.SessionToken,
		session.ClientIPHash, session.UserAgent,
		session.StartedAt, session.LastHeartbeatAt,
		session.JellyfinItemID, session.AudioStreamIndex, session.SubtitleStreamIndex,
		session.VideoBitrate, session.VideoCodec, session.VTTSubtitleIndex,
		session.MediaSourceID, session.MaxVideoHeight,
	)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	return nil
}

func (db *DB) GetSessionByToken(ctx context.Context, token string) (*models.ShareSession, error) {
	var session models.ShareSession
	query := `SELECT * FROM share_sessions WHERE session_token = $1`

	err := db.GetContext(ctx, &session, query, token)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	return &session, nil
}

func (db *DB) GetSessionByID(ctx context.Context, id uuid.UUID) (*models.ShareSession, error) {
	var session models.ShareSession
	query := `SELECT * FROM share_sessions WHERE id = $1`

	err := db.GetContext(ctx, &session, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	return &session, nil
}

func (db *DB) GetSessionsByShare(ctx context.Context, shareID uuid.UUID) ([]models.ShareSession, error) {
	var sessions []models.ShareSession
	query := `SELECT * FROM share_sessions WHERE share_id = $1 ORDER BY started_at DESC`

	err := db.SelectContext(ctx, &sessions, query, shareID)
	if err != nil {
		return nil, fmt.Errorf("failed to get sessions: %w", err)
	}
	return sessions, nil
}

func (db *DB) GetActiveSessionsByShare(ctx context.Context, shareID uuid.UUID, heartbeatTimeout time.Duration) ([]models.ShareSession, error) {
	var sessions []models.ShareSession
	cutoff := time.Now().Add(-heartbeatTimeout)
	query := `
		SELECT * FROM share_sessions
		WHERE share_id = $1
		  AND finished_at IS NULL
		  AND last_heartbeat_at > $2
		ORDER BY started_at DESC`

	err := db.SelectContext(ctx, &sessions, query, shareID, cutoff)
	if err != nil {
		return nil, fmt.Errorf("failed to get active sessions: %w", err)
	}
	return sessions, nil
}

func (db *DB) UpdateSessionHeartbeat(ctx context.Context, sessionID uuid.UUID, positionSecs *int64) error {
	var query string
	var args []interface{}

	if positionSecs != nil {
		query = `UPDATE share_sessions SET last_heartbeat_at = NOW(), last_position_secs = $2 WHERE id = $1`
		args = []interface{}{sessionID, *positionSecs}
	} else {
		query = `UPDATE share_sessions SET last_heartbeat_at = NOW() WHERE id = $1`
		args = []interface{}{sessionID}
	}

	_, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update heartbeat: %w", err)
	}
	return nil
}

func (db *DB) FinishSession(ctx context.Context, sessionID uuid.UUID, reason models.TerminationReason) error {
	query := `
		UPDATE share_sessions
		SET finished_at = NOW(), terminated_reason = $2
		WHERE id = $1 AND finished_at IS NULL`

	_, err := db.ExecContext(ctx, query, sessionID, string(reason))
	if err != nil {
		return fmt.Errorf("failed to finish session: %w", err)
	}
	return nil
}

func (db *DB) TerminateStaleSessionsForShare(ctx context.Context, shareID uuid.UUID, heartbeatTimeout time.Duration) (int64, error) {
	cutoff := time.Now().Add(-heartbeatTimeout)
	query := `
		UPDATE share_sessions
		SET finished_at = NOW(), terminated_reason = $3
		WHERE share_id = $1
		  AND finished_at IS NULL
		  AND last_heartbeat_at < $2`

	result, err := db.ExecContext(ctx, query, shareID, cutoff, string(models.TerminationReasonTimeout))
	if err != nil {
		return 0, fmt.Errorf("failed to terminate stale sessions: %w", err)
	}
	return result.RowsAffected()
}

func (db *DB) TerminateAllSessionsForShare(ctx context.Context, shareID uuid.UUID, reason models.TerminationReason) (int64, error) {
	query := `
		UPDATE share_sessions
		SET finished_at = NOW(), terminated_reason = $2
		WHERE share_id = $1 AND finished_at IS NULL`

	result, err := db.ExecContext(ctx, query, shareID, string(reason))
	if err != nil {
		return 0, fmt.Errorf("failed to terminate sessions: %w", err)
	}
	return result.RowsAffected()
}

func (db *DB) CountActiveSessionsForShare(ctx context.Context, shareID uuid.UUID, heartbeatTimeout time.Duration) (int, error) {
	var count int
	cutoff := time.Now().Add(-heartbeatTimeout)
	query := `
		SELECT COUNT(*) FROM share_sessions
		WHERE share_id = $1
		  AND finished_at IS NULL
		  AND last_heartbeat_at > $2`

	err := db.GetContext(ctx, &count, query, shareID, cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to count active sessions: %w", err)
	}
	return count, nil
}

func (db *DB) CleanupOldSessions(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	query := `DELETE FROM share_sessions WHERE finished_at < $1`

	result, err := db.ExecContext(ctx, query, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (db *DB) ReconcileConcurrentViewers(ctx context.Context, shareID uuid.UUID, heartbeatTimeout time.Duration) error {
	// Count actual active sessions and update the share's concurrent viewer count
	cutoff := time.Now().Add(-heartbeatTimeout)
	query := `
		UPDATE shares
		SET current_concurrent_viewers = (
			SELECT COUNT(*) FROM share_sessions
			WHERE share_id = $1
			  AND finished_at IS NULL
			  AND last_heartbeat_at > $2
		)
		WHERE id = $1`

	_, err := db.ExecContext(ctx, query, shareID, cutoff)
	if err != nil {
		return fmt.Errorf("failed to reconcile concurrent viewers: %w", err)
	}
	return nil
}
