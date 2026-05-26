package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"sync"
	"syscall"
	"time"

	"radar-sonar/ewm22a"
	"radar-sonar/mr20"
	"radar-sonar/readingcache"
	"radar-sonar/repl"
)

const (
	microPythonPort = "/dev/serial/by-id/usb-MicroPython_Board_in_FS_mode_e663b0359723542c-if00"
	mr20Port        = "/dev/serial/by-id/usb-WCH.CN_USB_Single_Serial_0006-if00"
	ewm22aPort      = "/dev/serial/by-id/usb-1a86_USB_Serial-if00-port0"
	httpAddr        = "0.0.0.0:8080"
	pollInterval    = 1 * time.Second
	alertPayload    = "SONAR_OVERRANGE_TO_NORMAL_ALERT"
	alertQueueSize  = 8

	loRaAddress   = 0xFFFF
	loRaChannel   = 0
	loRaNetworkID = 0
)

type replClient struct {
	mu  sync.Mutex
	raw *repl.MicroPythonREPL
}

type radarState struct {
	mu       sync.RWMutex
	obstacle string
	targets  []readingcache.RadarTarget
	lastErr  string
}

func newRadarState() *radarState {
	return &radarState{
		obstacle: "left",
		targets:  []readingcache.RadarTarget{},
	}
}

func (r *radarState) set(obstacle string, targets []readingcache.RadarTarget, lastErr string) {
	r.mu.Lock()
	r.obstacle = obstacle
	r.targets = targets
	r.lastErr = lastErr
	r.mu.Unlock()
}

func (r *radarState) get() (string, []readingcache.RadarTarget, string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]readingcache.RadarTarget, len(r.targets))
	copy(out, r.targets)
	return r.obstacle, out, r.lastErr
}

func (r *replClient) exec(code string) (string, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.raw.ExecRaw(code)
}

func (r *replClient) close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.raw.Close()
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Sonar board is temporarily unavailable; keep running with radar-only data.

	radar, err := mr20.New(mr20Port, mr20.DefaultBaud, 300*time.Millisecond, false)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if cerr := radar.Close(); cerr != nil {
			log.Printf("close mr20: %v", cerr)
		}
	}()

	alertClient, err := openAlertClient()
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if cerr := alertClient.Close(); cerr != nil {
			log.Printf("close ewm22a: %v", cerr)
		}
	}()

	state := readingcache.New(readingcache.Snapshot{LastError: "not yet polled"})
	radarData := newRadarState()

	alerts := make(chan string, alertQueueSize)
	go alertLoop(ctx, alertClient, alerts)
	go radarLoop(ctx, radar, radarData)
	go pollLoopNoSonar(ctx, radarData, state)

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		cleanPath := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
		switch cleanPath {
		case "/debug":
			http.ServeFile(w, r, "static/debug.html")
			return
		case "/", "/status", "/detailed", "/status/detailed":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(state.Get())
			return
		default:
			http.NotFound(w, r)
			return
		}
	})

	srv := &http.Server{
		Addr:              httpAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on http://%s", httpAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func openClient() (*replClient, error) {
	raw, err := repl.New(microPythonPort, repl.DefaultBaud, repl.DefaultTimeout, false)
	if err != nil {
		return nil, err
	}
	if err := raw.EnterRawREPL(); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return &replClient{raw: raw}, nil
}

func openAlertClient() (*ewm22a.EWM22A, error) {
	client, err := ewm22a.Open(ewm22aPort, ewm22a.DefaultOptions())
	if err != nil {
		return nil, err
	}

	if err := configureLoRa(client); err != nil {
		log.Printf("warning: LoRa startup AT config failed; continuing without reconfiguration: %v", err)
	}
	return client, nil
}

func configureLoRa(client *ewm22a.EWM22A) error {
	if _, err := client.SetModeAndReopen(ewm22a.ModeConfig); err != nil {
		return fmt.Errorf("set LoRa config mode: %w", err)
	}
	if _, err := client.SetAddress(loRaAddress); err != nil {
		return fmt.Errorf("set LoRa address: %w", err)
	}
	if _, err := client.SetChannel(loRaChannel); err != nil {
		return fmt.Errorf("set LoRa channel: %w", err)
	}
	if _, err := client.SetNetworkID(loRaNetworkID); err != nil {
		return fmt.Errorf("set LoRa network ID: %w", err)
	}
	if _, err := client.SetModeAndReopen(ewm22a.ModeUARTLoRaBLE); err != nil {
		return fmt.Errorf("set LoRa transparent mode: %w", err)
	}
	return nil
}

func pollLoop(ctx context.Context, client *replClient, radarData *radarState, alerts chan<- string, state *readingcache.Cache) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var (
		havePreviousStatus bool
		previousStatus     string
	)

	for {
		currentStatus, ok := updateOnce(client, radarData, state)
		if ok {
			if havePreviousStatus && previousStatus == "overrange" && currentStatus == "normal" {
				queueAlert(alerts)
			}
			previousStatus = currentStatus
			havePreviousStatus = true
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func pollLoopNoSonar(ctx context.Context, radarData *radarState, state *readingcache.Cache) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		obstacle, radarTargets, radarErr := radarData.get()
		state.Set(readingcache.Snapshot{
			Height:    0,
			Obstacle:  obstacle,
			Sonar:     readingcache.SonarData{HeightMM: 0, Status: "unknown"},
			Radar:     readingcache.RadarData{TargetCount: len(radarTargets), Targets: radarTargets},
			UpdatedAt: time.Now().UTC(),
			LastError: radarErr,
		})

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func updateOnce(client *replClient, radarData *radarState, state *readingcache.Cache) (string, bool) {
	code := strings.Join([]string{
		"import json",
		"print(json.dumps({\"height\": get_height(), \"sonar_status\": get_sonar_status()}))",
	}, "\n")

	stdout, stderr, err := client.exec(code)
	if err != nil {
		state.Set(readingcache.Snapshot{
			UpdatedAt: time.Now().UTC(),
			LastError: fmt.Sprintf("exec: %v", err),
		})
		log.Printf("poll error: %v", err)
		return "", false
	}
	if stderr != "" {
		log.Printf("device stderr: %s", strings.TrimSpace(stderr))
	}

	var payload struct {
		Height      float64 `json:"height"`
		SonarStatus string  `json:"sonar_status"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &payload); err != nil {
		state.Set(readingcache.Snapshot{
			UpdatedAt: time.Now().UTC(),
			LastError: fmt.Sprintf("decode: %v", err),
		})
		log.Printf("poll decode error: %v; raw=%q", err, stdout)
		return "", false
	}

	status := strings.TrimSpace(payload.SonarStatus)
	if status == "" {
		status = "unknown"
	}
	obstacle, radarTargets, radarErr := radarData.get()
	lastErr := radarErr

	state.Set(readingcache.Snapshot{
		Height:    payload.Height,
		Obstacle:  obstacle,
		Sonar:     readingcache.SonarData{HeightMM: payload.Height, Status: status},
		Radar:     readingcache.RadarData{TargetCount: len(radarTargets), Targets: radarTargets},
		UpdatedAt: time.Now().UTC(),
		LastError: lastErr,
	})
	return status, true
}

func radarLoop(ctx context.Context, radar *mr20.MR20, state *radarState) {
	var (
		targets  []readingcache.RadarTarget
		obstacle = "left"
		measSeen uint16
	)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		frame, err := radar.ReadFrame()
		if err != nil {
			if errors.Is(err, mr20.ErrReadTimeout) {
				continue
			}
			state.set(obstacle, targets, fmt.Sprintf("mr20: %v", err))
			continue
		}

		if status, ok := frame.ObjectStatus(); ok {
			if status.MeasurementCount != measSeen {
				measSeen = status.MeasurementCount
				targets = targets[:0]
			}
		}

		if t, ok := frame.Target(); ok {
			rt := readingcache.RadarTarget{
				XMM:          int(math.Round(t.LateralDistanceM * 1000)),
				YMM:          int(math.Round(t.LongitudinalDistanceM * 1000)),
				SpeedCMS:     int(math.Round(t.LongitudinalVelocityMPS * 100)),
				ResolutionMM: 0,
			}
			targets = append(targets, rt)

			nearest := targets[0]
			nearestDist := math.Hypot(float64(nearest.XMM), float64(nearest.YMM))
			for _, candidate := range targets[1:] {
				dist := math.Hypot(float64(candidate.XMM), float64(candidate.YMM))
				if dist < nearestDist {
					nearest = candidate
					nearestDist = dist
				}
			}
			if nearest.XMM >= 0 {
				obstacle = "right"
			} else {
				obstacle = "left"
			}
		}

		out := make([]readingcache.RadarTarget, len(targets))
		copy(out, targets)
		state.set(obstacle, out, "")
	}
}

func queueAlert(alerts chan<- string) {
	message := fmt.Sprintf("%s %s", time.Now().Format("2006-01-02 15:04:05"), alertPayload)
	select {
	case alerts <- message:
	default:
		log.Printf("alert queue full, dropping: %s", message)
	}
}

func alertLoop(ctx context.Context, client *ewm22a.EWM22A, alerts <-chan string) {
	for {
		select {
		case <-ctx.Done():
			return
		case message := <-alerts:
			if err := client.SendString(message); err != nil {
				log.Printf("alert send error: %v", err)
				continue
			}
			log.Printf("alert sent: %s", message)
		}
	}
}
