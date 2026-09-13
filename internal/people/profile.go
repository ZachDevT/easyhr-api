package people

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var ErrEmployeeNotFound = errors.New("employee not found")

type EmployeeProfile struct {
	ID            string  `json:"id"`
	FirstName     string  `json:"firstName"`
	LastName      string  `json:"lastName"`
	WorkEmail     string  `json:"workEmail"`
	PersonalEmail *string `json:"personalEmail"`
	Status        string  `json:"status"`
	DepartmentID  *string `json:"departmentId"`
	ManagerID     *string `json:"managerId"`
	JobTitle      *string `json:"jobTitle"`
	StartDate     *string `json:"startDate"`
	CreatedAt     string  `json:"createdAt"`
	UpdatedAt     string  `json:"updatedAt"`
}

func (directory *Directory) Get(ctx context.Context, organizationID, employeeID string) (EmployeeProfile, error) {
	var result EmployeeProfile
	err := directory.pool.QueryRow(ctx, `
		SELECT employee.id::text, employee.first_name, employee.last_name, employee.work_email,
		       profile.personal_email, employee.status,
		       assignment.department_id::text, assignment.manager_employee_id::text, assignment.job_title,
		       employee.hired_on::text, employee.created_at::text, employee.updated_at::text
		FROM employees employee
		LEFT JOIN employee_personal_profiles profile ON profile.employee_id = employee.id
		LEFT JOIN LATERAL (
			SELECT department_id, manager_employee_id, job_title
			FROM employment_assignments
			WHERE employee_id = employee.id AND effective_to IS NULL
			ORDER BY effective_from DESC
			LIMIT 1
		) assignment ON true
		WHERE employee.id = $1 AND employee.organization_id = $2`, employeeID, organizationID).
		Scan(&result.ID, &result.FirstName, &result.LastName, &result.WorkEmail, &result.PersonalEmail, &result.Status, &result.DepartmentID, &result.ManagerID, &result.JobTitle, &result.StartDate, &result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return EmployeeProfile{}, ErrEmployeeNotFound
	}
	if err != nil {
		return EmployeeProfile{}, fmt.Errorf("get employee: %w", err)
	}
	return result, nil
}
