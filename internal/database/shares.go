package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jellyfin-share/jellyfin-share-backend/internal/models"
)

func (db *DB) CreateShare(ctx context.Context, share *models.Share) error {
	query := `
		INSERT INTO shares (
			id, public_token, jellyfin_item_id, jellyfin_user_id, title, overview,
			runtime_seconds, poster_path, backdrop_path, item_type, max_total_plays,
			max_concurrent_viewers, expires_at, password_hash, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
		)`

	_, err := db.ExecContext(ctx, query,
		share.ID, share.PublicToken, share.JellyfinItemID, share.JellyfinUserID,
		share.Title, share.Overview, share.RuntimeSeconds, share.PosterPath,
		share.BackdropPath, share.ItemType, share.MaxTotalPlays, share.MaxConcurrentViewers,
		share.ExpiresAt, share.PasswordHash, share.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create share: %w", err)
	}
	return nil
}

func (db *DB) GetShareByToken(ctx context.Context, token string) (*models.Share, error) {
	var share models.Share
	query := `SELECT * FROM shares WHERE public_token = $1`

	err := db.GetContext(ctx, &share, query, token)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get share: %w", err)
	}
	return &share, nil
}

func (db *DB) GetShareByID(ctx context.Context, id uuid.UUID) (*models.Share, error) {
	var share models.Share
	query := `SELECT * FROM shares WHERE id = $1`

	err := db.GetContext(ctx, &share, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get share: %w", err)
	}
	return &share, nil
}

func (db *DB) GetSharesByUser(ctx context.Context, jellyfinUserID string) ([]models.Share, error) {
	var shares []models.Share
	query := `SELECT * FROM shares WHERE jellyfin_user_id = $1 ORDER BY created_at DESC`

	err := db.SelectContext(ctx, &shares, query, jellyfinUserID)
	if err != nil {
		return nil, fmt.Errorf("failed to get shares: %w", err)
	}
	return shares, nil
}

func (db *DB) GetAllShares(ctx context.Context, limit, offset int) ([]models.Share, error) {
	var shares []models.Share
	query := `SELECT * FROM shares ORDER BY created_at DESC LIMIT $1 OFFSET $2`

	err := db.SelectContext(ctx, &shares, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get shares: %w", err)
	}
	return shares, nil
}

func (db *DB) RevokeShare(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE shares SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL`
	result, err := db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to revoke share: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("share not found or already revoked")
	}
	return nil
}

func (db *DB) UpdateShare(ctx context.Context, id uuid.UUID, maxTotalPlays, maxConcurrentViewers *int, extendMinutes *int, passwordHash *string, removePassword bool) error {
	// Build dynamic update
	updates := []string{}
	args := []interface{}{}
	argIdx := 1

	if maxTotalPlays != nil {
		updates = append(updates, fmt.Sprintf("max_total_plays = $%d", argIdx))
		args = append(args, *maxTotalPlays)
		argIdx++
	}

	if maxConcurrentViewers != nil {
		updates = append(updates, fmt.Sprintf("max_concurrent_viewers = $%d", argIdx))
		args = append(args, *maxConcurrentViewers)
		argIdx++
	}

	if extendMinutes != nil {
		// A NULL expiry stays NULL: extending a share that never expires is a no-op
		// rather than silently giving it a deadline.
		updates = append(updates, fmt.Sprintf("expires_at = expires_at + INTERVAL '%d minutes'", *extendMinutes))
	}

	if passwordHash != nil {
		updates = append(updates, fmt.Sprintf("password_hash = $%d", argIdx))
		args = append(args, *passwordHash)
		argIdx++
	}

	if removePassword {
		updates = append(updates, "password_hash = NULL")
	}

	if len(updates) == 0 {
		return nil
	}

	query := "UPDATE shares SET "
	for i, u := range updates {
		if i > 0 {
			query += ", "
		}
		query += u
	}
	query += fmt.Sprintf(" WHERE id = $%d", argIdx)
	args = append(args, id)

	_, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update share: %w", err)
	}
	return nil
}

func (db *DB) IncrementPlayCount(ctx context.Context, shareID uuid.UUID) error {
	query := `
		UPDATE shares
		SET total_plays = total_plays + 1,
		    current_concurrent_viewers = current_concurrent_viewers + 1,
		    last_activity_at = NOW()
		WHERE id = $1`

	_, err := db.ExecContext(ctx, query, shareID)
	if err != nil {
		return fmt.Errorf("failed to increment play count: %w", err)
	}
	return nil
}

func (db *DB) DecrementConcurrentViewers(ctx context.Context, shareID uuid.UUID) error {
	query := `
		UPDATE shares
		SET current_concurrent_viewers = GREATEST(current_concurrent_viewers - 1, 0)
		WHERE id = $1`

	_, err := db.ExecContext(ctx, query, shareID)
	if err != nil {
		return fmt.Errorf("failed to decrement concurrent viewers: %w", err)
	}
	return nil
}

func (db *DB) UpdateLastActivity(ctx context.Context, shareID uuid.UUID) error {
	query := `UPDATE shares SET last_activity_at = NOW() WHERE id = $1`
	_, err := db.ExecContext(ctx, query, shareID)
	return err
}

func (db *DB) GetActiveSharesCount(ctx context.Context) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM shares WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at > NOW())`
	err := db.GetContext(ctx, &count, query)
	return count, err
}

func (db *DB) CleanupExpiredShares(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	query := `DELETE FROM shares WHERE expires_at IS NOT NULL AND expires_at < $1 AND revoked_at IS NOT NULL`
	result, err := db.ExecContext(ctx, query, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
