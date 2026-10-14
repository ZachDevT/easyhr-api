ALTER TABLE shift_schedules DROP COLUMN IF EXISTS is_active;
ALTER TABLE benefit_plans ADD CONSTRAINT benefit_plans_category_check CHECK (category IN ('health', 'retirement', 'transit', 'allowance', 'other'));
ALTER TABLE payroll_tax_rules DROP COLUMN IF EXISTS cadence;
ALTER TABLE payroll_tax_rules DROP COLUMN IF EXISTS applies_to_id;
ALTER TABLE payroll_tax_rules DROP COLUMN IF EXISTS applies_to;
ALTER TABLE payroll_tax_rules DROP COLUMN IF EXISTS amount_minor;
