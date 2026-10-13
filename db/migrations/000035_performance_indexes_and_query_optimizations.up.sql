-- 000035_performance_indexes_and_query_optimizations.up.sql

-- 1. Leave requests performance indexes
CREATE INDEX IF NOT EXISTS idx_leave_requests_org_submitted 
    ON leave_requests (organization_id, submitted_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_leave_requests_org_emp 
    ON leave_requests (organization_id, employee_id, submitted_at DESC);

-- 2. Leave ledger entries composite index for fast balance aggregation
CREATE INDEX IF NOT EXISTS idx_leave_ledger_composite 
    ON leave_ledger_entries (organization_id, employee_id, leave_type_id, entry_type);

CREATE INDEX IF NOT EXISTS idx_leave_ledger_emp_type 
    ON leave_ledger_entries (employee_id, leave_type_id, effective_on DESC);

-- 3. Leave policy assignments index for date filtering
CREATE INDEX IF NOT EXISTS idx_leave_policy_assignments_emp_eff 
    ON leave_policy_assignments (employee_id, effective_from, effective_to);

-- 4. Attendance punches indexes for fast lookups and live attendance aggregation
CREATE INDEX IF NOT EXISTS idx_attendance_punches_org_emp 
    ON attendance_punches (organization_id, employee_id, occurred_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_attendance_punches_emp_kind_occurred 
    ON attendance_punches (employee_id, kind, occurred_at);

-- 5. Hiring: job applicants & postings indexes
CREATE INDEX IF NOT EXISTS idx_job_applicants_posting_status 
    ON job_applicants (job_posting_id, status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_job_applicants_email 
    ON job_applicants (email);

CREATE INDEX IF NOT EXISTS idx_job_postings_org_status_created 
    ON job_postings (organization_id, status, created_at DESC);

-- 6. Messaging: threads & messages
CREATE INDEX IF NOT EXISTS idx_candidate_threads_org_last_msg 
    ON candidate_email_threads (organization_id, last_message_at DESC);

CREATE INDEX IF NOT EXISTS idx_candidate_messages_thread_created 
    ON candidate_email_messages (thread_id, created_at ASC);

-- 7. High-frequency foreign key indexes
CREATE INDEX IF NOT EXISTS idx_employment_assignments_dept 
    ON employment_assignments (department_id) WHERE effective_to IS NULL;

CREATE INDEX IF NOT EXISTS idx_department_positions_dept 
    ON department_positions (department_id);

CREATE INDEX IF NOT EXISTS idx_documents_employee 
    ON documents (employee_id);

CREATE INDEX IF NOT EXISTS idx_payroll_runs_period 
    ON payroll_runs (payroll_period_id);
