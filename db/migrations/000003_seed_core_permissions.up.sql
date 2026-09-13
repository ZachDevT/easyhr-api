INSERT INTO permissions (key, description) VALUES
    ('organization:read', 'View organization configuration'),
    ('organization:manage', 'Manage organization configuration and workspace setup'),
    ('memberships:manage', 'Invite, revoke and manage workspace members'),
    ('employees:read', 'View employee records in permitted scope'),
    ('employees:manage', 'Create and manage employee records'),
    ('documents:manage', 'Manage employee documents')
ON CONFLICT (key) DO NOTHING;
