DROP TABLE IF EXISTS payroll_tax_rules;
DROP TABLE IF EXISTS public_rate_limits;
DROP TABLE IF EXISTS timesheet_submissions;
DELETE FROM role_permissions WHERE permission_key IN ('timesheet:approve','timesheet:submit_self');
DELETE FROM permissions WHERE key IN ('timesheet:approve','timesheet:submit_self');
DROP INDEX IF EXISTS signature_recipients_public_token_idx;
ALTER TABLE signature_recipients DROP COLUMN IF EXISTS public_token_hash;
ALTER TABLE signature_requests ALTER COLUMN document_id SET NOT NULL;
