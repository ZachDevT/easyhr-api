CREATE TABLE compensation_records (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE RESTRICT,
    effective_date DATE NOT NULL,
    currency TEXT NOT NULL CHECK (char_length(currency) = 3),
    pay_frequency TEXT NOT NULL CHECK (pay_frequency IN ('monthly', 'weekly', 'biweekly')),
    base_amount_minor BIGINT NOT NULL CHECK (base_amount_minor >= 0),
    pay_type TEXT NOT NULL CHECK (pay_type IN ('salary', 'hourly')),
    reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX compensation_records_employee_date_idx ON compensation_records (employee_id, effective_date DESC);
