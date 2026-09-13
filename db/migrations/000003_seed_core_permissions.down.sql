DELETE FROM permissions
WHERE key IN (
    'organization:read', 'organization:manage', 'memberships:manage',
    'employees:read', 'employees:manage', 'documents:manage'
);
