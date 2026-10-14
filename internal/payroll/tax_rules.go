package payroll

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

func (s *Service) ListTaxRules(ctx context.Context, org string) ([]TaxRule, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,country,currency,rule_name,rule_type,bands,rate_percent,active FROM payroll_tax_rules WHERE organization_id=$1 ORDER BY country,rule_name`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaxRule{}
	for rows.Next() {
		var t TaxRule
		var b []byte
		if err := rows.Scan(&t.ID, &t.Country, &t.Currency, &t.RuleName, &t.RuleType, &b, &t.RatePercent, &t.Active); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &t.Bands); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Service) SaveTaxRule(ctx context.Context, org string, in TaxRuleInput) (TaxRule, error) {
	in.Country = strings.ToUpper(strings.TrimSpace(in.Country))
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	in.RuleName = strings.TrimSpace(in.RuleName)
	if len(in.Country) != 2 || len(in.Currency) != 3 || in.RuleName == "" || len(in.RuleName) > 120 {
		return TaxRule{}, errors.New("country, currency and rule name are required")
	}
	if in.RuleType != "progressive_tax" && in.RuleType != "flat_deduction" {
		return TaxRule{}, errors.New("ruleType must be progressive_tax or flat_deduction")
	}
	if in.RuleType == "flat_deduction" && (in.RatePercent < 0 || in.RatePercent > 100) {
		return TaxRule{}, errors.New("deduction rate must be from 0 to 100")
	}
	if in.RuleType == "progressive_tax" {
		if len(in.Bands) == 0 {
			return TaxRule{}, errors.New("progressive tax requires at least one band")
		}
		for _, b := range in.Bands {
			if b.UpTo < 0 || b.RatePercent < 0 || b.RatePercent > 100 {
				return TaxRule{}, errors.New("tax bands need non-negative thresholds and 0 to 100 rates")
			}
		}
	}
	raw, _ := json.Marshal(in.Bands)
	var out TaxRule
	err := s.pool.QueryRow(ctx, `INSERT INTO payroll_tax_rules (organization_id,country,currency,rule_name,rule_type,bands,rate_percent) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (organization_id,country,rule_name) DO UPDATE SET currency=EXCLUDED.currency,rule_type=EXCLUDED.rule_type,bands=EXCLUDED.bands,rate_percent=EXCLUDED.rate_percent,active=true,updated_at=now() RETURNING id::text,country,currency,rule_name,rule_type,bands,rate_percent,active`, org, in.Country, in.Currency, in.RuleName, in.RuleType, raw, in.RatePercent).Scan(&out.ID, &out.Country, &out.Currency, &out.RuleName, &out.RuleType, &raw, &out.RatePercent, &out.Active)
	if err == nil {
		_ = json.Unmarshal(raw, &out.Bands)
	}
	return out, err
}
