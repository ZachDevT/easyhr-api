package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/brianvoe/gofakeit/v6"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dbURL := os.Getenv("EASYHR_DATABASE_URL")
	if dbURL == "" { log.Fatal("EASYHR_DATABASE_URL is not set") }

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil { log.Fatalf("Failed to connect to db: %v", err) }
	defer pool.Close()

	fmt.Println("🌱 Starting Massive Seeder...")
	gofakeit.Seed(0)

	// 1. Organization
	var orgID string
	pool.QueryRow(ctx, "INSERT INTO organizations (name, country_code, timezone) VALUES ($1, $2, $3) RETURNING id::text", "Acme Global", "US", "UTC").Scan(&orgID)
	fmt.Printf("✅ Created Organization: %s\n", orgID)

	// 2. Platform Subscriptions & Invoices
	pool.Exec(ctx, "INSERT INTO platform_subscriptions (organization_id, plan_id, status, current_period_end) SELECT $1, id, 'active', now() + interval '1 year' FROM platform_billing_plans LIMIT 1", orgID)
	pool.Exec(ctx, "INSERT INTO tenant_invoices (organization_id, invoice_number, amount_due, status, due_date) VALUES ($1, $2, $3, 'paid', now() - interval '5 days')", orgID, "INV-001", 49900)

	// 3. Departments
	deptIDs := make(map[string]string)
	for _, d := range []string{"Engineering", "Sales", "HR", "Marketing", "Finance"} {
		var id string
		pool.QueryRow(ctx, "INSERT INTO departments (organization_id, name) VALUES ($1, $2) RETURNING id::text", orgID, d).Scan(&id)
		deptIDs[d] = id
	}

	// 4. Employees & Core HR
	var empIDs []string
	for i := 0; i < 20; i++ {
		email := gofakeit.Email()
		var userID string
		pool.QueryRow(ctx, "INSERT INTO users (email, first_name, last_name, external_identity_id) VALUES ($1, $2, $3, $4) RETURNING id::text", 
			email, gofakeit.FirstName(), gofakeit.LastName(), gofakeit.UUID()).Scan(&userID)
		
		pool.Exec(ctx, "INSERT INTO memberships (organization_id, user_id, status) VALUES ($1, $2, 'active')", orgID, userID)

		var empID string
		pool.QueryRow(ctx, `
			INSERT INTO employees (organization_id, user_id, employee_number, first_name, last_name, work_email, department_id, status, hired_on)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'active', $8) RETURNING id::text
		`, orgID, userID, fmt.Sprintf("EMP-%04d", i+1), gofakeit.FirstName(), gofakeit.LastName(), email, deptIDs["Engineering"], gofakeit.DateRange(time.Now().AddDate(-5, 0, 0), time.Now())).Scan(&empID)
		empIDs = append(empIDs, empID)

		// Personal Profile
		pool.Exec(ctx, "INSERT INTO employee_personal_profiles (employee_id, personal_email, phone_number, date_of_birth, nationality) VALUES ($1, $2, $3, $4, $5)", empID, gofakeit.Email(), gofakeit.Phone(), gofakeit.DateRange(time.Now().AddDate(-50, 0, 0), time.Now().AddDate(-20, 0, 0)), "US")

		// Address
		pool.Exec(ctx, "INSERT INTO employee_addresses (employee_id, address_type, line1, city, state, postal_code, country) VALUES ($1, 'home', $2, $3, $4, $5, $6)", empID, gofakeit.Street(), gofakeit.City(), gofakeit.State(), gofakeit.Zip(), "US")

		// Bank
		pool.Exec(ctx, "INSERT INTO employee_banks (employee_id, bank_name, account_number, routing_number, account_type) VALUES ($1, $2, $3, $4, 'checking')", empID, "Chase Bank", "1234567890", "098765432")

		// Compensation
		pool.Exec(ctx, "INSERT INTO compensation_records (organization_id, employee_id, effective_date, amount, currency, frequency, type) VALUES ($1, $2, now(), $3, 'USD', 'annual', 'salary')", orgID, empID, gofakeit.Number(60000, 150000))
	}
	fmt.Printf("✅ Created %d Employees with profiles, addresses, banks, and comp\n", len(empIDs))

	// 5. Time Off
	var policyID string
	pool.QueryRow(ctx, "INSERT INTO leave_policies (organization_id, name, type, allowance_days) VALUES ($1, 'Standard PTO', 'vacation', 20) RETURNING id::text", orgID).Scan(&policyID)
	for _, empID := range empIDs {
		pool.Exec(ctx, "INSERT INTO leave_balances (organization_id, employee_id, policy_id, balance) VALUES ($1, $2, $3, $4)", orgID, empID, policyID, gofakeit.Number(5, 20))
		if gofakeit.Bool() {
			pool.Exec(ctx, "INSERT INTO leave_requests (organization_id, employee_id, policy_id, start_date, end_date, status, days_requested) VALUES ($1, $2, $3, $4, $5, 'approved', 2)", orgID, empID, policyID, time.Now().AddDate(0, 1, 0), time.Now().AddDate(0, 1, 2))
		}
	}

	// 6. Attendance & Scheduling
	var scheduleID string
	pool.QueryRow(ctx, "INSERT INTO shift_schedules (organization_id, name) VALUES ($1, 'Morning Shift') RETURNING id::text", orgID).Scan(&scheduleID)
	pool.Exec(ctx, "INSERT INTO shift_entries (schedule_id, day_of_week, start_time, end_time) VALUES ($1, 1, '09:00:00', '17:00:00')", scheduleID)
	for i, empID := range empIDs {
		if i < 5 {
			pool.Exec(ctx, "INSERT INTO attendance_records (organization_id, employee_id, date, clock_in, status) VALUES ($1, $2, CURRENT_DATE, now() - interval '8 hours', 'present')", orgID, empID)
			pool.Exec(ctx, "INSERT INTO shift_assignments (organization_id, employee_id, schedule_id, effective_from) VALUES ($1, $2, $3, CURRENT_DATE)", orgID, empID, scheduleID)
		}
	}

	// 7. Payroll
	var periodID string
	pool.QueryRow(ctx, "INSERT INTO pay_periods (organization_id, start_date, end_date, status) VALUES ($1, date_trunc('month', CURRENT_DATE), (date_trunc('month', CURRENT_DATE) + interval '1 month' - interval '1 day'), 'open') RETURNING id::text", orgID).Scan(&periodID)
	for _, empID := range empIDs {
		pool.Exec(ctx, "INSERT INTO pay_slips (organization_id, pay_period_id, employee_id, gross_pay, net_pay, status) VALUES ($1, $2, $3, 5000, 3800, 'draft')", orgID, periodID, empID)
	}

	// 8. Performance & Goals
	for i, empID := range empIDs {
		if i < 10 {
			pool.Exec(ctx, "INSERT INTO performance_goals (organization_id, employee_id, title, status, progress) VALUES ($1, $2, 'Launch Q3 Campaign', 'in_progress', 50)", orgID, empID)
			pool.Exec(ctx, "INSERT INTO performance_reviews (organization_id, employee_id, reviewer_id, title, status) VALUES ($1, $2, $3, 'Q2 Review', 'completed')", orgID, empID, empIDs[0])
		}
	}

	// 9. Benefits
	var benefitID string
	pool.QueryRow(ctx, "INSERT INTO benefit_plans (organization_id, name, provider, type, cost_to_employee) VALUES ($1, 'BlueCross Gold', 'BlueCross', 'health', 150.00) RETURNING id::text", orgID).Scan(&benefitID)
	for i, empID := range empIDs {
		if i < 15 {
			pool.Exec(ctx, "INSERT INTO benefit_enrollments (organization_id, employee_id, plan_id, status) VALUES ($1, $2, $3, 'active')", orgID, empID, benefitID)
		}
	}

	// 10. Hiring & Onboarding
	var jobID string
	pool.QueryRow(ctx, "INSERT INTO job_postings (organization_id, title, department_id, location, status) VALUES ($1, 'Senior Backend Engineer', $2, 'Remote', 'published') RETURNING id::text", orgID, deptIDs["Engineering"]).Scan(&jobID)
	pool.Exec(ctx, "INSERT INTO candidates (organization_id, job_id, first_name, last_name, email, stage) VALUES ($1, $2, 'John', 'Doe', 'john.candidate@example.com', 'interview')", orgID, jobID)
	
	pool.Exec(ctx, "INSERT INTO onboarding_templates (organization_id, name) VALUES ($1, 'Standard Engineering Onboarding')", orgID)

	// 11. Helpdesk Tickets
	pool.Exec(ctx, "INSERT INTO support_tickets (organization_id, employee_id, subject, status, priority) VALUES ($1, $2, 'Laptop screen cracked', 'open', 'high')", orgID, empIDs[2])

	fmt.Println("🎉 Database is completely saturated with rich dummy data across 20+ tables!")
}
