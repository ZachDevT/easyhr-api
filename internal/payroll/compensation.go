package payroll

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrCompensationEmployeeNotFound = errors.New("active employee not found in organization")
var ErrCompensationConfigMissing = errors.New("save company payroll settings before entering employee compensation")

type CompensationRecord struct {
	ID              string `json:"id"`
	EmployeeID      string `json:"employeeId"`
	EmployeeName    string `json:"employeeName"`
	EffectiveDate   string `json:"effectiveDate"`
	Currency        string `json:"currency"`
	PayFrequency    string `json:"payFrequency"`
	BaseAmountMinor int64  `json:"baseAmountMinor"`
	PayType         string `json:"payType"`
	Reason          string `json:"reason"`
}

type CreateCompensationInput struct {
	EmployeeID      string `json:"employeeId"`
	Country         string `json:"country"`
	EffectiveDate   string `json:"effectiveDate"`
	Currency        string `json:"currency"`
	BaseAmountMinor int64  `json:"baseAmountMinor"`
	Reason          string `json:"reason"`
}

func ListCompensation(ctx context.Context, pool *pgxpool.Pool, organizationID string) ([]CompensationRecord, error) {
	rows, err := pool.Query(ctx, `
		SELECT cr.id::text, e.id::text, e.first_name || ' ' || e.last_name,
		       cr.effective_date::text, cr.currency, cr.pay_frequency,
		       cr.base_amount_minor, cr.pay_type, COALESCE(cr.reason, '')
		FROM compensation_records cr
		JOIN employees e ON e.id = cr.employee_id
		WHERE e.organization_id = $1
		ORDER BY cr.effective_date DESC, e.last_name, e.first_name`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]CompensationRecord, 0)
	for rows.Next() {
		var item CompensationRecord
		if err := rows.Scan(&item.ID, &item.EmployeeID, &item.EmployeeName, &item.EffectiveDate, &item.Currency, &item.PayFrequency, &item.BaseAmountMinor, &item.PayType, &item.Reason); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (service *Service) CreateCompensation(ctx context.Context, organizationID, actorUserID, requestID string, input CreateCompensationInput) (CompensationRecord, error) {
	input.EmployeeID = strings.TrimSpace(input.EmployeeID)
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.Reason = strings.TrimSpace(input.Reason)
	if input.EmployeeID == "" || input.EffectiveDate == "" || len(input.Currency) != 3 || input.BaseAmountMinor <= 0 {
		return CompensationRecord{}, fmt.Errorf("employee, effective date, three-letter currency, and positive monthly salary are required")
	}
	if len(input.Reason) > 1000 {
		return CompensationRecord{}, fmt.Errorf("reason must be 1000 characters or fewer")
	}
	input.Country = strings.ToUpper(strings.TrimSpace(input.Country))
	var configuredCurrency string
	if err := service.pool.QueryRow(ctx, `SELECT currency FROM payroll_configurations WHERE organization_id = $1 AND country = $2`, organizationID, input.Country).Scan(&configuredCurrency); err != nil {
		return CompensationRecord{}, ErrCompensationConfigMissing
	}
	if input.Currency != configuredCurrency {
		return CompensationRecord{}, fmt.Errorf("compensation currency must match the company payroll currency (%s)", configuredCurrency)
	}
	var item CompensationRecord
	item.EmployeeID = input.EmployeeID
	item.EffectiveDate = input.EffectiveDate
	item.Currency = input.Currency
	item.PayFrequency = "monthly"
	item.PayType = "salary"
	item.BaseAmountMinor = input.BaseAmountMinor
	item.Reason = input.Reason
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return CompensationRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	err = tx.QueryRow(ctx, `
		INSERT INTO compensation_records (employee_id, effective_date, currency, pay_frequency, base_amount_minor, pay_type, reason, created_by_user_id)
		SELECT e.id, $3::date, $4, 'monthly', $5, 'salary', NULLIF($6, ''), $7
		FROM employees e
		WHERE e.id = $1 AND e.organization_id = $2 AND e.status IN ('active', 'leave', 'onboarding')
		RETURNING id::text`, input.EmployeeID, organizationID, input.EffectiveDate, input.Currency, input.BaseAmountMinor, input.Reason, actorUserID).Scan(&item.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return CompensationRecord{}, ErrCompensationEmployeeNotFound
	}
	if err != nil {
		return CompensationRecord{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id, actor_user_id, action, resource_type, resource_id, request_id, metadata) VALUES ($1,$2,'payroll.compensation_created','compensation_record',$3,$4,jsonb_build_object('employeeId',$5::text,'effectiveDate',$6::date,'currency',$7::text))`, organizationID, actorUserID, item.ID, requestID, input.EmployeeID, input.EffectiveDate, input.Currency)
	if err != nil {
		return CompensationRecord{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CompensationRecord{}, err
	}
	return item, nil
}
