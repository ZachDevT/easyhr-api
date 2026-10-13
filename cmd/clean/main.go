package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
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

	fmt.Println("🧹 Cleaning up organizations, workspaces, and users...")

	tables := []string{
		"candidate_email_messages",
		"candidate_email_threads",
		"job_applicants",
		"job_postings",
		"leave_requests",
		"leave_policies",
		"leave_types",
		"attendance_records",
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
		"workspaces",
		"organizations",
		"users",
	}

	for _, table := range tables {
		_, err := pool.Exec(ctx, fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table))
		if err != nil {
			fmt.Printf("⚠️ Note on %s: %v\n", table, err)
		} else {
			fmt.Printf("✅ Truncated %s\n", table)
		}
	}

	// Verify
	var orgCount, userCount int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM organizations").Scan(&orgCount)
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM users").Scan(&userCount)

	fmt.Printf("\n✨ Clean complete! Remaining organizations: %d, users: %d\n", orgCount, userCount)
}
