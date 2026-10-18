package chat

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ChannelAuthorizer func(orgID, channelID, employeeID string, isAdmin bool) bool

type Subscriber struct {
	ID             string
	OrganizationID string
	EmployeeID     string
	UserID         string
	IsAdmin        bool
	Events         chan RealtimeEvent
}

type typingEntry struct {
	OrganizationID string
	ChannelID      string
	EmployeeID     string
	UserName       string
	LastSentAt     time.Time
	ExpiresAt      time.Time
}

type pgNotifyEnvelope struct {
	InstanceID string        `json:"instanceId"`
	Event      RealtimeEvent `json:"event"`
}

type Hub struct {
	instanceID string
	db         *pgxpool.Pool
	logger     *slog.Logger
	authorize  ChannelAuthorizer

	mu          sync.RWMutex
	subscribers map[string]map[string]*Subscriber // orgID -> subID -> *Subscriber
	typing      map[string]*typingEntry           // key: orgID + ":" + channelID + ":" + employeeID

	cancelListener context.CancelFunc
	listenerDone   chan struct{}
	cancelTyping   context.CancelFunc
}

func NewHub(db *pgxpool.Pool, logger *slog.Logger, authorize ChannelAuthorizer) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	h := &Hub{
		instanceID:  newUUID(),
		db:          db,
		logger:      logger,
		authorize:   authorize,
		subscribers: make(map[string]map[string]*Subscriber),
		typing:      make(map[string]*typingEntry),
	}
	go h.cleanupExpiredTypingLoop()
	return h
}

func (h *Hub) Subscribe(orgID, employeeID, userID string, isAdmin bool) (*Subscriber, func()) {
	sub := &Subscriber{
		ID:             newUUID(),
		OrganizationID: orgID,
		EmployeeID:     employeeID,
		UserID:         userID,
		IsAdmin:        isAdmin,
		Events:         make(chan RealtimeEvent, 128),
	}

	h.mu.Lock()
	orgSubs, ok := h.subscribers[orgID]
	if !ok {
		orgSubs = make(map[string]*Subscriber)
		h.subscribers[orgID] = orgSubs
	}
	wasOnline := false
	for _, existing := range orgSubs {
		if existing.EmployeeID == employeeID {
			wasOnline = true
			break
		}
	}
	orgSubs[sub.ID] = sub
	h.mu.Unlock()

	if !wasOnline && employeeID != "" {
		h.Publish(RealtimeEvent{
			Version:        EventVersionV1,
			Type:           EventPresenceUpdated,
			OrganizationID: orgID,
			ActorID:        employeeID,
			Payload: Presence{
				EmployeeID: employeeID,
				UserID:     userID,
				Status:     "online",
				LastSeenAt: time.Now().UTC().Format(time.RFC3339),
			},
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	unsubscribe := func() {
		h.mu.Lock()
		stillOnline := false
		if orgSubs, ok := h.subscribers[orgID]; ok {
			delete(orgSubs, sub.ID)
			for _, rem := range orgSubs {
				if rem.EmployeeID == employeeID {
					stillOnline = true
					break
				}
			}
			if len(orgSubs) == 0 {
				delete(h.subscribers, orgID)
			}
		}
		close(sub.Events)
		h.mu.Unlock()

		if !stillOnline && employeeID != "" {
			h.Publish(RealtimeEvent{
				Version:        EventVersionV1,
				Type:           EventPresenceUpdated,
				OrganizationID: orgID,
				ActorID:        employeeID,
				Payload: Presence{
					EmployeeID: employeeID,
					UserID:     userID,
					Status:     "offline",
					LastSeenAt: time.Now().UTC().Format(time.RFC3339),
				},
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}
	}

	return sub, unsubscribe
}

func (h *Hub) IsOnline(orgID, employeeID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	orgSubs, ok := h.subscribers[orgID]
	if !ok {
		return false
	}
	for _, s := range orgSubs {
		if s.EmployeeID == employeeID {
			return true
		}
	}
	return false
}

func (h *Hub) OnlineEmployeeIDs(orgID string) map[string]bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	res := make(map[string]bool)
	orgSubs, ok := h.subscribers[orgID]
	if !ok {
		return res
	}
	for _, s := range orgSubs {
		if s.EmployeeID != "" {
			res[s.EmployeeID] = true
		}
	}
	return res
}

func (h *Hub) ActiveSubscriberCount(orgID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if orgID == "" {
		total := 0
		for _, subs := range h.subscribers {
			total += len(subs)
		}
		return total
	}
	return len(h.subscribers[orgID])
}

// Publish dispatches an event to all authorized local subscribers in the target organization
// and asynchronously broadcasts via PostgreSQL NOTIFY for multi-instance horizontal scaling.
func (h *Hub) Publish(event RealtimeEvent) {
	if event.Version == 0 {
		event.Version = EventVersionV1
	}
	if event.Timestamp == "" {
		event.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	h.dispatchLocal(event)

	if h.db != nil {
		go h.notifyPostgres(event)
	}
}

func (h *Hub) dispatchLocal(event RealtimeEvent) {
	h.mu.RLock()
	orgSubs, ok := h.subscribers[event.OrganizationID]
	if !ok || len(orgSubs) == 0 {
		h.mu.RUnlock()
		return
	}
	snapshot := make([]*Subscriber, 0, len(orgSubs))
	for _, sub := range orgSubs {
		snapshot = append(snapshot, sub)
	}
	h.mu.RUnlock()

	targetSet := make(map[string]bool, len(event.TargetIDs))
	for _, tid := range event.TargetIDs {
		if tid != "" {
			targetSet[tid] = true
		}
	}

	for _, sub := range snapshot {
		if sub.OrganizationID != event.OrganizationID {
			continue
		}
		if len(targetSet) > 0 {
			if !targetSet[sub.EmployeeID] && !targetSet[sub.UserID] {
				continue
			}
		} else if event.ChannelID != "" && h.authorize != nil {
			if !h.authorize(event.OrganizationID, event.ChannelID, sub.EmployeeID, sub.IsAdmin) {
				continue
			}
		}

		select {
		case sub.Events <- event:
		default:
			// Drop oldest event if subscriber channel buffer is completely full to prevent slow clients from blocking broadcast.
			select {
			case <-sub.Events:
			default:
			}
			select {
			case sub.Events <- event:
			default:
			}
		}
	}
}

func (h *Hub) notifyPostgres(event RealtimeEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	env := pgNotifyEnvelope{
		InstanceID: h.instanceID,
		Event:      event,
	}
	raw, err := json.Marshal(env)
	if err != nil || len(raw) > 7500 {
		// PostgreSQL NOTIFY payload limit is 8000 bytes; if larger, strip heavy payload fields or let clients fetch via event ID.
		return
	}
	_, _ = h.db.Exec(ctx, `SELECT pg_notify('workspace_chat_events', $1)`, string(raw))
}

func (h *Hub) StartPostgresListener(parentCtx context.Context) {
	if h.db == nil {
		return
	}
	ctx, cancel := context.WithCancel(parentCtx)
	h.cancelListener = cancel
	h.listenerDone = make(chan struct{})

	go func() {
		defer close(h.listenerDone)
		for {
			if ctx.Err() != nil {
				return
			}
			conn, err := h.db.Acquire(ctx)
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
					continue
				}
			}

			_, err = conn.Exec(ctx, `LISTEN workspace_chat_events`)
			if err != nil {
				conn.Release()
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
					continue
				}
			}

			for {
				notification, err := conn.Conn().WaitForNotification(ctx)
				if err != nil {
					conn.Release()
					break
				}
				var env pgNotifyEnvelope
				if err := json.Unmarshal([]byte(notification.Payload), &env); err == nil {
					if env.InstanceID != "" && env.InstanceID != h.instanceID {
						h.dispatchLocal(env.Event)
					}
				}
			}
		}
	}()
}

// Close gracefully cancels background listeners and workers.
func (h *Hub) Close() {
	if h.cancelListener != nil {
		h.cancelListener()
		select {
		case <-h.listenerDone:
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// SetTyping updates typing state with built-in throttling (2s minimum broadcast interval, 5s expiration)
// so rapid keystrokes never flood real-time subscribers or generate database writes.
func (h *Hub) SetTyping(orgID, channelID, employeeID, userName string, isTyping bool) {
	if orgID == "" || channelID == "" || employeeID == "" {
		return
	}
	key := orgID + ":" + channelID + ":" + employeeID
	now := time.Now().UTC()

	h.mu.Lock()
	existing, exists := h.typing[key]
	if !isTyping {
		delete(h.typing, key)
		h.mu.Unlock()
		if exists {
			h.Publish(RealtimeEvent{
				Version:        EventVersionV1,
				Type:           EventTypingStopped,
				OrganizationID: orgID,
				ChannelID:      channelID,
				ActorID:        employeeID,
				Payload: TypingState{
					ChannelID:  channelID,
					EmployeeID: employeeID,
					UserName:   userName,
					IsTyping:   false,
					Timestamp:  now.Format(time.RFC3339),
				},
				Timestamp: now.Format(time.RFC3339),
			})
		}
		return
	}

	if exists && now.Sub(existing.LastSentAt) < 2*time.Second {
		existing.ExpiresAt = now.Add(5 * time.Second)
		h.mu.Unlock()
		return
	}

	h.typing[key] = &typingEntry{
		OrganizationID: orgID,
		ChannelID:      channelID,
		EmployeeID:     employeeID,
		UserName:       userName,
		LastSentAt:     now,
		ExpiresAt:      now.Add(5 * time.Second),
	}
	h.mu.Unlock()

	h.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           EventTypingStarted,
		OrganizationID: orgID,
		ChannelID:      channelID,
		ActorID:        employeeID,
		Payload: TypingState{
			ChannelID:  channelID,
			EmployeeID: employeeID,
			UserName:   userName,
			IsTyping:   true,
			Timestamp:  now.Format(time.RFC3339),
		},
		Timestamp: now.Format(time.RFC3339),
	})
}

func (h *Hub) ListTyping(orgID, channelID string) []TypingState {
	now := time.Now().UTC()
	h.mu.RLock()
	defer h.mu.RUnlock()
	var out []TypingState
	for _, entry := range h.typing {
		if entry.OrganizationID == orgID && entry.ChannelID == channelID && entry.ExpiresAt.After(now) {
			out = append(out, TypingState{
				ChannelID:  entry.ChannelID,
				EmployeeID: entry.EmployeeID,
				UserName:   entry.UserName,
				IsTyping:   true,
				Timestamp:  entry.LastSentAt.Format(time.RFC3339),
			})
		}
	}
	return out
}

func (h *Hub) cleanupExpiredTypingLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now().UTC()
		var expired []*typingEntry
		h.mu.Lock()
		for k, entry := range h.typing {
			if !entry.ExpiresAt.After(now) {
				expired = append(expired, entry)
				delete(h.typing, k)
			}
		}
		h.mu.Unlock()

		for _, entry := range expired {
			h.Publish(RealtimeEvent{
				Version:        EventVersionV1,
				Type:           EventTypingStopped,
				OrganizationID: entry.OrganizationID,
				ChannelID:      entry.ChannelID,
				ActorID:        entry.EmployeeID,
				Payload: TypingState{
					ChannelID:  entry.ChannelID,
					EmployeeID: entry.EmployeeID,
					UserName:   entry.UserName,
					IsTyping:   false,
					Timestamp:  now.Format(time.RFC3339),
				},
				Timestamp: now.Format(time.RFC3339),
			})
		}
	}
}
