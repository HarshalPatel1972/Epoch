package api

import (
	"net/http"
	"time"

	"github.com/HarshalPatel1972/epoch/aggregate"
	"github.com/HarshalPatel1972/epoch/timeline"
)

func (h *Handlers) Diff(w http.ResponseWriter, r *http.Request) {
	timelineName := r.URL.Query().Get("timeline")
	aggID := r.URL.Query().Get("aggregate_id")
	fromRaw := r.URL.Query().Get("from")
	toRaw := r.URL.Query().Get("to")

	var result timeline.DiffResult

	if timelineName != "" {
		// Mode B: Fork vs Main
		fork, err := h.Registry.Get(timelineName)
		if err != nil {
			Error(w, http.StatusNotFound, CodeProductNotFound, "timeline not found")
			return
		}

		var before, after []*aggregate.Product
		if aggID != "" {
			pMain, _ := h.Projector.Project(aggID, time.Time{})
			pFork, _ := fork.Projector().Project(aggID, time.Time{})
			if pMain != nil {
				before = append(before, pMain)
			}
			if pFork != nil {
				after = append(after, pFork)
			}
		} else {
			before, _ = h.Projector.ProjectAll(time.Time{})
			after, _ = fork.Projector().ProjectAll(time.Time{})
		}

		result = timeline.Diff(before, after)
		result.Mode = "fork"
		result.Timeline = &timelineName

	} else if fromRaw != "" {
		// Mode A: Temporal diff on main
		from, err := time.Parse(time.RFC3339, fromRaw)
		if err != nil {
			Error(w, http.StatusBadRequest, CodeInvalidAtParam, "invalid 'from' timestamp")
			return
		}

		to := time.Now()
		if toRaw != "" {
			var errTo error
			to, errTo = time.Parse(time.RFC3339, toRaw)
			if errTo != nil {
				Error(w, http.StatusBadRequest, CodeInvalidAtParam, "invalid 'to' timestamp")
				return
			}
		}

		var before, after []*aggregate.Product
		if aggID != "" {
			pFrom, _ := h.Projector.Project(aggID, from)
			pTo, _ := h.Projector.Project(aggID, to)
			if pFrom != nil {
				before = append(before, pFrom)
			}
			if pTo != nil {
				after = append(after, pTo)
			}
		} else {
			before, _ = h.Projector.ProjectAll(from)
			after, _ = h.Projector.ProjectAll(to)
		}

		result = timeline.Diff(before, after)
		result.Mode = "temporal"
		result.From = &from
		result.To = &to

	} else {
		Error(w, http.StatusBadRequest, CodeValidationError, "either 'timeline' or 'from' required for diff")
		return
	}

	JSON(w, http.StatusOK, result)
}
