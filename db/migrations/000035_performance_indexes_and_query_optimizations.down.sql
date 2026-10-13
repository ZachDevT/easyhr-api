-- 000035_performance_indexes_and_query_optimizations.down.sql

DROP INDEX IF EXISTS idx_leave_requests_org_submitted;
DROP INDEX IF EXISTS idx_leave_requests_org_emp;
DROP INDEX IF EXISTS idx_leave_ledger_composite;
DROP INDEX IF EXISTS idx_leave_ledger_emp_type;
DROP INDEX IF EXISTS idx_leave_policy_assignments_emp_eff;
DROP INDEX IF EXISTS idx_attendance_punches_org_emp;
DROP INDEX IF EXISTS idx_attendance_punches_emp_kind_occurred;
DROP INDEX IF EXISTS idx_job_applicants_posting_status;
DROP INDEX IF EXISTS idx_job_applicants_email;
DROP INDEX IF EXISTS idx_job_postings_org_status_created;
DROP INDEX IF EXISTS idx_candidate_threads_org_last_msg;
DROP INDEX IF EXISTS idx_candidate_messages_thread_created;
DROP INDEX IF EXISTS idx_employment_assignments_dept;
DROP INDEX IF EXISTS idx_department_positions_dept;
DROP INDEX IF EXISTS idx_documents_employee;
DROP INDEX IF EXISTS idx_payroll_runs_period;
