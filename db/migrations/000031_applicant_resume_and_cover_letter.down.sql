ALTER TABLE job_applicants
DROP COLUMN IF EXISTS resume_url,
DROP COLUMN IF EXISTS resume_filename,
DROP COLUMN IF EXISTS cover_letter;
