package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/config"
	"github.com/jrduncans/nwsl-season/internal/edgecache"
)

type syncPurger struct {
	calls [][]string
	err   error
}

func (p *syncPurger) PurgeTags(_ context.Context, tags []string) error {
	p.calls = append(p.calls, tags)
	return p.err
}

func useSyncPurger(t *testing.T, purger *syncPurger) {
	t.Helper()
	previous := newEdgeCachePurger
	newEdgeCachePurger = func(config.EdgeCacheConfig) edgecache.Purger { return purger }
	t.Cleanup(func() { newEdgeCachePurger = previous })
}

func TestPurgeEdgeCacheAfterSyncPurgesEveryPage(t *testing.T) {
	purger := &syncPurger{}
	useSyncPurger(t, purger)
	var output bytes.Buffer
	cfg := config.Config{EdgeCache: &config.EdgeCacheConfig{ZoneID: "zone", Token: "token"}}
	purgeEdgeCacheAfterSync(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), &output)()
	if want := [][]string{{"nwsl-page"}}; !slices.EqualFunc(purger.calls, want, slices.Equal) {
		t.Fatalf("purges = %v, want %v", purger.calls, want)
	}
	if output.Len() == 0 {
		t.Error("no purge confirmation printed")
	}
}

func TestPurgeEdgeCacheAfterSyncWithoutConfigurationDoesNothing(t *testing.T) {
	purger := &syncPurger{}
	useSyncPurger(t, purger)
	var output bytes.Buffer
	purgeEdgeCacheAfterSync(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), &output)()
	if len(purger.calls) != 0 || output.Len() != 0 {
		t.Fatalf("purges = %v, output = %q, want neither", purger.calls, output.String())
	}
}

func TestPurgeEdgeCacheAfterSyncFailureOnlyWarns(t *testing.T) {
	purger := &syncPurger{err: errors.New("cloudflare unavailable")}
	useSyncPurger(t, purger)
	var logs, output bytes.Buffer
	cfg := config.Config{EdgeCache: &config.EdgeCacheConfig{ZoneID: "zone", Token: "token"}}
	purgeEdgeCacheAfterSync(cfg, slog.New(slog.NewTextHandler(&logs, nil)), &output)()
	if output.Len() != 0 {
		t.Errorf("output = %q, want no confirmation after a failed purge", output.String())
	}
	if !bytes.Contains(logs.Bytes(), []byte("cloudflare unavailable")) {
		t.Errorf("logs = %q, want the purge failure", logs.String())
	}
}
