package api

import (
	"encoding/json"
	"net/http"
	"time"
)

func (h *Handlers) CreateFork(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name        string    `json:"name"`
		ForkedFrom  time.Time `json:"forked_from"`
		Description string    `json:"description"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		Error(w, http.StatusBadRequest, CodeValidationError, "invalid json payload")
		return
	}

	if input.Name == "" || input.ForkedFrom.IsZero() {
		Error(w, http.StatusBadRequest, CodeValidationError, "name and forked_from required")
		return
	}

	fork, err := h.Registry.Create(input.Name, input.ForkedFrom, input.Description)
	if err != nil {
		// Differentiate error types if needed, but for Phase 2 let's use 400 or 409
		// A simple way to check error msg string for 'already exists'
		// Or update the Registry to return a sentinel error type
		Error(w, http.StatusBadRequest, CodeValidationError, err.Error())
		return
	}

	res := map[string]interface{}{
		"name":        fork.Name,
		"forked_from": fork.ForkedFrom,
		"created_at":  fork.CreatedAt,
		"description": fork.Description,
		"event_count": fork.EventCount(),
	}

	JSON(w, http.StatusCreated, res)
}

func (h *Handlers) ListForks(w http.ResponseWriter, r *http.Request) {
	forks := h.Registry.List()
	
	type forkRes struct {
		Name        string    `json:"name"`
		ForkedFrom  time.Time `json:"forked_from"`
		CreatedAt   time.Time `json:"created_at"`
		Description string    `json:"description"`
		EventCount  int       `json:"event_count"`
	}

	res := make([]forkRes, len(forks))
	for i, f := range forks {
		res[i] = forkRes{
			Name:        f.Name,
			ForkedFrom:  f.ForkedFrom,
			CreatedAt:   f.CreatedAt,
			Description: f.Description,
			EventCount:  f.EventCount(),
		}
	}

	JSON(w, http.StatusOK, map[string]interface{}{"timelines": res})
}

func (h *Handlers) DeleteFork(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		Error(w, http.StatusBadRequest, CodeValidationError, "timeline name required")
		return
	}

	if err := h.Registry.Delete(name); err != nil {
		Error(w, http.StatusNotFound, CodeProductNotFound, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
