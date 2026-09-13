INSERT INTO permissions (key, description) VALUES
    ('workflows:manage', 'Manage workflow definitions')
ON CONFLICT (key) DO NOTHING;

CREATE TABLE workflow_definitions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 255),
    trigger_type TEXT NOT NULL CHECK (char_length(trigger_type) BETWEEN 1 AND 255),
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX workflows_org_idx ON workflow_definitions (organization_id);

CREATE TABLE workflow_steps (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_definition_id UUID NOT NULL REFERENCES workflow_definitions(id) ON DELETE CASCADE,
    step_order INTEGER NOT NULL CHECK (step_order >= 1),
    assignee_type TEXT NOT NULL CHECK (assignee_type IN ('manager', 'department_head', 'specific_user')),
    assignee_value UUID REFERENCES users(id) ON DELETE RESTRICT,
    action_type TEXT NOT NULL DEFAULT 'approval' CHECK (action_type IN ('approval', 'notification')),
    UNIQUE (workflow_definition_id, step_order)
);

CREATE TABLE workflow_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    workflow_definition_id UUID NOT NULL REFERENCES workflow_definitions(id) ON DELETE RESTRICT,
    resource_type TEXT NOT NULL,
    resource_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'in_progress' CHECK (status IN ('in_progress', 'approved', 'rejected', 'cancelled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    initiated_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT
);

CREATE TABLE workflow_tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    step_id UUID NOT NULL REFERENCES workflow_steps(id) ON DELETE RESTRICT,
    assignee_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed', 'skipped')),
    decision TEXT CHECK (decision IN ('approved', 'rejected')),
    comments TEXT,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX workflow_tasks_assignee_idx ON workflow_tasks (assignee_user_id, status);
