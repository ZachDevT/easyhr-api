ALTER TABLE payroll_tax_rules
    ADD COLUMN IF NOT EXISTS amount_minor BIGINT NOT NULL DEFAULT 0 CHECK (amount_minor >= 0),
    ADD COLUMN IF NOT EXISTS applies_to TEXT NOT NULL DEFAULT 'all_employees' CHECK (applies_to IN ('all_employees','department','position')),
    ADD COLUMN IF NOT EXISTS applies_to_id UUID,
    ADD COLUMN IF NOT EXISTS cadence TEXT NOT NULL DEFAULT 'monthly' CHECK (cadence IN ('monthly'));

ALTER TABLE benefit_plans DROP CONSTRAINT IF EXISTS benefit_plans_category_check;
ALTER TABLE shift_schedules ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT true;
