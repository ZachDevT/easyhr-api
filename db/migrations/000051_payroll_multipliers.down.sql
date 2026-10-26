ALTER TABLE payroll_configurations
  DROP COLUMN IF EXISTS overtime_multiplier,
  DROP COLUMN IF EXISTS holiday_multiplier;
