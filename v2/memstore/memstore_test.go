package memstore_test

import (
	"testing"

	"github.com/HarshalPatel1972/epoch/v2"
	"github.com/HarshalPatel1972/epoch/v2/epochtest"
	"github.com/HarshalPatel1972/epoch/v2/memstore"
)

func TestConformance(t *testing.T) {
	epochtest.Run(t, func(*testing.T) epoch.Store { return memstore.New() })
}

func BenchmarkMemstore(b *testing.B) {
	epochtest.Bench(b, func(*testing.B) epoch.Store { return memstore.New() })
}
