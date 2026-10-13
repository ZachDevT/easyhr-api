-- 000034_grant_permissions_and_seed_macros.down.sql
DELETE FROM email_macros WHERE shortcut IN ('interview', 'rejection', 'phone-screen');
