INSERT INTO permissions (key, description) VALUES
    ('onboarding:manage_templates', 'Create and manage onboarding templates'),
    ('onboarding:initiate', 'Initiate onboarding for employees'),
    ('onboarding:complete_tasks', 'Complete assigned onboarding tasks')
ON CONFLICT (key) DO NOTHING;

CREATE TABLE onboarding_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 160),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'archived')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    UNIQUE (organization_id, name, version)
);

CREATE TABLE onboarding_template_tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id UUID NOT NULL REFERENCES onboarding_templates(id) ON DELETE RESTRICT,
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    description TEXT,
    category TEXT NOT NULL CHECK (category IN ('it_setup', 'hr', 'facilities', 'training', 'documents', 'other')),
    assignee_type TEXT NOT NULL CHECK (assignee_type IN ('employee', 'manager', 'hr_team', 'it_team', 'facilities_team')),
    due_offset_days INTEGER NOT NULL DEFAULT 0 CHECK (due_offset_days BETWEEN -90 AND 365),
    is_required BOOLEAN NOT NULL DEFAULT true,
    sort_order INTEGER NOT NULL CHECK (sort_order >= 0),
    UNIQUE (template_id, sort_order)
);

CREATE TABLE onboarding_instances (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE RESTRICT,
    template_id UUID NOT NULL REFERENCES onboarding_templates(id) ON DELETE RESTRICT,
    template_version INTEGER NOT NULL,
    start_date DATE NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('not_started', 'on_track', 'at_risk', 'completed', 'cancelled')),
    initiated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    initiated_by_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    completed_at TIMESTAMPTZ,
    UNIQUE (employee_id, template_id, start_date)
);

CREATE INDEX onboarding_instances_org_status_idx ON onboarding_instances (organization_id, status, start_date);

CREATE TABLE onboarding_tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    onboarding_instance_id UUID NOT NULL REFERENCES onboarding_instances(id) ON DELETE RESTRICT,
    source_template_task_id UUID REFERENCES onboarding_template_tasks(id) ON DELETE RESTRICT,
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    description TEXT,
    category TEXT NOT NULL CHECK (category IN ('it_setup', 'hr', 'facilities', 'training', 'documents', 'other')),
    assignee_type TEXT NOT NULL CHECK (assignee_type IN ('employee', 'manager', 'hr_team', 'it_team', 'facilities_team')),
    assignee_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    due_date DATE,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'completed', 'skipped')),
    completed_at TIMESTAMPTZ,
    completed_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    completion_note TEXT,
    is_required BOOLEAN NOT NULL DEFAULT true,
    sort_order INTEGER NOT NULL CHECK (sort_order >= 0),
    UNIQUE (onboarding_instance_id, sort_order)
);

CREATE INDEX onboarding_tasks_assignee_idx ON onboarding_tasks (assignee_user_id, status, due_date) WHERE status = 'open';
