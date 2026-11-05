package handler

import (
	"encoding/csv"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/sky/blog-service/internal/app"
	"github.com/sky/blog-service/internal/transport/http/httpx"
)

// csvFlushEvery bounds how many rows are buffered before being sent.
const csvFlushEvery = 500

var csvHeader = []string{
	"id", "title", "author", "description", "views",
	"created_at", "updated_at", "created_by", "updated_by",
}

// Report serves the CSV export.
type Report struct {
	reports *app.ReportService
	clock   app.Clock
}

func NewReport(reports *app.ReportService, clock app.Clock) *Report {
	return &Report{reports: reports, clock: clock}
}

// Export: GET /api/v1/reports/blogs. Streams active blogs as CSV straight from
// the database cursor to the response; nothing is accumulated in memory.
//
// Permission is checked before the first row, so a forbidden caller still gets
// a normal JSON 403. Once streaming has started the status is already 200, so
// a later failure aborts the connection: the client sees a truncated transfer
// instead of a file that looks complete.
func (h *Report) Export(c *gin.Context) {
	var (
		w       *csv.Writer
		started bool
		rows    int
	)
	start := func() error {
		if started {
			return nil
		}
		started = true
		hd := c.Writer.Header()
		hd.Set("Content-Type", "text/csv; charset=utf-8")
		hd.Set("Content-Disposition", `attachment; filename="blogs-`+h.clock.Now().UTC().Format("20060102")+`.csv"`)
		hd.Set("Cache-Control", "no-store")
		c.Status(http.StatusOK)
		w = csv.NewWriter(c.Writer)
		return w.Write(csvHeader)
	}

	err := h.reports.Export(c.Request.Context(), httpx.ActorFrom(c), func(r app.ReportRow) error {
		if err := start(); err != nil {
			return err
		}
		rows++
		if err := w.Write([]string{
			r.ID.String(),
			sanitizeCell(r.Title),
			sanitizeCell(r.Author),
			sanitizeCell(r.Description),
			strconv.FormatInt(r.Views, 10),
			formatTime(r.CreatedAt),
			formatTime(r.UpdatedAt),
			r.CreatedBy.String(),
			r.UpdatedBy.String(),
		}); err != nil {
			return err
		}
		if rows%csvFlushEvery == 0 {
			w.Flush()
			if err := w.Error(); err != nil {
				return err
			}
			c.Writer.Flush()
		}
		return nil
	})

	if err == nil {
		err = start() // an empty report still returns the header row
	}
	if err != nil {
		if !started {
			httpx.WriteError(c, err)
			return
		}
		slog.Error("csv export aborted mid-stream",
			"request_id", httpx.RequestID(c), "rows_sent", rows, "error", err)
		panic(http.ErrAbortHandler)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		slog.Error("csv export flush failed", "request_id", httpx.RequestID(c), "error", err)
		panic(http.ErrAbortHandler)
	}
}

// sanitizeCell defuses CSV/formula injection: a cell that a spreadsheet could
// interpret as a formula (leading = + - @, or tab/CR which some apps strip
// before evaluating) is prefixed with a single quote.
func sanitizeCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
