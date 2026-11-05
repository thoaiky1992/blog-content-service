package domain

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPermissionMatrix(t *testing.T) {
	perms := []Permission{PermBlogRead, PermBlogCreate, PermBlogUpdate, PermBlogDelete, PermReportExport}
	uid := uuid.New()
	tests := []struct {
		name  string
		actor Actor
		want  map[Permission]bool
	}{
		{"admin", Actor{ID: uid, Role: RoleAdmin}, map[Permission]bool{
			PermBlogRead: true, PermBlogCreate: true, PermBlogUpdate: true, PermBlogDelete: true, PermReportExport: true}},
		{"editor", Actor{ID: uid, Role: RoleEditor}, map[Permission]bool{
			PermBlogRead: true, PermBlogCreate: true, PermBlogUpdate: true}},
		{"reader", Actor{ID: uid, Role: RoleReader}, map[Permission]bool{PermBlogRead: true}},
		{"reporter", Actor{ID: uid, Role: RoleReporter}, map[Permission]bool{PermBlogRead: true, PermReportExport: true}},
		{"anonymous", Anonymous(), map[Permission]bool{PermBlogRead: true}},
		{"unknown role", Actor{ID: uid, Role: "ghost"}, map[Permission]bool{}},
	}
	for _, tt := range tests {
		for _, p := range perms {
			if got := tt.actor.Can(p); got != tt.want[p] {
				t.Errorf("%s Can(%s) = %v, want %v", tt.name, p, got, tt.want[p])
			}
		}
	}
}

func TestRequireErrors(t *testing.T) {
	if err := Anonymous().Require(PermBlogCreate); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("anonymous: got %v, want ErrUnauthenticated", err)
	}
	reader := Actor{ID: uuid.New(), Role: RoleReader}
	if err := reader.Require(PermBlogCreate); !errors.Is(err, ErrForbidden) {
		t.Errorf("reader: got %v, want ErrForbidden", err)
	}
	if err := reader.Require(PermBlogRead); err != nil {
		t.Errorf("reader read: got %v", err)
	}
}

func TestBlogInputValidate(t *testing.T) {
	ok := BlogInput{Title: "  T  ", Author: " A ", Content: "c", Description: ""}
	got, err := ok.Validate()
	if err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	if got.Title != "T" || got.Author != "A" {
		t.Errorf("title/author not trimmed: %+v", got)
	}

	tests := []struct {
		name  string
		in    BlogInput
		field string
	}{
		{"missing title", BlogInput{Author: "a", Content: "c"}, "title"},
		{"blank title", BlogInput{Title: "   ", Author: "a", Content: "c"}, "title"},
		{"long title", BlogInput{Title: strings.Repeat("x", 256), Author: "a", Content: "c"}, "title"},
		{"missing author", BlogInput{Title: "t", Content: "c"}, "author"},
		{"long author", BlogInput{Title: "t", Author: strings.Repeat("x", 101), Content: "c"}, "author"},
		{"missing content", BlogInput{Title: "t", Author: "a"}, "content"},
		{"blank content", BlogInput{Title: "t", Author: "a", Content: " \n "}, "content"},
		{"long content", BlogInput{Title: "t", Author: "a", Content: strings.Repeat("x", 100001)}, "content"},
		{"long description", BlogInput{Title: "t", Author: "a", Content: "c", Description: strings.Repeat("x", 501)}, "description"},
	}
	for _, tt := range tests {
		_, err := tt.in.Validate()
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: got %v, want ValidationError", tt.name, err)
			continue
		}
		found := false
		for _, f := range ve.Fields {
			found = found || f.Field == tt.field
		}
		if !found {
			t.Errorf("%s: no error for field %q in %+v", tt.name, tt.field, ve.Fields)
		}
	}
}

func TestValidateCountsRunesNotBytes(t *testing.T) {
	in := BlogInput{Title: strings.Repeat("é", 255), Author: "a", Content: "c"} // 510 bytes, 255 runes
	if _, err := in.Validate(); err != nil {
		t.Errorf("255 runes must be accepted: %v", err)
	}
}

func TestValidateReportsAllFields(t *testing.T) {
	_, err := BlogInput{}.Validate()
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Fields) != 3 {
		t.Errorf("want 3 field errors (title, author, content), got %v", err)
	}
}

func TestNewPageRequest(t *testing.T) {
	tests := []struct {
		page, limit int
		ok          bool
	}{
		{1, 20, true}, {5, 100, true}, {1, 1, true},
		{0, 20, false}, {-1, 20, false}, {1, 0, false}, {1, 101, false}, {1, -5, false},
	}
	for _, tt := range tests {
		_, err := NewPageRequest(tt.page, tt.limit)
		if (err == nil) != tt.ok {
			t.Errorf("page=%d limit=%d: err=%v, want ok=%v", tt.page, tt.limit, err, tt.ok)
		}
	}
	p, _ := NewPageRequest(3, 20)
	if p.Offset() != 40 {
		t.Errorf("Offset = %d, want 40", p.Offset())
	}
}

func TestViewDateUTCBoundary(t *testing.T) {
	id := uuid.New()
	ip := IPHash{1}
	before := time.Date(2026, 11, 5, 23, 59, 59, 999_000_000, time.UTC)
	after := before.Add(time.Millisecond) // 2026-11-06 00:00:00 UTC
	if NewViewKey(id, before, ip).Date.Equal(NewViewKey(id, after, ip).Date) {
		t.Error("23:59:59.999 and 00:00:00 UTC must be different dates")
	}
	want := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	if got := ViewDate(before); !got.Equal(want) {
		t.Errorf("ViewDate = %v, want %v", got, want)
	}

	// Same instant in a non-UTC zone must map to the UTC date, not the local one.
	hcm := time.FixedZone("UTC+7", 7*3600)
	local := time.Date(2026, 11, 6, 5, 0, 0, 0, hcm) // = 2026-11-05 22:00 UTC
	if got := ViewDate(local); !got.Equal(want) {
		t.Errorf("ViewDate(local) = %v, want %v", got, want)
	}
}
