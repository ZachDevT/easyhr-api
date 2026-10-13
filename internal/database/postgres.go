package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	poolConfig.MaxConns = 80
	poolConfig.MinConns = 15
	poolConfig.MaxConnLifetime = 30 * time.Minute
	poolConfig.MaxConnIdleTime = 5 * time.Minute
	poolConfig.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	if err := EnsureSchema(ctx, pool); err != nil {
		// Log error but don't prevent server start
		fmt.Printf("warning: schema ensure: %v\n", err)
	}
	return pool, nil
}

func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	schemaSQL := `
		INSERT INTO permissions (key, description) VALUES
			('attendance:manage_activities', 'Create and manage attendance punch activities'),
			('announcements:manage', 'Create and delete company announcements'),
			('celebrations:wish', 'Send celebration wishes to teammates')
		ON CONFLICT (key) DO NOTHING;

		CREATE TABLE IF NOT EXISTS attendance_activities (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			code TEXT NOT NULL,
			is_billable BOOLEAN NOT NULL DEFAULT true,
			is_default BOOLEAN NOT NULL DEFAULT false,
			color TEXT NOT NULL DEFAULT '#7c3aed',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE (organization_id, name)
		);
		CREATE INDEX IF NOT EXISTS attendance_activities_org_idx ON attendance_activities (organization_id);

		ALTER TABLE attendance_punches ADD COLUMN IF NOT EXISTS activity TEXT;
		ALTER TABLE attendance_punches ADD COLUMN IF NOT EXISTS is_billable BOOLEAN DEFAULT true;

		CREATE TABLE IF NOT EXISTS company_announcements (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			category TEXT NOT NULL DEFAULT 'Company News',
			priority TEXT NOT NULL DEFAULT 'normal',
			author_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
			author_name TEXT NOT NULL,
			author_role TEXT DEFAULT 'HR Management',
			pinned BOOLEAN NOT NULL DEFAULT false,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS company_announcements_org_idx ON company_announcements (organization_id, pinned DESC, created_at DESC);

		CREATE TABLE IF NOT EXISTS celebration_wishes (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			recipient_employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
			sender_employee_id UUID REFERENCES employees(id) ON DELETE SET NULL,
			sender_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
			sender_name TEXT NOT NULL,
			sender_avatar TEXT,
			celebration_type TEXT NOT NULL DEFAULT 'birthday',
			message TEXT NOT NULL,
			reaction TEXT NOT NULL DEFAULT 'heart',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS celebration_wishes_org_recipient_idx ON celebration_wishes (organization_id, recipient_employee_id, created_at DESC);

		CREATE TABLE IF NOT EXISTS user_notifications (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			title TEXT NOT NULL,
			message TEXT NOT NULL,
			category TEXT NOT NULL DEFAULT 'general',
			link TEXT,
			is_read BOOLEAN NOT NULL DEFAULT false,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS user_notifications_user_idx ON user_notifications (organization_id, user_id, is_read, created_at DESC);

		CREATE TABLE IF NOT EXISTS user_preferences (
			user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
			active_widgets JSONB NOT NULL DEFAULT '["feed","birthdays"]'::jsonb,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
	`
	_, err := pool.Exec(ctx, schemaSQL)
	return err
}

