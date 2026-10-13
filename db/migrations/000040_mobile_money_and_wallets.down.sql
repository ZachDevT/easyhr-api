ALTER TABLE employee_personal_profiles
  DROP COLUMN IF EXISTS mobile_money_provider,
  DROP COLUMN IF EXISTS mobile_money_phone,
  DROP COLUMN IF EXISTS wallet_email;
