package mockserver

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"radar-sonar/readingcache"
)

// Serve starts a fixed-value radar/sonar HTTP mock server.
func Serve(addr string, obstacle string, height float64) error {
	snapshot := readingcache.Snapshot{
		Height:    height,
		Obstacle:  obstacle,
		UpdatedAt: time.Now().UTC(),
	}

	srv := &http.Server{
		Addr: addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" && r.URL.Path != "/status" {
				http.NotFound(w, r)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(snapshot)
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("mock %s/%0.1fmm listening on http://%s", obstacle, height, addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
