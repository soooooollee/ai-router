package clientstore

import (
	"context"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// seedUsage writes a daily and a minute bucket directly, so retention tests are
// not affected by the write-path pruning they are meant to observe.
func seedUsage(t *testing.T, store *BoltStore, clientID string, at time.Time) {
	t.Helper()
	scope := DefaultScope.Normalize()
	if err := store.db.Update(func(tx *bolt.Tx) error {
		day := UsageBucket{TenantID: scope.TenantID, ProjectID: scope.ProjectID, ClientID: clientID, WindowStart: startOfDay(at), WindowKind: "day", Requests: 1}
		if err := put(tx.Bucket(bucketUsageDaily), usageKey(DefaultScope, clientID, at), day); err != nil {
			return err
		}
		minute := UsageBucket{TenantID: scope.TenantID, ProjectID: scope.ProjectID, ClientID: clientID, WindowStart: startOfMinute(at), WindowKind: "minute", Requests: 1}
		return put(tx.Bucket(bucketUsageMinute), minuteUsageKey(DefaultScope, clientID, at), minute)
	}); err != nil {
		t.Fatal(err)
	}
}

func minuteUsageExists(t *testing.T, store *BoltStore, clientID string, at time.Time) bool {
	t.Helper()
	exists := false
	if err := store.db.View(func(tx *bolt.Tx) error {
		exists = tx.Bucket(bucketUsageMinute).Get(minuteUsageKey(DefaultScope, clientID, at)) != nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return exists
}

func TestBoltStorePrunesStaleMinuteUsageAndKeepsDailyUsage(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	client := "prune_client"
	seedUsage(t, store, client, now.Add(-48*time.Hour))
	seedUsage(t, store, client, now.Add(-30*time.Hour))
	seedUsage(t, store, client, now.Add(-time.Hour))

	before, err := store.GetUsage(context.Background(), DefaultScope, client, UsageQuery{From: now.Add(-72 * time.Hour), To: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Minute) == 0 {
		t.Fatalf("seeded minute usage is missing: %#v", before.Minute)
	}
	removed, err := store.PruneUsage(context.Background(), now.Add(-usageMinuteRetention))
	if err != nil || removed != 2 {
		t.Fatalf("prune removed %d buckets (err=%v), want 2", removed, err)
	}
	if minuteUsageExists(t, store, client, now.Add(-48*time.Hour)) || minuteUsageExists(t, store, client, now.Add(-30*time.Hour)) {
		t.Fatal("stale minute usage survived pruning")
	}
	if !minuteUsageExists(t, store, client, now.Add(-time.Hour)) {
		t.Fatal("recent minute usage was pruned")
	}
	after, err := store.GetUsage(context.Background(), DefaultScope, client, UsageQuery{From: now.Add(-72 * time.Hour), To: now})
	if err != nil {
		t.Fatal(err)
	}
	// Daily buckets are the long-range record and must never be pruned.
	if len(after.Daily) != len(before.Daily) {
		t.Fatalf("pruning changed daily usage: %d -> %d", len(before.Daily), len(after.Daily))
	}
	if len(after.Minute) != 1 {
		t.Fatalf("expected one surviving minute bucket, got %#v", after.Minute)
	}
	if removed, err = store.PruneUsage(context.Background(), now.Add(-usageMinuteRetention)); err != nil || removed != 0 {
		t.Fatalf("second prune removed %d buckets (err=%v), want 0", removed, err)
	}
}

func TestBoltStorePrunesUsageOnWritePath(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	client := "prune_write_client"
	seedUsage(t, store, client, now.Add(-48*time.Hour))
	if err := store.AddRejectedUsage(context.Background(), DefaultScope, client, now); err != nil {
		t.Fatal(err)
	}
	if minuteUsageExists(t, store, client, now.Add(-48*time.Hour)) {
		t.Fatal("a settled request did not trigger retention")
	}
	// Retention is throttled, so a second burst of writes must not rescan.
	seedUsage(t, store, client, now.Add(-48*time.Hour).Add(time.Minute))
	if err := store.AddRejectedUsage(context.Background(), DefaultScope, client, now); err != nil {
		t.Fatal(err)
	}
	if !minuteUsageExists(t, store, client, now.Add(-48*time.Hour).Add(time.Minute)) {
		t.Fatal("retention ran again inside the throttle window")
	}
}
