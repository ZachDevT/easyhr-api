package attendance

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type CreatePunchInput struct {
	EmployeeID             string   `json:"employeeId,omitempty"`
	Kind                   string   `json:"kind"`
	OccurredAt             string   `json:"occurredAt"`
	Source                 string   `json:"source"`
	Activity               string   `json:"activity,omitempty"`
	IsBillable             *bool    `json:"isBillable,omitempty"`
	DeviceID               string   `json:"deviceId,omitempty"`
	Latitude               *float64 `json:"latitude,omitempty"`
	Longitude              *float64 `json:"longitude,omitempty"`
	LocationAccuracyMeters *int     `json:"locationAccuracyMeters,omitempty"`
}
type Punch struct {
	ID            string `json:"id"`
	EmployeeID    string `json:"employeeId"`
	Kind          string `json:"kind"`
	OccurredAt    string `json:"occurredAt"`
	Source        string `json:"source"`
	Activity      string `json:"activity,omitempty"`
	IsBillable    bool   `json:"isBillable"`
	PolicyOutcome string `json:"policyOutcome"`
	CreatedAt     string `json:"createdAt"`
}
type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }
func (input *CreatePunchInput) Validate() error {
	if input.Source == "" {
		input.Source = "web"
	}
	if input.OccurredAt == "" {
		input.OccurredAt = time.Now().UTC().Format(time.RFC3339)
	}
	if input.Kind == "in" {
		input.Kind = "clock_in"
	} else if input.Kind == "out" {
		input.Kind = "clock_out"
	}
	if input.Kind != "clock_in" && input.Kind != "clock_out" && input.Kind != "break_start" && input.Kind != "break_end" {
		return fmt.Errorf("kind is invalid (must be clock_in, clock_out, break_start, or break_end)")
	}
	if input.Source != "web" && input.Source != "mobile" && input.Source != "kiosk" && input.Source != "manual" {
		return fmt.Errorf("source is invalid for self-service")
	}
	if _, err := time.Parse(time.RFC3339, input.OccurredAt); err != nil {
		return fmt.Errorf("occurredAt must be RFC 3339")
	}
	if (input.Latitude == nil) != (input.Longitude == nil) {
		return fmt.Errorf("latitude and longitude must be supplied together")
	}
	if input.Latitude != nil && (*input.Latitude < -90 || *input.Latitude > 90 || *input.Longitude < -180 || *input.Longitude > 180) {
		return fmt.Errorf("location coordinates are out of range")
	}
	if input.LocationAccuracyMeters != nil && *input.LocationAccuracyMeters < 0 {
		return fmt.Errorf("locationAccuracyMeters must be non-negative")
	}
	return nil
}
func (service *Service) CreateSelfPunch(ctx context.Context, org, userID string, input CreatePunchInput) (Punch, error) {
	if err := input.Validate(); err != nil {
		return Punch{}, err
	}
	var employeeID string
	err := service.pool.QueryRow(ctx, `SELECT id::text FROM employees WHERE organization_id=$1 AND user_id=$2 AND status IN ('onboarding','active','leave') LIMIT 1`, org, userID).Scan(&employeeID)
	if err != nil {
		_ = service.pool.QueryRow(ctx, `
			SELECT e.id::text FROM employees e
			JOIN users u ON LOWER(u.email) = LOWER(e.work_email)
			WHERE e.organization_id = $1 AND u.id = $2
			LIMIT 1`, org, userID).Scan(&employeeID)
	}
	if employeeID == "" {
		return Punch{}, fmt.Errorf("no active employee found to record punch")
	}
	if input.EmployeeID != "" && input.EmployeeID != employeeID {
		return Punch{}, fmt.Errorf("self-service punches can only be recorded for your own employee profile")
	}
	isBillable := true
	if input.IsBillable != nil {
		isBillable = *input.IsBillable
	} else if input.Activity != "" {
		_ = service.pool.QueryRow(ctx, `SELECT is_billable FROM attendance_activities WHERE organization_id=$1 AND name=$2 LIMIT 1`, org, input.Activity).Scan(&isBillable)
	}
	result := Punch{EmployeeID: employeeID, Kind: input.Kind, OccurredAt: input.OccurredAt, Source: input.Source, Activity: input.Activity, IsBillable: isBillable, PolicyOutcome: "not_evaluated"}
	err = service.pool.QueryRow(ctx, `INSERT INTO attendance_punches (organization_id,employee_id,kind,occurred_at,source,activity,is_billable,device_id,latitude,longitude,location_accuracy_meters,created_by_user_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id::text, created_at::text`, org, employeeID, input.Kind, input.OccurredAt, input.Source, nullIfBlank(input.Activity), isBillable, nullIfBlank(input.DeviceID), input.Latitude, input.Longitude, input.LocationAccuracyMeters, userID).Scan(&result.ID, &result.CreatedAt)
	if err != nil {
		return Punch{}, err
	}
	_, err = service.pool.Exec(ctx, `INSERT INTO audit_events (organization_id,actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES ($1,$2,'attendance.punch_created','attendance_punch',$3,'unknown',jsonb_build_object('kind',$4::text,'source',$5::text,'activity',$6::text))`, org, userID, result.ID, input.Kind, input.Source, input.Activity)
	if err != nil {
		return Punch{}, err
	}
	return result, nil
}
func nullIfBlank(value string) any {
	if value == "" {
		return nil
	}
	return value
}
