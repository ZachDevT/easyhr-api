package timeoff

import (
	"context"
	"fmt"
	"time"
)

// Balance is intentionally a read model. The ledger is authoritative; this
// structure is returned to the application after aggregation.
type Balance struct {
	EmployeeID       string  `json:"employeeId"`
	LeaveTypeID      string  `json:"leaveTypeId"`
	LeaveTypeCode    string  `json:"leaveTypeCode"`
	LeaveTypeName    string  `json:"leaveTypeName"`
	Unit             string  `json:"unit"`
	AvailableMinutes int64   `json:"availableMinutes"`
	ReservedMinutes  int64   `json:"reservedMinutes"`
	UsedMinutes      int64   `json:"usedMinutes"`
	AvailableHours   float64 `json:"availableHours"`
	UsedHours        float64 `json:"usedHours"`
	AccrualFrequency string  `json:"accrualFrequency"`
	AccrualHours     float64 `json:"accrualHours"`
	AsOf             string  `json:"asOf"`
}

// ListBalances derives a display balance from the append-only ledger.
func (service *Service) ListBalances(ctx context.Context, organizationID, employeeID string) ([]Balance, error) {
	fetch := func() ([]Balance, error) {
		rows, err := service.pool.Query(ctx, `
			WITH eligible_leave_types AS (
				SELECT DISTINCT 
					policy.leave_type_id,
					policy.accrual_frequency,
					policy.accrual_minutes
				FROM leave_policy_assignments assignment
				JOIN leave_policies policy ON policy.id = assignment.leave_policy_id
				WHERE policy.organization_id = $1
				  AND assignment.employee_id = $2
				  AND policy.status = 'active'
				  AND assignment.effective_from <= CURRENT_DATE
				  AND (assignment.effective_to IS NULL OR assignment.effective_to >= CURRENT_DATE)
			)
			SELECT 
				$2::text, 
				eligible.leave_type_id::text,
				t.code,
				t.name,
				t.unit,
				eligible.accrual_frequency,
				eligible.accrual_minutes,
				COALESCE(SUM(CASE 
					WHEN ledger.entry_type IN ('accrual', 'adjustment', 'carryover') THEN ledger.minutes 
					WHEN ledger.entry_type = 'usage' THEN -ledger.minutes
					ELSE 0 
				END), 0) AS available_mins,
				COALESCE(SUM(CASE WHEN ledger.entry_type = 'reservation' THEN ledger.minutes ELSE 0 END), 0) AS reserved_mins,
				COALESCE(SUM(CASE WHEN ledger.entry_type = 'usage' THEN ledger.minutes ELSE 0 END), 0) AS used_mins
			FROM eligible_leave_types eligible
			JOIN leave_types t ON t.id = eligible.leave_type_id
			LEFT JOIN leave_ledger_entries ledger
			  ON ledger.organization_id = $1
			 AND ledger.employee_id = $2
			 AND ledger.leave_type_id = eligible.leave_type_id
			GROUP BY eligible.leave_type_id, t.code, t.name, t.unit, eligible.accrual_frequency, eligible.accrual_minutes
			ORDER BY t.name ASC`, organizationID, employeeID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		result := make([]Balance, 0)
		asOf := time.Now().UTC().Format(time.RFC3339)
		for rows.Next() {
			var item Balance
			var accrualMins int
			if err = rows.Scan(
				&item.EmployeeID, 
				&item.LeaveTypeID, 
				&item.LeaveTypeCode, 
				&item.LeaveTypeName, 
				&item.Unit,
				&item.AccrualFrequency,
				&accrualMins,
				&item.AvailableMinutes, 
				&item.ReservedMinutes,
				&item.UsedMinutes,
			); err != nil {
				return nil, err
			}
			item.AsOf = asOf
			item.AvailableHours = float64(item.AvailableMinutes) / 60.0
			item.UsedHours = float64(item.UsedMinutes) / 60.0
			item.AccrualHours = float64(accrualMins) / 60.0
			result = append(result, item)
		}
		return result, rows.Err()
	}

	result, err := fetch()
	if err == nil && len(result) > 0 {
		return result, nil
	}

	// 1. Ensure leave types exist if result is empty
	var typeCount int
	_ = service.pool.QueryRow(ctx, `SELECT COUNT(*) FROM leave_types WHERE organization_id = $1 AND status = 'active'`, organizationID).Scan(&typeCount)
	if typeCount == 0 {
		_ = service.SeedDefaultLeaveTypes(ctx, organizationID)
	}

	// 2. Ensure employee has policy assignments
	var assignCount int
	_ = service.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM leave_policy_assignments a
		JOIN leave_policies p ON p.id = a.leave_policy_id
		WHERE a.employee_id = $1 AND p.organization_id = $2
	`, employeeID, organizationID).Scan(&assignCount)

	if assignCount == 0 {
		// Auto-assign existing policies to this employee
		rows, err := service.pool.Query(ctx, `SELECT id::text FROM leave_policies WHERE organization_id = $1 AND status = 'active'`, organizationID)
		if err == nil {
			var pIDs []string
			for rows.Next() {
				var pID string
				if err := rows.Scan(&pID); err == nil {
					pIDs = append(pIDs, pID)
				}
			}
			rows.Close()
			for _, pID := range pIDs {
				_ = service.AssignPolicy(ctx, organizationID, "system", pID, AssignPolicyInput{
					EmployeeID:    employeeID,
					EffectiveFrom: time.Now().Format("2006-01-02"),
				})
			}
		}
	}

	return fetch()
}

func (balance Balance) UsableMinutes() int64 {
	return balance.AvailableMinutes - balance.ReservedMinutes
}

func (balance Balance) CanReserve(minutes int, allowsNegativeBalance bool) error {
	if minutes <= 0 {
		return fmt.Errorf("requested minutes must be positive")
	}
	if !allowsNegativeBalance && int64(minutes) > balance.UsableMinutes() {
		return fmt.Errorf("leave balance is insufficient")
	}
	return nil
}
