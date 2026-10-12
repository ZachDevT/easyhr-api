ALTER TABLE job_applicants
DROP COLUMN IF EXISTS current_location,
DROP COLUMN IF EXISTS expected_salary_gross,
DROP COLUMN IF EXISTS currency,
DROP COLUMN IF EXISTS notice_period,
DROP COLUMN IF EXISTS linkedin_url,
DROP COLUMN IF EXISTS portfolio_url;
