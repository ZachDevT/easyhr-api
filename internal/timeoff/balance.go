package timeoff

import (
	"context"
	"fmt"
	"time"
)

// Balance is intentionally a read model. The ledger is authoritative; this
// structure is returned to the application after aggregation.
type Balance struct {
	EmployeeID       string `json:"employeeId"`
	LeaveTypeID      string `json:"leaveTypeId"`
	AvailableMinutes int64  `json:"availableMinutes"`
	ReservedMinutes  int64  `json:"reservedMinutes"`
	AsOf             string `json:"asOf"`
}

// ListBalances derives a display balance from the append-only ledger. It does
// not persist a mutable "balance" column, preventing drift after approvals,
// cancellations, or manual adjustments.
func (service *Service) ListBalances(ctx context.Context, organizationID, employeeID string) ([]Balance, error) {
	rows, err := service.pool.Query(ctx, `
		WITH eligible_leave_types AS (
			SELECT DISTINCT policy.leave_type_id
			FROM leave_policy_assignments assignment
			JOIN leave_policies policy ON policy.id = assignment.leave_policy_id
			WHERE policy.organization_id = $1
			  AND assignment.employee_id = $2
			  AND policy.status = 'active'
			  AND assignment.effective_from <= CURRENT_DATE
			  AND (assignment.effective_to IS NULL OR assignment.effective_to >= CURRENT_DATE)
		)
		SELECT $2::text, eligible.leave_type_id::text,
		       COALESCE(SUM(CASE WHEN ledger.entry_type = 'reservation' THEN 0 ELSE ledger.minutes END), 0),
		       COALESCE(-SUM(CASE WHEN ledger.entry_type = 'reservation' THEN ledger.minutes ELSE 0 END), 0)
		FROM eligible_leave_types eligible
		LEFT JOIN leave_ledger_entries ledger
		  ON ledger.organization_id = $1
		 AND ledger.employee_id = $2
		 AND ledger.leave_type_id = eligible.leave_type_id
		GROUP BY eligible.leave_type_id
		ORDER BY eligible.leave_type_id`, organizationID, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Balance, 0)
	asOf := time.Now().UTC().Format(time.RFC3339)
	for rows.Next() {
		var item Balance
		if err = rows.Scan(&item.EmployeeID, &item.LeaveTypeID, &item.AvailableMinutes, &item.ReservedMinutes); err != nil {
			return nil, err
		}
		item.AsOf = asOf
		result = append(result, item)
	}
	return result, rows.Err()
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
