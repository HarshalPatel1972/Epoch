package api

import (
	"encoding/json"
	"net/http"
	"time"
)

type ErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

const (
	CodeInvalidAtParam     = "INVALID_AT_PARAM"
	CodeProductNotFound    = "PRODUCT_NOT_FOUND"
	CodeValidationError    = "VALIDATION_ERROR"
	CodeInsufficientStock  = "INSUFFICIENT_STOCK"
	CodeInternalError      = "INTERNAL_ERROR"
)

func JSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func Error(w http.ResponseWriter, status int, code, msg string) {
	JSON(w, status, ErrorResponse{Error: msg, Code: code})
}

type TemporalResponse struct {
	AsOf        time.Time `json:"as_of"`
	IsHistorical bool      `json:"is_historical"`
}

func GetTemporalMetadata(t time.Time) TemporalResponse {
	if t.IsZero() {
		return TemporalResponse{
			AsOf:        time.Now(),
			IsHistorical: false,
		}
	}
	return TemporalResponse{
		AsOf:        t,
		IsHistorical: true,
	}
}
