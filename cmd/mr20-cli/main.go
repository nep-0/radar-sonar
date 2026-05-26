package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"radar-sonar/mr20"
)

type targetView struct {
	ID                      int     `json:"id"`
	LongitudinalDistanceM   float64 `json:"longitudinal_distance_m"`
	LateralDistanceM        float64 `json:"lateral_distance_m"`
	LongitudinalVelocityMPS float64 `json:"longitudinal_velocity_mps"`
	LateralVelocityMPS      float64 `json:"lateral_velocity_mps"`
	DynamicProperty         int     `json:"dynamic_property"`
	RCSDBM2                 float64 `json:"rcs_dbm2"`
	RangeM                  float64 `json:"range_m"`
	AngleRad                float64 `json:"angle_rad"`
	UpdatedAt               string  `json:"updated_at"`
}

type statusSnapshot struct {
	Targets          []targetView `json:"targets"`
	TargetCount      int          `json:"target_count"`
	ObjectCount      int          `json:"object_count"`
	MeasurementCount uint16       `json:"measurement_count"`
	Heartbeat        string       `json:"heartbeat"`
	UpdatedAt        string       `json:"updated_at"`
	LastError        string       `json:"last_error,omitempty"`
}

type cache struct {
	mu   sync.RWMutex
	data statusSnapshot
}

func (c *cache) set(s statusSnapshot) {
	c.mu.Lock()
	c.data = s
	c.mu.Unlock()
}

func (c *cache) get() statusSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.data
}

func main() {
	var (
		port     = flag.String("port", "", "serial port, for example COM3 or /dev/ttyUSB0")
		baud     = flag.Int("baud", mr20.DefaultBaud, "baud rate")
		timeout  = flag.Duration("timeout", mr20.DefaultTimeout, "serial read timeout")
		debug    = flag.Bool("debug", false, "log raw RX bytes")
		httpAddr = flag.String("http", "0.0.0.0:8083", "http listen address")
	)
	flag.Parse()

	if *port == "" {
		log.Fatal("missing -port")
	}

	radar, err := mr20.New(*port, *baud, *timeout, *debug)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if cerr := radar.Close(); cerr != nil {
			log.Printf("close error: %v", cerr)
		}
	}()

	state := &cache{}
	state.set(statusSnapshot{LastError: "not yet polled"})

	go pollLoop(radar, state)

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("cmd/mr20-cli/static"))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		cleanPath := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
		switch cleanPath {
		case "/debug":
			http.ServeFile(w, r, "cmd/mr20-cli/static/debug.html")
			return
		case "/", "/status":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(state.get())
			return
		default:
			http.NotFound(w, r)
		}
	})

	srv := &http.Server{
		Addr:              *httpAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("reading MR20 frames from %s at %d baud", *port, *baud)
	log.Printf("debug ui: http://%s/debug", *httpAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func pollLoop(radar *mr20.MR20, state *cache) {
	targets := map[int]targetView{}
	var (
		objCount   int
		measCount  uint16
		heartbeat  string
		lastErrMsg string
	)

	for {
		frame, err := radar.ReadFrame()
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if err != nil {
			lastErrMsg = err.Error()
			s := buildSnapshot(targets, objCount, measCount, heartbeat, now)
			s.LastError = lastErrMsg
			state.set(s)
			continue
		}
		lastErrMsg = ""

		if status, ok := frame.ObjectStatus(); ok {
			if status.MeasurementCount != measCount {
				// New measurement frame: discard old objects.
				targets = map[int]targetView{}
			}
			objCount = status.Count
			measCount = status.MeasurementCount
		}

		if t, ok := frame.Target(); ok {
			if math.Abs(t.LongitudinalDistanceM) <= 500 && math.Abs(t.LateralDistanceM) <= 500 {
				targets[t.ID] = targetView{
					ID:                      t.ID,
					LongitudinalDistanceM:   t.LongitudinalDistanceM,
					LateralDistanceM:        t.LateralDistanceM,
					LongitudinalVelocityMPS: t.LongitudinalVelocityMPS,
					LateralVelocityMPS:      t.LateralVelocityMPS,
					DynamicProperty:         t.DynamicProperty,
					RCSDBM2:                 t.RCSDBM2,
					RangeM:                  t.RangeM,
					AngleRad:                t.AngleRad,
					UpdatedAt:               now,
				}
			}
		}

		if hb, ok := frame.Heartbeat(); ok {
			heartbeat = fmt.Sprintf("%d.%d.%d", hb.Major, hb.Minor, hb.Patch)
		}

		s := buildSnapshot(targets, objCount, measCount, heartbeat, now)
		s.LastError = lastErrMsg
		state.set(s)
	}
}

func buildSnapshot(targets map[int]targetView, objectCount int, measurementCount uint16, heartbeat string, now string) statusSnapshot {
	out := make([]targetView, 0, len(targets))
	for _, t := range targets {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return statusSnapshot{
		Targets:          out,
		TargetCount:      len(out),
		ObjectCount:      objectCount,
		MeasurementCount: measurementCount,
		Heartbeat:        heartbeat,
		UpdatedAt:        now,
	}
}
