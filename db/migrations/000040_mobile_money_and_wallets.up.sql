ALTER TABLE employee_personal_profiles
  ADD COLUMN IF NOT EXISTS mobile_money_provider TEXT,
  ADD COLUMN IF NOT EXISTS mobile_money_phone TEXT,
  ADD COLUMN IF NOT EXISTS wallet_email TEXT;
