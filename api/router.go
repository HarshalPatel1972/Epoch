package api

import (
	"net/http"

	"github.com/HarshalPatel1972/epoch/api/middleware"
	"github.com/HarshalPatel1972/epoch/config"
)

func NewRouter(h *Handlers, cfg config.Config) http.Handler {
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

	rl := middleware.NewRateLimiter(cfg.RateLimitRPS)

	// Middleware stack: RequestID -> Logger -> CORS -> RateLimit -> Mux
	return middleware.RequestID(
		middleware.Logger(
			middleware.CORS(cfg.CORSOrigins)(
				rl.Middleware(mux),
			),
		),
	)
}
