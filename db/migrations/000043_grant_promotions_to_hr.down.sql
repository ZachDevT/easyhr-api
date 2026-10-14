DELETE FROM role_permissions
WHERE permission_key IN ('promotions:read', 'promotions:manage')
  AND role_id IN (
      SELECT r.id FROM roles r
      WHERE r.key = 'company_owner'
         OR r.key ILIKE '%admin%'
         OR r.key ILIKE '%hr%'
         OR r.display_name ILIKE '%owner%'
         OR r.display_name ILIKE '%admin%'
         OR r.display_name ILIKE '%human resource%'
         OR r.display_name ILIKE '%hr%'
  );
