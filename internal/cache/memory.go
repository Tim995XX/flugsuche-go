package cache

import (
	"context"
	"sync"
	"time"
)

// Memory ist ein In-Memory-Store mit TTL und Obergrenze für die Anzahl der Einträge.
//
// Abgelaufene Einträge werden beim Lesen und – sobald der Speicher voll ist –
// beim Schreiben entfernt; es läuft keine Hintergrund-Goroutine. Ist der Store
// auch danach noch voll, wird ein beliebiger Eintrag verdrängt (einfacher als LRU
// und für einen Kurzzeit-Cache ausreichend).
type Memory struct {
	mu         sync.Mutex
	entries    map[string]entry
	maxEntries int
	now        func() time.Time // austauschbar für Tests
}

type entry struct {
	val     []byte
	expires time.Time
}

// NewMemory erzeugt einen In-Memory-Store mit höchstens maxEntries Einträgen.
func NewMemory(maxEntries int) *Memory {
	if maxEntries < 1 {
		maxEntries = 1
	}
	return &Memory{entries: make(map[string]entry), maxEntries: maxEntries, now: time.Now}
}

func (m *Memory) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.entries[key]
	if !ok {
		return nil, false, nil
	}
	if !m.now().Before(e.expires) {
		delete(m.entries, key)
		return nil, false, nil
	}
	return e.val, true, nil
}

func (m *Memory) Set(_ context.Context, key string, val []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	if _, exists := m.entries[key]; !exists && len(m.entries) >= m.maxEntries {
		for k, e := range m.entries {
			if !now.Before(e.expires) {
				delete(m.entries, k)
			}
		}
		for k := range m.entries {
			if len(m.entries) < m.maxEntries {
				break
			}
			delete(m.entries, k)
		}
	}
	// Kopie, damit spätere Änderungen am Slice des Aufrufers den Cache nicht verändern.
	m.entries[key] = entry{val: append([]byte(nil), val...), expires: now.Add(ttl)}
	return nil
}

// Len liefert die Anzahl der gespeicherten (auch abgelaufenen) Einträge.
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}
