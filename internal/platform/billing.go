package platform

import (
	"context"
	"fmt"
	"time"
)

type Plan struct {
	ID                      string `json:"id"`
	Name                    string `json:"name"`
	PricePerEmployeeMinor   int64  `json:"pricePerEmployeeMinor"`
	Currency                string `json:"currency"`
	BillingInterval         string `json:"billingInterval"`
	RequiresManualInvoicing bool   `json:"requiresManualInvoicing"`
}

type SettlementMethod struct {
	ID           string `json:"id"`
	ProviderType string `json:"providerType"`
	MerchantName string `json:"merchantName"`
	Identifier   string `json:"identifier"`
	Region       string `json:"region"`
	Status       string `json:"status"`
}

type CreateSettlementMethodInput struct {
	ProviderType string `json:"providerType"`
	MerchantName string `json:"merchantName"`
	Identifier   string `json:"identifier"`
	Region       string `json:"region"`
}

type Subscription struct {
	ID                   string `json:"id"`
	OrganizationID       string `json:"organizationId"`
	PlanID               string `json:"planId"`
	Status               string `json:"status"`
	CurrentPeriodStart   string `json:"currentPeriodStart"`
	CurrentPeriodEnd     string `json:"currentPeriodEnd"`
	BilledEmployeesCount int    `json:"billedEmployeesCount"`
}

type CreateSubscriptionInput struct {
	OrganizationID string `json:"organizationId"`
	PlanID         string `json:"planId"`
}

type Invoice struct {
	ID             string  `json:"id"`
	InvoiceNumber  string  `json:"invoiceNumber"`
	OrganizationID string  `json:"organizationId"`
	SubscriptionID string  `json:"subscriptionId"`
	AmountMinor    int64   `json:"amountMinor"`
	Currency       string  `json:"currency"`
	Status         string  `json:"status"`
	DueDate        string  `json:"dueDate"`
	SentAt         *string `json:"sentAt"`
	PaidAt         *string `json:"paidAt"`
}

type RecordPaymentInput struct {
	SettlementMethodID string `json:"settlementMethodId"`
	AmountMinor        int64  `json:"amountMinor"`
}

type Transaction struct {
	ID                 string  `json:"id"`
	InvoiceID          string  `json:"invoiceId"`
	SettlementMethodID string  `json:"settlementMethodId"`
	AmountMinor        int64   `json:"amountMinor"`
	Status             string  `json:"status"`
	SettledAt          *string `json:"settledAt"`
}

func (s *Service) ListPlans(ctx context.Context) ([]Plan, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text, name, price_per_employee_minor, currency, billing_interval, requires_manual_invoicing FROM platform_plans ORDER BY price_per_employee_minor ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Plan
	for rows.Next() {
		var p Plan
		if err := rows.Scan(&p.ID, &p.Name, &p.PricePerEmployeeMinor, &p.Currency, &p.BillingInterval, &p.RequiresManualInvoicing); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, nil
}

func (s *Service) ListSettlementMethods(ctx context.Context) ([]SettlementMethod, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text, provider_type, merchant_name, identifier, region, status FROM platform_settlement_methods ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []SettlementMethod
	for rows.Next() {
		var m SettlementMethod
		if err := rows.Scan(&m.ID, &m.ProviderType, &m.MerchantName, &m.Identifier, &m.Region, &m.Status); err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return items, nil
}

func (s *Service) CreateSettlementMethod(ctx context.Context, input CreateSettlementMethodInput) (SettlementMethod, error) {
	var m SettlementMethod
	err := s.pool.QueryRow(ctx, `
		INSERT INTO platform_settlement_methods (provider_type, merchant_name, identifier, region)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text, provider_type, merchant_name, identifier, region, status
	`, input.ProviderType, input.MerchantName, input.Identifier, input.Region).Scan(
		&m.ID, &m.ProviderType, &m.MerchantName, &m.Identifier, &m.Region, &m.Status,
	)
	return m, err
}

func (s *Service) ListSubscriptions(ctx context.Context) ([]Subscription, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text, organization_id::text, plan_id::text, status, current_period_start::text, current_period_end::text, billed_employees_count FROM tenant_subscriptions ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Subscription
	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(&sub.ID, &sub.OrganizationID, &sub.PlanID, &sub.Status, &sub.CurrentPeriodStart, &sub.CurrentPeriodEnd, &sub.BilledEmployeesCount); err != nil {
			return nil, err
		}
		items = append(items, sub)
	}
	return items, nil
}

func (s *Service) CreateSubscription(ctx context.Context, input CreateSubscriptionInput) (Subscription, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Subscription{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Count employees
	var employeeCount int
	err = tx.QueryRow(ctx, "SELECT count(*) FROM employees WHERE organization_id = $1 AND status = 'Active'", input.OrganizationID).Scan(&employeeCount)
	if err != nil {
		return Subscription{}, err
	}

	// Get plan details
	var plan Plan
	err = tx.QueryRow(ctx, "SELECT price_per_employee_minor, currency, requires_manual_invoicing FROM platform_plans WHERE id = $1", input.PlanID).Scan(&plan.PricePerEmployeeMinor, &plan.Currency, &plan.RequiresManualInvoicing)
	if err != nil {
		return Subscription{}, err
	}

	start := time.Now()
	end := start.AddDate(0, 1, 0) // monthly

	var sub Subscription
	// UPSERT subscription
	err = tx.QueryRow(ctx, `
		INSERT INTO tenant_subscriptions (organization_id, plan_id, current_period_start, current_period_end, billed_employees_count, status)
		VALUES ($1, $2, $3, $4, $5, 'active')
		ON CONFLICT (organization_id) DO UPDATE SET
			plan_id = EXCLUDED.plan_id,
			current_period_start = EXCLUDED.current_period_start,
			current_period_end = EXCLUDED.current_period_end,
			billed_employees_count = EXCLUDED.billed_employees_count,
			status = 'active',
			updated_at = now()
		RETURNING id::text, organization_id::text, plan_id::text, status, current_period_start::text, current_period_end::text, billed_employees_count
	`, input.OrganizationID, input.PlanID, start, end, employeeCount).Scan(
		&sub.ID, &sub.OrganizationID, &sub.PlanID, &sub.Status, &sub.CurrentPeriodStart, &sub.CurrentPeriodEnd, &sub.BilledEmployeesCount,
	)
	if err != nil {
		return Subscription{}, err
	}

	// Generate Draft Invoice
	amount := int64(employeeCount) * plan.PricePerEmployeeMinor
	invoiceNum := fmt.Sprintf("INV-%s-%d", start.Format("2006-0102"), start.Unix()%10000)
	dueDate := start.AddDate(0, 0, 7) // Due in 7 days

	invoiceStatus := "draft"
	if !plan.RequiresManualInvoicing {
		invoiceStatus = "sent" // Auto-billing would charge immediately, we'll just mark it sent for this MVP
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO tenant_invoices (invoice_number, organization_id, subscription_id, amount_minor, currency, status, due_date)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, invoiceNum, input.OrganizationID, sub.ID, amount, plan.Currency, invoiceStatus, dueDate)
	if err != nil {
		return Subscription{}, err
	}

	return sub, tx.Commit(ctx)
}

func (s *Service) ListInvoices(ctx context.Context) ([]Invoice, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text, invoice_number, organization_id::text, subscription_id::text, amount_minor, currency, status, due_date::text, sent_at::text, paid_at::text FROM tenant_invoices ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Invoice
	for rows.Next() {
		var inv Invoice
		if err := rows.Scan(&inv.ID, &inv.InvoiceNumber, &inv.OrganizationID, &inv.SubscriptionID, &inv.AmountMinor, &inv.Currency, &inv.Status, &inv.DueDate, &inv.SentAt, &inv.PaidAt); err != nil {
			return nil, err
		}
		items = append(items, inv)
	}
	return items, nil
}

func (s *Service) SendInvoice(ctx context.Context, invoiceID string) (Invoice, error) {
	var inv Invoice
	err := s.pool.QueryRow(ctx, `
		UPDATE tenant_invoices 
		SET status = 'sent', sent_at = now() 
		WHERE id = $1 AND status = 'draft'
		RETURNING id::text, invoice_number, organization_id::text, subscription_id::text, amount_minor, currency, status, due_date::text, sent_at::text, paid_at::text
	`, invoiceID).Scan(
		&inv.ID, &inv.InvoiceNumber, &inv.OrganizationID, &inv.SubscriptionID, &inv.AmountMinor, &inv.Currency, &inv.Status, &inv.DueDate, &inv.SentAt, &inv.PaidAt,
	)
	if err != nil {
		return Invoice{}, err
	}
	return inv, nil
}

func (s *Service) RecordPayment(ctx context.Context, invoiceID string, input RecordPaymentInput) (Transaction, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Transaction{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Create cleared transaction
	var t Transaction
	err = tx.QueryRow(ctx, `
		INSERT INTO tenant_transactions (invoice_id, settlement_method_id, amount_minor, status, settled_at)
		VALUES ($1, $2, $3, 'cleared', now())
		RETURNING id::text, invoice_id::text, settlement_method_id::text, amount_minor, status, settled_at::text
	`, invoiceID, input.SettlementMethodID, input.AmountMinor).Scan(
		&t.ID, &t.InvoiceID, &t.SettlementMethodID, &t.AmountMinor, &t.Status, &t.SettledAt,
	)
	if err != nil {
		return Transaction{}, err
	}

	// Update invoice to paid
	_, err = tx.Exec(ctx, `
		UPDATE tenant_invoices 
		SET status = 'paid', paid_at = now() 
		WHERE id = $1
	`, invoiceID)
	if err != nil {
		return Transaction{}, err
	}

	return t, tx.Commit(ctx)
}
