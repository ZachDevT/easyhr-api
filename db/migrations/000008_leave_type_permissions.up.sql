INSERT INTO permissions (key, description) VALUES
    ('leave:manage_policies', 'Create and manage company leave types and leave policies')
ON CONFLICT (key) DO NOTHING;
