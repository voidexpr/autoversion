package main

import (
	"time"
)

// EventType represents the type of file system event
type EventType int

const (
	EventCreated EventType = iota
	EventModified
	EventDeleted
)

// Event represents a file system event with first and last timestamps
type Event struct {
	Path           string
	Action         EventType
	FirstTimestamp time.Time
	LastTimestamp  time.Time
}

// EventBuffer buffers file system events and collapses them
type EventBuffer struct {
	events map[string]Event // filepath -> event with first/last timestamps
}

const maxEventAge = 5 * time.Minute

// NewEventBuffer creates a new event buffer
func NewEventBuffer() *EventBuffer {
	return &EventBuffer{
		events: make(map[string]Event),
	}
}

// Add adds an event to the buffer (updates last timestamp and action)
func (b *EventBuffer) Add(path string, eventType EventType) {
	now := time.Now()
	if existing, ok := b.events[path]; ok {
		// Update existing event - keep first timestamp, update last
		existing.Action = eventType
		existing.LastTimestamp = now
		b.events[path] = existing
	} else {
		// New event
		b.events[path] = Event{
			Path:           path,
			Action:         eventType,
			FirstTimestamp: now,
			LastTimestamp:  now,
		}
	}
}

// Flush returns and removes events that should be processed:
// - Events with no activity for olderThan duration, OR
// - Events where first timestamp is older than maxEventAge (5 minutes)
func (b *EventBuffer) Flush(olderThan time.Duration) []Event {
	now := time.Now()
	cutoffRecent := now.Add(-olderThan)
	var result []Event

	for path, event := range b.events {
		shouldFlush := false

		// Flush if last activity was longer ago than olderThan
		if event.LastTimestamp.Before(cutoffRecent) {
			shouldFlush = true
		}

		// Flush if first timestamp is older than maxEventAge (continuously updated file)
		if now.Sub(event.FirstTimestamp) > maxEventAge {
			shouldFlush = true
		}

		if shouldFlush {
			result = append(result, event)
			delete(b.events, path)
		}
	}

	return result
}

// Size returns the number of buffered events
func (b *EventBuffer) Size() int {
	return len(b.events)
}
