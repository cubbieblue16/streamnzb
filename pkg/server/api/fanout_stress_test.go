package api

import (
	"sync"
	"testing"
	"time"
)

// These tests validate Fix #5's fan-out safety. broadcastStats, broadcastLogs and
// BroadcastNZBAttemptsUpdate share the same pattern: iterate s.clients under
// clientsMu and send non-blocking (select/default). RemoveClient deletes from the
// map under the lock, then closes client.send only AFTER releasing the lock — so a
// concurrent broadcast can never send on a closed channel. BroadcastNZBAttemptsUpdate
// is the representative fan-out loop here (collectStats requires a full Server, but
// the channel mechanics it drives are identical).

// TestBroadcastNonBlockingDropsOnFullChannel proves a slow/stuck client cannot
// block the fan-out: when its send buffer is full the broadcast drops the message
// (hits the default case) and leaves the already-buffered messages intact.
func TestBroadcastNonBlockingDropsOnFullChannel(t *testing.T) {
	s := &Server{clients: make(map[*Client]bool)}
	client := &Client{send: make(chan WSMessage, 2)}
	s.AddClient(client)

	// Saturate the buffer with sentinel messages.
	client.send <- WSMessage{Type: "first"}
	client.send <- WSMessage{Type: "second"}

	done := make(chan struct{})
	go func() {
		s.BroadcastNZBAttemptsUpdate()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("BroadcastNZBAttemptsUpdate blocked on a full client channel")
	}

	if got := len(client.send); got != 2 {
		t.Fatalf("send buffer len = %d, want 2 (dropped message must not evict existing)", got)
	}
	if m := <-client.send; m.Type != "first" {
		t.Fatalf("first buffered = %q, want first", m.Type)
	}
	if m := <-client.send; m.Type != "second" {
		t.Fatalf("second buffered = %q, want second", m.Type)
	}
}

// TestRemoveClientClosesSend confirms RemoveClient both unregisters the client and
// closes its send channel exactly once.
func TestRemoveClientClosesSend(t *testing.T) {
	s := &Server{clients: make(map[*Client]bool)}
	client := &Client{send: make(chan WSMessage, 1)}
	s.AddClient(client)

	s.clientsMu.Lock()
	if !s.clients[client] {
		s.clientsMu.Unlock()
		t.Fatal("client not registered after AddClient")
	}
	s.clientsMu.Unlock()

	s.RemoveClient(client)

	s.clientsMu.Lock()
	_, present := s.clients[client]
	s.clientsMu.Unlock()
	if present {
		t.Fatal("client still registered after RemoveClient")
	}
	if _, ok := <-client.send; ok {
		t.Fatal("send channel should be closed after RemoveClient")
	}
}

// TestStatsFanoutConcurrentAddRemove is the core race test: a continuous broadcaster
// fans out while many clients churn through AddClient -> drain -> RemoveClient. With
// a live reader per client and the broadcaster sending under the lock, the only way
// this stays panic-free is the delete-under-lock-then-close-after-unlock ordering in
// RemoveClient. Run under -race it also proves the map access is fully synchronized.
func TestStatsFanoutConcurrentAddRemove(t *testing.T) {
	s := &Server{clients: make(map[*Client]bool)}

	stop := make(chan struct{})
	var broadcasters sync.WaitGroup
	for b := 0; b < 4; b++ {
		broadcasters.Add(1)
		go func() {
			defer broadcasters.Done()
			for {
				select {
				case <-stop:
					return
				default:
					s.BroadcastNZBAttemptsUpdate()
				}
			}
		}()
	}

	const workers = 64
	const cycles = 200
	var churn sync.WaitGroup
	for w := 0; w < workers; w++ {
		churn.Add(1)
		go func() {
			defer churn.Done()
			for i := 0; i < cycles; i++ {
				client := &Client{send: make(chan WSMessage, 8)}
				s.AddClient(client)

				// Live reader: drains until RemoveClient closes the channel.
				var reader sync.WaitGroup
				reader.Add(1)
				go func() {
					defer reader.Done()
					for range client.send {
					}
				}()

				s.RemoveClient(client)
				reader.Wait()
			}
		}()
	}

	churn.Wait()
	close(stop)
	broadcasters.Wait()

	s.clientsMu.Lock()
	remaining := len(s.clients)
	s.clientsMu.Unlock()
	if remaining != 0 {
		t.Fatalf("clients map = %d after churn, want 0 (all removed)", remaining)
	}
}
