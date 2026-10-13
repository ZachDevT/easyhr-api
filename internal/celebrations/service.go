package celebrations

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Wish struct {
	ID                  string `json:"id"`
	RecipientEmployeeID string `json:"recipientEmployeeId"`
	SenderEmployeeID    string `json:"senderEmployeeId"`
	SenderName          string `json:"senderName"`
	SenderAvatar        string `json:"senderAvatar"`
	CelebrationType     string `json:"celebrationType"`
	Message             string `json:"message"`
	Reaction            string `json:"reaction"`
	CreatedAt           string `json:"createdAt"`
	TimeAgo             string `json:"timeAgo"`
}

type CelebrationItem struct {
	ID              string `json:"id"`
	EmployeeID      string `json:"employeeId"`
	Name            string `json:"name"`
	Avatar          string `json:"avatar"`
	Department      string `json:"department"`
	JobTitle        string `json:"jobTitle"`
	Type            string `json:"type"` // "birthday" | "anniversary"
	Milestone       string `json:"milestone"`
	CelebrationDate string `json:"celebrationDate"`
	DaysUntil       int    `json:"daysUntil"`
	WishesCount     int    `json:"wishesCount"`
	Wishes          []Wish `json:"wishes"`
}

type SendWishInput struct {
	RecipientEmployeeID string `json:"recipientEmployeeId"`
	CelebrationType     string `json:"celebrationType"`
	Message             string `json:"message"`
	Reaction            string `json:"reaction"`
}

type CelebrationStatus struct {
	IsCelebrationToday bool   `json:"isCelebrationToday"`
	CelebrationType    string `json:"celebrationType,omitempty"`
	Title              string `json:"title,omitempty"`
	Subtitle           string `json:"subtitle,omitempty"`
	Milestone          string `json:"milestone,omitempty"`
	EmployeeName       string `json:"employeeName,omitempty"`
	Wishes             []Wish `json:"wishes"`
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func getOrdinalSuffix(n int) string {
	if n%100 >= 11 && n%100 <= 13 {
		return fmt.Sprintf("%dth", n)
	}
	switch n % 10 {
	case 1:
		return fmt.Sprintf("%dst", n)
	case 2:
		return fmt.Sprintf("%dnd", n)
	case 3:
		return fmt.Sprintf("%drd", n)
	default:
		return fmt.Sprintf("%dth", n)
	}
}

func (s *Service) ListUpcomingCelebrations(ctx context.Context, orgID, filterType string) ([]CelebrationItem, error) {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	// Fetch all active employees with their birthday and hire date
	rows, err := s.pool.Query(ctx, `
		SELECT 
			e.id::text,
			TRIM(CONCAT(e.first_name, ' ', e.last_name)) as full_name,
			COALESCE(u.avatar_url, p.avatar_url, '') as avatar,
			COALESCE(d.name, 'General') as department,
			COALESCE(ea.job_title, 'Team Member') as job_title,
			e.hired_on,
			p.birth_date
		FROM employees e
		LEFT JOIN users u ON u.id = e.user_id
		LEFT JOIN employee_personal_profiles p ON p.employee_id = e.id
		LEFT JOIN employment_assignments ea ON ea.employee_id = e.id AND ea.effective_to IS NULL
		LEFT JOIN departments d ON d.id = ea.department_id
		WHERE e.organization_id = $1 AND e.status IN ('active', 'onboarding')
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type empRecord struct {
		id         string
		name       string
		avatar     string
		department string
		jobTitle   string
		hiredOn    *time.Time
		birthDate  *time.Time
	}

	var records []empRecord
	for rows.Next() {
		var r empRecord
		if err := rows.Scan(&r.id, &r.name, &r.avatar, &r.department, &r.jobTitle, &r.hiredOn, &r.birthDate); err != nil {
			return nil, err
		}
		records = append(records, r)
	}

	// Fetch recent wishes for all employees in this org
	wishMap := make(map[string][]Wish)
	wishRows, err := s.pool.Query(ctx, `
		SELECT 
			w.id::text,
			w.recipient_employee_id::text,
			COALESCE(w.sender_employee_id::text, ''),
			w.sender_name,
			COALESCE(w.sender_avatar, ''),
			w.celebration_type,
			w.message,
			w.reaction,
			to_char(w.created_at, 'Mon DD, YYYY'),
			w.created_at
		FROM celebration_wishes w
		WHERE w.organization_id = $1
		ORDER BY w.created_at DESC
		LIMIT 200
	`, orgID)
	if err == nil {
		defer wishRows.Close()
		for wishRows.Next() {
			var w Wish
			var createdAt time.Time
			if err := wishRows.Scan(
				&w.ID, &w.RecipientEmployeeID, &w.SenderEmployeeID, &w.SenderName,
				&w.SenderAvatar, &w.CelebrationType, &w.Message, &w.Reaction,
				&w.CreatedAt, &createdAt,
			); err == nil {
				w.TimeAgo = formatWishTimeAgo(now.Sub(createdAt))
				key := fmt.Sprintf("%s:%s", w.RecipientEmployeeID, w.CelebrationType)
				wishMap[key] = append(wishMap[key], w)
			}
		}
	}

	var results []CelebrationItem

	for _, rec := range records {
		// 1. Birthday
		if (filterType == "" || filterType == "all" || filterType == "birthday") && rec.birthDate != nil {
			bDay := *rec.birthDate
			// Next occurrence in current or next year
			nextDate := time.Date(today.Year(), bDay.Month(), bDay.Day(), 0, 0, 0, 0, time.UTC)
			if nextDate.Before(today) {
				nextDate = time.Date(today.Year()+1, bDay.Month(), bDay.Day(), 0, 0, 0, 0, time.UTC)
			}
			daysUntil := int(nextDate.Sub(today).Hours() / 24)

			// Include if within the next 90 days or if requested
			if daysUntil <= 90 {
				key := fmt.Sprintf("%s:birthday", rec.id)
				wishes := wishMap[key]
				results = append(results, CelebrationItem{
					ID:              fmt.Sprintf("%s-birthday", rec.id),
					EmployeeID:      rec.id,
					Name:            rec.name,
					Avatar:          rec.avatar,
					Department:      rec.department,
					JobTitle:        rec.jobTitle,
					Type:            "birthday",
					Milestone:       "Birthday",
					CelebrationDate: nextDate.Format("Jan 02"),
					DaysUntil:       daysUntil,
					WishesCount:     len(wishes),
					Wishes:          wishes,
				})
			}
		}

		// 2. Work Anniversary
		if (filterType == "" || filterType == "all" || filterType == "anniversary") && rec.hiredOn != nil {
			hDate := *rec.hiredOn
			if !hDate.After(today) {
				nextDate := time.Date(today.Year(), hDate.Month(), hDate.Day(), 0, 0, 0, 0, time.UTC)
				anniversaryYear := today.Year() - hDate.Year()
				if nextDate.Before(today) {
					nextDate = time.Date(today.Year()+1, hDate.Month(), hDate.Day(), 0, 0, 0, 0, time.UTC)
					anniversaryYear = (today.Year() + 1) - hDate.Year()
				}
				daysUntil := int(nextDate.Sub(today).Hours() / 24)

				if anniversaryYear > 0 && daysUntil <= 90 {
					key := fmt.Sprintf("%s:anniversary", rec.id)
					wishes := wishMap[key]
					results = append(results, CelebrationItem{
						ID:              fmt.Sprintf("%s-anniversary", rec.id),
						EmployeeID:      rec.id,
						Name:            rec.name,
						Avatar:          rec.avatar,
						Department:      rec.department,
						JobTitle:        rec.jobTitle,
						Type:            "anniversary",
						Milestone:       fmt.Sprintf("%s Work Anniversary", getOrdinalSuffix(anniversaryYear)),
						CelebrationDate: nextDate.Format("Jan 02"),
						DaysUntil:       daysUntil,
						WishesCount:     len(wishes),
						Wishes:          wishes,
					})
				}
			}
		}
	}

	// Sort chronologically by DaysUntil ascending (Today first, then tomorrow, etc.)
	sort.Slice(results, func(i, j int) bool {
		if results[i].DaysUntil == results[j].DaysUntil {
			return results[i].Name < results[j].Name
		}
		return results[i].DaysUntil < results[j].DaysUntil
	})

	return results, nil
}

func (s *Service) SendWish(ctx context.Context, orgID, senderUserID string, input SendWishInput) (Wish, error) {
	if input.RecipientEmployeeID == "" {
		return Wish{}, fmt.Errorf("recipientEmployeeId is required")
	}
	if input.Message == "" {
		return Wish{}, fmt.Errorf("message is required")
	}
	if input.CelebrationType == "" {
		input.CelebrationType = "birthday"
	}
	if input.Reaction == "" {
		input.Reaction = "heart"
	}

	// Sender info
	var senderEmpID *string
	var senderName, senderAvatar string
	senderName = "A Teammate"

	_ = s.pool.QueryRow(ctx, `
		SELECT 
			e.id::text,
			TRIM(CONCAT(e.first_name, ' ', e.last_name)),
			COALESCE(u.avatar_url, '')
		FROM users u
		LEFT JOIN employees e ON e.user_id = u.id AND e.organization_id = $1
		WHERE u.id = $2
		LIMIT 1
	`, orgID, senderUserID).Scan(&senderEmpID, &senderName, &senderAvatar)

	var w Wish
	var createdAt time.Time
	err := s.pool.QueryRow(ctx, `
		INSERT INTO celebration_wishes (
			organization_id, recipient_employee_id, sender_employee_id, sender_user_id,
			sender_name, sender_avatar, celebration_type, message, reaction
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id::text, recipient_employee_id::text, COALESCE(sender_employee_id::text, ''),
		          sender_name, COALESCE(sender_avatar, ''), celebration_type, message, reaction,
		          to_char(created_at, 'Mon DD, YYYY'), created_at
	`, orgID, input.RecipientEmployeeID, senderEmpID, senderUserID, senderName, senderAvatar, input.CelebrationType, input.Message, input.Reaction).Scan(
		&w.ID, &w.RecipientEmployeeID, &w.SenderEmployeeID, &w.SenderName,
		&w.SenderAvatar, &w.CelebrationType, &w.Message, &w.Reaction,
		&w.CreatedAt, &createdAt,
	)
	if err != nil {
		return Wish{}, err
	}
	w.TimeAgo = "Just now"

	// Create an in-app notification for recipient user if linked
	var recipientUserID *string
	_ = s.pool.QueryRow(ctx, `SELECT user_id::text FROM employees WHERE id = $1 AND organization_id = $2`, input.RecipientEmployeeID, orgID).Scan(&recipientUserID)
	if recipientUserID != nil && *recipientUserID != "" {
		celebrationWord := "birthday"
		if input.CelebrationType == "anniversary" {
			celebrationWord = "work anniversary"
		}
		title := fmt.Sprintf("%s sent you a %s wish!", senderName, celebrationWord)
		_, _ = s.pool.Exec(ctx, `
			INSERT INTO user_notifications (organization_id, user_id, title, message, category, link)
			VALUES ($1, $2, $3, $4, 'wish', '/dashboard')
		`, orgID, *recipientUserID, title, input.Message)
	}

	return w, nil
}

func (s *Service) GetCelebrationStatus(ctx context.Context, orgID, userID string) (CelebrationStatus, error) {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	var empID string
	var empName string
	var hiredOn, birthDate *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT 
			e.id::text,
			TRIM(CONCAT(e.first_name, ' ', e.last_name)),
			e.hired_on,
			p.birth_date
		FROM employees e
		LEFT JOIN employee_personal_profiles p ON p.employee_id = e.id
		WHERE e.organization_id = $1 AND e.user_id = $2 AND e.status IN ('active', 'onboarding')
		LIMIT 1
	`, orgID, userID).Scan(&empID, &empName, &hiredOn, &birthDate)
	if err != nil {
		return CelebrationStatus{IsCelebrationToday: false}, nil
	}

	// Check if today is birthday
	if birthDate != nil && birthDate.Month() == today.Month() && birthDate.Day() == today.Day() {
		wishes := s.fetchWishes(ctx, orgID, empID, "birthday")
		return CelebrationStatus{
			IsCelebrationToday: true,
			CelebrationType:    "birthday",
			Title:              fmt.Sprintf("Happy Birthday, %s!", empName),
			Subtitle:           "Wishing you an exceptional year of joy, success, and accomplishments from the whole EasyHR team!",
			Milestone:          "Birthday",
			EmployeeName:       empName,
			Wishes:             wishes,
		}, nil
	}

	// Check if today is work anniversary
	if hiredOn != nil && hiredOn.Month() == today.Month() && hiredOn.Day() == today.Day() && today.Year() > hiredOn.Year() {
		years := today.Year() - hiredOn.Year()
		milestone := fmt.Sprintf("%s Work Anniversary", getOrdinalSuffix(years))
		wishes := s.fetchWishes(ctx, orgID, empID, "anniversary")
		return CelebrationStatus{
			IsCelebrationToday: true,
			CelebrationType:    "anniversary",
			Title:              fmt.Sprintf("Happy %s, %s!", milestone, empName),
			Subtitle:           fmt.Sprintf("Thank you for %d wonderful year(s) of dedication, leadership, and contribution!", years),
			Milestone:          milestone,
			EmployeeName:       empName,
			Wishes:             wishes,
		}, nil
	}

	return CelebrationStatus{IsCelebrationToday: false}, nil
}

func (s *Service) fetchWishes(ctx context.Context, orgID, empID, cType string) []Wish {
	now := time.Now().UTC()
	rows, err := s.pool.Query(ctx, `
		SELECT 
			id::text, recipient_employee_id::text, COALESCE(sender_employee_id::text, ''),
			sender_name, COALESCE(sender_avatar, ''), celebration_type, message, reaction,
			to_char(created_at, 'Mon DD, YYYY'), created_at
		FROM celebration_wishes
		WHERE organization_id = $1 AND recipient_employee_id = $2 AND celebration_type = $3
		ORDER BY created_at DESC
		LIMIT 50
	`, orgID, empID, cType)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var wishes []Wish
	for rows.Next() {
		var w Wish
		var createdAt time.Time
		if err := rows.Scan(
			&w.ID, &w.RecipientEmployeeID, &w.SenderEmployeeID, &w.SenderName,
			&w.SenderAvatar, &w.CelebrationType, &w.Message, &w.Reaction,
			&w.CreatedAt, &createdAt,
		); err == nil {
			w.TimeAgo = formatWishTimeAgo(now.Sub(createdAt))
			wishes = append(wishes, w)
		}
	}
	return wishes
}

func formatWishTimeAgo(d time.Duration) string {
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
