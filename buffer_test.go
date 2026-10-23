package main

import (
	"testing"
	"time"
)

func TestEventBuffer_Add(t *testing.T) {
	buffer := NewEventBuffer()

	buffer.Add("/tmp/file1.txt", EventCreated)
	buffer.Add("/tmp/file2.txt", EventModified)

	if buffer.Size() != 2 {
		t.Errorf("Expected 2 events, got %d", buffer.Size())
	}
}

func TestEventBuffer_DirChangedIsKept(t *testing.T) {
	buffer := NewEventBuffer()

	buffer.Add("/tmp/dir", EventDirChanged)
	buffer.Add("/tmp/dir", EventDeleted)

	events := buffer.Flush(0)
	if len(events) != 1 || events[0].Action != EventDirChanged {
		t.Errorf("Expected one EventDirChanged, got %v", events)
	}
}

func TestEventBuffer_UpdateSameFile(t *testing.T) {
	buffer := NewEventBuffer()

	// Add same file multiple times - should keep only last action
	buffer.Add("/tmp/file.txt", EventCreated)
	time.Sleep(10 * time.Millisecond)
	buffer.Add("/tmp/file.txt", EventModified)
	time.Sleep(10 * time.Millisecond)
	buffer.Add("/tmp/file.txt", EventDeleted)

	if buffer.Size() != 1 {
		t.Errorf("Expected 1 event after updates, got %d", buffer.Size())
	}

	events := buffer.Flush(0)
	if len(events) != 1 {
		t.Fatalf("Expected 1 event in flush, got %d", len(events))
	}

	if events[0].Action != EventDeleted {
		t.Errorf("Expected last action (Deleted), got %v", events[0].Action)
	}

	// Verify timestamps: first should be earlier than last
	if !events[0].FirstTimestamp.Before(events[0].LastTimestamp) {
		t.Errorf("Expected FirstTimestamp before LastTimestamp")
	}
}

func TestEventBuffer_FlushByInactivity(t *testing.T) {
	buffer := NewEventBuffer()

	// Add old event
	buffer.Add("/tmp/old.txt", EventCreated)
	time.Sleep(100 * time.Millisecond)

	// Add new event
	buffer.Add("/tmp/new.txt", EventModified)

	// Flush events with no activity for 50ms
	events := buffer.Flush(50 * time.Millisecond)

	if len(events) != 1 {
		t.Fatalf("Expected 1 inactive event, got %d", len(events))
	}

	if events[0].Path != "/tmp/old.txt" {
		t.Errorf("Expected old.txt, got %s", events[0].Path)
	}

	// New event should still be in buffer
	if buffer.Size() != 1 {
		t.Errorf("Expected 1 event remaining, got %d", buffer.Size())
	}
}

func TestEventBuffer_FlushByMaxAge(t *testing.T) {
	buffer := NewEventBuffer()

	// Add event and manually set first timestamp to be old
	buffer.Add("/tmp/file.txt", EventCreated)
	event := buffer.events["/tmp/file.txt"]
	event.FirstTimestamp = time.Now().Add(-6 * time.Minute) // Older than maxEventAge
	buffer.events["/tmp/file.txt"] = event

	// Flush should return the event even if it was just updated
	events := buffer.Flush(10 * time.Second)

	if len(events) != 1 {
		t.Fatalf("Expected 1 event due to maxEventAge, got %d", len(events))
	}

	if events[0].Path != "/tmp/file.txt" {
		t.Errorf("Expected file.txt, got %s", events[0].Path)
	}

	if buffer.Size() != 0 {
		t.Errorf("Expected empty buffer after flush, got %d", buffer.Size())
	}
}

func TestEventBuffer_ContinuousUpdates(t *testing.T) {
	buffer := NewEventBuffer()

	// Simulate continuous updates to same file
	buffer.Add("/tmp/active.txt", EventCreated)
	firstTime := buffer.events["/tmp/active.txt"].FirstTimestamp

	// Update multiple times
	for i := 0; i < 5; i++ {
		time.Sleep(10 * time.Millisecond)
		buffer.Add("/tmp/active.txt", EventModified)
	}

	// First timestamp should remain the same
	if buffer.events["/tmp/active.txt"].FirstTimestamp != firstTime {
		t.Errorf("FirstTimestamp should not change on updates")
	}

	// Last timestamp should be recent
	lastTime := buffer.events["/tmp/active.txt"].LastTimestamp
	if time.Since(lastTime) > 50*time.Millisecond {
		t.Errorf("LastTimestamp should be recent")
	}

	// Should still be buffered (not old enough)
	events := buffer.Flush(100 * time.Millisecond)
	if len(events) != 0 {
		t.Errorf("Expected no events flushed yet, got %d", len(events))
	}
}

func TestEventBuffer_FlushAll(t *testing.T) {
	buffer := NewEventBuffer()

	buffer.Add("/tmp/file1.txt", EventCreated)
	buffer.Add("/tmp/file2.txt", EventModified)
	buffer.Add("/tmp/file3.txt", EventDeleted)

	// Flush all events (0 duration = flush everything)
	events := buffer.Flush(0)

	if len(events) != 3 {
		t.Errorf("Expected 3 events, got %d", len(events))
	}

	if buffer.Size() != 0 {
		t.Errorf("Expected empty buffer after flush all, got %d", buffer.Size())
	}
}
