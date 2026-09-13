DROP TABLE IF EXISTS onboarding_tasks;
DROP TABLE IF EXISTS onboarding_instances;
DROP TABLE IF EXISTS onboarding_template_tasks;
DROP TABLE IF EXISTS onboarding_templates;
DELETE FROM permissions WHERE key IN ('onboarding:manage_templates', 'onboarding:initiate', 'onboarding:complete_tasks');
