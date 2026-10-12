package people

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type EmployeeSummary struct {
	ID            string  `json:"id"`
	FirstName     string  `json:"firstName"`
	LastName      string  `json:"lastName"`
	PreferredName *string `json:"preferredName"`
	WorkEmail     string  `json:"workEmail"`
	Status        string  `json:"status"`
	DepartmentID  *string `json:"departmentId"`
	Department    string  `json:"department"`
	DivisionID    *string `json:"divisionId"`
	Division      string  `json:"division"`
	LocationID    *string `json:"locationId"`
	Location      string  `json:"location"`
	ManagerID     *string `json:"managerId"`
	Manager       string  `json:"manager"`
	JobTitle      *string `json:"jobTitle"`
	HiredOn       *string `json:"hiredOn"`
	Avatar        string  `json:"avatar"`
}

type Directory struct {
	pool *pgxpool.Pool
}

func NewDirectory(pool *pgxpool.Pool) *Directory { return &Directory{pool: pool} }

func (directory *Directory) List(ctx context.Context, organizationID, search, status string, limit int) ([]EmployeeSummary, error) {
	if limit < 1 || limit > 100 {
		limit = 100
	}
	if len(search) > 120 {
		return nil, fmt.Errorf("search must contain at most 120 characters")
	}
	if status != "" && !isEmployeeStatus(status) {
		return nil, fmt.Errorf("status is invalid")
	}
	rows, err := directory.pool.Query(ctx, `
        SELECT 
            e.id::text, 
            e.first_name, 
            e.last_name, 
            e.preferred_name, 
            e.work_email, 
            e.status,
            a.department_id::text, 
            COALESCE(d.name, 'General') AS department,
            a.division_id::text,
            COALESCE(div.name, '') AS division,
            a.location_id::text,
            COALESCE(loc.name, 'Remote') AS location,
            a.manager_employee_id::text, 
            COALESCE(NULLIF(TRIM(CONCAT(m.first_name, ' ', m.last_name)), ''), 'No manager assigned') AS manager,
            a.job_title, 
            e.hired_on::text,
            COALESCE(u.avatar_url, '') AS avatar
        FROM employees e
        LEFT JOIN LATERAL (
            SELECT department_id, division_id, location_id, manager_employee_id, job_title
            FROM employment_assignments
            WHERE employee_id = e.id AND effective_to IS NULL
            ORDER BY effective_from DESC
            LIMIT 1
        ) a ON true
        LEFT JOIN departments d ON d.id = a.department_id
        LEFT JOIN divisions div ON div.id = a.division_id
        LEFT JOIN locations loc ON loc.id = a.location_id
        LEFT JOIN employees m ON m.id = a.manager_employee_id
        LEFT JOIN users u ON u.id = e.user_id
        WHERE e.organization_id = $1
          AND ($2 = '' OR e.status = $2)
          AND ($3 = '' OR concat_ws(' ', e.first_name, e.last_name, coalesce(e.preferred_name, ''), e.work_email, a.job_title, d.name) ILIKE '%' || $3 || '%')
        ORDER BY e.last_name ASC, e.first_name ASC, e.id ASC
        LIMIT $4`, organizationID, status, strings.TrimSpace(search), limit)
	if err != nil {
		return nil, fmt.Errorf("list employees: %w", err)
	}
	defer rows.Close()

	items := make([]EmployeeSummary, 0, limit)
	for rows.Next() {
		var employee EmployeeSummary
		if err := rows.Scan(
			&employee.ID,
			&employee.FirstName,
			&employee.LastName,
			&employee.PreferredName,
			&employee.WorkEmail,
			&employee.Status,
			&employee.DepartmentID,
			&employee.Department,
			&employee.DivisionID,
			&employee.Division,
			&employee.LocationID,
			&employee.Location,
			&employee.ManagerID,
			&employee.Manager,
			&employee.JobTitle,
			&employee.HiredOn,
			&employee.Avatar,
		); err != nil {
			return nil, fmt.Errorf("scan employee: %w", err)
		}
		items = append(items, employee)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate employees: %w", err)
	}
	return items, nil
}

func (directory *Directory) Activate(ctx context.Context, organizationID, employeeID string) error {
	result, err := directory.pool.Exec(ctx, `UPDATE employees SET status = 'active', updated_at = now() WHERE id = $1::uuid AND organization_id = $2`, employeeID, organizationID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrEmployeeNotFound
	}
	return nil
}

func isEmployeeStatus(status string) bool {
	switch status {
	case "onboarding", "active", "leave", "suspended", "terminated":
		return true
	default:
		return false
	}
}

func (directory *Directory) IsEmployeeUser(ctx context.Context, organizationID, employeeID, userID string) (bool, error) {
	var matches bool
	err := directory.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM employees WHERE id = $1 AND organization_id = $2 AND user_id = $3)`, employeeID, organizationID, userID).Scan(&matches)
	if err != nil {
		return false, fmt.Errorf("verify employee-user link: %w", err)
	}
	return matches, nil
}
