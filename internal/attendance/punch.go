package attendance

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type CreatePunchInput struct {
	Kind                   string   `json:"kind"`
	OccurredAt             string   `json:"occurredAt"`
	Source                 string   `json:"source"`
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
	PolicyOutcome string `json:"policyOutcome"`
	CreatedAt     string `json:"createdAt"`
}
type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }
func (input CreatePunchInput) Validate() error {
	if input.Kind != "clock_in" && input.Kind != "clock_out" && input.Kind != "break_start" && input.Kind != "break_end" {
		return fmt.Errorf("kind is invalid")
	}
	if input.Source != "web" && input.Source != "mobile" && input.Source != "kiosk" {
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
	err := service.pool.QueryRow(ctx, `SELECT id::text FROM employees WHERE organization_id=$1 AND user_id=$2 AND status IN ('onboarding','active','leave')`, org, userID).Scan(&employeeID)
	if err != nil {
		return Punch{}, fmt.Errorf("find self employee: %w", err)
	}
	result := Punch{EmployeeID: employeeID, Kind: input.Kind, OccurredAt: input.OccurredAt, Source: input.Source, PolicyOutcome: "not_evaluated"}
	err = service.pool.QueryRow(ctx, `INSERT INTO attendance_punches (organization_id,employee_id,kind,occurred_at,source,device_id,latitude,longitude,location_accuracy_meters,created_by_user_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id::text, created_at::text`, org, employeeID, input.Kind, input.OccurredAt, input.Source, nullIfBlank(input.DeviceID), input.Latitude, input.Longitude, input.LocationAccuracyMeters, userID).Scan(&result.ID, &result.CreatedAt)
	if err != nil {
		return Punch{}, err
	}
	_, err = service.pool.Exec(ctx, `INSERT INTO audit_events (organization_id,actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES ($1,$2,'attendance.punch_created','attendance_punch',$3,'unknown',jsonb_build_object('kind',$4,'source',$5))`, org, userID, result.ID, input.Kind, input.Source)
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
