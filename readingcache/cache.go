package readingcache

import (
	"sync"
	"time"
)

// Snapshot is the latest observed sensor reading.
type Snapshot struct {
	Height    float64   `json:"height"`
	Obstacle  string    `json:"obstacle"`
	Sonar     SonarData `json:"sonar"`
	Radar     RadarData `json:"radar"`
	UpdatedAt time.Time `json:"updated_at"`
	LastError string    `json:"last_error,omitempty"`
}

// SonarData is the structured sonar payload returned by the HTTP API.
type SonarData struct {
	HeightMM float64 `json:"height_mm"`
	Status   string  `json:"status"`
}

// RadarTarget is one parsed LD2450 target.
type RadarTarget struct {
	XMM          int `json:"x_mm"`
	YMM          int `json:"y_mm"`
	SpeedCMS     int `json:"speed_cms"`
	ResolutionMM int `json:"resolution_mm"`
}

// RadarData is the structured radar payload returned by the HTTP API.
type RadarData struct {
	TargetCount int           `json:"target_count"`
	Targets     []RadarTarget `json:"targets"`
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
