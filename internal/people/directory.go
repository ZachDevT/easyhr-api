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
	CreatedAt     *string `json:"createdAt"`
	Avatar        string  `json:"avatar"`
}

type Directory struct {
	pool *pgxpool.Pool
}

func NewDirectory(pool *pgxpool.Pool) *Directory { return &Directory{pool: pool} }

func (directory *Directory) List(ctx context.Context, organizationID, search, status, sortOrder string, limit int) ([]EmployeeSummary, error) {
	if limit < 1 || limit > 100 {
		limit = 100
	}
	if len(search) > 120 {
		return nil, fmt.Errorf("search must contain at most 120 characters")
	}
	if status != "" && !isEmployeeStatus(status) {
		return nil, fmt.Errorf("status is invalid")
	}

	_ = directory.EnsureMemberEmployees(ctx, organizationID)

	orderClause := "e.created_at ASC, e.last_name ASC, e.first_name ASC"
	if sortOrder == "newest" || sortOrder == "hired_desc" {
		orderClause = "COALESCE(e.hired_on, e.created_at::date) DESC, e.created_at DESC"
	} else if sortOrder == "oldest" {
		orderClause = "COALESCE(e.hired_on, e.created_at::date) ASC, e.created_at ASC"
	}

	query := fmt.Sprintf(`
        SELECT 
            e.id::text, 
            e.first_name, 
            e.last_name, 
            e.preferred_name, 
            e.work_email, 
            e.status,
            a.department_id::text, 
            COALESCE(d.name, 'Administration') AS department,
            a.division_id::text,
            COALESCE(div.name, '') AS division,
            a.location_id::text,
            COALESCE(loc.name, 'Headquarters') AS location,
            a.manager_employee_id::text, 
            COALESCE(NULLIF(TRIM(CONCAT(m.first_name, ' ', m.last_name)), ''), 'No manager assigned') AS manager,
            COALESCE(a.job_title, 'Team Member') AS job_title, 
            e.hired_on::text,
            to_char(e.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
            COALESCE(NULLIF(profile.avatar_url, ''), COALESCE(u.avatar_url, '')) AS avatar
        FROM employees e
        LEFT JOIN employee_personal_profiles profile ON profile.employee_id = e.id
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
          AND ($3 = '' OR concat_ws(' ', e.first_name, e.last_name, coalesce(e.preferred_name, ''), e.work_email, a.job_title, d.name) ILIKE '%%' || $3 || '%%')
        ORDER BY %s
        LIMIT $4`, orderClause)

	rows, err := directory.pool.Query(ctx, query, organizationID, status, strings.TrimSpace(search), limit)
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
			&employee.CreatedAt,
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

func (directory *Directory) EnsureMemberEmployees(ctx context.Context, organizationID string) error {
	if organizationID == "" {
		return nil
	}

	// 1. Link any unlinked employees whose work_email matches an active member's email
	_, _ = directory.pool.Exec(ctx, `
		UPDATE employees e
		SET user_id = m.user_id
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		WHERE e.organization_id = $1
		  AND m.organization_id = $1
		  AND e.user_id IS NULL
		  AND LOWER(e.work_email) = LOWER(u.email)`, organizationID)

	// 2. Find all active members who lack an employee record in this organization
	rows, err := directory.pool.Query(ctx, `
		SELECT 
			m.user_id::text,
			COALESCE(NULLIF(u.first_name, ''), 'Team') AS first_name,
			COALESCE(NULLIF(u.last_name, ''), 'Member') AS last_name,
			u.email,
			COALESCE(u.avatar_url, '') AS avatar_url,
			COALESCE(string_agg(r.key, ','), 'employee') AS roles_agg
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		LEFT JOIN membership_roles mr ON mr.membership_id = m.id
		LEFT JOIN roles r ON r.id = mr.role_id
		WHERE m.organization_id = $1
		  AND m.status = 'active'
		  AND NOT EXISTS (
			  SELECT 1 FROM employees e 
			  WHERE e.organization_id = $1 AND (e.user_id = m.user_id OR LOWER(e.work_email) = LOWER(u.email))
		  )
		GROUP BY m.user_id, u.first_name, u.last_name, u.email, u.avatar_url`, organizationID)
	if err != nil {
		return err
	}
	defer rows.Close()

	type memberToProvision struct {
		userID    string
		firstName string
		lastName  string
		email     string
		avatarURL string
		rolesAgg  string
	}
	var toProvision []memberToProvision
	for rows.Next() {
		var item memberToProvision
		if err := rows.Scan(&item.userID, &item.firstName, &item.lastName, &item.email, &item.avatarURL, &item.rolesAgg); err == nil {
			toProvision = append(toProvision, item)
		}
	}
	rows.Close()

	for _, member := range toProvision {
		var empCount int
		_ = directory.pool.QueryRow(ctx, `SELECT count(*) FROM employees WHERE organization_id = $1`, organizationID).Scan(&empCount)
		empNumber := fmt.Sprintf("EMP-%04d", empCount+1)

		jobTitle := "Team Member"
		if strings.Contains(member.rolesAgg, "owner") {
			jobTitle = "Founder & Executive"
		} else if strings.Contains(member.rolesAgg, "hr") {
			jobTitle = "HR Director"
		}

		var empID string
		err := directory.pool.QueryRow(ctx, `
			INSERT INTO employees (
				organization_id, user_id, employee_number, first_name, last_name, work_email, status, hired_on
			) VALUES (
				$1, $2::uuid, $3, $4, $5, $6, 'active', CURRENT_DATE
			)
			ON CONFLICT (organization_id, work_email) DO UPDATE SET user_id = EXCLUDED.user_id
			RETURNING id::text`, organizationID, member.userID, empNumber, member.firstName, member.lastName, member.email).Scan(&empID)
		if err != nil {
			continue
		}

		_, _ = directory.pool.Exec(ctx, `
			INSERT INTO employment_assignments (employee_id, job_title, effective_from)
			VALUES ($1::uuid, $2, CURRENT_DATE)
			ON CONFLICT DO NOTHING`, empID, jobTitle)

		_, _ = directory.pool.Exec(ctx, `
			INSERT INTO employment_terms (employee_id, effective_from, employment_type, worker_type, work_arrangement, fte_basis_points, pay_type)
			VALUES ($1::uuid, CURRENT_DATE, 'full_time', 'employee', 'on_site', 10000, 'salary')
			ON CONFLICT DO NOTHING`, empID)

		_, _ = directory.pool.Exec(ctx, `
			INSERT INTO employee_personal_profiles (employee_id, personal_email, avatar_url)
			VALUES ($1::uuid, $2, $3)
			ON CONFLICT (employee_id) DO UPDATE SET avatar_url = COALESCE(EXCLUDED.avatar_url, employee_personal_profiles.avatar_url)`,
			empID, member.email, member.avatarURL)
	}

	return nil
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
