ALTER TABLE employment_assignments
    ADD COLUMN IF NOT EXISTS position_id UUID REFERENCES department_positions(id) ON DELETE SET NULL;

ALTER TABLE promotion_events
    ADD COLUMN IF NOT EXISTS new_position_id UUID REFERENCES department_positions(id) ON DELETE SET NULL;
