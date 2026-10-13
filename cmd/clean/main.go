package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

func loadEnvFile(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			v = strings.Trim(v, `"'`)
			if os.Getenv(k) == "" {
				os.Setenv(k, v)
			}
		}
	}
}

func main() {
	dryRun := flag.Bool("inspect", false, "Inspect existing data without deleting")
	flag.Parse()

	if os.Getenv("EASYHR_DATABASE_URL") == "" {
		loadEnvFile(".env")
	}

	dbURL := os.Getenv("EASYHR_DATABASE_URL")
	if dbURL == "" {
		log.Fatal("EASYHR_DATABASE_URL is not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to db: %v", err)
	}
	defer pool.Close()

	fmt.Println("--- Database Content Inspection ---")

	// 1. Inspect Organizations & Workspaces
	orgRows, err := pool.Query(ctx, `
		SELECT o.id, o.name, COALESCE(w.subdomain, ''), o.created_at
		FROM organizations o
		LEFT JOIN workspaces w ON w.organization_id = o.id
		ORDER BY o.created_at DESC
	`)
	if err != nil {
		log.Printf("Error inspecting organizations: %v", err)
	} else {
		fmt.Println("\nExisting Organizations:")
		count := 0
		for orgRows.Next() {
			var id, name, subdomain string
			var createdAt any
			if err := orgRows.Scan(&id, &name, &subdomain, &createdAt); err == nil {
				count++
				fmt.Printf("  [%d] ID: %s | Name: %s | Subdomain: %s | Created: %v\n", count, id, name, subdomain, createdAt)
			}
		}
		orgRows.Close()
		if count == 0 {
			fmt.Println("  (No organizations found)")
		}
	}

	// 2. Inspect Users
	userRows, err := pool.Query(ctx, `
		SELECT id, email, first_name, last_name, external_identity_id, created_at
		FROM users
		ORDER BY created_at DESC
	`)
	if err != nil {
		log.Printf("Error inspecting users: %v", err)
	} else {
		fmt.Println("\nExisting Users:")
		count := 0
		for userRows.Next() {
			var id, email, fName, lName, extID string
			var createdAt any
			if err := userRows.Scan(&id, &email, &fName, &lName, &extID, &createdAt); err == nil {
				count++
				fmt.Printf("  [%d] ID: %s | Name: %s %s | Email: %s | External ID: %s\n", count, id, fName, lName, email, extID)
			}
		}
		userRows.Close()
		if count == 0 {
			fmt.Println("  (No users found)")
		}
	}

	// 3. Inspect Employees
	empRows, err := pool.Query(ctx, `
		SELECT id, first_name, last_name, work_email, organization_id, created_at
		FROM employees
		ORDER BY created_at DESC
	`)
	if err == nil {
		fmt.Println("\nExisting Employees:")
		count := 0
		for empRows.Next() {
			var id, fName, lName, wEmail, orgID string
			var createdAt any
			if err := empRows.Scan(&id, &fName, &lName, &wEmail, &orgID, &createdAt); err == nil {
				count++
				fmt.Printf("  [%d] ID: %s | Name: %s %s | Work Email: %s | Org ID: %s\n", count, id, fName, lName, wEmail, orgID)
			}
		}
		empRows.Close()
		if count == 0 {
			fmt.Println("  (No employees found)")
		}
	}

	if *dryRun {
		fmt.Println("\nInspection mode finished. No data was deleted.")
		return
	}

	fmt.Println("\nStarting cleanup of test data...")

	tables := []string{
		"candidate_email_messages",
		"candidate_email_threads",
		"job_applicants",
		"job_postings",
		"leave_requests",
		"leave_policies",
		"leave_types",
		"attendance_records",
		"employee_emergency_contacts",
		"employee_bank_details",
		"employee_personal_profiles",
		"employment_assignments",
		"employee_compensations",
		"employee_documents",
		"employee_signatures",
		"employees",
		"audit_events",
		"bootstrap_idempotency_keys",
		"idempotency_keys",
		"membership_roles",
		"memberships",
		"geofence_rules",
		"locations",
		"departments",
		"divisions",
		"organization_legal_profiles",
		"organization_social_links",
		"tenant_invoices",
		"platform_subscriptions",
		"workspaces",
		"organizations",
		"users",
	}

	for _, table := range tables {
		_, err := pool.Exec(ctx, fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table))
		if err != nil {
			fmt.Printf("Notice on table %s: %v\n", table, err)
		} else {
			fmt.Printf("Truncated table: %s\n", table)
		}
	}

	// Also delete any non-system tenant-specific roles
	tag, err := pool.Exec(ctx, "DELETE FROM roles WHERE organization_id IS NOT NULL")
	if err != nil {
		fmt.Printf("Notice on tenant roles cleanup: %v\n", err)
	} else {
		fmt.Printf("Deleted %d tenant-specific roles\n", tag.RowsAffected())
	}

	// Verification
	var orgCount, userCount, empCount, workspaceCount int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM organizations").Scan(&orgCount)
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM users").Scan(&userCount)
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM employees").Scan(&empCount)
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM workspaces").Scan(&workspaceCount)

	fmt.Printf("\nCleanup complete. Verification counts:\n")
	fmt.Printf("  Organizations: %d\n", orgCount)
	fmt.Printf("  Workspaces:    %d\n", workspaceCount)
	fmt.Printf("  Users:         %d\n", userCount)
	fmt.Printf("  Employees:     %d\n", empCount)

	// Verify system lookup tables still exist
	var permCount, sysRoleCount int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM permissions").Scan(&permCount)
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM roles WHERE is_system = true").Scan(&sysRoleCount)
	fmt.Printf("  System Permissions preserved: %d\n", permCount)
	fmt.Printf("  System Roles preserved:       %d\n", sysRoleCount)
}
