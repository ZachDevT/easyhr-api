package timeoff

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrLeaveTypeConflict = errors.New("leave type conflicts with an existing company leave type")
var leaveTypeCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)

type CreateLeaveTypeInput struct {
	Code             string `json:"code"`
	Name             string `json:"name"`
	Unit             string `json:"unit"`
	IsPaid           bool   `json:"isPaid"`
	RequiresApproval bool   `json:"requiresApproval"`
}

type UpdateLeaveTypeInput struct {
	Code             string `json:"code"`
	Name             string `json:"name"`
	Unit             string `json:"unit"`
	IsPaid           bool   `json:"isPaid"`
	RequiresApproval bool   `json:"requiresApproval"`
	Status           string `json:"status"`
}

type LeaveType struct {
	ID               string `json:"id"`
	Code             string `json:"code"`
	Name             string `json:"name"`
	Unit             string `json:"unit"`
	IsPaid           bool   `json:"isPaid"`
	RequiresApproval bool   `json:"requiresApproval"`
	Status           string `json:"status"`
}

func SanitizeLeaveTypeCode(code, name string) string {
	raw := strings.ToLower(strings.TrimSpace(code))
	if raw == "" {
		raw = strings.ToLower(strings.TrimSpace(name))
	}
	raw = strings.ReplaceAll(raw, " ", "_")
	raw = strings.ReplaceAll(raw, "-", "_")
	raw = strings.ReplaceAll(raw, ".", "_")
	raw = strings.ReplaceAll(raw, "/", "_")

	var clean strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			clean.WriteRune(r)
		}
	}
	res := clean.String()
	res = strings.Trim(res, "_")
	if res == "" {
		res = "custom_leave"
	}
	if res[0] < 'a' || res[0] > 'z' {
		res = "lt_" + res
	}
	if len(res) < 2 {
		res = res + "_leave"
	}
	if len(res) > 60 {
		res = res[:60]
	}
	return res
}

func (input CreateLeaveTypeInput) Validate() error {
	if n := len(strings.TrimSpace(input.Name)); n < 1 || n > 120 {
		return fmt.Errorf("name must contain between 1 and 120 characters")
	}
	return nil
}

func (service *Service) CreateLeaveType(ctx context.Context, organizationID, actorUserID string, input CreateLeaveTypeInput) (LeaveType, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return LeaveType{}, fmt.Errorf("name is required")
	}
	if len(name) > 120 {
		name = name[:120]
	}

	code := SanitizeLeaveTypeCode(input.Code, name)

	unit := strings.ToLower(strings.TrimSpace(input.Unit))
	if unit != "days" {
		unit = "minutes"
	}

	result := LeaveType{
		Code:             code,
		Name:             name,
		Unit:             unit,
		IsPaid:           input.IsPaid,
		RequiresApproval: input.RequiresApproval,
		Status:           "active",
	}

	var typeID string
	err := service.pool.QueryRow(ctx, `
		INSERT INTO leave_types (organization_id, code, name, unit, is_paid, requires_approval, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'active')
		ON CONFLICT (organization_id, code) DO UPDATE
		SET name = EXCLUDED.name, unit = EXCLUDED.unit, is_paid = EXCLUDED.is_paid, requires_approval = EXCLUDED.requires_approval, status = 'active', updated_at = now()
		RETURNING id::text
	`, organizationID, result.Code, result.Name, result.Unit, result.IsPaid, result.RequiresApproval).Scan(&typeID)

	if err != nil {
		var pge *pgconn.PgError
		if errors.As(err, &pge) && pge.Code == "23505" {
			// Name conflict with existing record; update existing record smoothly
			err = service.pool.QueryRow(ctx, `
				UPDATE leave_types
				SET unit = $3, is_paid = $4, requires_approval = $5, status = 'active', updated_at = now()
				WHERE organization_id = $1 AND name = $2
				RETURNING id::text, code
			`, organizationID, result.Name, result.Unit, result.IsPaid, result.RequiresApproval).Scan(&typeID, &result.Code)
		}
		if err != nil {
			return LeaveType{}, fmt.Errorf("save leave type: %w", err)
		}
	}

	result.ID = typeID
	_, _ = service.pool.Exec(ctx, `INSERT INTO audit_events (organization_id,actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES ($1,$2,'leave_type.saved','leave_type',$3,'unknown',jsonb_build_object('paid',$4::boolean,'unit',$5::text))`, organizationID, actorUserID, result.ID, result.IsPaid, result.Unit)
	return result, nil
}

func (service *Service) UpdateLeaveType(ctx context.Context, organizationID, typeID string, input UpdateLeaveTypeInput) (LeaveType, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return LeaveType{}, fmt.Errorf("name is required")
	}
	if len(name) > 120 {
		name = name[:120]
	}

	unit := strings.ToLower(strings.TrimSpace(input.Unit))
	if unit != "days" {
		unit = "minutes"
	}

	status := strings.ToLower(strings.TrimSpace(input.Status))
	if status != "archived" {
		status = "active"
	}

	var result LeaveType
	err := service.pool.QueryRow(ctx, `
		UPDATE leave_types
		SET name = $3, unit = $4, is_paid = $5, requires_approval = $6, status = $7, updated_at = now()
		WHERE id = $1 AND organization_id = $2
		RETURNING id::text, code, name, unit, is_paid, requires_approval, status
	`, typeID, organizationID, name, unit, input.IsPaid, input.RequiresApproval, status).
		Scan(&result.ID, &result.Code, &result.Name, &result.Unit, &result.IsPaid, &result.RequiresApproval, &result.Status)

	if errors.Is(err, pgx.ErrNoRows) {
		return LeaveType{}, errors.New("leave type not found")
	}
	if err != nil {
		return LeaveType{}, fmt.Errorf("update leave type: %w", err)
	}

	return result, nil
}

func (service *Service) ListLeaveTypes(ctx context.Context, organizationID string) ([]LeaveType, error) {
	// If organization has no leave types, automatically seed defaults
	var count int
	_ = service.pool.QueryRow(ctx, `SELECT COUNT(*) FROM leave_types WHERE organization_id = $1`, organizationID).Scan(&count)
	if count == 0 {
		_ = service.SeedDefaultLeaveTypes(ctx, organizationID)
	}

	rows, err := service.pool.Query(ctx, `
		SELECT id::text, code, name, unit, is_paid, requires_approval, status
		FROM leave_types
		WHERE organization_id = $1 AND status = 'active'
		ORDER BY name ASC
	`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []LeaveType{}
	for rows.Next() {
		var item LeaveType
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.Unit, &item.IsPaid, &item.RequiresApproval, &item.Status); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (service *Service) SeedDefaultLeaveTypes(ctx context.Context, organizationID string) error {
	defaults := []struct {
		code, name, unit string
		paid, approval   bool
		accrualFreq      string
		accrualMinutes   int
	}{
		{code: "pto", name: "Paid Time Off (PTO)", unit: "minutes", paid: true, approval: true, accrualFreq: "monthly", accrualMinutes: 840}, // 14 hours per month!
		{code: "sick_leave", name: "Sick Leave", unit: "minutes", paid: true, approval: true, accrualFreq: "annual", accrualMinutes: 2400},  // 40 hours per year
		{code: "parental", name: "Parental Leave", unit: "days", paid: true, approval: true, accrualFreq: "none", accrualMinutes: 0},
		{code: "unpaid", name: "Unpaid Time Off", unit: "days", paid: false, approval: true, accrualFreq: "none", accrualMinutes: 0},
	}

	for _, d := range defaults {
		var typeID string
		err := service.pool.QueryRow(ctx, `
			INSERT INTO leave_types (organization_id, code, name, unit, is_paid, requires_approval)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (organization_id, code) DO UPDATE SET name = EXCLUDED.name
			RETURNING id::text
		`, organizationID, d.code, d.name, d.unit, d.paid, d.approval).Scan(&typeID)
		if err != nil {
			continue
		}

		// Create default policy for this type if not exists
		var policyID string
		policyName := fmt.Sprintf("Standard %s Policy", d.name)
		err = service.pool.QueryRow(ctx, `
			INSERT INTO leave_policies (organization_id, leave_type_id, name, accrual_frequency, accrual_minutes, effective_from, status)
			VALUES ($1, $2, $3, $4, $5, CURRENT_DATE, 'active')
			ON CONFLICT (organization_id, name, effective_from) DO UPDATE SET accrual_minutes = EXCLUDED.accrual_minutes
			RETURNING id::text
		`, organizationID, typeID, policyName, d.accrualFreq, d.accrualMinutes).Scan(&policyID)
		if err != nil {
			continue
		}

		// Assign policy to all active employees and credit initial balance if accrual > 0
		rows, err := service.pool.Query(ctx, `SELECT id::text FROM employees WHERE organization_id = $1 AND status IN ('active', 'onboarding')`, organizationID)
		if err != nil {
			continue
		}
		var empIDs []string
		for rows.Next() {
			var eID string
			if err := rows.Scan(&eID); err == nil {
				empIDs = append(empIDs, eID)
			}
		}
		rows.Close()

		for _, empID := range empIDs {
			_, _ = service.pool.Exec(ctx, `
				INSERT INTO leave_policy_assignments (employee_id, leave_policy_id, effective_from)
				VALUES ($1, $2, CURRENT_DATE)
				ON CONFLICT (employee_id, leave_policy_id, effective_from) DO NOTHING
			`, empID, policyID)

			if d.accrualMinutes > 0 {
				_, _ = service.pool.Exec(ctx, `
					INSERT INTO leave_ledger_entries (organization_id, employee_id, leave_type_id, entry_type, minutes, effective_on, source_type, note)
					VALUES ($1, $2, $3, 'accrual', $4, CURRENT_DATE, 'initial_grant', 'Initial leave balance credit')
					ON CONFLICT DO NOTHING
				`, organizationID, empID, typeID, d.accrualMinutes)
			}
		}
	}
	return nil
}
