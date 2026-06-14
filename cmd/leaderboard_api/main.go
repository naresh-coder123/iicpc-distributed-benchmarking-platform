package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/iicpc/platform/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	var (
		addr            = flag.String("addr", ":8080", "http listen addr")
		redisAddr       = flag.String("redis", envOr("REDIS_ADDR", "localhost:6379"), "redis addr host:port")
		pgURL           = flag.String("pg", envOr("PG_URL", ""), "timescaledb postgres url (optional)")
		judgeInternalURL = flag.String("judge-url", envOr("JUDGE_INTERNAL_URL", ""), "internal cluster URL of judge-api (e.g. http://judge-api.iicpc-runner.svc.cluster.local:8081)")
		allowedOrigins  = flag.String("allowed-origins", envOr("ALLOWED_ORIGINS", ""), "comma-separated CORS allowed origins (empty = wildcard fallback for dev)")
	)
	flag.Parse()

	// Build origin allow-list.
	originSet := buildOriginSet(*allowedOrigins)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// ── Redis (with connection pool) ─────────────────────────────────────────
	rdb := redis.NewClient(&redis.Options{
		Addr:            *redisAddr,
		PoolSize:        10,
		MinIdleConns:    2,
		ConnMaxLifetime: 5 * time.Minute,
		DialTimeout:     5 * time.Second,
		ReadTimeout:     3 * time.Second,
		WriteTimeout:    3 * time.Second,
	})
	defer rdb.Close()

	// ── TimescaleDB (optional — history endpoints return 503 if not configured) ─
	var ts *store.Timescale
	if *pgURL != "" {
		cfg, err := pgxpool.ParseConfig(*pgURL)
		if err != nil {
			log.Printf("warn: invalid PG_URL: %v — history endpoints disabled", err)
		} else {
			cfg.MaxConns = 10
			cfg.MinConns = 1
			cfg.MaxConnLifetime = 10 * time.Minute
			pool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				log.Printf("warn: timescaledb connect error: %v — history endpoints disabled", err)
			} else {
				ts = store.NewTimescale(pool)
				defer pool.Close()
				log.Printf("timescaledb connected")
			}
		}
	}

	mux := http.NewServeMux()

	// ── Health ────────────────────────────────────────────────────────────────
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	// ── Leaderboard (Redis) ───────────────────────────────────────────────────
	mux.HandleFunc("/leaderboard", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w, r, originSet)
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
			jsonErr(w, http.StatusInternalServerError, err.Error())
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
				jsonErr(w, http.StatusInternalServerError, err.Error())
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
		setCORS(w, r, originSet)
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
	mux.HandleFunc("/contestants/", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w, r, originSet)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if ts == nil {
			jsonErr(w, http.StatusServiceUnavailable, "timescaledb not configured")
			return
		}

		path := r.URL.Path
		rest := path[len("/contestants/"):]
		slash := strings.Index(rest, "/")
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
				jsonErr(w, http.StatusInternalServerError, err.Error())
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

	// ── Run Stats (TimescaleDB) ───────────────────────────────────────────────
	mux.HandleFunc("/runs/", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w, r, originSet)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if ts == nil {
			jsonErr(w, http.StatusServiceUnavailable, "timescaledb not configured")
			return
		}

		path := r.URL.Path
		rest := path[len("/runs/"):]
		slash := strings.LastIndex(rest, "/")
		if slash < 0 || rest[slash+1:] != "stats" {
			http.NotFound(w, r)
			return
		}
		runID := rest[:slash]

		data, err := ts.QueryRunStats(r.Context(), runID)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(data)
	})

	// ── Judge reverse proxy (/proxy/judge/*) ─────────────────────────────────
	// Routes all judge-api traffic through leaderboard-api using cluster DNS.
	// This avoids consuming a 4th Azure Public IP for judge-api (Free Tier limit = 3).
	if *judgeInternalURL != "" {
		target, err := url.Parse(*judgeInternalURL)
		if err != nil {
			log.Fatalf("invalid --judge-url: %v", err)
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("judge proxy error: %v", err)
			jsonErr(w, http.StatusBadGateway, "judge-api unreachable: "+err.Error())
		}
		// Strip CORS headers from backend response to prevent browser errors due to duplicate wildcard headers (*, *).
		proxy.ModifyResponse = func(resp *http.Response) error {
			resp.Header.Del("Access-Control-Allow-Origin")
			resp.Header.Del("Access-Control-Allow-Methods")
			resp.Header.Del("Access-Control-Allow-Headers")
			resp.Header.Del("Access-Control-Allow-Credentials")
			return nil
		}
		// Strip /proxy/judge prefix before forwarding so judge-api sees /contestants, /submissions, etc.
		mux.Handle("/proxy/judge/", http.StripPrefix("/proxy/judge", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			setCORS(w, r, originSet)
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			proxy.ServeHTTP(w, r)
		})))
		log.Printf("judge reverse proxy enabled: /proxy/judge/* → %s", *judgeInternalURL)
	} else {
		log.Printf("judge reverse proxy disabled (JUDGE_INTERNAL_URL not set)")
	}

	// ── Server ────────────────────────────────────────────────────────────────
	srv := &http.Server{
		Addr:         *addr,
		Handler:      panicRecovery(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // 0 = no timeout for SSE streams
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		log.Println("leaderboard_api shutdown complete")
	}()

	log.Printf("leaderboard_api listening on %s (redis=%s pg=%v judge-proxy=%v)", *addr, *redisAddr, *pgURL != "", *judgeInternalURL != "")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("server error: %v", err)
		os.Exit(1)
	}
}

// ── CORS helpers ─────────────────────────────────────────────────────────────

// buildOriginSet parses a comma-separated list of allowed origins into a set.
// Returns nil if the list is empty (signals wildcard fallback).
func buildOriginSet(csv string) map[string]struct{} {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil
	}
	set := make(map[string]struct{})
	for _, o := range strings.Split(csv, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			set[o] = struct{}{}
		}
	}
	return set
}

// setCORS writes Access-Control-Allow-Origin to w.
// If originSet is nil, falls back to "*" (dev/local mode).
// Otherwise, reflects the request origin only if it is in the allow-list.
func setCORS(w http.ResponseWriter, r *http.Request, originSet map[string]struct{}) {
	origin := r.Header.Get("Origin")
	if originSet == nil {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	} else if _, ok := originSet[origin]; ok {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

// ── Panic recovery middleware ─────────────────────────────────────────────────

// panicRecovery wraps next with a defer/recover that catches any panics,
// logs the stack trace, and returns a clean JSON 500 to the client.
func panicRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic recovered in %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "internal server error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ── Small helpers ─────────────────────────────────────────────────────────────

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
