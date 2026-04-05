package main

import (
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/HarshalPatel1972/epoch/aggregate"
	"github.com/HarshalPatel1972/epoch/api"
	"github.com/HarshalPatel1972/epoch/config"
	"github.com/HarshalPatel1972/epoch/seed"
	"github.com/HarshalPatel1972/epoch/store"
	"github.com/HarshalPatel1972/epoch/timeline"
)

func main() {
	cfg := config.Load()

	// Initialize slog
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	var eventStore store.EventStore
	var snapStore store.SnapshotStore

	if cfg.DBDir != "" {
		bs, err := store.NewBadgerEventStore(cfg.DBDir)
		if err != nil {
			slog.Error("failed to open BadgerDB", "err", err)
			os.Exit(1)
		}
		defer bs.Close()
		bss := store.NewBadgerSnapshotStore(bs.DB())
		bs.SetSnapshotStore(bss)
		eventStore = bs
		snapStore = bss
		slog.Info("BadgerDB store initialized", "dir", cfg.DBDir)
	} else {
		ms := store.NewMemoryEventStore()
		mss := store.NewMemorySnapshotStore()
		ms.SetSnapshotStore(mss)
		eventStore = ms
		snapStore = mss
		slog.Info("Memory store initialized")
	}

	projector := &aggregate.Projector{
		Events:    eventStore,
		Snapshots: snapStore,
	}

	// Wiring snapshot trigger
	snapshotRequest := func(aggregateID string, currentVersion int64, asOf time.Time) error {
		prod, err := projector.Project(aggregateID, asOf)
		if err != nil {
			slog.Error("snapshot error: projection failed", "id", aggregateID, "err", err)
			return err
		}

		if prod == nil {
			return nil
		}

		state, _ := json.Marshal(prod)
		err = snapStore.Save(store.Snapshot{
			AggregateID: aggregateID,
			State:       state,
			AsOf:        asOf,
			Version:     currentVersion,
		})
		if err != nil {
			slog.Error("snapshot error: save failed", "id", aggregateID, "err", err)
		} else {
			slog.Info("snapshot created", "id", aggregateID, "version", currentVersion)
		}
		return err
	}

	if ms, ok := eventStore.(*store.MemoryEventStore); ok {
		ms.RequestSnapshot = snapshotRequest
	} else if bs, ok := eventStore.(*store.BadgerEventStore); ok {
		bs.RequestSnapshot = snapshotRequest
	}

	registry := timeline.NewForkRegistry(eventStore, snapStore)

	handlers := &api.Handlers{
		Store:     eventStore,
		Projector: projector,
		Registry:  registry,
	}

	if cfg.Seed {
		seed.Run(eventStore)
		slog.Info("seeded demo data successfully")
	}

	router := api.NewRouter(handlers, cfg)
	slog.Info("epoch started", "port", cfg.Port, "db", orDefault(cfg.DBDir, "memory"))
	log.Fatal(http.ListenAndServe(":"+cfg.Port, router))
}

func orDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}
