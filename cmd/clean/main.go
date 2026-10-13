package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
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

type ClerkUser struct {
	ID             string `json:"id"`
	EmailAddresses []struct {
		EmailAddress string `json:"email_address"`
	} `json:"email_addresses"`
}

type ClerkInvitation struct {
	ID           string `json:"id"`
	EmailAddress string `json:"email_address"`
	Status       string `json:"status"`
}

func inspectAndCleanClerk(secretKey string, label string, dryRun bool) int {
	if secretKey == "" {
		return 0
	}
	fmt.Printf("\n--- Clerk (%s) Inspection ---\n", label)

	// 1. Fetch Users
	req, err := http.NewRequest("GET", "https://api.clerk.com/v1/users?limit=100", nil)
	if err != nil {
		log.Printf("Error creating Clerk request: %v", err)
		return 0
	}
	req.Header.Set("Authorization", "Bearer "+secretKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("Error querying Clerk API: %v", err)
		return 0
	}
	defer resp.Body.Close()

	var users []ClerkUser
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		log.Printf("Error decoding Clerk response: %v", err)
		return 0
	}

	if len(users) == 0 {
		fmt.Printf("  (No users found in Clerk %s)\n", label)
	} else {
		for i, u := range users {
			email := "no-email"
			if len(u.EmailAddresses) > 0 {
				email = u.EmailAddresses[0].EmailAddress
			}
			fmt.Printf("  [%d] User ID: %s | Email: %s\n", i+1, u.ID, email)
		}
	}

	// 2. Fetch Invitations
	invReq, _ := http.NewRequest("GET", "https://api.clerk.com/v1/invitations?status=pending", nil)
	invReq.Header.Set("Authorization", "Bearer "+secretKey)
	invResp, err := http.DefaultClient.Do(invReq)
	var invitations []ClerkInvitation
	if err == nil {
		defer invResp.Body.Close()
		_ = json.NewDecoder(invResp.Body).Decode(&invitations)
		if len(invitations) > 0 {
			fmt.Printf("  Pending invitations found: %d\n", len(invitations))
		}
	}

	if dryRun {
		return len(users)
	}

	// Delete users
	if len(users) > 0 {
		fmt.Printf("\nDeleting %d users from Clerk (%s)...\n", len(users), label)
		for _, u := range users {
			delReq, _ := http.NewRequest("DELETE", "https://api.clerk.com/v1/users/"+u.ID, nil)
			delReq.Header.Set("Authorization", "Bearer "+secretKey)
			delResp, err := http.DefaultClient.Do(delReq)
			if err != nil {
				fmt.Printf("  Failed to delete Clerk user %s: %v\n", u.ID, err)
			} else {
				delResp.Body.Close()
				fmt.Printf("  Successfully deleted Clerk user: %s\n", u.ID)
			}
		}
	}

	// Revoke invitations
	for _, inv := range invitations {
		revReq, _ := http.NewRequest("POST", fmt.Sprintf("https://api.clerk.com/v1/invitations/%s/revoke", inv.ID), nil)
		revReq.Header.Set("Authorization", "Bearer "+secretKey)
		revResp, err := http.DefaultClient.Do(revReq)
		if err == nil {
			revResp.Body.Close()
			fmt.Printf("  Revoked invitation: %s (%s)\n", inv.ID, inv.EmailAddress)
		}
	}

	// Re-verify
	vReq, _ := http.NewRequest("GET", "https://api.clerk.com/v1/users?limit=100", nil)
	vReq.Header.Set("Authorization", "Bearer "+secretKey)
	vResp, err := http.DefaultClient.Do(vReq)
	if err == nil {
		defer vResp.Body.Close()
		var rem []ClerkUser
		_ = json.NewDecoder(vResp.Body).Decode(&rem)
		return len(rem)
	}
	return 0
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

	fmt.Println("==================================================")
	fmt.Println("          EASYHR CLEANUP & INSPECTION            ")
	fmt.Println("==================================================")

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
		fmt.Println("\nExisting Database Organizations:")
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
		fmt.Println("\nExisting Database Users:")
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
		fmt.Println("\nExisting Database Employees:")
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

	// 4. Clerk inspection & cleanup
	clerkProdKey := os.Getenv("EASYHR_CLERK_SECRET_KEY")
	clerkDevKey := os.Getenv("EASYHR_CLERK_DEV_SECRET_KEY")

	clerkProdRemaining := inspectAndCleanClerk(clerkProdKey, "PRODUCTION", *dryRun)
	clerkDevRemaining := inspectAndCleanClerk(clerkDevKey, "DEVELOPMENT", *dryRun)

	if *dryRun {
		fmt.Println("\nInspection mode finished. No data was deleted.")
		return
	}

	fmt.Println("\n--------------------------------------------------")
	fmt.Println("Starting cascading truncation of database tables...")
	fmt.Println("--------------------------------------------------")

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

	// Delete any non-system tenant-specific roles
	tag, err := pool.Exec(ctx, "DELETE FROM roles WHERE organization_id IS NOT NULL")
	if err != nil {
		fmt.Printf("Notice on tenant roles cleanup: %v\n", err)
	} else {
		fmt.Printf("Deleted %d tenant-specific roles\n", tag.RowsAffected())
	}

	// Database Verification
	var orgCount, userCount, empCount, workspaceCount int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM organizations").Scan(&orgCount)
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM users").Scan(&userCount)
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM employees").Scan(&empCount)
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM workspaces").Scan(&workspaceCount)

	// Verify system lookup tables still exist
	var permCount, sysRoleCount int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM permissions").Scan(&permCount)
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM roles WHERE is_system = true").Scan(&sysRoleCount)

	fmt.Println("\n==================================================")
	fmt.Println("             CLEANUP VERIFICATION REPORT         ")
	fmt.Println("==================================================")
	fmt.Printf("  Neon DB Organizations:        %d\n", orgCount)
	fmt.Printf("  Neon DB Workspaces:           %d\n", workspaceCount)
	fmt.Printf("  Neon DB Users:                %d\n", userCount)
	fmt.Printf("  Neon DB Employees:            %d\n", empCount)
	fmt.Printf("  Clerk Production Users:       %d\n", clerkProdRemaining)
	fmt.Printf("  Clerk Development Users:      %d\n", clerkDevRemaining)
	fmt.Printf("  System Permissions Preserved: %d\n", permCount)
	fmt.Printf("  System Roles Preserved:       %d\n", sysRoleCount)
	fmt.Println("==================================================")
}
