package chat

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func drainInitialEvents(sub *Subscriber) {
	time.Sleep(10 * time.Millisecond)
	for {
		select {
		case <-sub.Events:
		default:
			return
		}
	}
}

func TestHubSubscriptionAndTenantIsolation(t *testing.T) {
	hub := NewHub(nil, nil, func(orgID, channelID, employeeID string, isAdmin bool) bool {
		return true
	})

	orgA := "org-alpha"
	orgB := "org-beta"

	subA1, unsubA1 := hub.Subscribe(orgA, "emp-a1", "user-a1", false)
	defer unsubA1()

	subA2, unsubA2 := hub.Subscribe(orgA, "emp-a2", "user-a2", false)
	defer unsubA2()

	subB, unsubB := hub.Subscribe(orgB, "emp-b1", "user-b1", false)
	defer unsubB()

	drainInitialEvents(subA1)
	drainInitialEvents(subA2)
	drainInitialEvents(subB)

	if !hub.IsOnline(orgA, "emp-a1") {
		t.Fatal("expected emp-a1 to be online")
	}
	if hub.IsOnline(orgB, "emp-a1") {
		t.Fatal("expected emp-a1 NOT to be online in orgB")
	}

	// Publish event to Org A
	hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           EventMessageCreated,
		OrganizationID: orgA,
		ChannelID:      "ch-1",
		ActorID:        "emp-a1",
		Payload:        map[string]string{"text": "hello org A"},
	})

	// Sub A1 and A2 should receive the event
	select {
	case ev := <-subA1.Events:
		if ev.OrganizationID != orgA || ev.Type != EventMessageCreated {
			t.Fatalf("unexpected event for subA1: %+v", ev)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for subA1 event")
	}

	select {
	case ev := <-subA2.Events:
		if ev.OrganizationID != orgA || ev.Type != EventMessageCreated {
			t.Fatalf("unexpected event for subA2: %+v", ev)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for subA2 event")
	}

	// Sub B must NOT receive any event (strict tenant isolation)
	select {
	case ev := <-subB.Events:
		t.Fatalf("cross-tenant data leakage detected: Sub B received event for Org A: %+v", ev)
	case <-time.After(100 * time.Millisecond):
		// Expected: no event received
	}
}

func TestHubChannelAuthorizationFiltering(t *testing.T) {
	// Only emp-allowed is authorized for secret-channel
	hub := NewHub(nil, nil, func(orgID, channelID, employeeID string, isAdmin bool) bool {
		if channelID == "secret-channel" {
			return employeeID == "emp-allowed" || isAdmin
		}
		return true
	})

	org := "org-sec"
	subAllowed, unsub1 := hub.Subscribe(org, "emp-allowed", "user-1", false)
	defer unsub1()

	subBlocked, unsub2 := hub.Subscribe(org, "emp-blocked", "user-2", false)
	defer unsub2()

	drainInitialEvents(subAllowed)
	drainInitialEvents(subBlocked)

	hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           EventMessageCreated,
		OrganizationID: org,
		ChannelID:      "secret-channel",
		ActorID:        "emp-allowed",
		Payload:        map[string]string{"secret": "confidential"},
	})

	select {
	case ev := <-subAllowed.Events:
		if ev.ChannelID != "secret-channel" || ev.Type != EventMessageCreated {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for authorized subscriber event")
	}

	select {
	case ev := <-subBlocked.Events:
		t.Fatalf("unauthorized subscriber received secret channel event: %+v", ev)
	case <-time.After(100 * time.Millisecond):
		// Expected: blocked subscriber receives nothing
	}
}

func TestHubTypingThrottlingAndExpiration(t *testing.T) {
	hub := NewHub(nil, nil, nil)
	org := "org-typing"
	sub, unsub := hub.Subscribe(org, "emp-listener", "user-l", false)
	defer unsub()

	drainInitialEvents(sub)

	// 1. First typing event starts typing
	hub.SetTyping(org, "ch-1", "emp-writer", "Alice", true)

	select {
	case ev := <-sub.Events:
		if ev.Type != EventTypingStarted {
			t.Fatalf("expected typing.started, got %s", ev.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for typing.started")
	}

	// 2. Rapid typing within 2 seconds should be throttled (no duplicate broadcast)
	hub.SetTyping(org, "ch-1", "emp-writer", "Alice", true)
	hub.SetTyping(org, "ch-1", "emp-writer", "Alice", true)

	select {
	case ev := <-sub.Events:
		t.Fatalf("expected rapid typing to be throttled, but received: %s", ev.Type)
	case <-time.After(150 * time.Millisecond):
		// Expected: throttled
	}

	// 3. Explicit typing stop emits typing.stopped immediately
	hub.SetTyping(org, "ch-1", "emp-writer", "Alice", false)

	select {
	case ev := <-sub.Events:
		if ev.Type != EventTypingStopped {
			t.Fatalf("expected typing.stopped, got %s", ev.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for typing.stopped")
	}
}

func TestHubHighConcurrencyBroadcast(t *testing.T) {
	hub := NewHub(nil, nil, func(orgID, channelID, employeeID string, isAdmin bool) bool {
		return true
	})

	org := "org-load"
	const subscriberCount = 100
	const messagesPerSender = 50

	unsubs := make([]func(), subscriberCount)
	subs := make([]*Subscriber, subscriberCount)

	for i := 0; i < subscriberCount; i++ {
		s, unsub := hub.Subscribe(org, "emp-"+newUUID(), "user-"+newUUID(), false)
		subs[i] = s
		unsubs[i] = unsub
	}
	defer func() {
		for _, u := range unsubs {
			u()
		}
	}()

	var totalReceived atomic.Int64
	var wg sync.WaitGroup

	// Consumers
	stopChan := make(chan struct{})
	for _, s := range subs {
		wg.Add(1)
		go func(sub *Subscriber) {
			defer wg.Done()
			for {
				select {
				case <-stopChan:
					return
				case _, ok := <-sub.Events:
					if !ok {
						return
					}
					totalReceived.Add(1)
				}
			}
		}(s)
	}

	// Concurrent publishers
	var pubWg sync.WaitGroup
	start := time.Now()
	for p := 0; p < 5; p++ {
		pubWg.Add(1)
		go func(senderIdx int) {
			defer pubWg.Done()
			for m := 0; m < messagesPerSender; m++ {
				hub.Publish(RealtimeEvent{
					Version:        EventVersionV1,
					Type:           EventMessageCreated,
					OrganizationID: org,
					ChannelID:      "ch-perf",
					ActorID:        "emp-perf",
					Payload:        map[string]int{"sender": senderIdx, "msg": m},
				})
			}
		}(p)
	}

	pubWg.Wait()
	elapsed := time.Since(start)

	time.Sleep(200 * time.Millisecond)
	close(stopChan)
	wg.Wait()

	totalSent := 5 * messagesPerSender
	t.Logf("High concurrency test: %d messages dispatched to %d subscribers in %v (total deliveries: %d)",
		totalSent, subscriberCount, elapsed, totalReceived.Load())

	if totalReceived.Load() == 0 {
		t.Fatal("expected deliveries, received 0")
	}
}
