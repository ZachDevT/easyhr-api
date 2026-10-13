package notifications

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Notification struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Message   string `json:"message"`
	Category  string `json:"category"`
	Link      string `json:"link"`
	IsRead    bool   `json:"isRead"`
	CreatedAt string `json:"createdAt"`
	TimeAgo   string `json:"timeAgo"`
}

type NotificationSummary struct {
	UnreadCount int            `json:"unreadCount"`
	Items       []Notification `json:"items"`
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (s *Service) ListNotifications(ctx context.Context, orgID, userID string) (NotificationSummary, error) {
	now := time.Now().UTC()

	var unreadCount int
	_ = s.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM user_notifications
		WHERE organization_id = $1 AND user_id = $2 AND is_read = false
	`, orgID, userID).Scan(&unreadCount)

	rows, err := s.pool.Query(ctx, `
		SELECT 
			id::text,
			title,
			message,
			category,
			COALESCE(link, '/dashboard'),
			is_read,
			to_char(created_at, 'Mon DD, YYYY'),
			created_at
		FROM user_notifications
		WHERE organization_id = $1 AND user_id = $2
		ORDER BY created_at DESC
		LIMIT 40
	`, orgID, userID)
	if err != nil {
		return NotificationSummary{}, err
	}
	defer rows.Close()

	var items []Notification
	for rows.Next() {
		var n Notification
		var createdAt time.Time
		if err := rows.Scan(
			&n.ID, &n.Title, &n.Message, &n.Category, &n.Link, &n.IsRead,
			&n.CreatedAt, &createdAt,
		); err != nil {
			return NotificationSummary{}, err
		}
		n.TimeAgo = formatNotificationTimeAgo(now.Sub(createdAt))
		items = append(items, n)
	}

	return NotificationSummary{
		UnreadCount: unreadCount,
		Items:       items,
	}, nil
}

func (s *Service) MarkAsRead(ctx context.Context, orgID, userID string, notificationID *string) error {
	if notificationID != nil && *notificationID != "" {
		_, err := s.pool.Exec(ctx, `
			UPDATE user_notifications
			SET is_read = true
			WHERE organization_id = $1 AND user_id = $2 AND id = $3
		`, orgID, userID, *notificationID)
		return err
	}

	// Mark all read
	_, err := s.pool.Exec(ctx, `
		UPDATE user_notifications
		SET is_read = true
		WHERE organization_id = $1 AND user_id = $2 AND is_read = false
	`, orgID, userID)
	return err
}

func formatNotificationTimeAgo(d time.Duration) string {
	if d < time.Minute {
		return "Just now"
	} else if d < time.Hour {
		mins := int(d.Minutes())
		if mins == 1 {
			return "1m ago"
		}
		return fmt.Sprintf("%dm ago", mins)
	} else if d < 24*time.Hour {
		hrs := int(d.Hours())
		if hrs == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", hrs)
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "Yesterday"
	}
	return fmt.Sprintf("%dd ago", days)
}
