package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dbURL := os.Getenv("EASYHR_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		log.Fatal("EASYHR_DATABASE_URL or DATABASE_URL environment variable is required")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer pool.Close()

	// 1. Get primary org and user
	var orgID, userID string
	err = pool.QueryRow(ctx, "SELECT id::text FROM organizations ORDER BY created_at ASC LIMIT 1").Scan(&orgID)
	if err != nil {
		log.Fatalf("No organization found: %v", err)
	}
	err = pool.QueryRow(ctx, "SELECT id::text FROM users ORDER BY created_at ASC LIMIT 1").Scan(&userID)
	if err != nil {
		log.Fatalf("No user found: %v", err)
	}

	fmt.Printf("Seeding benchmark data for Org: %s, User: %s\n", orgID, userID)

	// Check existing employee count
	var existingEmployees int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM employees WHERE organization_id = $1", orgID).Scan(&existingEmployees)
	if existingEmployees >= 200 {
		fmt.Printf("Already seeded with %d employees. Skipping bulk insert.\n", existingEmployees)
		return
	}

	// 2. Fetch or create a department
	var deptID string
	err = pool.QueryRow(ctx, "SELECT id::text FROM departments WHERE organization_id = $1 LIMIT 1", orgID).Scan(&deptID)
	if err != nil {
		err = pool.QueryRow(ctx, `INSERT INTO departments (organization_id, name, code) VALUES ($1, 'Engineering', 'ENG') RETURNING id::text`, orgID).Scan(&deptID)
		if err != nil {
			log.Fatalf("Failed to create department: %v", err)
		}
	}

	// 3. Fetch leave types & policies
	var leaveTypeIDs []string
	rows, err := pool.Query(ctx, "SELECT id::text FROM leave_types WHERE organization_id = $1", orgID)
	if err == nil {
		for rows.Next() {
			var ltID string
			if err := rows.Scan(&ltID); err == nil {
				leaveTypeIDs = append(leaveTypeIDs, ltID)
			}
		}
		rows.Close()
	}

	firstNames := []string{"James", "Mary", "John", "Patricia", "Robert", "Jennifer", "Michael", "Linda", "William", "Elizabeth", "David", "Barbara", "Richard", "Susan", "Joseph", "Jessica", "Thomas", "Sarah", "Charles", "Karen"}
	lastNames := []string{"Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia", "Miller", "Davis", "Rodriguez", "Martinez", "Hernandez", "Lopez", "Gonzalez", "Wilson", "Anderson", "Thomas", "Taylor", "Moore", "Jackson", "Martin"}
	titles := []string{"Senior Software Engineer", "Product Manager", "UI/UX Designer", "DevOps Engineer", "QA Engineer", "Data Analyst", "HR Specialist", "Account Executive"}

	// Seed 250 employees
	batchSize := 250
	fmt.Printf("Inserting %d employees...\n", batchSize)

	var empIDs []string
	for i := 1; i <= batchSize; i++ {
		fn := firstNames[rand.Intn(len(firstNames))]
		ln := lastNames[rand.Intn(len(lastNames))]
		empNum := fmt.Sprintf("EMP-%04d", i+existingEmployees)
		email := fmt.Sprintf("%s.%s.%d@benchmarks.local", fn, ln, i+existingEmployees)

		var empID string
		err := pool.QueryRow(ctx, `
			INSERT INTO employees (organization_id, employee_number, first_name, last_name, work_email, status, hired_on)
			VALUES ($1, $2, $3, $4, $5, 'active', CURRENT_DATE - (i || ' days')::interval)
			ON CONFLICT (organization_id, employee_number) DO UPDATE SET first_name = EXCLUDED.first_name
			RETURNING id::text
		`, orgID, empNum, fn, ln, email).Scan(&empID)
		if err != nil {
			continue
		}
		empIDs = append(empIDs, empID)

		// Employment assignment
		title := titles[rand.Intn(len(titles))]
		_, _ = pool.Exec(ctx, `
			INSERT INTO employment_assignments (employee_id, department_id, job_title, is_primary, effective_from)
			VALUES ($1, $2, $3, true, CURRENT_DATE - interval '180 days')
			ON CONFLICT DO NOTHING
		`, empID, deptID, title)

		// Attendance punches (clock_in and clock_out for past 5 days)
		for d := 0; d < 5; d++ {
			punchDate := time.Now().UTC().AddDate(0, 0, -d)
			clockIn := time.Date(punchDate.Year(), punchDate.Month(), punchDate.Day(), 9, 0, 0, 0, time.UTC)
			clockOut := time.Date(punchDate.Year(), punchDate.Month(), punchDate.Day(), 17, 30, 0, 0, time.UTC)
			_, _ = pool.Exec(ctx, `
				INSERT INTO attendance_punches (organization_id, employee_id, kind, occurred_at, source)
				VALUES ($1, $2, 'clock_in', $3, 'web'), ($1, $2, 'clock_out', $4, 'web')
				ON CONFLICT DO NOTHING
			`, orgID, empID, clockIn, clockOut)
		}

		// Leave requests & ledger entries
		if len(leaveTypeIDs) > 0 {
			ltID := leaveTypeIDs[rand.Intn(len(leaveTypeIDs))]
			// Ledger accrual
			_, _ = pool.Exec(ctx, `
				INSERT INTO leave_ledger_entries (organization_id, employee_id, leave_type_id, entry_type, minutes, effective_on, source_type)
				VALUES ($1, $2, $3, 'accrual', 9600, CURRENT_DATE - interval '60 days', 'accrual_run')
				ON CONFLICT DO NOTHING
			`, orgID, empID, ltID)

			// Leave request
			_, _ = pool.Exec(ctx, `
				INSERT INTO leave_requests (organization_id, employee_id, leave_type_id, start_date, end_date, requested_minutes, reason, status, submitted_at)
				VALUES ($1, $2, $3, CURRENT_DATE + interval '10 days', CURRENT_DATE + interval '12 days', 960, 'Vacation', 'approved', CURRENT_DATE - interval '5 days')
				ON CONFLICT DO NOTHING
			`, orgID, empID, ltID)
		}
	}

	// Link primary user to first employee if not yet linked
	if len(empIDs) > 0 {
		_, _ = pool.Exec(ctx, `UPDATE employees SET user_id = $1 WHERE id = $2 AND NOT EXISTS (SELECT 1 FROM employees WHERE user_id = $1)`, userID, empIDs[0])
	}

	// 4. Seed job applicants if job posting exists
	var jobID string
	err = pool.QueryRow(ctx, "SELECT id::text FROM job_postings WHERE organization_id = $1 LIMIT 1", orgID).Scan(&jobID)
	if err == nil {
		fmt.Println("Seeding job applicants...")
		for a := 1; a <= 100; a++ {
			afn := firstNames[rand.Intn(len(firstNames))]
			aln := lastNames[rand.Intn(len(lastNames))]
			aEmail := fmt.Sprintf("%s.%s.%d@candidate.bench", afn, aln, a)
			_, _ = pool.Exec(ctx, `
				INSERT INTO job_applicants (job_posting_id, first_name, last_name, email, status)
				VALUES ($1, $2, $3, $4, 'new')
				ON CONFLICT DO NOTHING
			`, jobID, afn, aln, aEmail)
		}
	}

	// Run VACUUM ANALYZE to update PostgreSQL optimizer statistics
	fmt.Println("Running VACUUM ANALYZE...")
	_, _ = pool.Exec(ctx, "VACUUM ANALYZE employees; VACUUM ANALYZE employment_assignments; VACUUM ANALYZE attendance_punches; VACUUM ANALYZE leave_ledger_entries; VACUUM ANALYZE leave_requests; VACUUM ANALYZE job_applicants;")

	fmt.Println("Seeding and optimizer statistics update completed successfully!")
}
