INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.permission_key
FROM roles r
CROSS JOIN (VALUES ('promotions:read'), ('promotions:manage')) AS p(permission_key)
WHERE r.key = 'company_owner'
   OR r.key ILIKE '%admin%'
   OR r.key ILIKE '%hr%'
   OR r.display_name ILIKE '%owner%'
   OR r.display_name ILIKE '%admin%'
   OR r.display_name ILIKE '%human resource%'
   OR r.display_name ILIKE '%hr%'
ON CONFLICT DO NOTHING;
