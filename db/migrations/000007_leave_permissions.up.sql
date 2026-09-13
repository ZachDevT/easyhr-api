INSERT INTO permissions (key, description) VALUES
    ('leave:request_self', 'Submit leave requests for own employee record'),
    ('leave:request_any', 'Submit leave requests on behalf of an employee'),
    ('leave:approve_manager', 'Approve leave requests for direct reports'),
    ('leave:approve_hr', 'Approve leave requests at the HR stage')
ON CONFLICT (key) DO NOTHING;
