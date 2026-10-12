ALTER TABLE job_postings
DROP COLUMN IF EXISTS position_id,
DROP COLUMN IF EXISTS department_id;

DROP TABLE IF EXISTS department_positions;

ALTER TABLE departments
DROP COLUMN IF EXISTS head_employee_id,
DROP COLUMN IF EXISTS color,
DROP COLUMN IF EXISTS description;
