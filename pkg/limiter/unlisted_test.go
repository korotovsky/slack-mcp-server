package limiter

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCapPage(t *testing.T) {
	dir := t.TempDir()
	q := &UnlistedQuota{Dir: dir, TeamID: "T1", Method: "conversations.history", PageSize: UnlistedPageSize, now: time.Now}
	if q.CapPage(100) != 100 {
		t.Fatalf("listed cap: got %d", q.CapPage(100))
	}
	q.MarkUnlisted()
	if got := q.CapPage(100); got != 15 {
		t.Fatalf("unlisted cap 100: got %d", got)
	}
	if got := q.CapPage(0); got != 15 {
		t.Fatalf("unlisted cap 0: got %d", got)
	}
	if got := q.CapPage(7); got != 7 {
		t.Fatalf("unlisted cap 7: got %d", got)
	}
}

func TestCapPageDisabled(t *testing.T) {
	q := &UnlistedQuota{Dir: t.TempDir(), disabled: true, PageSize: 15, now: time.Now}
	q.MarkUnlisted()
	if q.CapPage(100) != 100 {
		t.Fatal("disabled quota should not cap")
	}
}

func TestCapPageForced(t *testing.T) {
	q := &UnlistedQuota{Dir: t.TempDir(), force: true, PageSize: 15, now: time.Now}
	if q.CapPage(100) != 15 {
		t.Fatal("forced unlisted should cap before any 429")
	}
}

func TestWaitSkipsWhenFresh(t *testing.T) {
	q := &UnlistedQuota{
		Dir:      t.TempDir(),
		TeamID:   "T1",
		Method:   "history",
		Interval: time.Hour,
		now:      time.Now,
		force:    true,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := q.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWaitSleepsUntilInterval(t *testing.T) {
	now := time.Now()
	q := &UnlistedQuota{
		Dir:      t.TempDir(),
		TeamID:   "T1",
		Method:   "history",
		Interval: 80 * time.Millisecond,
		now:      func() time.Time { return now },
		force:    true,
	}
	q.Touch()
	q.now = time.Now
	start := time.Now()
	if err := q.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Fatalf("wait returned too quickly: %s", time.Since(start))
	}
}
func TestDoUnlistedRetryAndMark(t *testing.T) {
	q := &UnlistedQuota{
		Dir:       t.TempDir(),
		TeamID:    "T1",
		Method:    "conversations.history",
		Interval:  time.Millisecond,
		PageSize:  15,
		MarkAfter: 10 * time.Millisecond,
		now:       time.Now,
	}
	var n atomic.Int32
	res, err := DoUnlisted(context.Background(), q, func(error) time.Duration { return 25 * time.Millisecond }, func() (int, error) {
		if n.Add(1) == 1 {
			return 0, errors.New("rate limited")
		}
		return 7, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if res != 7 || n.Load() != 2 {
		t.Fatalf("res=%d n=%d", res, n.Load())
	}
	if !q.unlisted() {
		t.Fatal("long Retry-After should mark unlisted")
	}
}

func TestDoUnlistedShortRetryDoesNotMark(t *testing.T) {
	q := &UnlistedQuota{
		Dir:      t.TempDir(),
		TeamID:   "T1",
		Method:   "conversations.replies",
		Interval: time.Millisecond,
		now:      time.Now,
	}
	_, err := DoUnlisted(context.Background(), q, func(error) time.Duration { return 5 * time.Millisecond }, func() (int, error) {
		return 1, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if q.unlisted() {
		t.Fatal("success should not mark unlisted")
	}
}

func TestLockSerializes(t *testing.T) {
	q := &UnlistedQuota{Dir: t.TempDir(), TeamID: "T1", Method: "history", Interval: time.Second, now: time.Now}
	ctx := context.Background()
	unlock, err := q.Lock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var gotLock atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		u, err := q.Lock(ctx)
		if err == nil {
			gotLock.Store(true)
			u()
		}
	}()
	time.Sleep(40 * time.Millisecond)
	if gotLock.Load() {
		t.Fatal("second lock acquired while first held")
	}
	unlock()
	wg.Wait()
}
