// Package registry manages contestant and submission records in Redis.
//
// Data model:
//
//	contestant:{id}          → JSON Contestant
//	contestants              → Redis Set of all contestant IDs
//	submission:{id}          → JSON Submission
//	submissions:contestant:{id} → Redis List of submission IDs (newest first, capped at 100)
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/iicpc/platform/internal/judge"
	"github.com/redis/go-redis/v9"
)

// Contestant is a registered participant.
type Contestant struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	RegisteredAt time.Time `json:"registered_at"`
}

var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
)

// Registry wraps Redis to provide typed access to contestant and submission data.
type Registry struct {
	rdb *redis.Client
}

func New(rdb *redis.Client) *Registry {
	return &Registry{rdb: rdb}
}

// ── Contestants ──────────────────────────────────────────────────────────────

func (r *Registry) RegisterContestant(ctx context.Context, c Contestant) error {
	key := "contestant:" + c.ID
	exists, err := r.rdb.Exists(ctx, key).Result()
	if err != nil {
		return err
	}
	if exists > 0 {
		return fmt.Errorf("%w: contestant %q", ErrAlreadyExists, c.ID)
	}
	b, _ := json.Marshal(c)
	pipe := r.rdb.Pipeline()
	pipe.Set(ctx, key, string(b), 0)
	pipe.SAdd(ctx, "contestants", c.ID)
	_, err = pipe.Exec(ctx)
	return err
}

func (r *Registry) GetContestant(ctx context.Context, id string) (*Contestant, error) {
	val, err := r.rdb.Get(ctx, "contestant:"+id).Result()
	if errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("%w: contestant %q", ErrNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	var c Contestant
	if err := json.Unmarshal([]byte(val), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *Registry) ListContestants(ctx context.Context) ([]Contestant, error) {
	ids, err := r.rdb.SMembers(ctx, "contestants").Result()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []Contestant{}, nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = "contestant:" + id
	}
	vals, err := r.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make([]Contestant, 0, len(vals))
	for _, v := range vals {
		if v == nil {
			continue
		}
		var c Contestant
		if err := json.Unmarshal([]byte(v.(string)), &c); err == nil {
			out = append(out, c)
		}
	}
	return out, nil
}

// ── Submissions ───────────────────────────────────────────────────────────────

func (r *Registry) SaveSubmission(ctx context.Context, s judge.Submission) error {
	b, _ := json.Marshal(s)
	pipe := r.rdb.Pipeline()
	pipe.Set(ctx, "submission:"+s.ID, string(b), 0)
	pipe.LPush(ctx, "submissions:contestant:"+s.ContestantID, s.ID)
	pipe.LTrim(ctx, "submissions:contestant:"+s.ContestantID, 0, 99) // keep last 100
	_, err := pipe.Exec(ctx)
	return err
}

func (r *Registry) GetSubmission(ctx context.Context, id string) (*judge.Submission, error) {
	val, err := r.rdb.Get(ctx, "submission:"+id).Result()
	if errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("%w: submission %q", ErrNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	var s judge.Submission
	if err := json.Unmarshal([]byte(val), &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *Registry) ListSubmissions(ctx context.Context, contestantID string) ([]judge.Submission, error) {
	ids, err := r.rdb.LRange(ctx, "submissions:contestant:"+contestantID, 0, 99).Result()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []judge.Submission{}, nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = "submission:" + id
	}
	vals, err := r.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make([]judge.Submission, 0, len(vals))
	for _, v := range vals {
		if v == nil {
			continue
		}
		var s judge.Submission
		if err := json.Unmarshal([]byte(v.(string)), &s); err == nil {
			out = append(out, s)
		}
	}
	return out, nil
}

// ── Run Results ───────────────────────────────────────────────────────────────

func (r *Registry) GetRunResult(ctx context.Context, runID string) (*judge.RunResult, error) {
	val, err := r.rdb.Get(ctx, "run:"+runID).Result()
	if errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("%w: run %q", ErrNotFound, runID)
	}
	if err != nil {
		return nil, err
	}
	var res judge.RunResult
	if err := json.Unmarshal([]byte(val), &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (r *Registry) ListRunsForContestant(ctx context.Context, contestantID string, limit int64) ([]judge.RunResult, error) {
	if limit <= 0 {
		limit = 20
	}
	ids, err := r.rdb.LRange(ctx, "runs:contestant:"+contestantID, 0, limit-1).Result()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []judge.RunResult{}, nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = "run:" + id
	}
	vals, err := r.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make([]judge.RunResult, 0, len(vals))
	for _, v := range vals {
		if v == nil {
			continue
		}
		var res judge.RunResult
		if err := json.Unmarshal([]byte(v.(string)), &res); err == nil {
			out = append(out, res)
		}
	}
	return out, nil
}
