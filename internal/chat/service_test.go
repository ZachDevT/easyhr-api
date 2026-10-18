package chat

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func getTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://mac@localhost:5432/easyhr_chat_test?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("skipping database test: postgres connection failed: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping database test: ping failed: %v", err)
	}
	return pool
}

type testFixture struct {
	OrgID   string
	Actor1  Actor
	Actor2  Actor
	Admin   Actor
	Foreign Actor
}

func setupTestFixture(t *testing.T, pool *pgxpool.Pool) testFixture {
	t.Helper()
	ctx := context.Background()

	orgID := newUUID()
	foreignOrgID := newUUID()

	_, err := pool.Exec(ctx, `
		INSERT INTO organizations (id, name, country_code, timezone)
		VALUES 
			($1::uuid, 'Chat Test Corp', 'US', 'America/New_York'),
			($2::uuid, 'Foreign Corp', 'US', 'America/New_York')
		ON CONFLICT DO NOTHING
	`, orgID, foreignOrgID)
	if err != nil {
		t.Fatalf("failed seeding organizations: %v", err)
	}

	u1, e1 := newUUID(), newUUID()
	u2, e2 := newUUID(), newUUID()
	uAdmin, eAdmin := newUUID(), newUUID()
	uForeign, eForeign := newUUID(), newUUID()

	// Seed Users
	_, err = pool.Exec(ctx, `
		INSERT INTO users (id, email, first_name, last_name, external_identity_id)
		VALUES 
			($1::uuid, $1 || '@test.com', 'Alice', 'Smith', 'ext-' || $1),
			($2::uuid, $2 || '@test.com', 'Bob', 'Jones', 'ext-' || $2),
			($3::uuid, $3 || '@test.com', 'Admin', 'User', 'ext-' || $3),
			($4::uuid, $4 || '@test.com', 'Eve', 'Foreign', 'ext-' || $4)
		ON CONFLICT DO NOTHING
	`, u1, u2, uAdmin, uForeign)
	if err != nil {
		t.Fatalf("failed seeding users: %v", err)
	}

	// Seed Employees
	_, err = pool.Exec(ctx, `
		INSERT INTO employees (id, organization_id, user_id, employee_number, first_name, last_name, work_email, status, hired_on)
		VALUES 
			($1::uuid, $5::uuid, $7::uuid, 'EMP-1-' || $1, 'Alice', 'Smith', $1 || '@test.com', 'active', CURRENT_DATE),
			($2::uuid, $5::uuid, $8::uuid, 'EMP-2-' || $2, 'Bob', 'Jones', $2 || '@test.com', 'active', CURRENT_DATE),
			($3::uuid, $5::uuid, $9::uuid, 'EMP-3-' || $3, 'Admin', 'User', $3 || '@test.com', 'active', CURRENT_DATE),
			($4::uuid, $6::uuid, $10::uuid, 'EMP-4-' || $4, 'Eve', 'Foreign', $4 || '@test.com', 'active', CURRENT_DATE)
		ON CONFLICT DO NOTHING
	`, e1, e2, eAdmin, eForeign, orgID, foreignOrgID, u1, u2, uAdmin, uForeign)
	if err != nil {
		t.Fatalf("failed seeding employees: %v", err)
	}

	return testFixture{
		OrgID: orgID,
		Actor1: Actor{
			OrganizationID: orgID,
			UserID:         u1,
			EmployeeID:     e1,
			FullName:       "Alice Smith",
			RoleTitle:      "Developer",
			Department:     "Engineering",
		},
		Actor2: Actor{
			OrganizationID: orgID,
			UserID:         u2,
			EmployeeID:     e2,
			FullName:       "Bob Jones",
			RoleTitle:      "Designer",
			Department:     "Design",
		},
		Admin: Actor{
			OrganizationID: orgID,
			UserID:         uAdmin,
			EmployeeID:     eAdmin,
			FullName:       "Admin User",
			RoleTitle:      "VP Ops",
			IsAdmin:        true,
		},
		Foreign: Actor{
			OrganizationID: foreignOrgID,
			UserID:         uForeign,
			EmployeeID:     eForeign,
			FullName:       "Eve Foreign",
			RoleTitle:      "Hacker",
		},
	}
}

func TestWorkspaceChatFullLifecycle(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()

	hub := NewHub(pool, nil, nil)
	svc := NewService(pool, hub, nil, "test-chat-signing-key", nil)
	fix := setupTestFixture(t, pool)
	ctx := context.Background()

	// 1. Default Channels Seeding
	channels, err := svc.ListChannels(ctx, fix.Actor1)
	if err != nil {
		t.Fatalf("ListChannels failed: %v", err)
	}
	if len(channels) == 0 {
		t.Fatal("expected default channels to be seeded")
	}

	var generalCh Channel
	for _, c := range channels {
		if c.Name == "general" {
			generalCh = c
			break
		}
	}
	if generalCh.ID == "" {
		t.Fatal("general channel was not found in seeded list")
	}

	// 2. Public Channel Message Sending & Retrieval
	msg1, err := svc.SendMessage(ctx, fix.Actor1, SendMessageInput{
		ChannelID:   generalCh.ID,
		Content:     "Hello from Alice! #welcome",
		ClientMsgID: "test-client-1",
	})
	if err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}
	if msg1.ID == "" || msg1.Content != "Hello from Alice! #welcome" {
		t.Fatalf("unexpected sent message: %+v", msg1)
	}

	// 3. Idempotency Deduplication (duplicate submit returns existing message)
	msgDup, err := svc.SendMessage(ctx, fix.Actor1, SendMessageInput{
		ChannelID:   generalCh.ID,
		Content:     "Hello from Alice! #welcome",
		ClientMsgID: "test-client-1",
	})
	if err != nil {
		t.Fatalf("SendMessage duplicate failed: %v", err)
	}
	if msgDup.ID != msg1.ID {
		t.Fatalf("idempotency failed: expected ID %s, got %s", msg1.ID, msgDup.ID)
	}

	// 4. Message Editing
	edited, err := svc.EditMessage(ctx, fix.Actor1, generalCh.ID, msg1.ID, "Hello from Alice! (Edited)")
	if err != nil {
		t.Fatalf("EditMessage failed: %v", err)
	}
	if edited.Content != "Hello from Alice! (Edited)" {
		t.Fatalf("unexpected edited content: %s", edited.Content)
	}

	// 5. Thread Replies
	reply, err := svc.SendMessage(ctx, fix.Actor2, SendMessageInput{
		ChannelID: generalCh.ID,
		Content:   "Hi Alice, Bob here replying in thread!",
		ReplyToID: msg1.ID,
	})
	if err != nil {
		t.Fatalf("SendMessage reply failed: %v", err)
	}
	if reply.ReplyToID != msg1.ID {
		t.Fatalf("expected ReplyToID %s, got %s", msg1.ID, reply.ReplyToID)
	}

	replies, err := svc.GetThreadReplies(ctx, fix.Actor1, generalCh.ID, msg1.ID)
	if err != nil {
		t.Fatalf("GetThreadReplies failed: %v", err)
	}
	if len(replies) != 1 || replies[0].ID != reply.ID {
		t.Fatalf("expected 1 thread reply with ID %s, got %d replies", reply.ID, len(replies))
	}

	// 6. Emoji Reactions Toggle
	reactions, err := svc.ToggleReaction(ctx, fix.Actor2, generalCh.ID, msg1.ID, "👍")
	if err != nil {
		t.Fatalf("ToggleReaction add failed: %v", err)
	}
	if len(reactions["👍"]) != 1 || reactions["👍"][0] != fix.Actor2.EmployeeID {
		t.Fatalf("expected Bob in reactions, got %+v", reactions)
	}

	reactions2, err := svc.ToggleReaction(ctx, fix.Actor2, generalCh.ID, msg1.ID, "👍")
	if err != nil {
		t.Fatalf("ToggleReaction remove failed: %v", err)
	}
	if len(reactions2["👍"]) != 0 {
		t.Fatalf("expected 👍 reaction to be toggled off, got %+v", reactions2)
	}

	// 7. Interactive Polls: creation, voting, closing
	pollMsg, err := svc.SendMessage(ctx, fix.Actor1, SendMessageInput{
		ChannelID: generalCh.ID,
		Content:   "Where should we have lunch?",
		Poll: &Poll{
			Question: "Where should we have lunch?",
			Options: []PollOption{
				{Text: "Tacos"},
				{Text: "Pizza"},
				{Text: "Sushi"},
			},
			AllowMultiple: false,
		},
	})
	if err != nil {
		t.Fatalf("SendMessage with poll failed: %v", err)
	}
	if pollMsg.Poll == nil || len(pollMsg.Poll.Options) != 3 {
		t.Fatalf("expected poll with 3 options, got %+v", pollMsg.Poll)
	}

	tacoOptionID := pollMsg.Poll.Options[0].ID
	updatedPoll, err := svc.VotePoll(ctx, fix.Actor2, generalCh.ID, pollMsg.ID, tacoOptionID)
	if err != nil {
		t.Fatalf("VotePoll failed: %v", err)
	}
	if len(updatedPoll.Options[0].VoterIDs) != 1 || updatedPoll.Options[0].VoterIDs[0] != fix.Actor2.EmployeeID {
		t.Fatalf("expected Bob's vote on option 0, got %+v", updatedPoll.Options[0])
	}

	closedPoll, err := svc.ToggleClosePoll(ctx, fix.Actor1, generalCh.ID, pollMsg.ID)
	if err != nil {
		t.Fatalf("ToggleClosePoll failed: %v", err)
	}
	if !closedPoll.Closed {
		t.Fatal("expected poll to be closed")
	}

	// 8. Message Pinning and Saving
	isPinned, err := svc.TogglePinMessage(ctx, fix.Actor1, generalCh.ID, msg1.ID)
	if err != nil {
		t.Fatalf("TogglePinMessage failed: %v", err)
	}
	if !isPinned {
		t.Fatal("expected message to be pinned")
	}

	isSaved, err := svc.ToggleSaveMessage(ctx, fix.Actor2, msg1.ID)
	if err != nil {
		t.Fatalf("ToggleSaveMessage failed: %v", err)
	}
	if !isSaved {
		t.Fatal("expected message to be saved for Bob")
	}

	// 9. Cursor-Based Pagination
	page, err := svc.ListMessages(ctx, fix.Actor1, ListMessagesFilter{
		ChannelID: generalCh.ID,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(page.Messages) < 2 {
		t.Fatalf("expected at least 2 messages in channel, got %d", len(page.Messages))
	}

	// 10. Full-Text Search
	searchRes, err := svc.SearchMessages(ctx, fix.Actor1, SearchMessagesInput{
		Query: "Alice",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchMessages failed: %v", err)
	}
	if len(searchRes.Messages) == 0 {
		t.Fatal("expected search query 'Alice' to match messages")
	}

	// 11. Read States and Unread Tracking
	err = svc.MarkChannelRead(ctx, fix.Actor2, generalCh.ID, msg1.ID)
	if err != nil {
		t.Fatalf("MarkChannelRead failed: %v", err)
	}

	// 12. Direct Messages (1-to-1)
	dmCh, err := svc.GetOrCreateDM(ctx, fix.Actor1, CreateDMInput{
		ParticipantIDs: []string{fix.Actor2.EmployeeID},
	})
	if err != nil {
		t.Fatalf("GetOrCreateDM failed: %v", err)
	}
	if dmCh.ChannelType != "dm" {
		t.Fatalf("expected channelType 'dm', got %s", dmCh.ChannelType)
	}

	// Calling GetOrCreateDM again returns existing channel
	dmCh2, err := svc.GetOrCreateDM(ctx, fix.Actor2, CreateDMInput{
		ParticipantIDs: []string{fix.Actor1.EmployeeID},
	})
	if err != nil {
		t.Fatalf("GetOrCreateDM second call failed: %v", err)
	}
	if dmCh2.ID != dmCh.ID {
		t.Fatalf("expected same DM channel ID %s, got %s", dmCh.ID, dmCh2.ID)
	}

	// 13. Private Channel Access Control & Join Requests
	privCh, err := svc.CreateChannel(ctx, fix.Actor1, CreateChannelInput{
		Name:      "confidential-project",
		IsPrivate: true,
	})
	if err != nil {
		t.Fatalf("CreateChannel private failed: %v", err)
	}

	// Bob is NOT in private channel -> SendMessage should return ErrForbidden
	_, err = svc.SendMessage(ctx, fix.Actor2, SendMessageInput{
		ChannelID: privCh.ID,
		Content:   "Sneaking in?",
	})
	if err != ErrForbidden {
		t.Fatalf("expected ErrForbidden for non-member in private channel, got: %v", err)
	}

	// Bob requests to join
	err = svc.RequestToJoinChannel(ctx, fix.Actor2, privCh.ID)
	if err != nil {
		t.Fatalf("RequestToJoinChannel failed: %v", err)
	}

	// Alice approves Bob's join request
	err = svc.ReviewJoinRequest(ctx, fix.Actor1, privCh.ID, fix.Actor2.EmployeeID, true)
	if err != nil {
		t.Fatalf("ReviewJoinRequest failed: %v", err)
	}

	// Bob can now participate
	privMsg, err := svc.SendMessage(ctx, fix.Actor2, SendMessageInput{
		ChannelID: privCh.ID,
		Content:   "Now authorized in confidential channel!",
	})
	if err != nil {
		t.Fatalf("expected Bob to post after approval, got: %v", err)
	}
	if privMsg.ID == "" {
		t.Fatal("empty private message ID")
	}

	// 14. Cross-Workspace Security Attack
	// Eve from Foreign Corp attempts to access Org A's channel
	_, err = svc.SendMessage(ctx, fix.Foreign, SendMessageInput{
		ChannelID: generalCh.ID,
		Content:   "Cross-tenant exploit attempt",
	})
	if err != ErrForbidden && err != ErrNotFound {
		t.Fatalf("cross-workspace security vulnerability: foreign actor not blocked! Got: %v", err)
	}

	// 15. Message Soft Deletion
	err = svc.DeleteMessage(ctx, fix.Actor1, generalCh.ID, msg1.ID)
	if err != nil {
		t.Fatalf("DeleteMessage failed: %v", err)
	}

	// 16. Attachments: Upload & HMAC Token Security
	att, err := svc.UploadAttachment(ctx, fix.Actor1, UploadAttachmentInput{
		ChannelID:   generalCh.ID,
		FileName:    "report.pdf",
		ContentType: "application/pdf",
		SizeBytes:   1024,
		Data:        []byte("%PDF-1.4 test report content"),
	})
	if err != nil {
		t.Fatalf("UploadAttachment failed: %v", err)
	}
	if att.ID == "" || att.URL == "" {
		t.Fatalf("unexpected attachment: %+v", att)
	}

	// Extract token from URL
	token := svc.SignAttachmentURL(att.ID)
	tokenParts := strings.Split(token, "token=")
	if len(tokenParts) < 2 {
		t.Fatalf("invalid signed URL: %s", token)
	}
	extractedToken := tokenParts[1]

	// Access with valid token
	rec, rc, err := svc.GetAttachmentContent(ctx, att.ID, extractedToken, nil)
	if err != nil {
		t.Fatalf("GetAttachmentContent with valid token failed: %v", err)
	}
	rc.Close()
	if rec.FileName != "report.pdf" {
		t.Fatalf("unexpected filename: %s", rec.FileName)
	}

	// Access with forged/invalid token
	_, _, err = svc.GetAttachmentContent(ctx, att.ID, "tampered.invalidtoken", nil)
	if err != ErrForbidden {
		t.Fatalf("expected ErrForbidden with forged token, got: %v", err)
	}
}

func TestConcurrentMessageCreation(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()

	hub := NewHub(pool, nil, nil)
	svc := NewService(pool, hub, nil, "test-secret", nil)
	fix := setupTestFixture(t, pool)
	ctx := context.Background()

	ch, err := svc.CreateChannel(ctx, fix.Actor1, CreateChannelInput{
		Name:      "concurrency-stress",
		IsPrivate: false,
	})
	if err != nil {
		t.Fatalf("CreateChannel failed: %v", err)
	}

	const workerCount = 10
	const messagesPerWorker = 20
	var wg sync.WaitGroup
	errCount := 0
	var mu sync.Mutex

	start := time.Now()
	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for m := 0; m < messagesPerWorker; m++ {
				_, err := svc.SendMessage(ctx, fix.Actor1, SendMessageInput{
					ChannelID: ch.ID,
					Content:   "Concurrent load test message",
				})
				if err != nil {
					mu.Lock()
					errCount++
					mu.Unlock()
				}
			}
		}(w)
	}

	wg.Wait()
	elapsed := time.Since(start)

	totalMsgs := workerCount * messagesPerWorker
	t.Logf("Concurrent test: %d messages inserted across %d concurrent workers in %v (errCount=%d)",
		totalMsgs, workerCount, elapsed, errCount)

	if errCount > 0 {
		t.Fatalf("concurrent message insertion had %d failures", errCount)
	}

	page, err := svc.ListMessages(ctx, fix.Actor1, ListMessagesFilter{
		ChannelID: ch.ID,
		Limit:     100,
	})
	if err != nil {
		t.Fatalf("ListMessages after concurrent insert failed: %v", err)
	}
	if len(page.Messages) != 100 {
		t.Fatalf("expected 100 messages in first page, got %d", len(page.Messages))
	}
}
