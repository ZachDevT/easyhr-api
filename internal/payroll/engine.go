package payroll

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Engine interface {
	Calculate(ctx context.Context, config Configuration, employeeID string, period Period) (RunItemData, error)
}

type RunItemData struct {
	BasePay    int64
	Earnings   int64
	Deductions int64
	NetPay     int64
}

// EngineFactory creates an appropriate engine for the given country.
func EngineFactory(pool *pgxpool.Pool, country string) Engine {
	switch country {
	case "UG":
		return &UgandaEngine{pool: pool}
	default:
		return &StandardEngine{pool: pool}
	}
}

// StandardEngine provides a simple flat percentage fallback.
type StandardEngine struct {
	pool *pgxpool.Pool
}

func (e *StandardEngine) Calculate(ctx context.Context, config Configuration, employeeID string, period Period) (RunItemData, error) {
	var baseAmount int64
	var payType string
	err := e.pool.QueryRow(ctx, `
		SELECT base_amount_minor, pay_type 
		FROM compensation_records 
		WHERE employee_id = $1 AND effective_date <= $2 
		ORDER BY effective_date DESC LIMIT 1
	`, employeeID, period.EndDate).Scan(&baseAmount, &payType)

	if err != nil {
		// If no compensation record exists, return 0
		return RunItemData{}, nil
	}

	// Basic logic: 20% flat tax for fallback
	earnings := int64(0)
	deductions := (baseAmount + earnings) * 20 / 100
	netPay := (baseAmount + earnings) - deductions

	return RunItemData{
		BasePay:    baseAmount,
		Earnings:   earnings,
		Deductions: deductions,
		NetPay:     netPay,
	}, nil
}

// UgandaEngine implements Uganda PAYE and NSSF tax logic.
type UgandaEngine struct {
	pool *pgxpool.Pool
}

func (e *UgandaEngine) Calculate(ctx context.Context, config Configuration, employeeID string, period Period) (RunItemData, error) {
	var baseAmount int64
	var payType string
	err := e.pool.QueryRow(ctx, `
		SELECT base_amount_minor, pay_type 
		FROM compensation_records 
		WHERE employee_id = $1 AND effective_date <= $2 
		ORDER BY effective_date DESC LIMIT 1
	`, employeeID, period.EndDate).Scan(&baseAmount, &payType)

	if err != nil {
		return RunItemData{}, nil
	}

	// Uganda NSSF (5% employee contribution)
	nssf := baseAmount * 5 / 100

	// Uganda PAYE Calculation (Simplified monthly brackets)
	// Up to 235,000: 0%
	// 235,001 to 335,000: 10% of amount exceeding 235,000
	// 335,001 to 410,000: 10,000 + 20% of amount exceeding 335,000
	// Above 410,000: 25,000 + 30% of amount exceeding 410,000
	// + 10% of amount exceeding 10,000,000 (Tier 2)

	taxableIncome := baseAmount - nssf
	var paye int64 = 0

	if taxableIncome > 410000 {
		paye = 25000 + ((taxableIncome - 410000) * 30 / 100)
		if taxableIncome > 10000000 {
			paye += (taxableIncome - 10000000) * 10 / 100
		}
	} else if taxableIncome > 335000 {
		paye = 10000 + ((taxableIncome - 335000) * 20 / 100)
	} else if taxableIncome > 235000 {
		paye = (taxableIncome - 235000) * 10 / 100
	}

	deductions := nssf + paye
	netPay := baseAmount - deductions

	return RunItemData{
		BasePay:    baseAmount,
		Earnings:   0, // Overtime / bonuses would be queried from attendance/ledger here
		Deductions: deductions,
		NetPay:     netPay,
	}, nil
}
