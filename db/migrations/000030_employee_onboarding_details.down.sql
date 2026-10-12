ALTER TABLE employee_personal_profiles
DROP COLUMN IF EXISTS emergency_contact_name,
DROP COLUMN IF EXISTS emergency_contact_relationship,
DROP COLUMN IF EXISTS emergency_contact_phone,
DROP COLUMN IF EXISTS payment_method,
DROP COLUMN IF EXISTS bank_name,
DROP COLUMN IF EXISTS bank_routing_number,
DROP COLUMN IF EXISTS bank_account_number,
DROP COLUMN IF EXISTS handbook_signed_at;
