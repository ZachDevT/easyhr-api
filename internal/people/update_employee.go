package people

import (
	"context"
	"encoding/json"
	"errors"
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
	PersonalPhone OptionalString `json:"personalPhone"`
	DepartmentID  OptionalString `json:"departmentId"`
	DivisionID    OptionalString `json:"divisionId"`
	LocationID    OptionalString `json:"locationId"`
	ManagerID     OptionalString `json:"managerId"`
	JobTitle      OptionalString `json:"jobTitle"`
	Status        OptionalString `json:"status"`
}

func (input UpdateEmployeeInput) Validate() error {
	if !input.FirstName.Set && !input.LastName.Set && !input.PersonalEmail.Set && !input.PersonalPhone.Set &&
		!input.DepartmentID.Set && !input.DivisionID.Set && !input.LocationID.Set && !input.ManagerID.Set &&
		!input.JobTitle.Set && !input.Status.Set {
		return fmt.Errorf("at least one field must be supplied")
	}
	for field, rule := range map[string]struct {
		value   OptionalString
		maximum int
	}{
		"firstName": {input.FirstName, 120},
		"lastName":  {input.LastName, 120},
		"jobTitle":  {input.JobTitle, 160},
	} {
		if rule.value.Set && (rule.value.Value == nil || len(strings.TrimSpace(*rule.value.Value)) == 0 || len(strings.TrimSpace(*rule.value.Value)) > rule.maximum) {
			return fmt.Errorf("%s must contain between 1 and %d characters", field, rule.maximum)
		}
	}
	if input.PersonalEmail.Set && input.PersonalEmail.Value != nil && strings.TrimSpace(*input.PersonalEmail.Value) != "" {
		if _, err := mail.ParseAddress(strings.TrimSpace(*input.PersonalEmail.Value)); err != nil {
			return fmt.Errorf("personalEmail must be valid")
		}
	}
	return nil
}

// Update performs employee profile and current-assignment changes explicitly
// allowed by the PATCH contract.
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

	// Update basic employee fields
	if input.FirstName.Set || input.LastName.Set || input.Status.Set {
		if _, err = tx.Exec(ctx, `
			UPDATE employees SET
				first_name = CASE WHEN $3 THEN $4 ELSE first_name END,
				last_name = CASE WHEN $5 THEN $6 ELSE last_name END,
				status = CASE WHEN $7 THEN $8 ELSE status END,
				updated_at = now()
			WHERE id = $1 AND organization_id = $2`,
			employeeID, organizationID,
			input.FirstName.Set, trimmedOptional(input.FirstName),
			input.LastName.Set, trimmedOptional(input.LastName),
			input.Status.Set, trimmedOptional(input.Status),
		); err != nil {
			return EmployeeProfile{}, err
		}
	}

	// Update personal profile
	if input.PersonalEmail.Set || input.PersonalPhone.Set {
		if _, err = tx.Exec(ctx, `
			INSERT INTO employee_personal_profiles (employee_id, personal_email, personal_phone)
			VALUES ($1, $2, $3)
			ON CONFLICT (employee_id) DO UPDATE SET
				personal_email = CASE WHEN $4 THEN EXCLUDED.personal_email ELSE employee_personal_profiles.personal_email END,
				personal_phone = CASE WHEN $5 THEN EXCLUDED.personal_phone ELSE employee_personal_profiles.personal_phone END,
				updated_at = now()`,
			employeeID,
			trimmedOptional(input.PersonalEmail),
			trimmedOptional(input.PersonalPhone),
			input.PersonalEmail.Set,
			input.PersonalPhone.Set,
		); err != nil {
			return EmployeeProfile{}, err
		}
	}

	// Update assignments (department, division, location, manager, job title)
	if input.DepartmentID.Set || input.DivisionID.Set || input.LocationID.Set || input.ManagerID.Set || input.JobTitle.Set {
		var departmentID, divisionID, locationID, managerID *string
		var jobTitle string
		var effectiveToday bool

		err = tx.QueryRow(ctx, `
			SELECT department_id::text, division_id::text, location_id::text, manager_employee_id::text, job_title, effective_from = CURRENT_DATE 
			FROM employment_assignments 
			WHERE employee_id = $1 AND effective_to IS NULL 
			ORDER BY effective_from DESC LIMIT 1 
			FOR UPDATE`,
			employeeID,
		).Scan(&departmentID, &divisionID, &locationID, &managerID, &jobTitle, &effectiveToday)

		if errors.Is(err, pgx.ErrNoRows) {
			// No active assignment, insert fresh
			departmentID = trimmedOptionalPointer(input.DepartmentID)
			divisionID = trimmedOptionalPointer(input.DivisionID)
			locationID = trimmedOptionalPointer(input.LocationID)
			managerID = trimmedOptionalPointer(input.ManagerID)
			if input.JobTitle.Set && input.JobTitle.Value != nil {
				jobTitle = strings.TrimSpace(*input.JobTitle.Value)
			} else {
				jobTitle = "Team Member"
			}
			_, err = tx.Exec(ctx, `
				INSERT INTO employment_assignments (employee_id, department_id, division_id, location_id, manager_employee_id, job_title, effective_from, created_by_user_id)
				VALUES ($1, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, CURRENT_DATE, $7)`,
				employeeID, departmentID, divisionID, locationID, managerID, jobTitle, actorUserID,
			)
			if err != nil {
				return EmployeeProfile{}, fmt.Errorf("create assignment: %w", err)
			}
		} else if err != nil {
			return EmployeeProfile{}, fmt.Errorf("assignment lookup: %w", err)
		} else {
			if input.DepartmentID.Set {
				departmentID = trimmedOptionalPointer(input.DepartmentID)
			}
			if input.DivisionID.Set {
				divisionID = trimmedOptionalPointer(input.DivisionID)
			}
			if input.LocationID.Set {
				locationID = trimmedOptionalPointer(input.LocationID)
			}
			if input.ManagerID.Set {
				managerID = trimmedOptionalPointer(input.ManagerID)
			}
			if input.JobTitle.Set && input.JobTitle.Value != nil {
				jobTitle = strings.TrimSpace(*input.JobTitle.Value)
			}

			if effectiveToday {
				_, err = tx.Exec(ctx, `
					UPDATE employment_assignments 
					SET department_id = $2::uuid, division_id = $3::uuid, location_id = $4::uuid, manager_employee_id = $5::uuid, job_title = $6 
					WHERE employee_id = $1 AND effective_to IS NULL`,
					employeeID, departmentID, divisionID, locationID, managerID, jobTitle,
				)
			} else {
				_, err = tx.Exec(ctx, `UPDATE employment_assignments SET effective_to = CURRENT_DATE - 1 WHERE employee_id = $1 AND effective_to IS NULL`, employeeID)
				if err == nil {
					_, err = tx.Exec(ctx, `
						INSERT INTO employment_assignments (employee_id, department_id, division_id, location_id, manager_employee_id, job_title, effective_from, created_by_user_id) 
						VALUES ($1, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, CURRENT_DATE, $7)`,
						employeeID, departmentID, divisionID, locationID, managerID, jobTitle, actorUserID,
					)
				}
			}
			if err != nil {
				return EmployeeProfile{}, fmt.Errorf("update assignment: %w", err)
			}
		}
	}

	auditMeta, _ := json.Marshal(map[string]any{
		"firstName":    input.FirstName.Value,
		"lastName":     input.LastName.Value,
		"departmentId": input.DepartmentID.Value,
		"managerId":    input.ManagerID.Value,
		"jobTitle":     input.JobTitle.Value,
	})
	_, _ = tx.Exec(ctx, `
		INSERT INTO audit_events (organization_id, actor_user_id, action, resource_type, resource_id, request_id, metadata)
		VALUES ($1, $2, 'employee.profile_updated', 'employee', $3, 'unknown', $4::jsonb)`,
		organizationID, actorUserID, employeeID, auditMeta,
	)

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
		{input.DivisionID, "divisions", "divisionId"},
		{input.LocationID, "locations", "locationId"},
		{input.ManagerID, "employees", "managerId"},
	} {
		if !reference.value.Set || reference.value.Value == nil || strings.TrimSpace(*reference.value.Value) == "" {
			continue
		}
		refVal := strings.TrimSpace(*reference.value.Value)
		if reference.table == "employees" && refVal == employeeID {
			return fmt.Errorf("managerId cannot be the employee themselves")
		}
		var exists bool
		query := fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s WHERE id = $1 AND organization_id = $2)", reference.table)
		if err := tx.QueryRow(ctx, query, refVal, organizationID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%s does not belong to this organization", reference.field)
		}
	}
	return nil
}

func trimmedOptional(value OptionalString) any {
	if !value.Set || value.Value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value.Value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

func trimmedOptionalPointer(value OptionalString) *string {
	if !value.Set || value.Value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value.Value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
