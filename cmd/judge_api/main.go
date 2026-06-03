// judge_api is the HTTP service that accepts contestant submissions,
// queues them for judging, and exposes run results.
//
// Endpoints:
//
//	POST   /contestants                  Register a contestant
//	GET    /contestants                  List all contestants
//	GET    /contestants/{id}             Get a contestant
//
//	POST   /submissions                  Submit an image for judging
//	GET    /submissions/{id}             Get a submission
//	GET    /contestants/{id}/submissions List submissions for a contestant
//
//	GET    /runs/{id}                    Get a run result
//	GET    /contestants/{id}/runs        List recent runs for a contestant
//
//	GET    /healthz                      Health check
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"time"

	"github.com/iicpc/platform/internal/judge"
	kafkapub "github.com/iicpc/platform/internal/kafka"
	"github.com/iicpc/platform/internal/registry"
	"github.com/redis/go-redis/v9"
)

func main() {
	var (
		addr         = flag.String("addr", ":8081", "http listen addr")
		redisAddr    = flag.String("redis", envOr("REDIS_ADDR", "localhost:6379"), "redis addr")
		kafkaBrokers = flag.String("kafka", envOr("KAFKA_BROKERS", ""), "kafka brokers csv (optional)")
		kafkaTopic   = flag.String("kafka-topic", envOr("KAFKA_TOPIC", "metrics"), "kafka topic")
		sandboxPort  = flag.Int("sandbox-port", 50052, "host port for contestant sandbox containers")
		botCount     = flag.Int("bots", 50, "bot fleet size")
		botOps       = flag.Int("ops", 200, "orders/sec per bot")
		botDuration  = flag.Int("duration", 60, "run duration in seconds")
		sandboxMem   = flag.String("sandbox-mem", "512m", "sandbox memory limit")
		sandboxCPUs  = flag.Int("sandbox-cpus", 1, "sandbox CPU limit (integer cores)")
		sandboxPIDs  = flag.Int64("sandbox-pids", 50, "sandbox PID limit")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	rdb := redis.NewClient(&redis.Options{Addr: *redisAddr})
	defer rdb.Close()

	reg := registry.New(rdb)

	judgeConfig := judge.Config{
		SandboxMemory:  *sandboxMem,
		SandboxCPUs:    *sandboxCPUs,
		SandboxPIDs:    *sandboxPIDs,
		SandboxPort:    *sandboxPort,
		BotCount:       *botCount,
		OrdersPerSec:   *botOps,
		Duration:       time.Duration(*botDuration) * time.Second,
		Symbol:         "AAPL",
		KafkaBrokers:   kafkapub.BrokersFromCSV(*kafkaBrokers),
		KafkaTopic:     *kafkaTopic,
		RedisAddr:      *redisAddr,
	}

	eng, err := judge.New(judgeConfig, rdb)
	if err != nil {
		log.Printf("warn: judge engine unavailable (Docker not reachable): %v", err)
		log.Printf("warn: submission judging will be disabled; API still serves registry endpoints")
		eng = nil
	}

	h := &handler{reg: reg, eng: eng, rdb: rdb}
	h.queue = make(chan judge.Submission, maxQueue)
	go h.judgeWorker(ctx)

	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	// Contestants
	mux.HandleFunc("POST /contestants", h.registerContestant)
	mux.HandleFunc("GET /contestants", h.listContestants)
	mux.HandleFunc("GET /contestants/{id}", h.getContestant)
	mux.HandleFunc("GET /contestants/{id}/submissions", h.listSubmissions)
	mux.HandleFunc("GET /contestants/{id}/runs", h.listRuns)

	// Submissions
	mux.HandleFunc("POST /submissions", h.createSubmission)
	mux.HandleFunc("GET /submissions/{id}", h.getSubmission)

	// Runs
	mux.HandleFunc("GET /runs/{id}", h.getRun)

	// Admin
	mux.HandleFunc("DELETE /leaderboard", h.resetLeaderboard)
	mux.HandleFunc("GET /admin/queue", h.getQueueStatus)
	mux.HandleFunc("DELETE /admin/runs", h.clearRuns)
	mux.HandleFunc("GET /admin/health", h.deepHealth)

	srv := &http.Server{
		Addr:    *addr,
		Handler: corsMiddleware(mux),
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Printf("judge_api listening on %s (redis=%s)", *addr, *redisAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}
}

// handler holds shared dependencies for all HTTP handlers.
type handler struct {
	reg     *registry.Registry
	eng     *judge.Engine // may be nil if Docker is unavailable
	rdb     *redis.Client
	queue   chan judge.Submission // buffered, capacity = maxQueue
	pending atomic.Int64
	running atomic.Int64
}

const maxQueue = 20

// ── Contestants ───────────────────────────────────────────────────────────────

func (h *handler) registerContestant(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.ID == "" || body.DisplayName == "" {
		httpErr(w, http.StatusBadRequest, "id and display_name are required")
		return
	}
	if !isValidID(body.ID) {
		httpErr(w, http.StatusBadRequest, "id must be alphanumeric with hyphens/underscores, max 64 chars")
		return
	}

	c := registry.Contestant{
		ID:           body.ID,
		DisplayName:  body.DisplayName,
		RegisteredAt: time.Now().UTC(),
	}
	if err := h.reg.RegisterContestant(r.Context(), c); err != nil {
		if errors.Is(err, registry.ErrAlreadyExists) {
			httpErr(w, http.StatusConflict, err.Error())
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResp(w, http.StatusCreated, c)
}

func (h *handler) listContestants(w http.ResponseWriter, r *http.Request) {
	cs, err := h.reg.ListContestants(r.Context())
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResp(w, http.StatusOK, cs)
}

func (h *handler) getContestant(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := h.reg.GetContestant(r.Context(), id)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			httpErr(w, http.StatusNotFound, err.Error())
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResp(w, http.StatusOK, c)
}

// ── Submissions ───────────────────────────────────────────────────────────────

func (h *handler) createSubmission(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ContestantID string `json:"contestant_id"`
		ImageTag     string `json:"image_tag"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.ContestantID == "" || body.ImageTag == "" {
		httpErr(w, http.StatusBadRequest, "contestant_id and image_tag are required")
		return
	}

	// Verify contestant exists.
	if _, err := h.reg.GetContestant(r.Context(), body.ContestantID); err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			httpErr(w, http.StatusNotFound, "contestant not found: "+body.ContestantID)
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	sub := judge.Submission{
		ID:           fmt.Sprintf("%s-%d", body.ContestantID, time.Now().UnixNano()),
		ContestantID: body.ContestantID,
		ImageTag:     body.ImageTag,
		SubmittedAt:  time.Now().UTC(),
	}
	if err := h.reg.SaveSubmission(r.Context(), sub); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Enqueue for judging.
	if h.eng != nil {
		select {
		case h.queue <- sub:
			h.pending.Add(1)
		default:
			httpErr(w, http.StatusServiceUnavailable, "judge queue full, try again later")
			return
		}
	} else {
		log.Printf("judge engine unavailable; submission %s saved but not judged", sub.ID)
	}

	jsonResp(w, http.StatusAccepted, sub)
}

// judgeWorker processes submissions sequentially from the queue.
func (h *handler) judgeWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case sub := <-h.queue:
			h.pending.Add(-1)
			h.running.Add(1)
			runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			if _, err := h.eng.Run(runCtx, sub); err != nil {
				log.Printf("judge run failed for submission %s: %v", sub.ID, err)
			}
			cancel()
			h.running.Add(-1)
		}
	}
}

func (h *handler) getSubmission(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s, err := h.reg.GetSubmission(r.Context(), id)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			httpErr(w, http.StatusNotFound, err.Error())
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResp(w, http.StatusOK, s)
}

func (h *handler) listSubmissions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	subs, err := h.reg.ListSubmissions(r.Context(), id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResp(w, http.StatusOK, subs)
}

// ── Runs ──────────────────────────────────────────────────────────────────────

func (h *handler) getRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := h.reg.GetRunResult(r.Context(), id)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			httpErr(w, http.StatusNotFound, err.Error())
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResp(w, http.StatusOK, res)
}

func (h *handler) listRuns(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	runs, err := h.reg.ListRunsForContestant(r.Context(), id, 20)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResp(w, http.StatusOK, runs)
}

// ── Admin ─────────────────────────────────────────────────────────────────────

func (h *handler) resetLeaderboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// Delete the sorted set.
	if err := h.rdb.Del(ctx, "leaderboard").Err(); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Delete all leaderboard:* hash keys.
	var cursor uint64
	for {
		keys, next, err := h.rdb.Scan(ctx, cursor, "leaderboard:*", 100).Result()
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if len(keys) > 0 {
			if err := h.rdb.Del(ctx, keys...).Err(); err != nil {
				httpErr(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	jsonResp(w, http.StatusOK, map[string]string{"status": "leaderboard reset"})
}

func (h *handler) getQueueStatus(w http.ResponseWriter, r *http.Request) {
	jsonResp(w, http.StatusOK, map[string]int64{
		"pending": h.pending.Load(),
		"running": h.running.Load(),
	})
}

func (h *handler) clearRuns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	patterns := []string{"run:*", "runs:*"}
	for _, pattern := range patterns {
		var cursor uint64
		for {
			keys, next, err := h.rdb.Scan(ctx, cursor, pattern, 100).Result()
			if err != nil {
				httpErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			if len(keys) > 0 {
				if err := h.rdb.Del(ctx, keys...).Err(); err != nil {
					httpErr(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	jsonResp(w, http.StatusOK, map[string]string{"status": "runs cleared"})
}

func (h *handler) deepHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	result := map[string]string{}

	// Redis ping.
	if err := h.rdb.Ping(ctx).Err(); err != nil {
		result["redis"] = "error: " + err.Error()
	} else {
		result["redis"] = "ok"
	}

	// Docker ping (via judge engine).
	if h.eng != nil {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err := h.eng.PingDocker(pingCtx); err != nil {
			result["docker"] = "error: " + err.Error()
		} else {
			result["docker"] = "ok"
		}
	} else {
		result["docker"] = "unavailable"
	}

	if result["redis"] == "ok" && (result["docker"] == "ok" || result["docker"] == "unavailable") {
		result["status"] = "ok"
	} else {
		result["status"] = "degraded"
	}

	jsonResp(w, http.StatusOK, result)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func jsonResp(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// isValidID checks that an ID is safe to use as a Redis key segment.
func isValidID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
