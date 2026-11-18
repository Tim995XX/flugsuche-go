package cache

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Memory ist ein In-Memory-Store mit TTL und einer Obergrenze in Bytes.
//
// Die Grenze zählt Bytes, nicht Einträge: Eine Anbieter-Antwort darf bis zu 1 MiB
// groß sein, eine Grenze von "10.000 Einträgen" könnte also ~10 GB bedeuten und
// den Prozess wegen Speichermangel abstürzen lassen.
//
// Abgelaufene Einträge werden beim Lesen und – sobald der Speicher voll ist –
// beim Schreiben entfernt; es läuft keine Hintergrund-Goroutine. Reicht das nicht,
// werden beliebige Einträge verdrängt (einfacher als LRU und für einen
// Kurzzeit-Cache ausreichend).
type Memory struct {
	mu       sync.Mutex
	entries  map[string]entry
	used     int // belegte Bytes (Schlüssel + Wert)
	maxBytes int
	now      func() time.Time // austauschbar für Tests
}

type entry struct {
	val     []byte
	expires time.Time
}

func entrySize(key string, val []byte) int { return len(key) + len(val) }

// NewMemory erzeugt einen In-Memory-Store mit höchstens maxBytes Nutzdaten.
// (Der tatsächliche Speicherverbrauch liegt durch Map-Verwaltung etwas höher.)
func NewMemory(maxBytes int) *Memory {
	if maxBytes < 1 {
		maxBytes = 1
	}
	return &Memory{entries: make(map[string]entry), maxBytes: maxBytes, now: time.Now}
}

func (m *Memory) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.entries[key]
	if !ok {
		return nil, false, nil
	}
	if !m.now().Before(e.expires) {
		m.remove(key, e)
		return nil, false, nil
	}
	return e.val, true, nil
}

func (m *Memory) Set(_ context.Context, key string, val []byte, ttl time.Duration) error {
	size := entrySize(key, val)
	if size > m.maxBytes {
		return fmt.Errorf("eintrag mit %d bytes größer als cache-limit %d", size, m.maxBytes)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if old, exists := m.entries[key]; exists {
		m.remove(key, old)
	}
	if m.used+size > m.maxBytes {
		now := m.now()
		for k, e := range m.entries {
			if !now.Before(e.expires) {
				m.remove(k, e)
			}
		}
		for k, e := range m.entries {
			if m.used+size <= m.maxBytes {
				break
			}
			m.remove(k, e)
		}
	}
	// Kopie, damit spätere Änderungen am Slice des Aufrufers den Cache nicht verändern.
	m.entries[key] = entry{val: append([]byte(nil), val...), expires: m.now().Add(ttl)}
	m.used += size
	return nil
}

// remove löscht einen Eintrag; m.mu muss gehalten werden.
func (m *Memory) remove(key string, e entry) {
	delete(m.entries, key)
	m.used -= entrySize(key, e.val)
}

// Len liefert die Anzahl der gespeicherten (auch abgelaufenen) Einträge.
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

// Bytes liefert die belegten Nutzdaten in Bytes.
func (m *Memory) Bytes() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.used
}
