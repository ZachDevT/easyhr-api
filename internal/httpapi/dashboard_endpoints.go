package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/easyhr/easyhr-be/internal/announcements"
	"github.com/easyhr/easyhr-be/internal/attendance"
	"github.com/easyhr/easyhr-be/internal/celebrations"
	"github.com/easyhr/easyhr-be/internal/identity"
)

// 1. Attendance Activities
func (s *Server) listAttendanceActivities(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	activities, err := s.attendance.ListActivities(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "attendance.activities_failed", "Internal error", err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{"data": activities})
}

func (s *Server) createAttendanceActivity(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	var input attendance.CreateActivityInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}

	activity, err := s.attendance.CreateActivity(c.Request().Context(), principal.OrganizationID, input)
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "attendance.activity_create_failed", "Validation error", err.Error())
	}
	return c.JSON(http.StatusCreated, activity)
}

func (s *Server) deleteAttendanceActivity(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	id := c.Param("id")
	if err := s.attendance.DeleteActivity(c.Request().Context(), principal.OrganizationID, id); err != nil {
		return problem(c, http.StatusInternalServerError, "attendance.activity_delete_failed", "Internal error", err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

// 2. Company Announcements
func (s *Server) listAnnouncements(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	items, err := s.announcements.ListAnnouncements(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "announcements.list_failed", "Internal error", err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{"data": items})
}

func (s *Server) createAnnouncement(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	var input announcements.CreateAnnouncementInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}

	item, err := s.announcements.CreateAnnouncement(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "announcements.create_failed", "Validation error", err.Error())
	}
	return c.JSON(http.StatusCreated, item)
}

func (s *Server) deleteAnnouncement(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	id := c.Param("id")
	if err := s.announcements.DeleteAnnouncement(c.Request().Context(), principal.OrganizationID, id); err != nil {
		return problem(c, http.StatusInternalServerError, "announcements.delete_failed", "Internal error", err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

// 3. Celebrations & Wishes
func (s *Server) listCelebrations(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	filterType := c.QueryParam("type")
	items, err := s.celebrations.ListUpcomingCelebrations(c.Request().Context(), principal.OrganizationID, filterType)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "celebrations.list_failed", "Internal error", err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{"data": items})
}

func (s *Server) sendCelebrationWish(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	var input celebrations.SendWishInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}

	wish, err := s.celebrations.SendWish(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "celebrations.wish_failed", "Validation error", err.Error())
	}
	return c.JSON(http.StatusCreated, wish)
}

func (s *Server) getMyCelebrationStatus(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	status, err := s.celebrations.GetCelebrationStatus(c.Request().Context(), principal.OrganizationID, principal.UserID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "celebrations.status_failed", "Internal error", err.Error())
	}
	return c.JSON(http.StatusOK, status)
}

// 4. Notifications
func (s *Server) listNotifications(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	summary, err := s.notifications.ListNotifications(c.Request().Context(), principal.OrganizationID, principal.UserID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "notifications.list_failed", "Internal error", err.Error())
	}
	return c.JSON(http.StatusOK, summary)
}

type markReadInput struct {
	NotificationID *string `json:"notificationId,omitempty"`
}

func (s *Server) markNotificationRead(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	var input markReadInput
	_ = c.Bind(&input)

	if err := s.notifications.MarkAsRead(c.Request().Context(), principal.OrganizationID, principal.UserID, input.NotificationID); err != nil {
		return problem(c, http.StatusInternalServerError, "notifications.mark_failed", "Internal error", err.Error())
	}
	return c.JSON(http.StatusOK, map[string]bool{"success": true})
}

// 5. User Preferences (Widgets persistence)
type userPreferencesInput struct {
	ActiveWidgets []string `json:"activeWidgets"`
}

func (s *Server) getUserPreferences(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	var activeWidgetsRaw []byte
	err := s.pool.QueryRow(c.Request().Context(), `
		SELECT active_widgets
		FROM user_preferences
		WHERE user_id = $1
	`, principal.UserID).Scan(&activeWidgetsRaw)

	if err != nil {
		// Default widgets
		return c.JSON(http.StatusOK, map[string]any{"activeWidgets": []string{"feed", "birthdays"}})
	}

	var activeWidgets []string
	if err := json.Unmarshal(activeWidgetsRaw, &activeWidgets); err != nil {
		activeWidgets = []string{"feed", "birthdays"}
	}

	return c.JSON(http.StatusOK, map[string]any{"activeWidgets": activeWidgets})
}

func (s *Server) updateUserPreferences(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	var input userPreferencesInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}

	widgetsBytes, err := json.Marshal(input.ActiveWidgets)
	if err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid_json", "Invalid JSON", err.Error())
	}

	_, err = s.pool.Exec(c.Request().Context(), `
		INSERT INTO user_preferences (user_id, active_widgets, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (user_id) DO UPDATE SET active_widgets = EXCLUDED.active_widgets, updated_at = now()
	`, principal.UserID, widgetsBytes)

	if err != nil {
		return problem(c, http.StatusInternalServerError, "preferences.update_failed", "Internal error", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{"activeWidgets": input.ActiveWidgets})
}
