package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/sky/blog-service/internal/domain"
)

func TestListBlogsAPI(t *testing.T) {
	a := newIntegrationAPI(t)
	a.create("First")
	a.create("Second")
	w := a.do(http.MethodGet, "/api/v1/blogs?page=1&limit=20", nil, domain.Anonymous())
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Items []struct{ Title string } `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) < 2 || body.Items[0].Title != "Second" || body.Items[1].Title != "First" {
		t.Fatalf("items=%+v", body.Items)
	}
}

func TestGetBlogAPI(t *testing.T) {
	a := newIntegrationAPI(t)
	id := a.create("Detail")
	for i := 0; i < 2; i++ {
		w := a.do(http.MethodGet, "/api/v1/blogs/"+id, nil, domain.Anonymous())
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var body struct {
			Title string `json:"title"`
			Views int64  `json:"views"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Title != "Detail" || body.Views != 1 {
			t.Fatalf("body=%+v", body)
		}
	}
}

func TestCreateBlogAPI(t *testing.T) {
	a := newIntegrationAPI(t)
	w := a.do(http.MethodPost, "/api/v1/blogs", integrationBlogInput("Created"), a.editor)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	a.track(body.ID)
	if body.Title != "Created" || body.Version != 1 || w.Header().Get("Location") != "/api/v1/blogs/"+body.ID {
		t.Fatalf("body=%+v location=%q", body, w.Header().Get("Location"))
	}
	var storedTitle string
	if err := pool.QueryRow(context.Background(), `SELECT title FROM blogs WHERE id = $1`, body.ID).Scan(&storedTitle); err != nil {
		t.Fatalf("read created row from real PostgreSQL: %v", err)
	}
	if storedTitle != "Created" {
		t.Fatalf("stored title=%q", storedTitle)
	}
}

func TestUpdateBlogAPI(t *testing.T) {
	a := newIntegrationAPI(t)
	id := a.create("Before")
	w := a.do(http.MethodPut, "/api/v1/blogs/"+id, integrationBlogInput("After"), a.editor)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Title   string `json:"title"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Title != "After" || body.Version != 2 {
		t.Fatalf("body=%+v", body)
	}
}

func TestDeleteBlogAPI(t *testing.T) {
	a := newIntegrationAPI(t)
	id := a.create("Delete")
	w := a.do(http.MethodDelete, "/api/v1/blogs/"+id, nil, a.admin)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	w = a.do(http.MethodGet, "/api/v1/blogs/"+id, nil, domain.Anonymous())
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET after DELETE returned status=%d body=%s", w.Code, w.Body.String())
	}
	var deletedAt *time.Time
	if err := pool.QueryRow(context.Background(), `SELECT deleted_at FROM blogs WHERE id = $1`, id).Scan(&deletedAt); err != nil {
		t.Fatalf("query soft-deleted row: %v", err)
	}
	if deletedAt == nil {
		t.Fatal("DELETE removed the row or failed to mark it as soft-deleted")
	}
}
