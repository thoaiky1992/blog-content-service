package app

import (
	"context"

	"github.com/sky/blog-service/internal/domain"
)

// ReportService streams CSV report rows.
type ReportService struct {
	repo BlogRepository
}

func NewReportService(repo BlogRepository) *ReportService {
	return &ReportService{repo: repo}
}

// Export checks permission, then streams active blogs to fn. Permission is checked
// before the first row so the handler can still return a 403 response.
func (s *ReportService) Export(ctx context.Context, actor domain.Actor, fn func(ReportRow) error) error {
	if err := actor.Require(domain.PermReportExport); err != nil {
		return err
	}
	return s.repo.StreamActive(ctx, fn)
}
