package readingcache

import (
	"sync"
	"time"
)

// Snapshot is the latest observed sensor reading.
type Snapshot struct {
	Height    float64   `json:"height"`
	Obstacle  string    `json:"obstacle"`
	UpdatedAt time.Time `json:"updated_at"`
	LastError string    `json:"last_error,omitempty"`
}

// Cache stores the latest sensor reading in memory.
type Cache struct {
	mu   sync.RWMutex
	data Snapshot
}

// New returns a Cache initialized with the provided snapshot.
func New(initial Snapshot) *Cache {
	c := &Cache{}
	c.Set(initial)
	return c
}

// Set replaces the cached reading.
func (c *Cache) Set(s Snapshot) {
	c.mu.Lock()
	c.data = s
	c.mu.Unlock()
}

// Get returns the latest cached reading.
func (c *Cache) Get() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.data
}
