package announcements

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Announcement struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organizationId"`
	Title          string `json:"title"`
	Content        string `json:"content"`
	Category       string `json:"category"`
	Priority       string `json:"priority"`
	AuthorName     string `json:"authorName"`
	AuthorRole     string `json:"authorRole"`
	AuthorAvatar   string `json:"authorAvatar"`
	Pinned         bool   `json:"pinned"`
	CreatedAt      string `json:"createdAt"`
	TimeAgo        string `json:"timeAgo"`
}

type CreateAnnouncementInput struct {
	Title    string `json:"title"`
	Content  string `json:"content"`
	Category string `json:"category"`
	Priority string `json:"priority"`
	Pinned   bool   `json:"pinned"`
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (s *Service) ListAnnouncements(ctx context.Context, orgID string) ([]Announcement, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT 
			a.id::text, 
			a.organization_id::text, 
			a.title, 
			a.content, 
			a.category, 
			a.priority, 
			a.author_name, 
			COALESCE(a.author_role, 'HR Management'), 
			COALESCE(u.avatar_url, ''),
			a.pinned, 
			to_char(a.created_at, 'Mon DD, YYYY'),
			a.created_at
		FROM company_announcements a
		LEFT JOIN users u ON u.id = a.author_user_id
		WHERE a.organization_id = $1
		ORDER BY a.pinned DESC, a.created_at DESC
		LIMIT 50
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Announcement
	now := time.Now()
	for rows.Next() {
		var a Announcement
		var createdAt time.Time
		if err := rows.Scan(
			&a.ID,
			&a.OrganizationID,
			&a.Title,
			&a.Content,
			&a.Category,
			&a.Priority,
			&a.AuthorName,
			&a.AuthorRole,
			&a.AuthorAvatar,
			&a.Pinned,
			&a.CreatedAt,
			&createdAt,
		); err != nil {
			return nil, err
		}
		a.TimeAgo = formatTimeAgo(now.Sub(createdAt))
		list = append(list, a)
	}

	return list, nil
}

func (s *Service) CreateAnnouncement(ctx context.Context, orgID, userID string, input CreateAnnouncementInput) (Announcement, error) {
	if input.Title == "" {
		return Announcement{}, fmt.Errorf("title is required")
	}
	if input.Content == "" {
		return Announcement{}, fmt.Errorf("content is required")
	}
	if input.Category == "" {
		input.Category = "Company News"
	}
	if input.Priority == "" {
		input.Priority = "normal"
	}

	var authorName, authorRole, authorAvatar string
	authorName = "People Operations"
	authorRole = "HR & People Team"

	_ = s.pool.QueryRow(ctx, `
		SELECT 
			COALESCE(TRIM(CONCAT(e.first_name, ' ', e.last_name)), u.email),
			COALESCE(ea.job_title, 'HR Admin'),
			COALESCE(u.avatar_url, '')
		FROM users u
		LEFT JOIN employees e ON e.user_id = u.id AND e.organization_id = $1
		LEFT JOIN employment_assignments ea ON ea.employee_id = e.id AND ea.effective_to IS NULL
		WHERE u.id = $2
		LIMIT 1
	`, orgID, userID).Scan(&authorName, &authorRole, &authorAvatar)

	var a Announcement
	var createdAt time.Time
	err := s.pool.QueryRow(ctx, `
		INSERT INTO company_announcements (organization_id, title, content, category, priority, author_user_id, author_name, author_role, pinned)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id::text, organization_id::text, title, content, category, priority, author_name, author_role, pinned, to_char(created_at, 'Mon DD, YYYY'), created_at
	`, orgID, input.Title, input.Content, input.Category, input.Priority, userID, authorName, authorRole, input.Pinned).Scan(
		&a.ID, &a.OrganizationID, &a.Title, &a.Content, &a.Category, &a.Priority, &a.AuthorName, &a.AuthorRole, &a.Pinned, &a.CreatedAt, &createdAt,
	)
	if err != nil {
		return Announcement{}, err
	}
	a.AuthorAvatar = authorAvatar
	a.TimeAgo = "Just now"
	return a, nil
}

func (s *Service) DeleteAnnouncement(ctx context.Context, orgID, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM company_announcements WHERE organization_id = $1 AND id = $2`, orgID, id)
	return err
}

func formatTimeAgo(d time.Duration) string {
	if d < time.Minute {
		return "Just now"
	} else if d < time.Hour {
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	} else if d < 24*time.Hour {
		hrs := int(d.Hours())
		if hrs == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hrs)
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "Yesterday"
	} else if days < 30 {
		return fmt.Sprintf("%d days ago", days)
	}
	return "Over a month ago"
}
