package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"sync"
	"syscall"
	"time"

	"radar-sonar/ewm22a"
	"radar-sonar/readingcache"
	"radar-sonar/repl"
)

const (
	microPythonPort = "/dev/serial/by-id/usb-MicroPython_Board_in_FS_mode_e663b0359723542c-if00"
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

	client, err := openClient()
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if cerr := client.close(); cerr != nil {
			log.Printf("close repl: %v", cerr)
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

	alerts := make(chan string, alertQueueSize)
	go alertLoop(ctx, alertClient, alerts)
	go pollLoop(ctx, client, alerts, state)

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

func pollLoop(ctx context.Context, client *replClient, alerts chan<- string, state *readingcache.Cache) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var (
		havePreviousStatus bool
		previousStatus     string
	)

	for {
		currentStatus, ok := updateOnce(client, state)
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

func updateOnce(client *replClient, state *readingcache.Cache) (string, bool) {
	code := strings.Join([]string{
		"import json",
		"print(json.dumps({\"height\": get_height(), \"sonar_status\": get_sonar_status(), \"obstacle\": get_obstacle(), \"radar_targets\": get_radar_targets()}))",
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
		Height       float64                    `json:"height"`
		SonarStatus  string                     `json:"sonar_status"`
		Obstacle     string                     `json:"obstacle"`
		RadarTargets []readingcache.RadarTarget `json:"radar_targets"`
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

	state.Set(readingcache.Snapshot{
		Height:    payload.Height,
		Obstacle:  payload.Obstacle,
		Sonar:     readingcache.SonarData{HeightMM: payload.Height, Status: status},
		Radar:     readingcache.RadarData{TargetCount: len(payload.RadarTargets), Targets: payload.RadarTargets},
		UpdatedAt: time.Now().UTC(),
		LastError: "",
	})
	return status, true
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
