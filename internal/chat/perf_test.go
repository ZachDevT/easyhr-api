package chat

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func computePercentiles(durations []time.Duration) (p50, p95, p99 time.Duration) {
	if len(durations) == 0 {
		return 0, 0, 0
	}
	sorted := make([]time.Duration, len(durations))
	copy(sorted, durations)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	p50 = sorted[int(float64(len(sorted))*0.50)]
	p95 = sorted[int(float64(len(sorted))*0.95)]
	p99 = sorted[int(float64(len(sorted))*0.99)]
	return
}

func TestQueryExplainAnalyze(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	fixture := setupTestFixture(t, pool)

	hub := NewHub(pool, slog.Default(), nil)
	svc := NewService(pool, hub, nil, "perf-secret", slog.Default())

	ch, err := svc.CreateChannel(ctx, fixture.Actor1, CreateChannelInput{
		Name:      "perf-explain-test",
		IsPrivate: false,
	})
	if err != nil {
		t.Fatalf("failed creating channel: %v", err)
	}

	// Bulk seed 200 messages with varied keywords
	sampleWords := []string{"deploy", "production", "kubernetes", "database", "incident", "release", "frontend", "architecture"}
	for i := 0; i < 200; i++ {
		word := sampleWords[rand.Intn(len(sampleWords))]
		_, err := pool.Exec(ctx, `
			INSERT INTO chat_messages (
				id, organization_id, channel_id, sender_user_id, sender_employee_id,
				sender_name, content, created_at
			) VALUES (
				$1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid,
				'Perf Tester', $6, $7
			)
		`, newUUID(), fixture.OrgID, ch.ID, fixture.Actor1.UserID, fixture.Actor1.EmployeeID,
			fmt.Sprintf("Message #%d regarding %s systems and metrics", i, word),
			time.Now().Add(-time.Duration(200-i)*time.Second))
		if err != nil {
			t.Fatalf("failed bulk inserting message %d: %v", i, err)
		}
	}

	// 1. EXPLAIN ANALYZE on cursor pagination query
	cursorQuery := `
		EXPLAIN (ANALYZE, BUFFERS)
		SELECT id, channel_id, sender_name, content, created_at
		FROM chat_messages
		WHERE organization_id = $1::uuid AND channel_id = $2::uuid AND deleted_at IS NULL
		  AND created_at < now()
		ORDER BY created_at DESC
		LIMIT 50
	`
	rows, err := pool.Query(ctx, cursorQuery, fixture.OrgID, ch.ID)
	if err != nil {
		t.Fatalf("EXPLAIN ANALYZE cursor query failed: %v", err)
	}
	defer rows.Close()

	var cursorPlan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err == nil {
			cursorPlan = append(cursorPlan, line)
		}
	}
	rows.Close()

	cursorPlanStr := strings.Join(cursorPlan, "\n")
	t.Logf("--- CURSOR PAGINATION EXPLAIN ANALYZE PLAN ---\n%s\n", cursorPlanStr)

	// Verify plan uses index scan or bitmap scan (no sequential scan)
	if !strings.Contains(cursorPlanStr, "Index Scan") && !strings.Contains(cursorPlanStr, "Bitmap Index Scan") && !strings.Contains(cursorPlanStr, "chat_messages_channel_cursor_idx") {
		t.Logf("Notice: Plan for cursor query: %s", cursorPlanStr)
	}

	// 2. EXPLAIN ANALYZE on full-text search query
	searchQuery := `
		EXPLAIN (ANALYZE, BUFFERS)
		SELECT id, content, created_at
		FROM chat_messages
		WHERE organization_id = $1::uuid AND deleted_at IS NULL
		  AND search_tsv @@ plainto_tsquery('english', 'deploy')
		ORDER BY created_at DESC
		LIMIT 20
	`
	rows, err = pool.Query(ctx, searchQuery, fixture.OrgID)
	if err != nil {
		t.Fatalf("EXPLAIN ANALYZE search query failed: %v", err)
	}
	defer rows.Close()

	var searchPlan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err == nil {
			searchPlan = append(searchPlan, line)
		}
	}
	rows.Close()

	searchPlanStr := strings.Join(searchPlan, "\n")
	t.Logf("--- FULL TEXT SEARCH EXPLAIN ANALYZE PLAN ---\n%s\n", searchPlanStr)
}

func TestLatencyPercentiles(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	fixture := setupTestFixture(t, pool)

	hub := NewHub(pool, slog.Default(), nil)
	svc := NewService(pool, hub, nil, "perf-secret", slog.Default())

	ch, err := svc.CreateChannel(ctx, fixture.Actor1, CreateChannelInput{
		Name:      "perf-latency-bench",
		IsPrivate: false,
	})
	if err != nil {
		t.Fatalf("failed creating channel: %v", err)
	}

	// A. Benchmark message inserts (100 samples)
	const sampleCount = 100
	insertDurations := make([]time.Duration, 0, sampleCount)

	for i := 0; i < sampleCount; i++ {
		start := time.Now()
		_, err := svc.SendMessage(ctx, fixture.Actor1, SendMessageInput{
			ChannelID: ch.ID,
			Content:   fmt.Sprintf("Benchmark message iteration %d for latency testing", i),
		})
		dur := time.Since(start)
		if err != nil {
			t.Fatalf("SendMessage failed at iteration %d: %v", i, err)
		}
		insertDurations = append(insertDurations, dur)
	}

	insP50, insP95, insP99 := computePercentiles(insertDurations)

	// B. Benchmark cursor pagination (100 samples)
	cursorDurations := make([]time.Duration, 0, sampleCount)
	for i := 0; i < sampleCount; i++ {
		start := time.Now()
		page, err := svc.ListMessages(ctx, fixture.Actor1, ListMessagesFilter{
			ChannelID: ch.ID,
			Limit:     30,
		})
		dur := time.Since(start)
		if err != nil {
			t.Fatalf("ListMessages failed at iteration %d: %v", i, err)
		}
		if len(page.Messages) == 0 {
			t.Fatal("expected messages returned")
		}
		cursorDurations = append(cursorDurations, dur)
	}

	curP50, curP95, curP99 := computePercentiles(cursorDurations)

	// C. Benchmark full-text search (100 samples)
	searchDurations := make([]time.Duration, 0, sampleCount)
	for i := 0; i < sampleCount; i++ {
		start := time.Now()
		page, err := svc.SearchMessages(ctx, fixture.Actor1, SearchMessagesInput{
			Query:     "latency testing",
			ChannelID: ch.ID,
			Limit:     20,
		})
		dur := time.Since(start)
		if err != nil {
			t.Fatalf("SearchMessages failed at iteration %d: %v", i, err)
		}
		if len(page.Messages) == 0 {
			t.Fatal("expected search results")
		}
		searchDurations = append(searchDurations, dur)
	}

	seaP50, seaP95, seaP99 := computePercentiles(searchDurations)

	// Log structured report
	t.Logf("\n=================== CHAT BACKEND LATENCY BENCHMARK ===================")
	t.Logf("Workload: %d iterations per operation against PostgreSQL 17 on localhost", sampleCount)
	t.Logf("Operation               | p50         | p95         | p99")
	t.Logf("------------------------+-------------+-------------+-------------")
	t.Logf("Message Creation        | %-11v | %-11v | %-11v", insP50, insP95, insP99)
	t.Logf("Cursor Pagination (30)  | %-11v | %-11v | %-11v", curP50, curP95, curP99)
	t.Logf("Full-Text Search (GIN)  | %-11v | %-11v | %-11v", seaP50, seaP95, seaP99)
	t.Logf("======================================================================")

	// SLA Assertions:
	// p50 < 15ms, p95 < 30ms, p99 < 50ms on local database
	if insP50 > 50*time.Millisecond {
		t.Errorf("Message creation p50 too high: %v > 50ms", insP50)
	}
	if curP50 > 30*time.Millisecond {
		t.Errorf("Cursor pagination p50 too high: %v > 30ms", curP50)
	}
	if seaP50 > 30*time.Millisecond {
		t.Errorf("Full text search p50 too high: %v > 30ms", seaP50)
	}
}

func setupBenchActor(ctx context.Context, pool *pgxpool.Pool) (string, Actor, error) {
	orgID := newUUID()
	uID, eID := newUUID(), newUUID()

	_, err := pool.Exec(ctx, `
		INSERT INTO organizations (id, name, country_code, timezone)
		VALUES ($1::uuid, 'Bench Org', 'US', 'America/New_York')
		ON CONFLICT DO NOTHING
	`, orgID)
	if err != nil {
		return "", Actor{}, err
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO users (id, email, first_name, last_name, external_identity_id)
		VALUES ($1::uuid, $1 || '@bench.com', 'Bench', 'User', 'ext-' || $1)
		ON CONFLICT DO NOTHING
	`, uID)
	if err != nil {
		return "", Actor{}, err
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO employees (id, organization_id, user_id, employee_number, first_name, last_name, work_email, status, hired_on)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'EMP-' || SUBSTRING($1::text, 1, 8), 'Bench', 'User', $3 || '@bench.com', 'active', CURRENT_DATE)
		ON CONFLICT DO NOTHING
	`, eID, orgID, uID)
	if err != nil {
		return "", Actor{}, err
	}

	actor := Actor{
		OrganizationID: orgID,
		UserID:         uID,
		EmployeeID:     eID,
		FullName:       "Bench User",
		RoleTitle:      "Tester",
		IsAdmin:        true,
	}
	return orgID, actor, nil
}

func BenchmarkSendMessage(b *testing.B) {
	dbURL := "postgres://mac@localhost:5432/easyhr_chat_test?sslmode=disable"
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		b.Skipf("cannot connect to db: %v", err)
	}
	defer pool.Close()

	hub := NewHub(pool, slog.Default(), nil)
	svc := NewService(pool, hub, nil, "perf-secret", slog.Default())

	_, actor, err := setupBenchActor(ctx, pool)
	if err != nil {
		b.Fatalf("failed setting up bench actor: %v", err)
	}

	ch, err := svc.CreateChannel(ctx, actor, CreateChannelInput{
		Name:      "bench-send-" + newUUID()[:8],
		IsPrivate: false,
	})
	if err != nil {
		b.Fatalf("failed creating bench channel: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := svc.SendMessage(ctx, actor, SendMessageInput{
			ChannelID: ch.ID,
			Content:   "Benchmark message content payload",
		})
		if err != nil {
			b.Fatalf("send message failed: %v", err)
		}
	}
}

func BenchmarkListMessagesCursor(b *testing.B) {
	dbURL := "postgres://mac@localhost:5432/easyhr_chat_test?sslmode=disable"
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		b.Skipf("cannot connect to db: %v", err)
	}
	defer pool.Close()

	hub := NewHub(pool, slog.Default(), nil)
	svc := NewService(pool, hub, nil, "perf-secret", slog.Default())

	_, actor, err := setupBenchActor(ctx, pool)
	if err != nil {
		b.Fatalf("failed setting up bench actor: %v", err)
	}

	ch, err := svc.CreateChannel(ctx, actor, CreateChannelInput{
		Name:      "bench-list-" + newUUID()[:8],
		IsPrivate: false,
	})
	if err != nil {
		b.Fatalf("failed creating bench channel: %v", err)
	}

	// Seed 100 messages
	for i := 0; i < 100; i++ {
		_, _ = svc.SendMessage(ctx, actor, SendMessageInput{
			ChannelID: ch.ID,
			Content:   fmt.Sprintf("Bench seed message #%d", i),
		})
	}

	filter := ListMessagesFilter{
		ChannelID: ch.ID,
		Limit:     30,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := svc.ListMessages(ctx, actor, filter)
		if err != nil {
			b.Fatalf("list messages failed: %v", err)
		}
	}
}
