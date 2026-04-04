package temporal

import (
	"fmt"
	"net/http"
	"time"
)

func ParseAt(r *http.Request) (time.Time, error) {
	raw := r.URL.Query().Get("at")
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid ?at= value: must be RFC3339 (e.g. 2025-01-15T10:00:00Z)")
	}
	return t, nil
}
