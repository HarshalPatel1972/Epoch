package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/HarshalPatel1972/epoch/aggregate"
	"github.com/HarshalPatel1972/epoch/store"
	"github.com/HarshalPatel1972/epoch/temporal"
	"github.com/HarshalPatel1972/epoch/timeline"
	"github.com/google/uuid"
)

type Handlers struct {
	Store     store.EventStore
	Projector *aggregate.Projector
	Registry  *timeline.ForkRegistry
}

func (h *Handlers) resolveProjector(r *http.Request) (*aggregate.Projector, error) {
	name := r.URL.Query().Get("timeline")
	if name == "" {
		return h.Projector, nil
	}
	fork, err := h.Registry.Get(name)
	if err != nil {
		return nil, err
	}
	return fork.Projector(), nil
}

func (h *Handlers) resolveStore(r *http.Request) (store.EventStore, error) {
	name := r.URL.Query().Get("timeline")
	if name == "" {
		return h.Store, nil
	}
	fork, err := h.Registry.Get(name)
	if err != nil {
		return nil, err
	}
	return fork.EventStore(), nil
}

func (h *Handlers) CreateProduct(w http.ResponseWriter, r *http.Request) {
	s, err := h.resolveStore(r)
	if err != nil {
		Error(w, http.StatusNotFound, CodeProductNotFound, err.Error())
		return
	}

	p, err := h.resolveProjector(r)
	if err != nil {
		Error(w, http.StatusNotFound, CodeProductNotFound, err.Error())
		return
	}

	var input struct {
		Name     string  `json:"name"`
		SKU      string  `json:"sku"`
		Price    float64 `json:"price"`
		Stock    int     `json:"stock"`
		Category string  `json:"category"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		Error(w, http.StatusBadRequest, CodeValidationError, "invalid json payload")
		return
	}

	if input.Name == "" || input.SKU == "" || input.Price <= 0 {
		Error(w, http.StatusBadRequest, CodeValidationError, "name, sku, and price > 0 required")
		return
	}

	id := uuid.New().String()
	payload := store.ProductCreatedPayload{
		ID:       id,
		Name:     input.Name,
		SKU:      input.SKU,
		Price:    input.Price,
		Stock:    input.Stock,
		Category: input.Category,
	}

	payloadBytes, _ := json.Marshal(payload)
	_, err = s.Append(store.Event{
		Type:        store.EventProductCreated,
		AggregateID: id,
		Payload:     payloadBytes,
		OccurredAt:  time.Now(),
	})
	if err != nil {
		Error(w, http.StatusInternalServerError, CodeInternalError, "failed to append event")
		return
	}

	product, err := p.Project(id, time.Time{})
	if err != nil {
		Error(w, http.StatusInternalServerError, CodeInternalError, "failed to project product")
		return
	}

	res := map[string]interface{}{
		"id":         product.ID,
		"name":       product.Name,
		"sku":        product.SKU,
		"price":      product.Price,
		"stock":      product.Stock,
		"category":   product.Category,
		"created_at": product.CreatedAt,
		"updated_at": product.UpdatedAt,
		"deleted":    product.Deleted,
	}
	if timelineName := r.URL.Query().Get("timeline"); timelineName != "" {
		res["timeline"] = timelineName
	}

	JSON(w, http.StatusCreated, res)
}

func (h *Handlers) ListProducts(w http.ResponseWriter, r *http.Request) {
	proj, err := h.resolveProjector(r)
	if err != nil {
		Error(w, http.StatusNotFound, CodeProductNotFound, err.Error())
		return
	}

	at, err := temporal.ParseAt(r)
	if err != nil {
		Error(w, http.StatusBadRequest, CodeInvalidAtParam, err.Error())
		return
	}

	categoryFilter := r.URL.Query().Get("category")

	products, err := proj.ProjectAll(at)
	if err != nil {
		Error(w, http.StatusInternalServerError, CodeInternalError, "failed to project products")
		return
	}

	var filtered []*aggregate.Product
	if categoryFilter != "" {
		for _, p := range products {
			if p.Category == categoryFilter {
				filtered = append(filtered, p)
			}
		}
	} else {
		filtered = products
	}

	meta := GetTemporalMetadata(at)
	res := map[string]interface{}{
		"as_of":         meta.AsOf,
		"is_historical": meta.IsHistorical,
		"count":         len(filtered),
		"products":      filtered,
	}
	if timelineName := r.URL.Query().Get("timeline"); timelineName != "" {
		res["timeline"] = timelineName
	}

	JSON(w, http.StatusOK, res)
}

func (h *Handlers) GetProduct(w http.ResponseWriter, r *http.Request) {
	proj, err := h.resolveProjector(r)
	if err != nil {
		Error(w, http.StatusNotFound, CodeProductNotFound, err.Error())
		return
	}

	id := r.PathValue("id")
	at, err := temporal.ParseAt(r)
	if err != nil {
		Error(w, http.StatusBadRequest, CodeInvalidAtParam, err.Error())
		return
	}

	product, err := proj.Project(id, at)
	if err != nil {
		Error(w, http.StatusInternalServerError, CodeInternalError, "failed to project product")
		return
	}

	if product == nil || product.Deleted {
		Error(w, http.StatusNotFound, CodeProductNotFound, "product not found at requested time")
		return
	}

	meta := GetTemporalMetadata(at)
	res := map[string]interface{}{
		"product":       product,
		"as_of":         meta.AsOf,
		"is_historical": meta.IsHistorical,
	}
	if timelineName := r.URL.Query().Get("timeline"); timelineName != "" {
		res["timeline"] = timelineName
	}

	// Spread product fields into res
	res["id"] = product.ID
	res["name"] = product.Name
	res["sku"] = product.SKU
	res["price"] = product.Price
	res["stock"] = product.Stock
	res["category"] = product.Category
	res["created_at"] = product.CreatedAt
	res["updated_at"] = product.UpdatedAt
	res["deleted"] = product.Deleted

	JSON(w, http.StatusOK, res)
}

func (h *Handlers) UpdatePrice(w http.ResponseWriter, r *http.Request) {
	s, err := h.resolveStore(r)
	if err != nil {
		Error(w, http.StatusNotFound, CodeProductNotFound, err.Error())
		return
	}

	proj, err := h.resolveProjector(r)
	if err != nil {
		Error(w, http.StatusNotFound, CodeProductNotFound, err.Error())
		return
	}

	id := r.PathValue("id")
	var input struct {
		Price float64 `json:"price"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		Error(w, http.StatusBadRequest, CodeValidationError, "invalid json payload")
		return
	}

	if input.Price <= 0 {
		Error(w, http.StatusBadRequest, CodeValidationError, "price must be > 0")
		return
	}

	current, err := proj.Project(id, time.Time{})
	if err != nil || current == nil || current.Deleted {
		Error(w, http.StatusNotFound, CodeProductNotFound, "product not found")
		return
	}

	payload := store.PriceUpdatedPayload{
		OldPrice: current.Price,
		NewPrice: input.Price,
	}
	payloadBytes, _ := json.Marshal(payload)

	_, err = s.Append(store.Event{
		Type:        store.EventProductPriceUpdate,
		AggregateID: id,
		Payload:     payloadBytes,
		OccurredAt:  time.Now(),
	})
	if err != nil {
		Error(w, http.StatusInternalServerError, CodeInternalError, "failed to append event")
		return
	}

	updated, _ := proj.Project(id, time.Time{})
	JSON(w, http.StatusOK, updated)
}

func (h *Handlers) UpdateStock(w http.ResponseWriter, r *http.Request) {
	s, err := h.resolveStore(r)
	if err != nil {
		Error(w, http.StatusNotFound, CodeProductNotFound, err.Error())
		return
	}

	proj, err := h.resolveProjector(r)
	if err != nil {
		Error(w, http.StatusNotFound, CodeProductNotFound, err.Error())
		return
	}

	id := r.PathValue("id")
	var input struct {
		Delta int `json:"delta"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		Error(w, http.StatusBadRequest, CodeValidationError, "invalid json payload")
		return
	}

	current, err := proj.Project(id, time.Time{})
	if err != nil || current == nil || current.Deleted {
		Error(w, http.StatusNotFound, CodeProductNotFound, "product not found")
		return
	}

	newStock := current.Stock + input.Delta
	if newStock < 0 {
		Error(w, http.StatusBadRequest, CodeInsufficientStock, "insufficient stock")
		return
	}

	payload := store.StockUpdatedPayload{
		Delta:    input.Delta,
		NewStock: newStock,
	}
	payloadBytes, _ := json.Marshal(payload)

	_, err = s.Append(store.Event{
		Type:        store.EventProductStockUpdate,
		AggregateID: id,
		Payload:     payloadBytes,
		OccurredAt:  time.Now(),
	})
	if err != nil {
		Error(w, http.StatusInternalServerError, CodeInternalError, "failed to append event")
		return
	}

	updated, _ := proj.Project(id, time.Time{})
	JSON(w, http.StatusOK, updated)
}

func (h *Handlers) DeleteProduct(w http.ResponseWriter, r *http.Request) {
	s, err := h.resolveStore(r)
	if err != nil {
		Error(w, http.StatusNotFound, CodeProductNotFound, err.Error())
		return
	}

	proj, err := h.resolveProjector(r)
	if err != nil {
		Error(w, http.StatusNotFound, CodeProductNotFound, err.Error())
		return
	}

	id := r.PathValue("id")

	current, err := proj.Project(id, time.Time{})
	if err != nil || current == nil || current.Deleted {
		Error(w, http.StatusNotFound, CodeProductNotFound, "product not found")
		return
	}

	_, err = s.Append(store.Event{
		Type:        store.EventProductDeleted,
		AggregateID: id,
		Payload:     []byte("{}"),
		OccurredAt:  time.Now(),
	})
	if err != nil {
		Error(w, http.StatusInternalServerError, CodeInternalError, "failed to append event")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) ListEvents(w http.ResponseWriter, r *http.Request) {
	s, err := h.resolveStore(r)
	if err != nil {
		Error(w, http.StatusNotFound, CodeProductNotFound, err.Error())
		return
	}

	idFilter := r.URL.Query().Get("aggregate_id")
	var events []store.Event

	if idFilter != "" {
		events, err = s.Load(idFilter)
	} else {
		events, err = s.LoadAll()
	}

	if err != nil {
		Error(w, http.StatusInternalServerError, CodeInternalError, "failed to load events")
		return
	}

	type decodedEvent struct {
		ID          string      `json:"id"`
		Type        string      `json:"type"`
		AggregateID string      `json:"aggregate_id"`
		OccurredAt  time.Time   `json:"occurred_at"`
		Version     int64       `json:"version"`
		Payload     interface{} `json:"payload"`
	}

	resEvents := make([]decodedEvent, len(events))
	for i, e := range events {
		var payload interface{}
		json.Unmarshal(e.Payload, &payload)
		resEvents[i] = decodedEvent{
			ID:          e.ID,
			Type:        string(e.Type),
			AggregateID: e.AggregateID,
			OccurredAt:  e.OccurredAt,
			Version:     e.Version,
			Payload:     payload,
		}
	}

	JSON(w, http.StatusOK, map[string]interface{}{
		"count":  len(resEvents),
		"events": resEvents,
	})
}

func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	s, _ := h.resolveStore(r)
	events, _ := s.LoadAll()
	ids := s.AllAggregateIDs()

	JSON(w, http.StatusOK, map[string]interface{}{
		"status":          "ok",
		"event_count":     len(events),
		"aggregate_count": len(ids),
	})
}
