package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/iicpc/platform/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	var (
		addr      = flag.String("addr", ":8080", "http listen addr")
		redisAddr = flag.String("redis", envOr("REDIS_ADDR", "localhost:6379"), "redis addr host:port")
		pgURL     = flag.String("pg", envOr("PG_URL", ""), "timescaledb postgres url (optional, enables /history and /stats endpoints)")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	rdb := redis.NewClient(&redis.Options{Addr: *redisAddr})
	defer rdb.Close()

	// TimescaleDB is optional — history endpoints return 503 if not configured.
	var ts *store.Timescale
	if *pgURL != "" {
		pool, err := pgxpool.New(ctx, *pgURL)
		if err != nil {
			log.Printf("warn: timescaledb connect error: %v — history endpoints disabled", err)
		} else {
			ts = store.NewTimescale(pool)
			defer pool.Close()
			log.Printf("timescaledb connected")
		}
	}

	mux := http.NewServeMux()

	// ── Health ────────────────────────────────────────────────────────────────
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	// ── Leaderboard (Redis) ───────────────────────────────────────────────────
	mux.HandleFunc("/leaderboard", func(w http.ResponseWriter, r *http.Request) {
		enableCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		limit := int64(50)
		if s := r.URL.Query().Get("limit"); s != "" {
			if v, err := strconv.ParseInt(s, 10, 64); err == nil && v > 0 && v <= 1000 {
				limit = v
			}
		}

		cids, err := rdb.ZRevRange(r.Context(), "leaderboard", 0, limit-1).Result()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		keys := make([]string, 0, len(cids))
		for _, cid := range cids {
			keys = append(keys, "leaderboard:"+cid)
		}

		var payload []any
		if len(keys) > 0 {
			vals, err := rdb.MGet(r.Context(), keys...).Result()
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			payload = make([]any, 0, len(vals))
			for i, v := range vals {
				if v == nil {
					continue
				}
				var obj map[string]any
				if err := json.Unmarshal([]byte(v.(string)), &obj); err == nil {
					obj["contestant_id"] = cids[i]
					payload = append(payload, obj)
				}
			}
		} else {
			payload = []any{}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})

	// ── SSE stream ────────────────────────────────────────────────────────────
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		enableCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", 500)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		sub := rdb.Subscribe(r.Context(), "leaderboard_updates")
		defer sub.Close()

		fmt.Fprintf(w, ": connected\n\n")
		flusher.Flush()

		ch := sub.Channel(redis.WithChannelSize(1024))
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ctx.Done():
				return
			case <-heartbeat.C:
				fmt.Fprintf(w, "event: ping\ndata: {}\n\n")
				flusher.Flush()
			case msg := <-ch:
				if msg == nil {
					continue
				}
				fmt.Fprintf(w, "event: update\ndata: %q\n\n", msg.Payload)
				flusher.Flush()
			}
		}
	})

	// ── History (TimescaleDB) ─────────────────────────────────────────────────
	// GET /contestants/{id}/history?hours=N
	// Returns per-minute aggregated stats for a contestant.
	mux.HandleFunc("/contestants/", func(w http.ResponseWriter, r *http.Request) {
		enableCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if ts == nil {
			http.Error(w, `{"error":"timescaledb not configured"}`, http.StatusServiceUnavailable)
			return
		}

		// Parse /contestants/{id}/history or /contestants/{id}/stats
		path := r.URL.Path // e.g. /contestants/team-alpha/history
		// strip leading /contestants/
		rest := path[len("/contestants/"):]
		slash := -1
		for i, c := range rest {
			if c == '/' {
				slash = i
				break
			}
		}
		if slash < 0 {
			http.NotFound(w, r)
			return
		}
		contestantID := rest[:slash]
		endpoint := rest[slash+1:]

		switch endpoint {
		case "history":
			hours := 1
			if s := r.URL.Query().Get("hours"); s != "" {
				if v, err := strconv.Atoi(s); err == nil && v > 0 {
					hours = v
				}
			}
			data, err := ts.QueryHistory(r.Context(), contestantID, hours)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if data == nil {
				data = []store.ContestantStats{}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(data)

		default:
			http.NotFound(w, r)
		}
	})

	// GET /runs/{id}/stats — aggregate stats for a specific test run from TimescaleDB
	mux.HandleFunc("/runs/", func(w http.ResponseWriter, r *http.Request) {
		enableCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if ts == nil {
			http.Error(w, `{"error":"timescaledb not configured"}`, http.StatusServiceUnavailable)
			return
		}

		// Parse /runs/{id}/stats
		path := r.URL.Path
		rest := path[len("/runs/"):]
		slash := -1
		for i, c := range rest {
			if c == '/' {
				slash = i
				break
			}
		}
		if slash < 0 || rest[slash+1:] != "stats" {
			http.NotFound(w, r)
			return
		}
		runID := rest[:slash]

		data, err := ts.QueryRunStats(r.Context(), runID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(data)
	})

	srv := &http.Server{
		Addr:    *addr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	log.Printf("leaderboard_api listening on %s (redis=%s pg=%v)", *addr, *redisAddr, *pgURL != "")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("server error: %v", err)
		os.Exit(1)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func enableCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}
