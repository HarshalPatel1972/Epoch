package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/HarshalPatel1972/epoch/aggregate"
	"github.com/HarshalPatel1972/epoch/api"
	"github.com/HarshalPatel1972/epoch/config"
	"github.com/HarshalPatel1972/epoch/store"
	"github.com/HarshalPatel1972/epoch/timeline"
)

func newTestServer(t *testing.T) *httptest.Server {
	es := store.NewMemoryEventStore()
	ss := store.NewMemorySnapshotStore()
	es.SetSnapshotStore(ss)

	proj := &aggregate.Projector{
		Events:    es,
		Snapshots: ss,
	}

	reg := timeline.NewForkRegistry(es, ss)
	h := &api.Handlers{
		Store:     es,
		Projector: proj,
		Registry:  reg,
		StartTime: time.Now(),
	}

	cfg := config.Config{
		RateLimitRPS: 1000,
		CORSOrigins:  []string{"*"},
	}

	router := api.NewRouter(h, cfg)
	return httptest.NewServer(router)
}

func TestPointInTimeRead(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	body := `{"name":"T0 Product","sku":"T0","price":100,"stock":5}`
	http.Post(ts.URL+"/products", "application/json", bytes.NewBuffer([]byte(body)))
	
	time.Sleep(100 * time.Millisecond)
	
	// Capture t0 AFTER creation
	t0 := time.Now()

	time.Sleep(100 * time.Millisecond)

	// Update price
	resp, _ := http.Get(ts.URL + "/products")
	var listRes struct {
		Products []map[string]interface{} `json:"products"`
	}
	json.NewDecoder(resp.Body).Decode(&listRes)
	id := listRes.Products[0]["id"].(string)

	updateBody := `{"price":200}`
	req, _ := http.NewRequest("PUT", ts.URL+"/products/"+id+"/price", bytes.NewBuffer([]byte(updateBody)))
	http.DefaultClient.Do(req)
	
	time.Sleep(100 * time.Millisecond)
	t1 := time.Now()

	// Check T0 (100)
	u := fmt.Sprintf("%s/products/%s?at=%s", ts.URL, id, url.QueryEscape(t0.Format(time.RFC3339Nano)))
	resp, _ = http.Get(u)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 at T0, got %d", resp.StatusCode)
	}
	var p map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&p)
	if p["price"].(float64) != 100 {
		t.Errorf("expected 100 at T0, got %v", p["price"])
	}

	// Check T1 (200)
	u = fmt.Sprintf("%s/products/%s?at=%s", ts.URL, id, url.QueryEscape(t1.Format(time.RFC3339Nano)))
	resp, _ = http.Get(u)
	json.NewDecoder(resp.Body).Decode(&p)
	if p["price"].(float64) != 200 {
		t.Errorf("expected 200 at T1, got %v", p["price"])
	}
}

func TestForkIsolation(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	body := `{"name":"Main Prod","sku":"M1","price":100,"stock":5}`
	http.Post(ts.URL+"/products", "application/json", bytes.NewBuffer([]byte(body)))
	
	time.Sleep(100 * time.Millisecond)
	resp, _ := http.Get(ts.URL + "/products")
	var listRes struct {
		Products []map[string]interface{} `json:"products"`
	}
	json.NewDecoder(resp.Body).Decode(&listRes)
	id := listRes.Products[0]["id"].(string)

	forkPoint := time.Now()
	time.Sleep(100 * time.Millisecond)

	forkBody := fmt.Sprintf(`{"name":"test-fork","forked_from":"%s"}`, forkPoint.Format(time.RFC3339Nano))
	http.Post(ts.URL+"/timelines/fork", "application/json", bytes.NewBuffer([]byte(forkBody)))

	updateBody := `{"price":50}`
	req, _ := http.NewRequest("PUT", ts.URL+"/products/"+id+"/price?timeline=test-fork", bytes.NewBuffer([]byte(updateBody)))
	http.DefaultClient.Do(req)

	resp, _ = http.Get(ts.URL + "/products/" + id + "?timeline=test-fork")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on fork get, got %d", resp.StatusCode)
	}
	var p map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&p)
	if p["price"].(float64) != 50 {
		t.Errorf("expected 50 on fork, got %v", p["price"])
	}

	resp, _ = http.Get(ts.URL + "/products/" + id)
	json.NewDecoder(resp.Body).Decode(&p)
	if p["price"].(float64) != 100 {
		t.Errorf("expected 100 on main, got %v", p["price"])
	}
}

func TestCreateAndRetrieveProduct(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	body := `{"name":"Test Phone","sku":"PHONE-001","price":999.99,"stock":10,"category":"tech"}`
	resp, _ := http.Post(ts.URL+"/products", "application/json", bytes.NewBuffer([]byte(body)))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var product map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&product)
	id := product["id"].(string)

	resp, _ = http.Get(ts.URL + "/products/" + id)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var got map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&got)
	if got["name"] != "Test Phone" {
		t.Errorf("expected Test Phone, got %v", got["name"])
	}
}

func TestDeletedProductHistory(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	body := `{"name":"Ghost","sku":"G","price":10,"stock":1}`
	http.Post(ts.URL+"/products", "application/json", bytes.NewBuffer([]byte(body)))
	
	time.Sleep(100 * time.Millisecond)
	resp, _ := http.Get(ts.URL + "/products")
	var listRes struct {
		Products []map[string]interface{} `json:"products"`
	}
	json.NewDecoder(resp.Body).Decode(&listRes)
	id := listRes.Products[0]["id"].(string)
	
	tAlive := time.Now()

	time.Sleep(100 * time.Millisecond)

	req, _ := http.NewRequest("DELETE", ts.URL+"/products/"+id, nil)
	http.DefaultClient.Do(req)

	resp, _ = http.Get(ts.URL + "/products/" + id)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}

	u := fmt.Sprintf("%s/products/%s?at=%s", ts.URL, id, url.QueryEscape(tAlive.Format(time.RFC3339Nano)))
	resp, _ = http.Get(u)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 in past, got %d", resp.StatusCode)
	}
}

func TestDiffForkVsMain(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	body := `{"name":"Diff Prod","sku":"D1","price":100,"stock":5}`
	http.Post(ts.URL+"/products", "application/json", bytes.NewBuffer([]byte(body)))
	
	time.Sleep(100 * time.Millisecond)
	resp, _ := http.Get(ts.URL + "/products")
	var listRes struct {
		Products []map[string]interface{} `json:"products"`
	}
	json.NewDecoder(resp.Body).Decode(&listRes)
	id := listRes.Products[0]["id"].(string)

	forkPoint := time.Now()
	time.Sleep(100 * time.Millisecond)

	forkBody := fmt.Sprintf(`{"name":"diff-fork","forked_from":"%s"}`, forkPoint.Format(time.RFC3339Nano))
	http.Post(ts.URL+"/timelines/fork", "application/json", bytes.NewBuffer([]byte(forkBody)))

	updateBody := `{"price":80}`
	req, _ := http.NewRequest("PUT", ts.URL+"/products/"+id+"/price?timeline=diff-fork", bytes.NewBuffer([]byte(updateBody)))
	http.DefaultClient.Do(req)

	resp, _ = http.Get(ts.URL + "/diff?timeline=diff-fork")
	var diff map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&diff)

	summary := diff["summary"].(map[string]interface{})
	if summary["products_changed"].(float64) != 1 {
		t.Errorf("expected 1 change, got %v", summary["products_changed"])
	}
}

func TestRateLimitAndRequestID(t *testing.T) {
	es := store.NewMemoryEventStore()
	h := &api.Handlers{Store: es, StartTime: time.Now()}
	cfg := config.Config{RateLimitRPS: 1}
	router := api.NewRouter(h, cfg)
	ts := httptest.NewServer(router)
	defer ts.Close()

	resp1, _ := http.Get(ts.URL + "/health")
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("first request failed: %d", resp1.StatusCode)
	}

	resp2, _ := http.Get(ts.URL + "/health")
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", resp2.StatusCode)
	}
	
	if resp2.Header.Get("X-Request-ID") == "" {
		t.Error("missing X-Request-ID in 429 response")
	}

	req, _ := http.NewRequest("GET", ts.URL+"/health", nil)
	req.Header.Set("X-Request-ID", "custom-id")
	resp3, _ := http.DefaultClient.Do(req)
	if resp3.Header.Get("X-Request-ID") != "custom-id" {
		t.Errorf("expected custom-id, got %v", resp3.Header.Get("X-Request-ID"))
	}
}

func TestInvalidAtParam(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/products?at=not-a-date")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}
