package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/HarshalPatel1972/epoch/aggregate"
	"github.com/HarshalPatel1972/epoch/api"
	"github.com/HarshalPatel1972/epoch/seed"
	"github.com/HarshalPatel1972/epoch/store"
	"github.com/HarshalPatel1972/epoch/timeline"
)

func main() {
	seedFlag := flag.Bool("seed", false, "populate demo data before starting")
	port := flag.String("port", "8080", "listen port")
	dbDir := flag.String("db", "", "path to BadgerDB directory (default: in-memory)")
	flag.Parse()

	var eventStore store.EventStore
	var snapStore store.SnapshotStore

	if *dbDir != "" {
		bs, err := store.NewBadgerEventStore(*dbDir)
		if err != nil {
			log.Fatalf("failed to open BadgerDB: %v", err)
		}
		defer bs.Close()
		bss := store.NewBadgerSnapshotStore(bs.DB())
		bs.SetSnapshotStore(bss)
		eventStore = bs
		snapStore = bss
	} else {
		ms := store.NewMemoryEventStore()
		mss := store.NewMemorySnapshotStore()
		ms.SetSnapshotStore(mss)
		eventStore = ms
		snapStore = mss
	}

	projector := &aggregate.Projector{
		Events:    eventStore,
		Snapshots: snapStore,
	}

	// Wiring snapshot trigger
	snapshotRequest := func(aggregateID string, currentVersion int64, asOf time.Time) error {
		prod, err := projector.Project(aggregateID, asOf)
		if err != nil {
			log.Printf("snapshot error: projection failed for %s: %v", aggregateID, err)
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
			log.Printf("snapshot error: save failed for %s: %v", aggregateID, err)
		} else {
			log.Printf("snapshot created: %s at version %d", aggregateID, currentVersion)
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

	if *seedFlag {
		seed.Run(eventStore)
		log.Println("seeded demo data successfully")
	}

	router := api.NewRouter(handlers)
	log.Printf("Epoch listening on :%s (db=%s)\n", *port, orDefault(*dbDir, "memory"))
	log.Fatal(http.ListenAndServe(":"+*port, router))
}

func orDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}
