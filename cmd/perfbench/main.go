package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type BenchmarkResult struct {
	Name         string
	TotalReqs    int
	Concurrency  int
	SuccessReqs  int
	ErrorReqs    int
	ErrorRate    float64
	TotalTime    time.Duration
	RPS          float64
	MinLatency   time.Duration
	MeanLatency  time.Duration
	P50Latency   time.Duration
	P90Latency   time.Duration
	P95Latency   time.Duration
	P99Latency   time.Duration
	MaxLatency   time.Duration
}

func main() {
	mode := flag.String("mode", "all", "benchmark mode: http, db, or all")
	concurrency := flag.Int("concurrency", 1000, "number of concurrent requests")
	totalReqs := flag.Int("requests", 1000, "total number of requests to execute")
	flag.Parse()

	fmt.Println("================================================================================")
	fmt.Printf(" EasyHR Performance Benchmark Runner: %d Requests @ Concurrency %d\n", *totalReqs, *concurrency)
	fmt.Println("================================================================================")

	dbURL := os.Getenv("EASYHR_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		log.Fatal("EASYHR_DATABASE_URL or DATABASE_URL environment variable is required")
	}

	ctx := context.Background()
	poolConfig, err := pgxpool.ParseConfig(dbURL)
	var pool *pgxpool.Pool
	if err == nil {
		poolConfig.MaxConns = 80
		poolConfig.MinConns = 15
		pool, err = pgxpool.NewWithConfig(ctx, poolConfig)
	}
	if err != nil {
		fmt.Printf("Warning: Failed to connect directly to DB: %v\n", err)
	} else {
		defer pool.Close()
	}

	var results []BenchmarkResult

	// 1. Direct Database Query Benchmarks (Simulate 1,000 concurrent database queries)
	if (*mode == "all" || *mode == "db") && pool != nil {
		fmt.Println("\n--- [1] DATABASE ENGINE CONCURRENT QUERY BENCHMARKS ---")

		// Query A: Leave Balances Aggregation
		qLeaveBalances := `
			WITH eligible_leave_types AS (
				SELECT DISTINCT policy.leave_type_id, policy.accrual_frequency, policy.accrual_minutes
				FROM leave_policy_assignments assignment
				JOIN leave_policies policy ON policy.id = assignment.leave_policy_id
				WHERE policy.organization_id = (SELECT id FROM organizations LIMIT 1)
				  AND assignment.employee_id = (SELECT id FROM employees LIMIT 1)
				  AND policy.status = 'active'
				  AND assignment.effective_from <= CURRENT_DATE
				  AND (assignment.effective_to IS NULL OR assignment.effective_to >= CURRENT_DATE)
			)
			SELECT eligible.leave_type_id::text, t.code, t.name,
				COALESCE(SUM(CASE WHEN ledger.entry_type IN ('accrual', 'adjustment') THEN ledger.minutes WHEN ledger.entry_type = 'usage' THEN -ledger.minutes ELSE 0 END), 0) AS available_mins
			FROM eligible_leave_types eligible
			JOIN leave_types t ON t.id = eligible.leave_type_id
			LEFT JOIN leave_ledger_entries ledger
			  ON ledger.organization_id = (SELECT id FROM organizations LIMIT 1)
			 AND ledger.employee_id = (SELECT id FROM employees LIMIT 1)
			 AND ledger.leave_type_id = eligible.leave_type_id
			GROUP BY eligible.leave_type_id, t.code, t.name;`
		results = append(results, runDBBenchmark(ctx, pool, "DB: Leave Balances Aggregation", qLeaveBalances, *totalReqs, *concurrency))

		// Query B: Leave Requests Listing
		qLeaveRequests := `
			SELECT request.id::text, request.employee_id::text, request.start_date, request.end_date, request.status
			FROM leave_requests request
			WHERE request.organization_id = (SELECT id FROM organizations LIMIT 1)
			ORDER BY request.submitted_at DESC, request.id DESC
			LIMIT 50;`
		results = append(results, runDBBenchmark(ctx, pool, "DB: Leave Requests Listing (Recent)", qLeaveRequests, *totalReqs, *concurrency))

		// Query C: Attendance Live Punches
		qAttendance := `
			SELECT e.id::text, e.employee_number, MIN(p_in.occurred_at), MAX(p_out.occurred_at)
			FROM employees e
			LEFT JOIN attendance_punches p_in ON p_in.employee_id = e.id AND p_in.kind = 'clock_in' AND p_in.occurred_at >= CURRENT_DATE
			LEFT JOIN attendance_punches p_out ON p_out.employee_id = e.id AND p_out.kind = 'clock_out' AND p_out.occurred_at >= CURRENT_DATE
			WHERE e.organization_id = (SELECT id FROM organizations LIMIT 1)
			GROUP BY e.id, e.employee_number
			LIMIT 50;`
		results = append(results, runDBBenchmark(ctx, pool, "DB: Attendance Punches Summary", qAttendance, *totalReqs, *concurrency))

		// Query D: Employee Directory
		qEmployees := `
			SELECT e.id::text, e.first_name, e.last_name, e.work_email, e.status, COALESCE(d.name, 'General')
			FROM employees e
			LEFT JOIN employment_assignments a ON a.employee_id = e.id AND a.effective_to IS NULL
			LEFT JOIN departments d ON d.id = a.department_id
			WHERE e.organization_id = (SELECT id FROM organizations LIMIT 1)
			LIMIT 100;`
		results = append(results, runDBBenchmark(ctx, pool, "DB: Employee Directory + Depts", qEmployees, *totalReqs, *concurrency))
	}

	// 2. HTTP Endpoint Benchmarks
	if *mode == "all" || *mode == "http" {
		fmt.Println("\n--- [2] HTTP API ENDPOINT CONCURRENT BENCHMARKS ---")
		endpoints := []struct {
			name string
			url  string
		}{
			{"API: Public Jobs Listing", "http://localhost:8080/api/v1/public/jobs"},
			{"API: Public Company Info", "http://localhost:8080/api/v1/public/company?slug=gitop"},
			{"API: System Health Endpoint", "http://localhost:8080/healthz"},
		}

		for _, ep := range endpoints {
			results = append(results, runHTTPBenchmark(ep.name, ep.url, *totalReqs, *concurrency))
		}
	}

	// Print Summary Table
	printSummaryTable(results)
}

func runDBBenchmark(ctx context.Context, pool *pgxpool.Pool, name, query string, totalReqs, concurrency int) BenchmarkResult {
	fmt.Printf("\nExecuting %s (%d queries, concurrency %d)...\n", name, totalReqs, concurrency)

	latencies := make([]time.Duration, totalReqs)
	var successCount, errorCount int64

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	startTime := time.Now()

	for i := 0; i < totalReqs; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			t0 := time.Now()
			rows, err := pool.Query(ctx, query)
			duration := time.Since(t0)
			latencies[idx] = duration

			if err != nil {
				atomic.AddInt64(&errorCount, 1)
			} else {
				rows.Close()
				atomic.AddInt64(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()
	totalDuration := time.Since(startTime)

	return calculateResult(name, totalReqs, concurrency, int(successCount), int(errorCount), totalDuration, latencies)
}

func runHTTPBenchmark(name, url string, totalReqs, concurrency int) BenchmarkResult {
	fmt.Printf("\nExecuting %s (%d requests, concurrency %d)...\n", name, totalReqs, concurrency)

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        concurrency,
			MaxIdleConnsPerHost: concurrency,
			MaxConnsPerHost:     concurrency,
		},
	}

	latencies := make([]time.Duration, totalReqs)
	var successCount, errorCount int64

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	startTime := time.Now()

	for i := 0; i < totalReqs; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			t0 := time.Now()
			resp, err := client.Get(url)
			duration := time.Since(t0)
			latencies[idx] = duration

			if err != nil || (resp != nil && resp.StatusCode >= 400 && resp.StatusCode != 404) {
				atomic.AddInt64(&errorCount, 1)
			} else {
				if resp != nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
				atomic.AddInt64(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()
	totalDuration := time.Since(startTime)

	return calculateResult(name, totalReqs, concurrency, int(successCount), int(errorCount), totalDuration, latencies)
}

func calculateResult(name string, totalReqs, concurrency, successCount, errorCount int, totalDuration time.Duration, latencies []time.Duration) BenchmarkResult {
	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	var sum time.Duration
	for _, l := range latencies {
		sum += l
	}

	mean := time.Duration(0)
	if len(latencies) > 0 {
		mean = sum / time.Duration(len(latencies))
	}

	p50 := percentile(latencies, 0.50)
	p90 := percentile(latencies, 0.90)
	p95 := percentile(latencies, 0.95)
	p99 := percentile(latencies, 0.99)
	min := latencies[0]
	max := latencies[len(latencies)-1]

	rps := float64(totalReqs) / totalDuration.Seconds()
	errRate := (float64(errorCount) / float64(totalReqs)) * 100.0

	res := BenchmarkResult{
		Name:         name,
		TotalReqs:    totalReqs,
		Concurrency:  concurrency,
		SuccessReqs:  successCount,
		ErrorReqs:    errorCount,
		ErrorRate:    errRate,
		TotalTime:    totalDuration,
		RPS:          rps,
		MinLatency:   min,
		MeanLatency:  mean,
		P50Latency:   p50,
		P90Latency:   p90,
		P95Latency:   p95,
		P99Latency:   p99,
		MaxLatency:   max,
	}

	fmt.Printf("  -> Success: %d/%d (Errors: %.1f%%)\n", successCount, totalReqs, errRate)
	fmt.Printf("  -> Throughput: %.1f req/s (Elapsed: %v)\n", rps, totalDuration.Round(time.Millisecond))
	fmt.Printf("  -> Mean: %v | p50: %v | p95: %v | p99: %v | Max: %v\n",
		mean.Round(time.Microsecond),
		p50.Round(time.Microsecond),
		p95.Round(time.Microsecond),
		p99.Round(time.Microsecond),
		max.Round(time.Microsecond),
	)

	return res
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func printSummaryTable(results []BenchmarkResult) {
	fmt.Println("\n==========================================================================================================")
	fmt.Printf("%-36s | %-8s | %-9s | %-9s | %-9s | %-9s | %-8s\n", "BENCHMARK TARGET", "REQS", "MEAN", "P50", "P95", "P99", "ERR %")
	fmt.Println("-------------------------------------+----------+-----------+-----------+-----------+-----------+---------")
	for _, r := range results {
		fmt.Printf("%-36s | %-8d | %-9s | %-9s | %-9s | %-9s | %-6.1f%%\n",
			r.Name,
			r.TotalReqs,
			fmtDuration(r.MeanLatency),
			fmtDuration(r.P50Latency),
			fmtDuration(r.P95Latency),
			fmtDuration(r.P99Latency),
			r.ErrorRate,
		)
	}
	fmt.Println("==========================================================================================================")
}

func fmtDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000.0)
	}
	return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000.0)
}
