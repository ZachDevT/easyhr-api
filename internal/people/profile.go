package people

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var ErrEmployeeNotFound = errors.New("employee not found")

type EmployeeProfile struct {
	ID             string  `json:"id"`
	EmployeeNumber string  `json:"employeeNumber"`
	FirstName      string  `json:"firstName"`
	LastName       string  `json:"lastName"`
	WorkEmail      string  `json:"workEmail"`
	PersonalEmail  *string `json:"personalEmail"`
	PersonalPhone  *string `json:"personalPhone"`
	Status         string  `json:"status"`
	DepartmentID   *string `json:"departmentId"`
	Department     string  `json:"department"`
	DivisionID     *string `json:"divisionId"`
	Division       string  `json:"division"`
	LocationID     *string `json:"locationId"`
	Location       string  `json:"location"`
	ManagerID      *string `json:"managerId"`
	Manager        string  `json:"manager"`
	JobTitle       *string `json:"jobTitle"`
	StartDate      *string `json:"startDate"`
	Avatar         string  `json:"avatar"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
}

func (directory *Directory) Get(ctx context.Context, organizationID, employeeID string) (EmployeeProfile, error) {
	var result EmployeeProfile
	err := directory.pool.QueryRow(ctx, `
		SELECT 
			e.id::text, 
			e.employee_number,
			e.first_name, 
			e.last_name, 
			e.work_email,
			profile.personal_email, 
			profile.personal_phone,
			e.status,
			assignment.department_id::text, 
			COALESCE(d.name, 'General') AS department,
			assignment.division_id::text,
			COALESCE(div.name, '') AS division,
			assignment.location_id::text,
			COALESCE(loc.name, 'Remote') AS location,
			assignment.manager_employee_id::text, 
			COALESCE(NULLIF(TRIM(CONCAT(m.first_name, ' ', m.last_name)), ''), 'No manager assigned') AS manager,
			assignment.job_title,
			e.hired_on::text, 
			COALESCE(u.avatar_url, '') AS avatar,
			e.created_at::text, 
			e.updated_at::text
		FROM employees e
		LEFT JOIN employee_personal_profiles profile ON profile.employee_id = e.id
		LEFT JOIN LATERAL (
			SELECT department_id, division_id, location_id, manager_employee_id, job_title
			FROM employment_assignments
			WHERE employee_id = e.id AND effective_to IS NULL
			ORDER BY effective_from DESC
			LIMIT 1
		) assignment ON true
		LEFT JOIN departments d ON d.id = assignment.department_id
		LEFT JOIN divisions div ON div.id = assignment.division_id
		LEFT JOIN locations loc ON loc.id = assignment.location_id
		LEFT JOIN employees m ON m.id = assignment.manager_employee_id
		LEFT JOIN users u ON u.id = e.user_id
		WHERE e.id = $1::uuid AND e.organization_id = $2`, employeeID, organizationID).
		Scan(
			&result.ID, 
			&result.EmployeeNumber,
			&result.FirstName, 
			&result.LastName, 
			&result.WorkEmail, 
			&result.PersonalEmail, 
			&result.PersonalPhone,
			&result.Status, 
			&result.DepartmentID, 
			&result.Department,
			&result.DivisionID,
			&result.Division,
			&result.LocationID,
			&result.Location,
			&result.ManagerID, 
			&result.Manager,
			&result.JobTitle, 
			&result.StartDate, 
			&result.Avatar,
			&result.CreatedAt, 
			&result.UpdatedAt,
		)
	if errors.Is(err, pgx.ErrNoRows) {
		return EmployeeProfile{}, ErrEmployeeNotFound
	}
	if err != nil {
		return EmployeeProfile{}, fmt.Errorf("get employee: %w", err)
	}
	return result, nil
}
