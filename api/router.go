package api

import (
	"log/slog"
	"net/http"
	"time"
)

func NewRouter(h *Handlers) http.Handler {
	mux := http.NewServeMux()

	// Routes using Go 1.22 pattern matching
	mux.HandleFunc("POST /products", h.CreateProduct)
	mux.HandleFunc("GET /products", h.ListProducts)
	mux.HandleFunc("GET /products/{id}", h.GetProduct)
	mux.HandleFunc("PUT /products/{id}/price", h.UpdatePrice)
	mux.HandleFunc("PUT /products/{id}/stock", h.UpdateStock)
	mux.HandleFunc("DELETE /products/{id}", h.DeleteProduct)
	mux.HandleFunc("GET /events", h.ListEvents)
	mux.HandleFunc("GET /health", h.Health)
	mux.HandleFunc("POST /timelines/fork", h.CreateFork)
	mux.HandleFunc("GET /timelines", h.ListForks)
	mux.HandleFunc("DELETE /timelines/{name}", h.DeleteFork)
	mux.HandleFunc("GET /diff", h.Diff)

	// Logging middleware
	return LoggingMiddleware(mux)
}

func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		
		// Create a custom response writer to capture status code
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		
		next.ServeHTTP(rw, r)
		
		slog.Info("request completed",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration", time.Since(start).String(),
		)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}
