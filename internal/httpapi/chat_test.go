package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/easyhr/easyhr-be/internal/chat"
	"github.com/easyhr/easyhr-be/internal/identity"
)

func getTestPoolForHTTP(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://mac@localhost:5432/easyhr_chat_test?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("skipping http database test: postgres connection failed: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping http database test: ping failed: %v", err)
	}
	return pool
}

func TestChatHTTPEndpoints(t *testing.T) {
	pool := getTestPoolForHTTP(t)
	defer pool.Close()

	ctx := context.Background()
	orgID := chat.NewUUIDForTest()
	userID := chat.NewUUIDForTest()
	empID := chat.NewUUIDForTest()
	email := "alice-" + userID[:8] + "@http.com"
	extID := "ext-" + userID[:8]

	// Seed organization, user, and employee
	if _, err := pool.Exec(ctx, `
		INSERT INTO organizations (id, name, country_code, timezone)
		VALUES ($1::uuid, 'HTTP Chat Corp', 'US', 'America/New_York')
	`, orgID); err != nil {
		t.Fatalf("failed seeding test organization: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, first_name, last_name, external_identity_id)
		VALUES ($1::uuid, $2, 'Alice', 'HTTP', $3)
	`, userID, email, extID); err != nil {
		t.Fatalf("failed seeding test user: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO employees (id, organization_id, user_id, employee_number, first_name, last_name, work_email, status, hired_on)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 'Alice', 'HTTP', $5, 'active', CURRENT_DATE)
	`, empID, orgID, userID, "EMP-"+empID[:8], email); err != nil {
		t.Fatalf("failed seeding test employee: %v", err)
	}

	// Mock authentication middleware that injects Alice's Principal
	authMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := identity.Principal{
				UserID:         userID,
				MembershipID:   "mem-" + userID[:8],
				OrganizationID: orgID,
				ExternalID:     extID,
				Permissions: map[string]struct{}{
					"chat:read":            {},
					"chat:write":           {},
					"chat:manage_channels": {},
					"workspace.admin":      {},
				},
			}
			ctx := identity.WithPrincipal(r.Context(), p)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	server := New(pool, []string{"http://localhost:3000"}, authMiddleware, "test-secret")
	defer server.Close()

	// 1. GET /api/v1/chat/channels
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/channels", nil)
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/chat/channels returned status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var chListResp struct {
		Channels []chat.Channel `json:"channels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &chListResp); err != nil {
		t.Fatalf("failed decoding channels JSON: %v", err)
	}
	if len(chListResp.Channels) == 0 {
		t.Fatal("expected channels to be returned")
	}

	generalChID := chListResp.Channels[0].ID

	// 2. POST /api/v1/chat/messages
	sendBody, _ := json.Marshal(map[string]any{
		"channelId": generalChID,
		"content":   "Test message via HTTP API",
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/messages", bytes.NewReader(sendBody))
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/chat/messages returned status %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var createdMsg chat.Message
	if err := json.Unmarshal(rec.Body.Bytes(), &createdMsg); err != nil {
		t.Fatalf("failed decoding message JSON: %v", err)
	}
	if createdMsg.Content != "Test message via HTTP API" {
		t.Fatalf("unexpected message content: %s", createdMsg.Content)
	}

	// 3. GET /api/v1/chat/messages
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/chat/messages?channelId="+generalChID, nil)
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/chat/messages returned status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var page chat.MessagePage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("failed decoding message page JSON: %v", err)
	}
	if len(page.Messages) == 0 {
		t.Fatal("expected messages in channel")
	}

	// 4. POST /api/v1/chat/messages/:id/reactions
	reactBody, _ := json.Marshal(map[string]any{
		"channelId": generalChID,
		"emoji":     "🔥",
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/messages/"+createdMsg.ID+"/reactions", bytes.NewReader(reactBody))
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/chat/messages/:id/reactions returned status %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// 5. POST /api/v1/chat/messages/:id/pin
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/messages/"+createdMsg.ID+"/pin?channelId="+generalChID, nil)
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/chat/messages/:id/pin returned status %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// 6. POST /api/v1/chat/messages/:id/save
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/messages/"+createdMsg.ID+"/save", nil)
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/chat/messages/:id/save returned status %d, want 200", rec.Code)
	}

	// 7. GET /api/v1/chat/messages (saved messages filter)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/chat/messages?channelId="+generalChID+"&saved=true", nil)
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/chat/messages?saved=true returned status %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// 8. POST /api/v1/chat/messages (with Poll)
	pollMsgBody, _ := json.Marshal(map[string]any{
		"channelId": generalChID,
		"content":   "Where for lunch?",
		"poll": map[string]any{
			"question": "Where for lunch?",
			"options": []map[string]any{
				{"text": "Tacos"},
				{"text": "Sushi"},
			},
		},
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/messages", bytes.NewReader(pollMsgBody))
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/chat/messages (with poll) returned status %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var pollMsg chat.Message
	if err := json.Unmarshal(rec.Body.Bytes(), &pollMsg); err != nil {
		t.Fatalf("failed decoding poll message JSON: %v", err)
	}
	if pollMsg.Poll == nil || len(pollMsg.Poll.Options) == 0 {
		t.Fatalf("expected poll on message, got nil or empty")
	}

	// 9. POST /api/v1/chat/messages/:messageId/poll/vote
	voteBody, _ := json.Marshal(map[string]any{
		"channelId": generalChID,
		"optionId":  pollMsg.Poll.Options[0].ID,
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/messages/"+pollMsg.ID+"/poll/vote", bytes.NewReader(voteBody))
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/chat/messages/:id/poll/vote returned status %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// 10. GET /api/v1/chat/messages/search
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/chat/messages/search?q=HTTP", nil)
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/chat/messages/search returned status %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// 11. POST /api/v1/chat/typing
	typingBody, _ := json.Marshal(map[string]any{
		"channelId": generalChID,
		"isTyping":  true,
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/typing", bytes.NewReader(typingBody))
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/chat/typing returned status %d, want 200", rec.Code)
	}

	// 12. GET /api/v1/chat/presence
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/chat/presence", nil)
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/chat/presence returned status %d, want 200", rec.Code)
	}

	// 13. PATCH /api/v1/chat/presence
	presenceBody, _ := json.Marshal(map[string]any{
		"status":     "dnd",
		"statusText": "In deep work",
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/chat/presence", bytes.NewReader(presenceBody))
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/v1/chat/presence returned status %d, want 200", rec.Code)
	}

	// 14. POST /api/v1/chat/channels/:channelId/read
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/channels/"+generalChID+"/read", nil)
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/chat/channels/:id/read returned status %d, want 200", rec.Code)
	}

	// 15. POST /api/v1/chat/channels (Create new channel)
	createChBody, _ := json.Marshal(map[string]any{
		"name":        "engineering-backend",
		"topic":       "Backend architecture discussions",
		"description": "EasyHR Go backend services",
		"isPrivate":   false,
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/channels", bytes.NewReader(createChBody))
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/chat/channels returned status %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var newCh chat.Channel
	if err := json.Unmarshal(rec.Body.Bytes(), &newCh); err != nil {
		t.Fatalf("failed decoding new channel JSON: %v", err)
	}
	if newCh.Name != "engineering-backend" {
		t.Fatalf("unexpected new channel name: %s", newCh.Name)
	}

	// 16. Seed Bob for Direct Message (1:1) test
	bobUserID := chat.NewUUIDForTest()
	bobEmpID := chat.NewUUIDForTest()
	bobEmail := "bob-" + bobUserID[:8] + "@http.com"
	bobExtID := "ext-" + bobUserID[:8]

	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, first_name, last_name, external_identity_id)
		VALUES ($1::uuid, $2, 'Bob', 'HTTP', $3)
	`, bobUserID, bobEmail, bobExtID); err != nil {
		t.Fatalf("failed seeding Bob user: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO employees (id, organization_id, user_id, employee_number, first_name, last_name, work_email, status, hired_on)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 'Bob', 'HTTP', $5, 'active', CURRENT_DATE)
	`, bobEmpID, orgID, bobUserID, "EMP-"+bobEmpID[:8], bobEmail); err != nil {
		t.Fatalf("failed seeding Bob employee: %v", err)
	}

	// 17. POST /api/v1/chat/channels/dm
	dmBody, _ := json.Marshal(map[string]any{
		"participantIds": []string{bobEmpID},
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/channels/dm", bytes.NewReader(dmBody))
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/chat/channels/dm returned status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var dmCh chat.Channel
	if err := json.Unmarshal(rec.Body.Bytes(), &dmCh); err != nil {
		t.Fatalf("failed decoding DM channel JSON: %v", err)
	}
	if dmCh.ChannelType != "dm" {
		t.Fatalf("expected channel type 'dm', got %s", dmCh.ChannelType)
	}

	// 18. POST /api/v1/chat/messages (Thread Reply)
	replyBody, _ := json.Marshal(map[string]any{
		"channelId": generalChID,
		"content":   "This is a threaded reply to Alice's message",
		"replyToId": createdMsg.ID,
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/messages", bytes.NewReader(replyBody))
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/chat/messages (reply) returned status %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var replyMsg chat.Message
	if err := json.Unmarshal(rec.Body.Bytes(), &replyMsg); err != nil {
		t.Fatalf("failed decoding reply JSON: %v", err)
	}

	// 19. GET /api/v1/chat/messages/:messageId/replies
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/chat/messages/"+createdMsg.ID+"/replies?channelId="+generalChID, nil)
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/chat/messages/:id/replies returned status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var repliesResp struct {
		Replies []chat.Message `json:"replies"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &repliesResp); err != nil {
		t.Fatalf("failed decoding replies JSON: %v", err)
	}
	if len(repliesResp.Replies) == 0 {
		t.Fatal("expected at least 1 threaded reply")
	}

	// 20. PATCH /api/v1/chat/messages/:messageId (Edit Message)
	editBody, _ := json.Marshal(map[string]any{
		"channelId": generalChID,
		"content":   "Edited message content via API",
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/chat/messages/"+createdMsg.ID, bytes.NewReader(editBody))
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/v1/chat/messages/:id returned status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var editedMsg chat.Message
	if err := json.Unmarshal(rec.Body.Bytes(), &editedMsg); err != nil {
		t.Fatalf("failed decoding edited message JSON: %v", err)
	}
	if editedMsg.Content != "Edited message content via API" {
		t.Fatalf("unexpected content after edit: %s", editedMsg.Content)
	}

	// 21. DELETE /api/v1/chat/messages/:messageId (Soft Delete)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/chat/messages/"+replyMsg.ID+"?channelId="+generalChID, nil)
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE /api/v1/chat/messages/:id returned status %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// 22. PATCH /api/v1/chat/channels/:channelId/preferences (Mute channel)
	prefBody, _ := json.Marshal(map[string]any{
		"isMuted": true,
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/chat/channels/"+generalChID+"/preferences", bytes.NewReader(prefBody))
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/v1/chat/channels/:id/preferences returned status %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// 23. POST /api/v1/chat/channels/:channelId/archive
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/channels/"+newCh.ID+"/archive", nil)
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/chat/channels/:id/archive returned status %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// 24. Cross-Tenant Security Isolation Test
	// Mallory belongs to another organization (tenant). She must NOT be able to list Alice's channels or read Alice's messages.
	otherOrgID := chat.NewUUIDForTest()
	malloryUserID := chat.NewUUIDForTest()
	malloryEmpID := chat.NewUUIDForTest()
	malloryEmail := "mallory-" + malloryUserID[:8] + "@evil.com"
	malloryExtID := "ext-" + malloryUserID[:8]

	if _, err := pool.Exec(ctx, `
		INSERT INTO organizations (id, name, country_code, timezone)
		VALUES ($1::uuid, 'Attacker Org', 'US', 'America/New_York')
	`, otherOrgID); err != nil {
		t.Fatalf("failed seeding attacker org: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, first_name, last_name, external_identity_id)
		VALUES ($1::uuid, $2, 'Mallory', 'Hacker', $3)
	`, malloryUserID, malloryEmail, malloryExtID); err != nil {
		t.Fatalf("failed seeding Mallory user: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO employees (id, organization_id, user_id, employee_number, first_name, last_name, work_email, status, hired_on)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 'Mallory', 'Hacker', $5, 'active', CURRENT_DATE)
	`, malloryEmpID, otherOrgID, malloryUserID, "EMP-"+malloryEmpID[:8], malloryEmail); err != nil {
		t.Fatalf("failed seeding Mallory employee: %v", err)
	}

	attackerAuth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := identity.Principal{
				UserID:         malloryUserID,
				MembershipID:   "mem-" + malloryUserID[:8],
				OrganizationID: otherOrgID,
				ExternalID:     malloryExtID,
				Permissions: map[string]struct{}{
					"chat:read":  {},
					"chat:write": {},
				},
			}
			ctx := identity.WithPrincipal(r.Context(), p)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	attackerServer := New(pool, []string{"http://localhost:3000"}, attackerAuth, "test-secret")
	defer attackerServer.Close()

	// Mallory tries to read Alice's channel messages
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/chat/messages?channelId="+generalChID, nil)
	attackerServer.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("Cross-tenant violation: Attacker received status %d, expected 403 Forbidden: %s", rec.Code, rec.Body.String())
	}
}
