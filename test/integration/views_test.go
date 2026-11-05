package integration

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/sky/blog-service/internal/domain"
)

func TestDailyUniqueViewCounting(t *testing.T) {
	a := newIntegrationAPI(t)
	id := a.create("daily views")
	for i := 0; i < 2; i++ {
		if w := a.doIP(http.MethodGet, "/api/v1/blogs/"+id, nil, domain.Anonymous(), "198.51.100.20"); w.Code != http.StatusOK {
			t.Fatalf("day-one read %d: status=%d body=%s", i, w.Code, w.Body.String())
		}
	}
	a.clk.Advance(24 * time.Hour)
	if w := a.doIP(http.MethodGet, "/api/v1/blogs/"+id, nil, domain.Anonymous(), "198.51.100.20"); w.Code != http.StatusOK {
		t.Fatalf("day-two read: status=%d body=%s", w.Code, w.Body.String())
	}
	assertStoredViews(t, id, 2, 2)
}

func TestConcurrentRequestsFromSameIP(t *testing.T) {
	a := newIntegrationAPI(t)
	id := a.create("same-IP concurrency")
	runConcurrentReads(t, a, id, 20, func(int) string { return "203.0.113.10" })
	assertStoredViews(t, id, 1, 1)
}

func TestConcurrentRequestsFromDifferentIPs(t *testing.T) {
	a := newIntegrationAPI(t)
	id := a.create("different-IP concurrency")
	const requests = 20
	runConcurrentReads(t, a, id, requests, func(i int) string { return fmt.Sprintf("203.0.113.%d", i+1) })
	assertStoredViews(t, id, requests, requests)
}

func TestViewRacingWithDeleteDoesNotCountDeletedBlog(t *testing.T) {
	a := newIntegrationAPI(t)
	id := a.create("view racing with delete")

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(context.Background(), `
		UPDATE blogs
		SET deleted_at = $2, deleted_by = $3
		WHERE id = $1`, id, a.clk.Now(), a.admin.ID); err != nil {
		t.Fatal(err)
	}

	result := make(chan int, 1)
	go func() {
		w := a.doIP(http.MethodGet, "/api/v1/blogs/"+id, nil, domain.Anonymous(), "198.51.100.30")
		result <- w.Code
	}()

	select {
	case status := <-result:
		t.Fatalf("request returned status %d before the delete transaction completed", status)
	case <-time.After(100 * time.Millisecond):
		// The request is waiting for the delete transaction's row lock.
	}

	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status := <-result; status != http.StatusNotFound {
		t.Fatalf("status=%d, want=%d", status, http.StatusNotFound)
	}
	assertStoredViews(t, id, 0, 0)
}

func runConcurrentReads(t *testing.T, a *integrationAPI, id string, count int, ip func(int) string) {
	t.Helper()
	start := make(chan struct{})
	statuses := make(chan int, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			statuses <- a.doIP(http.MethodGet, "/api/v1/blogs/"+id, nil, domain.Anonymous(), ip(i)).Code
		}(i)
	}
	close(start)
	wg.Wait()
	close(statuses)
	for status := range statuses {
		if status != http.StatusOK {
			t.Errorf("concurrent read returned status=%d", status)
		}
	}
}

func assertStoredViews(t *testing.T, id string, wantViews, wantMarkers int) {
	t.Helper()
	var views, markers int
	if err := pool.QueryRow(context.Background(), `SELECT views FROM blogs WHERE id = $1`, id).Scan(&views); err != nil {
		t.Fatalf("query stored views: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM blog_daily_views WHERE blog_id = $1`, id).Scan(&markers); err != nil {
		t.Fatalf("query view markers: %v", err)
	}
	if views != wantViews || markers != wantMarkers {
		t.Fatalf("stored views=%d markers=%d, want %d/%d", views, markers, wantViews, wantMarkers)
	}
}
