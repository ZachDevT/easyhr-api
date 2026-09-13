package payroll

import (
	"context"
)

// Engine defines how a country-specific tax calculator behaves.
// In the future, this will be implemented per country (e.g. UgandaEngine, USEngine).
type Engine interface {
	Calculate(ctx context.Context, orgID, employeeID, periodID string) (RunItemData, error)
}

type RunItemData struct {
	BasePay    int64
	Earnings   int64
	Deductions int64
	NetPay     int64
}

type DefaultEngine struct{}

func (e *DefaultEngine) Calculate(ctx context.Context, orgID, employeeID, periodID string) (RunItemData, error) {
	// A real implementation would pull the compensation band, count attendance hours, apply tax rules, etc.
	// For the MVP, return a fixed structure representing a baseline.
	return RunItemData{
		BasePay:    500000,
		Earnings:   0,
		Deductions: 50000, // mock flat tax
		NetPay:     450000,
	}, nil
}
