package people

import (
	"context"
	"encoding/json"
	"fmt"
	"net/mail"
	"strings"

	"github.com/jackc/pgx/v5"
)

// OptionalString distinguishes an omitted JSON property from an explicit null,
// which is required for PATCH semantics (for example, clearing a manager).
type OptionalString struct {
	Set   bool
	Value *string
}

func (value *OptionalString) UnmarshalJSON(data []byte) error {
	value.Set = true
	if string(data) == "null" {
		value.Value = nil
		return nil
	}
	var parsed string
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	value.Value = &parsed
	return nil
}

type UpdateEmployeeInput struct {
	FirstName     OptionalString `json:"firstName"`
	LastName      OptionalString `json:"lastName"`
	PersonalEmail OptionalString `json:"personalEmail"`
	DepartmentID  OptionalString `json:"departmentId"`
	ManagerID     OptionalString `json:"managerId"`
	JobTitle      OptionalString `json:"jobTitle"`
}

func (input UpdateEmployeeInput) Validate() error {
	if !input.FirstName.Set && !input.LastName.Set && !input.PersonalEmail.Set && !input.DepartmentID.Set && !input.ManagerID.Set && !input.JobTitle.Set {
		return fmt.Errorf("at least one field must be supplied")
	}
	for field, rule := range map[string]struct {
		value   OptionalString
		maximum int
	}{"firstName": {input.FirstName, 120}, "lastName": {input.LastName, 120}, "jobTitle": {input.JobTitle, 160}} {
		if rule.value.Set && (rule.value.Value == nil || len(strings.TrimSpace(*rule.value.Value)) == 0 || len(strings.TrimSpace(*rule.value.Value)) > rule.maximum) {
			return fmt.Errorf("%s must contain between 1 and %d characters", field, rule.maximum)
		}
	}
	if input.PersonalEmail.Set && input.PersonalEmail.Value != nil {
		if _, err := mail.ParseAddress(strings.TrimSpace(*input.PersonalEmail.Value)); err != nil {
			return fmt.Errorf("personalEmail must be valid")
		}
	}
	return nil
}

// Update performs only the profile and current-assignment changes explicitly
// allowed by the PATCH contract. Compensation, work terms and status remain in
// separate workflows because each has distinct legal and audit requirements.
func (directory *Directory) Update(ctx context.Context, organizationID, actorUserID, employeeID string, input UpdateEmployeeInput) (EmployeeProfile, error) {
	if err := input.Validate(); err != nil {
		return EmployeeProfile{}, err
	}
	tx, err := directory.pool.Begin(ctx)
	if err != nil {
		return EmployeeProfile{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1 AND organization_id = $2)`, employeeID, organizationID).Scan(&exists); err != nil {
		return EmployeeProfile{}, err
	}
	if !exists {
		return EmployeeProfile{}, ErrEmployeeNotFound
	}
	if err = validateUpdateReferences(ctx, tx, organizationID, employeeID, input); err != nil {
		return EmployeeProfile{}, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE employees SET
			first_name = CASE WHEN $3 THEN $4 ELSE first_name END,
			last_name = CASE WHEN $5 THEN $6 ELSE last_name END,
			updated_at = now()
		WHERE id = $1 AND organization_id = $2`, employeeID, organizationID, input.FirstName.Set, trimmedOptional(input.FirstName), input.LastName.Set, trimmedOptional(input.LastName)); err != nil {
		return EmployeeProfile{}, err
	}
	if input.PersonalEmail.Set {
		if _, err = tx.Exec(ctx, `INSERT INTO employee_personal_profiles (employee_id, personal_email) VALUES ($1, $2) ON CONFLICT (employee_id) DO UPDATE SET personal_email = EXCLUDED.personal_email, updated_at = now()`, employeeID, trimmedOptional(input.PersonalEmail)); err != nil {
			return EmployeeProfile{}, err
		}
	}
	if input.DepartmentID.Set || input.ManagerID.Set || input.JobTitle.Set {
		var departmentID, managerID *string
		var jobTitle string
		var effectiveToday bool
		err = tx.QueryRow(ctx, `SELECT department_id::text, manager_employee_id::text, job_title, effective_from = CURRENT_DATE FROM employment_assignments WHERE employee_id = $1 AND effective_to IS NULL FOR UPDATE`, employeeID).Scan(&departmentID, &managerID, &jobTitle, &effectiveToday)
		if err != nil {
			return EmployeeProfile{}, fmt.Errorf("current employment assignment not found: %w", err)
		}
		if input.DepartmentID.Set {
			departmentID = trimmedOptionalPointer(input.DepartmentID)
		}
		if input.ManagerID.Set {
			managerID = trimmedOptionalPointer(input.ManagerID)
		}
		if input.JobTitle.Set {
			jobTitle = strings.TrimSpace(*input.JobTitle.Value)
		}
		if effectiveToday {
			_, err = tx.Exec(ctx, `UPDATE employment_assignments SET department_id = $2::uuid, manager_employee_id = $3::uuid, job_title = $4 WHERE employee_id = $1 AND effective_to IS NULL`, employeeID, departmentID, managerID, jobTitle)
		} else {
			_, err = tx.Exec(ctx, `UPDATE employment_assignments SET effective_to = CURRENT_DATE - 1 WHERE employee_id = $1 AND effective_to IS NULL`, employeeID)
			if err == nil {
				_, err = tx.Exec(ctx, `INSERT INTO employment_assignments (employee_id, department_id, manager_employee_id, job_title, effective_from, created_by_user_id) VALUES ($1, $2::uuid, $3::uuid, $4, CURRENT_DATE, $5)`, employeeID, departmentID, managerID, jobTitle, actorUserID)
			}
		}
		if err != nil {
			return EmployeeProfile{}, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id, actor_user_id, action, resource_type, resource_id, request_id, metadata) VALUES ($1, $2, 'employee.profile_updated', 'employee', $3, 'unknown', jsonb_build_object('firstName', $4::text, 'lastName', $5::text, 'personalEmail', $6::text, 'departmentId', $7::text, 'managerId', $8::text, 'jobTitle', $9::text))`, organizationID, actorUserID, employeeID, input.FirstName.Set, input.LastName.Set, input.PersonalEmail.Set, input.DepartmentID.Set, input.ManagerID.Set, input.JobTitle.Set); err != nil {
		return EmployeeProfile{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return EmployeeProfile{}, err
	}
	return directory.Get(ctx, organizationID, employeeID)
}

func validateUpdateReferences(ctx context.Context, tx pgx.Tx, organizationID, employeeID string, input UpdateEmployeeInput) error {
	for _, reference := range []struct {
		value OptionalString
		table string
		field string
	}{
		{input.DepartmentID, "departments", "departmentId"},
		{input.ManagerID, "employees", "managerId"},
	} {
		if !reference.value.Set || reference.value.Value == nil {
			continue
		}
		if reference.table == "employees" && *reference.value.Value == employeeID {
			return fmt.Errorf("managerId cannot be the employee")
		}
		var exists bool
		query := fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s WHERE id = $1 AND organization_id = $2)", reference.table)
		if err := tx.QueryRow(ctx, query, strings.TrimSpace(*reference.value.Value), organizationID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%s does not belong to this organization", reference.field)
		}
	}
	return nil
}

func trimmedOptional(value OptionalString) any {
	if value.Value == nil {
		return nil
	}
	return strings.TrimSpace(*value.Value)
}

func trimmedOptionalPointer(value OptionalString) *string {
	if value.Value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value.Value)
	return &trimmed
}
