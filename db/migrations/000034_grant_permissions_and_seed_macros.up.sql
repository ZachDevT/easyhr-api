-- 000034_grant_permissions_and_seed_macros.up.sql

-- 1. Ensure all system roles have appropriate permissions
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.key
FROM roles r
CROSS JOIN permissions p
WHERE r.key = 'company_owner'
ON CONFLICT DO NOTHING;

-- Also grant leave and hiring permissions to any role named HR or Admin
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.key
FROM roles r
CROSS JOIN permissions p
WHERE (r.key LIKE '%admin%' OR r.key LIKE '%owner%' OR r.display_name ILIKE '%admin%' OR r.display_name ILIKE '%owner%')
  AND p.key IN (
    'leave:manage_policies', 'leave:approve_hr', 'leave:approve_manager', 'leave:request_self', 'leave:request_any',
    'hiring:manage', 'hiring:read', 'organization:manage', 'organization:read'
  )
ON CONFLICT DO NOTHING;

-- 2. Seed default email macros for existing organizations
INSERT INTO email_macros (organization_id, title, shortcut, category, subject, body_html)
SELECT 
    o.id,
    'Interview Invitation',
    'interview',
    'hiring',
    'Interview Invitation: {{role}} at {{company}}',
    '<p>Hi {{candidate_name}},</p><p>Thank you for applying for the <strong>{{role}}</strong> position at {{company}}.</p><p>We were very impressed by your qualifications and would love to invite you for an interview. Please let us know which of the following dates and times work best for you next week.</p><p>Looking forward to speaking with you!</p><p>Best regards,<br/><strong>The Hiring Team</strong></p>'
FROM organizations o
WHERE NOT EXISTS (
    SELECT 1 FROM email_macros m WHERE m.organization_id = o.id AND m.shortcut = 'interview'
);

INSERT INTO email_macros (organization_id, title, shortcut, category, subject, body_html)
SELECT 
    o.id,
    'Polite Rejection',
    'rejection',
    'hiring',
    'Update on your application for {{role}} at {{company}}',
    '<p>Dear {{candidate_name}},</p><p>Thank you for taking the time to apply for the <strong>{{role}}</strong> position at {{company}} and sharing your experience with us.</p><p>While your background is commendable, we have decided to move forward with other candidates whose experience more closely matches the specific requirements for this role at this time.</p><p>We will keep your profile in our talent pool for future openings that match your skills. We wish you every success in your job search!</p><p>Warm regards,<br/><strong>The Talent Acquisition Team</strong></p>'
FROM organizations o
WHERE NOT EXISTS (
    SELECT 1 FROM email_macros m WHERE m.organization_id = o.id AND m.shortcut = 'rejection'
);

INSERT INTO email_macros (organization_id, title, shortcut, category, subject, body_html)
SELECT 
    o.id,
    'Phone Screen Follow-up',
    'phone-screen',
    'hiring',
    'Quick phone screen regarding {{role}}',
    '<p>Hi {{candidate_name}},</p><p>Thanks for your interest in joining {{company}}! We would like to arrange a quick 15-20 minute introductory call to discuss the <strong>{{role}}</strong> role, tell you more about our team, and answer any questions you have.</p><p>Please share your phone number and a couple of convenient times for a call.</p><p>Best,<br/><strong>HR Operations</strong></p>'
FROM organizations o
WHERE NOT EXISTS (
    SELECT 1 FROM email_macros m WHERE m.organization_id = o.id AND m.shortcut = 'phone-screen'
);
