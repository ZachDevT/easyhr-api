package tenancy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrDepartmentNotFound = errors.New("department reference not found")
var ErrDepartmentConflict = errors.New("department name or code already exists")

type DepartmentService struct{ pool *pgxpool.Pool }

func NewDepartmentService(pool *pgxpool.Pool) *DepartmentService {
	return &DepartmentService{pool: pool}
}

type Department struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	Code               *string `json:"code"`
	Description        *string `json:"description"`
	Color              string  `json:"color"`
	DivisionID         *string `json:"divisionId"`
	ParentDepartmentID *string `json:"parentDepartmentId"`
	HeadEmployeeID     *string `json:"headEmployeeId"`
	HeadName           string  `json:"headName"`
	Headcount          int     `json:"headcount"`
	ActiveRoles        int     `json:"activeRoles"`
	OpenRoles          int     `json:"openRoles"`
	Status             string  `json:"status"`
}

type CreateDepartmentInput struct {
	Name               string  `json:"name"`
	Code               string  `json:"code"`
	Description        string  `json:"description"`
	Color              string  `json:"color"`
	DivisionID         string  `json:"divisionId"`
	ParentDepartmentID string  `json:"parentDepartmentId"`
	HeadEmployeeID     *string `json:"headEmployeeId"`
}

func (input CreateDepartmentInput) Validate() error {
	if name := strings.TrimSpace(input.Name); name == "" || len(name) > 160 {
		return fmt.Errorf("name must contain between 1 and 160 characters")
	}
	if len(strings.TrimSpace(input.Code)) > 80 {
		return fmt.Errorf("code must not exceed 80 characters")
	}
	return nil
}

type Position struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organizationId"`
	DepartmentID   string `json:"departmentId"`
	Title          string `json:"title"`
	Level          string `json:"level"`
	ReportsTo      string `json:"reportsTo"`
	PayType        string `json:"payType"`
	Currency       string `json:"currency"`
	MinPay         int64  `json:"minPay"`
	MaxPay         int64  `json:"maxPay"`
	Status         string `json:"status"`
	CreatedAt      string `json:"createdAt"`
}

type CreatePositionInput struct {
	Title     string `json:"title"`
	Level     string `json:"level"`
	ReportsTo string `json:"reportsTo"`
	PayType   string `json:"payType"`
	Currency  string `json:"currency"`
	MinPay    int64  `json:"minPay"`
	MaxPay    int64  `json:"maxPay"`
}

func (input CreatePositionInput) Validate() error {
	if title := strings.TrimSpace(input.Title); title == "" || len(title) > 160 {
		return fmt.Errorf("title must contain between 1 and 160 characters")
	}
	return nil
}

type DepartmentMember struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Role     string  `json:"role"`
	Manager  string  `json:"manager"`
	Location string  `json:"location"`
	Avatar   *string `json:"avatar"`
}

func (service *DepartmentService) List(ctx context.Context, organizationID string) ([]Department, error) {
	query := `
		SELECT 
			d.id::text, 
			d.name, 
			d.code, 
			d.description,
			COALESCE(d.color, '#1A73E8') AS color,
			d.division_id::text, 
			d.parent_department_id::text, 
			d.head_employee_id::text,
			COALESCE(NULLIF(TRIM(CONCAT(h.first_name, ' ', h.last_name)), ''), 'Unassigned') AS head_name,
			COALESCE(hc.headcount, 0) AS headcount,
			COALESCE(dp.pos_count, 0) AS active_roles,
			COALESCE(jp.open_count, 0) AS open_roles,
			d.status
		FROM departments d
		LEFT JOIN employees h ON h.id = d.head_employee_id
		LEFT JOIN (
			SELECT department_id, COUNT(DISTINCT employee_id) AS headcount
			FROM employment_assignments ea
			JOIN employees e ON e.id = ea.employee_id
			WHERE ea.effective_to IS NULL AND e.status = 'active'
			GROUP BY department_id
		) hc ON hc.department_id = d.id
		LEFT JOIN (
			SELECT department_id, COUNT(*) AS pos_count
			FROM department_positions
			WHERE status = 'active'
			GROUP BY department_id
		) dp ON dp.department_id = d.id
		LEFT JOIN (
			SELECT department_id, COUNT(*) AS open_count
			FROM job_postings
			WHERE status = 'published'
			GROUP BY department_id
		) jp ON jp.department_id = d.id
		WHERE d.organization_id = $1
		ORDER BY d.name ASC, d.id ASC
	`
	rows, err := service.pool.Query(ctx, query, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]Department, 0)
	for rows.Next() {
		var item Department
		var headName string
		if err = rows.Scan(
			&item.ID,
			&item.Name,
			&item.Code,
			&item.Description,
			&item.Color,
			&item.DivisionID,
			&item.ParentDepartmentID,
			&item.HeadEmployeeID,
			&headName,
			&item.Headcount,
			&item.ActiveRoles,
			&item.OpenRoles,
			&item.Status,
		); err != nil {
			return nil, err
		}
		item.HeadName = headName
		result = append(result, item)
	}
	return result, rows.Err()
}

func (service *DepartmentService) Create(ctx context.Context, organizationID, actorUserID string, input CreateDepartmentInput) (Department, error) {
	if err := input.Validate(); err != nil {
		return Department{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Department{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, reference := range []struct{ table, id string }{
		{"divisions", input.DivisionID},
		{"departments", input.ParentDepartmentID},
	} {
		if strings.TrimSpace(reference.id) == "" {
			continue
		}
		var exists bool
		query := fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s WHERE id = $1 AND organization_id = $2)", reference.table)
		if err = tx.QueryRow(ctx, query, strings.TrimSpace(reference.id), organizationID).Scan(&exists); err != nil {
			return Department{}, err
		}
		if !exists {
			return Department{}, ErrDepartmentNotFound
		}
	}

	color := strings.TrimSpace(input.Color)
	if color == "" {
		color = "#1A73E8"
	}

	result := Department{
		Name:        strings.TrimSpace(input.Name),
		Status:      "active",
		Color:       color,
		HeadName:    "Unassigned",
		ActiveRoles: 0,
		OpenRoles:   0,
		Headcount:   0,
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO departments (
			organization_id, 
			division_id, 
			parent_department_id, 
			name, 
			code,
			description,
			color,
			head_employee_id
		) VALUES (
			$1, 
			NULLIF($2, '')::uuid, 
			NULLIF($3, '')::uuid, 
			$4, 
			NULLIF($5, ''),
			NULLIF($6, ''),
			$7,
			$8::uuid
		) 
		RETURNING 
			id::text, 
			code, 
			description,
			color,
			division_id::text, 
			parent_department_id::text,
			head_employee_id::text
	`, organizationID,
		strings.TrimSpace(input.DivisionID),
		strings.TrimSpace(input.ParentDepartmentID),
		result.Name,
		strings.TrimSpace(input.Code),
		strings.TrimSpace(input.Description),
		color,
		input.HeadEmployeeID,
	).Scan(
		&result.ID,
		&result.Code,
		&result.Description,
		&result.Color,
		&result.DivisionID,
		&result.ParentDepartmentID,
		&result.HeadEmployeeID,
	)
	if err != nil {
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && pgError.Code == "23505" {
			return Department{}, ErrDepartmentConflict
		}
		return Department{}, err
	}

	if _, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id, actor_user_id, action, resource_type, resource_id, request_id) VALUES ($1, $2, 'department.created', 'department', $3, 'unknown')`, organizationID, actorUserID, result.ID); err != nil {
		return Department{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Department{}, err
	}
	return result, nil
}

func (service *DepartmentService) ListPositions(ctx context.Context, organizationID, departmentID string) ([]Position, error) {
	query := `
		SELECT 
			id::text,
			organization_id::text,
			department_id::text,
			title,
			level,
			COALESCE(reports_to, ''),
			pay_type,
			currency,
			min_pay,
			max_pay,
			status,
			to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM department_positions
		WHERE organization_id = $1 AND department_id = $2
		ORDER BY created_at DESC
	`
	rows, err := service.pool.Query(ctx, query, organizationID, departmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Position, 0)
	for rows.Next() {
		var pos Position
		if err := rows.Scan(
			&pos.ID,
			&pos.OrganizationID,
			&pos.DepartmentID,
			&pos.Title,
			&pos.Level,
			&pos.ReportsTo,
			&pos.PayType,
			&pos.Currency,
			&pos.MinPay,
			&pos.MaxPay,
			&pos.Status,
			&pos.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, pos)
	}
	return items, rows.Err()
}

func (service *DepartmentService) CreatePosition(ctx context.Context, organizationID, departmentID string, input CreatePositionInput) (Position, error) {
	if err := input.Validate(); err != nil {
		return Position{}, err
	}
	level := strings.TrimSpace(input.Level)
	if level == "" {
		level = "Mid"
	}
	payType := strings.TrimSpace(input.PayType)
	if payType == "" {
		payType = "Salaried"
	}
	currency := strings.TrimSpace(input.Currency)
	if currency == "" {
		currency = "CAD"
	}

	var pos Position
	err := service.pool.QueryRow(ctx, `
		INSERT INTO department_positions (
			organization_id,
			department_id,
			title,
			level,
			reports_to,
			pay_type,
			currency,
			min_pay,
			max_pay,
			status
		) VALUES (
			$1,
			$2::uuid,
			$3,
			$4,
			NULLIF($5, ''),
			$6,
			$7,
			$8,
			$9,
			'active'
		)
		RETURNING 
			id::text,
			organization_id::text,
			department_id::text,
			title,
			level,
			COALESCE(reports_to, ''),
			pay_type,
			currency,
			min_pay,
			max_pay,
			status,
			to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
	`, organizationID, departmentID, strings.TrimSpace(input.Title), level, strings.TrimSpace(input.ReportsTo), payType, currency, input.MinPay, input.MaxPay).Scan(
		&pos.ID,
		&pos.OrganizationID,
		&pos.DepartmentID,
		&pos.Title,
		&pos.Level,
		&pos.ReportsTo,
		&pos.PayType,
		&pos.Currency,
		&pos.MinPay,
		&pos.MaxPay,
		&pos.Status,
		&pos.CreatedAt,
	)
	if err != nil {
		return Position{}, err
	}
	return pos, nil
}

func (service *DepartmentService) ListMembers(ctx context.Context, organizationID, departmentID string) ([]DepartmentMember, error) {
	query := `
		SELECT 
			e.id::text,
			TRIM(CONCAT(e.first_name, ' ', e.last_name)) AS name,
			COALESCE(ea.job_title, 'Member') AS role,
			COALESCE(NULLIF(TRIM(CONCAT(m.first_name, ' ', m.last_name)), ''), 'Unassigned') AS manager,
			COALESCE(l.name, 'Headquarters') AS location,
			u.avatar_url
		FROM employees e
		JOIN employment_assignments ea ON ea.employee_id = e.id AND ea.department_id = $1::uuid AND ea.effective_to IS NULL
		LEFT JOIN employees m ON m.id = ea.manager_employee_id
		LEFT JOIN locations l ON l.id = ea.location_id
		LEFT JOIN users u ON u.id = e.user_id
		WHERE e.organization_id = $2 AND e.status = 'active'
		ORDER BY e.first_name ASC, e.last_name ASC
	`
	rows, err := service.pool.Query(ctx, query, departmentID, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := make([]DepartmentMember, 0)
	for rows.Next() {
		var mem DepartmentMember
		if err := rows.Scan(
			&mem.ID,
			&mem.Name,
			&mem.Role,
			&mem.Manager,
			&mem.Location,
			&mem.Avatar,
		); err != nil {
			return nil, err
		}
		members = append(members, mem)
	}
	return members, rows.Err()
}
