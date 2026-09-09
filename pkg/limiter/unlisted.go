package limiter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Unlisted commercially-distributed Slack apps get a special tier on
// conversations.history and conversations.replies: 1 request/minute and at
// most 15 objects per page. Marketplace and internal apps stay on Tier 3.
//
// https://docs.slack.dev/changelog/2025/05/29/rate-limit-changes-for-non-marketplace-apps/
const (
	UnlistedPageSize = 15
	UnlistedInterval = time.Minute
)

// SLACK_MCP_UNLISTED_HISTORY=1 forces the special tier (cap 15, 1/min) without
// waiting for a 429. =0 disables it even after a long Retry-After.
const unlistedEnv = "SLACK_MCP_UNLISTED_HISTORY"

type quotaState struct {
	Unlisted     bool  `json:"unlisted"`
	LastUnixNano int64 `json:"last_unix_nano"`
}

// UnlistedQuota is a process-shared special-tier gate for one Slack method
// (conversations.history or conversations.replies) in one workspace.
type UnlistedQuota struct {
	Dir       string
	TeamID    string
	Method    string
	Interval  time.Duration
	PageSize  int
	MarkAfter time.Duration
	now       func() time.Time

	force    bool
	disabled bool
}

func NewUnlistedQuota(teamID, method string) *UnlistedQuota {
	q := &UnlistedQuota{
		TeamID:   teamID,
		Method:   method,
		Interval: UnlistedInterval,
		PageSize: UnlistedPageSize,
		now:      time.Now,
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(unlistedEnv))) {
	case "1", "true", "yes":
		q.force = true
	case "0", "false", "no":
		q.disabled = true
	}
	return q
}

func (q *UnlistedQuota) CapPage(n int) int {
	if !q.unlisted() {
		return n
	}
	if q.PageSize <= 0 {
		return UnlistedPageSize
	}
	if n <= 0 || n > q.PageSize {
		return q.PageSize
	}
	return n
}

func (q *UnlistedQuota) unlisted() bool {
	if q.disabled {
		return false
	}
	if q.force {
		return true
	}
	st, _ := q.read()
	return st.Unlisted
}

func (q *UnlistedQuota) path() string {
	dir := q.Dir
	if dir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			cache = "."
		}
		dir = filepath.Join(cache, "slack-mcp-server")
	}
	team := q.TeamID
	if team == "" {
		team = "unknown"
	}
	method := strings.ReplaceAll(q.Method, ".", "_")
	if method == "" {
		method = "history"
	}
	return filepath.Join(dir, team+"_"+method+"_quota.json")
}

func (q *UnlistedQuota) read() (quotaState, error) {
	var st quotaState
	b, err := os.ReadFile(q.path())
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return quotaState{}, err
	}
	return st, nil
}

func (q *UnlistedQuota) write(st quotaState) error {
	path := q.path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "quota-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

func (q *UnlistedQuota) MarkUnlisted() {
	if q.disabled {
		return
	}
	st, _ := q.read()
	st.Unlisted = true
	_ = q.write(st)
}
func (q *UnlistedQuota) Touch() {
	st, _ := q.read()
	if q.force {
		st.Unlisted = true
	}
	st.LastUnixNano = q.now().UnixNano()
	_ = q.write(st)
}

// Lock serializes history/replies calls across slack-cli processes so they
// share the 1/min budget instead of all 429ing. Stale locks older than 2
// intervals are stolen.
func (q *UnlistedQuota) Lock(ctx context.Context) (func(), error) {
	path := q.path() + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return func() {}, err
	}
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", q.now().Unix())
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if info, statErr := os.Stat(path); statErr == nil {
			age := q.now().Sub(info.ModTime())
			interval := q.Interval
			if interval <= 0 {
				interval = UnlistedInterval
			}
			if age > 2*interval {
				_ = os.Remove(path)
				continue
			}
		}
		select {
		case <-ctx.Done():
			return func() {}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
func (q *UnlistedQuota) Wait(ctx context.Context) error {
	if !q.unlisted() {
		return nil
	}
	st, _ := q.read()
	if st.LastUnixNano == 0 {
		return nil
	}
	interval := q.Interval
	if interval <= 0 {
		interval = UnlistedInterval
	}
	next := time.Unix(0, st.LastUnixNano).Add(interval)
	now := q.now()
	if !now.Before(next) {
		return nil
	}
	return sleepCtx(ctx, next.Sub(now))
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (q *UnlistedQuota) markThreshold() time.Duration {
	if q.MarkAfter > 0 {
		return q.MarkAfter
	}
	return 20 * time.Second
}

// DoUnlisted runs fn under the special-tier lock: wait out the 1/min slot,
// retry 429s (honouring Retry-After), and remember a long Retry-After as
// unlisted so later processes cap the page size at 15.
func DoUnlisted[T any](ctx context.Context, q *UnlistedQuota, retryAfter func(error) time.Duration, fn func() (T, error)) (T, error) {
	var zero T
	unlock, err := q.Lock(ctx)
	if err != nil {
		return zero, err
	}
	defer unlock()
	if err := q.Wait(ctx); err != nil {
		return zero, err
	}
	const maxRetries = 3
	var last error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		res, err := fn()
		if err == nil {
			q.Touch()
			return res, nil
		}
		last = err
		wait := time.Duration(0)
		if retryAfter != nil {
			wait = retryAfter(err)
		}
		if wait <= 0 {
			return res, err
		}
		if wait >= q.markThreshold() {
			q.MarkUnlisted()
		}
		if attempt == maxRetries {
			return res, err
		}
		if err := sleepCtx(ctx, wait); err != nil {
			return zero, err
		}
	}
	return zero, last
}
