package payroll

import (
	"context"
	"errors"
)

type Payslip struct {
	ID             string `json:"id"`
	EmployeeID     string `json:"employeeId"`
	EmployeeName   string `json:"employeeName"`
	Email          string `json:"email"`
	PeriodStart    string `json:"periodStart"`
	PeriodEnd      string `json:"periodEnd"`
	PayDate        string `json:"payDate"`
	Currency       string `json:"currency"`
	BasePay        int64  `json:"basePay"`
	Earnings       int64  `json:"earnings"`
	Deductions     int64  `json:"deductions"`
	NetPay         int64  `json:"netPay"`
	DeliveryStatus string `json:"deliveryStatus"`
}

func (s *Service) ListPayslipsForRun(ctx context.Context, org, runID string) ([]Payslip, error) {
	rows, err := s.pool.Query(ctx, `SELECT ps.id::text,e.id::text,e.first_name||' '||e.last_name,COALESCE(e.work_email,u.email,''),pp.start_date::text,pp.end_date::text,pp.pay_date::text,COALESCE(pc.currency,'USD'),pri.base_pay,pri.earnings,pri.deductions,pri.net_pay,ps.delivery_status FROM paystubs ps JOIN payroll_run_items pri ON pri.id=ps.payroll_run_item_id JOIN employees e ON e.id=pri.employee_id LEFT JOIN users u ON u.id=e.user_id JOIN payroll_runs pr ON pr.id=pri.payroll_run_id JOIN payroll_periods pp ON pp.id=pr.payroll_period_id LEFT JOIN payroll_configurations pc ON pc.organization_id=pp.organization_id WHERE pr.id=$1 AND pp.organization_id=$2 AND pr.status='approved' ORDER BY e.first_name`, runID, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Payslip{}
	for rows.Next() {
		var p Payslip
		if err := rows.Scan(&p.ID, &p.EmployeeID, &p.EmployeeName, &p.Email, &p.PeriodStart, &p.PeriodEnd, &p.PayDate, &p.Currency, &p.BasePay, &p.Earnings, &p.Deductions, &p.NetPay, &p.DeliveryStatus); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) GetPayslipForUser(ctx context.Context, org, userID, id string) (Payslip, error) {
	var p Payslip
	err := s.pool.QueryRow(ctx, `SELECT ps.id::text,e.id::text,e.first_name||' '||e.last_name,COALESCE(e.work_email,u.email,''),pp.start_date::text,pp.end_date::text,pp.pay_date::text,COALESCE(pc.currency,'USD'),pri.base_pay,pri.earnings,pri.deductions,pri.net_pay,ps.delivery_status FROM paystubs ps JOIN payroll_run_items pri ON pri.id=ps.payroll_run_item_id JOIN employees e ON e.id=pri.employee_id LEFT JOIN users u ON u.id=e.user_id JOIN payroll_runs pr ON pr.id=pri.payroll_run_id JOIN payroll_periods pp ON pp.id=pr.payroll_period_id LEFT JOIN payroll_configurations pc ON pc.organization_id=pp.organization_id WHERE ps.id=$1 AND pp.organization_id=$2 AND e.user_id=$3 AND pr.status='approved'`, id, org, userID).Scan(&p.ID, &p.EmployeeID, &p.EmployeeName, &p.Email, &p.PeriodStart, &p.PeriodEnd, &p.PayDate, &p.Currency, &p.BasePay, &p.Earnings, &p.Deductions, &p.NetPay, &p.DeliveryStatus)
	return p, err
}

const payslipSelect = `SELECT ps.id::text,e.id::text,e.first_name||' '||e.last_name,COALESCE(e.work_email,u.email,''),pp.start_date::text,pp.end_date::text,pp.pay_date::text,COALESCE(pc.currency,'USD'),pri.base_pay,pri.earnings,pri.deductions,pri.net_pay,ps.delivery_status FROM paystubs ps JOIN payroll_run_items pri ON pri.id=ps.payroll_run_item_id JOIN employees e ON e.id=pri.employee_id LEFT JOIN users u ON u.id=e.user_id JOIN payroll_runs pr ON pr.id=pri.payroll_run_id JOIN payroll_periods pp ON pp.id=pr.payroll_period_id LEFT JOIN payroll_configurations pc ON pc.organization_id=pp.organization_id`

// GetPayslip returns any approved payslip in the organization (HR/admin access).
func (s *Service) GetPayslip(ctx context.Context, org, id string) (Payslip, error) {
	var p Payslip
	err := s.pool.QueryRow(ctx, payslipSelect+` WHERE ps.id=$1 AND pp.organization_id=$2 AND pr.status='approved'`, id, org).Scan(&p.ID, &p.EmployeeID, &p.EmployeeName, &p.Email, &p.PeriodStart, &p.PeriodEnd, &p.PayDate, &p.Currency, &p.BasePay, &p.Earnings, &p.Deductions, &p.NetPay, &p.DeliveryStatus)
	return p, err
}

// ListPayslipsForEmployee lists approved payslips for one employee. When employeeID is empty,
// the employee linked to userID is used (self-service).
func (s *Service) ListPayslipsForEmployee(ctx context.Context, org, employeeID, userID string) ([]Payslip, error) {
	query := payslipSelect + ` WHERE pp.organization_id=$1 AND pr.status='approved' AND `
	var arg string
	if employeeID != "" {
		query += `e.id::text=$2`
		arg = employeeID
	} else {
		query += `e.user_id::text=$2`
		arg = userID
	}
	query += ` ORDER BY pp.start_date DESC`
	rows, err := s.pool.Query(ctx, query, org, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Payslip{}
	for rows.Next() {
		var p Payslip
		if err := rows.Scan(&p.ID, &p.EmployeeID, &p.EmployeeName, &p.Email, &p.PeriodStart, &p.PeriodEnd, &p.PayDate, &p.Currency, &p.BasePay, &p.Earnings, &p.Deductions, &p.NetPay, &p.DeliveryStatus); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) MarkPayslipDelivered(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE paystubs SET delivery_status='delivered' WHERE id=$1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return errors.New("payslip not found")
	}
	return err
}
