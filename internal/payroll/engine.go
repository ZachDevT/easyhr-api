package payroll

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrMissingCompensation = errors.New("active employee is missing effective compensation")
var ErrUnsupportedJurisdiction = errors.New("payroll tax rules are not configured for this jurisdiction")
var ErrUnsupportedCompensation = errors.New("payroll only supports monthly salary compensation records")
var ErrCompensationCurrencyMismatch = errors.New("employee compensation currency does not match company payroll currency")

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
	return &ConfiguredEngine{pool: pool}
}

type unsupportedEngine struct{}

func (unsupportedEngine) Calculate(context.Context, Configuration, string, Period) (RunItemData, error) {
	return RunItemData{}, ErrUnsupportedJurisdiction
}

// UgandaEngine implements Uganda PAYE and NSSF tax logic.
type UgandaEngine struct {
	pool *pgxpool.Pool
}

type TaxBand struct {
	UpTo        int64   `json:"upTo"`
	RatePercent float64 `json:"ratePercent"`
}
type ConfiguredEngine struct{ pool *pgxpool.Pool }

func (e *ConfiguredEngine) Calculate(ctx context.Context, config Configuration, employeeID string, period Period) (RunItemData, error) {
	var base int64
	var payType, ccy, freq string
	err := e.pool.QueryRow(ctx, `SELECT base_amount_minor,pay_type,currency,pay_frequency FROM compensation_records WHERE employee_id=$1 AND effective_date<=$2 ORDER BY effective_date DESC LIMIT 1`, employeeID, period.EndDate).Scan(&base, &payType, &ccy, &freq)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunItemData{}, ErrMissingCompensation
	}
	if err != nil {
		return RunItemData{}, err
	}
	if payType != "salary" || freq != "monthly" {
		return RunItemData{}, ErrUnsupportedCompensation
	}
	if ccy != config.Currency {
		return RunItemData{}, ErrCompensationCurrencyMismatch
	}
	rows, err := e.pool.Query(ctx, `SELECT r.rule_type,r.bands,r.rate_percent,r.amount_minor,r.applies_to,r.applies_to_id::text
		FROM payroll_tax_rules r
		WHERE r.organization_id=$1 AND r.country=$2 AND r.active=true
		AND (r.applies_to='all_employees' OR (r.applies_to='department' AND EXISTS (SELECT 1 FROM employment_assignments ea WHERE ea.employee_id=$3 AND ea.effective_to IS NULL AND ea.department_id=r.applies_to_id)) OR (r.applies_to='position' AND EXISTS (SELECT 1 FROM employment_assignments ea WHERE ea.employee_id=$3 AND ea.effective_to IS NULL AND ea.position_id=r.applies_to_id))) ORDER BY r.id`, config.OrganizationID, config.Country, employeeID)
	if err != nil {
		return RunItemData{}, err
	}
	defer rows.Close()
	var rules int
	var deductions int64
	for rows.Next() {
		var kind string
		var raw []byte
		var rate float64
		var amount int64
		var scope string
		var scopeID *string
		if err := rows.Scan(&kind, &raw, &rate, &amount, &scope, &scopeID); err != nil {
			return RunItemData{}, err
		}
		rules++
		if kind == "flat_deduction" {
			if amount > 0 {
				deductions += amount
			} else {
				deductions += base * int64(rate*10000) / 1_000_000
			}
			continue
		}
		var bands []TaxBand
		if err := json.Unmarshal(raw, &bands); err != nil {
			return RunItemData{}, err
		}
		sort.Slice(bands, func(i, j int) bool {
			if bands[i].UpTo == 0 {
				return false
			}
			if bands[j].UpTo == 0 {
				return true
			}
			return bands[i].UpTo < bands[j].UpTo
		})
		remaining := base
		var lower int64
		var tax int64
		for i, b := range bands {
			if b.RatePercent < 0 || b.RatePercent > 100 || (b.UpTo != 0 && b.UpTo <= lower) || (b.UpTo == 0 && i != len(bands)-1) {
				return RunItemData{}, fmt.Errorf("invalid tax band configuration")
			}
			width := remaining
			if b.UpTo > 0 && width > b.UpTo-lower {
				width = b.UpTo - lower
			}
			tax += int64(float64(width) * b.RatePercent / 100)
			remaining -= width
			if b.UpTo > 0 {
				lower = b.UpTo
			}
			if remaining <= 0 {
				break
			}
		}
		if remaining > 0 && len(bands) > 0 {
			tax += int64(float64(remaining) * bands[len(bands)-1].RatePercent / 100)
		}
		deductions += tax
	}
	if err := rows.Err(); err != nil {
		return RunItemData{}, err
	}
	if rules == 0 {
		return RunItemData{}, ErrUnsupportedJurisdiction
	}
	if deductions > base {
		deductions = base
	}
	return RunItemData{BasePay: base, Deductions: deductions, NetPay: base - deductions}, nil
}

func (e *UgandaEngine) Calculate(ctx context.Context, config Configuration, employeeID string, period Period) (RunItemData, error) {
	var baseAmount int64
	var payType string
	var currency, frequency string
	err := e.pool.QueryRow(ctx, `
		SELECT base_amount_minor, pay_type, currency, pay_frequency
		FROM compensation_records 
		WHERE employee_id = $1 AND effective_date <= $2 
		ORDER BY effective_date DESC LIMIT 1
	`, employeeID, period.EndDate).Scan(&baseAmount, &payType, &currency, &frequency)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RunItemData{}, ErrMissingCompensation
		}
		return RunItemData{}, err
	}
	if payType != "salary" || frequency != "monthly" {
		return RunItemData{}, ErrUnsupportedCompensation
	}
	if currency != config.Currency {
		return RunItemData{}, ErrCompensationCurrencyMismatch
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
