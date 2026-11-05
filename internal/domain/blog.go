package domain

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Validation limits mirror the CHECK constraints in the migrations.
const (
	MaxTitleLen       = 255
	MaxAuthorLen      = 100
	MaxContentLen     = 100000
	MaxDescriptionLen = 500

	DefaultPage  = 1
	DefaultLimit = 20
	MaxLimit     = 100
)

// Blog is a complete blog record.
type Blog struct {
	ID          uuid.UUID
	Title       string
	Author      string
	Content     string
	Description string
	Views       int64
	Version     int
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   *time.Time
	CreatedBy   uuid.UUID
	UpdatedBy   uuid.UUID
	DeletedBy   *uuid.UUID
}

// BlogSummary is a list item.
type BlogSummary struct {
	ID          uuid.UUID
	Title       string
	Description string
}

// BlogInput holds editable fields; create and PUT replace all of them.
type BlogInput struct {
	Title       string
	Author      string
	Content     string
	Description string
}

// Validate trims title and author and checks all limits. It returns normalized input or
// a *ValidationError containing every invalid field.
func (in BlogInput) Validate() (BlogInput, error) {
	in.Title = strings.TrimSpace(in.Title)
	in.Author = strings.TrimSpace(in.Author)

	ve := &ValidationError{}
	checkLen(ve, "title", in.Title, 1, MaxTitleLen)
	checkLen(ve, "author", in.Author, 1, MaxAuthorLen)
	checkLen(ve, "content", in.Content, 1, MaxContentLen)
	if strings.TrimSpace(in.Content) == "" && in.Content != "" {
		ve.add("content", "must not be blank")
	}
	checkLen(ve, "description", in.Description, 0, MaxDescriptionLen)
	if err := ve.orNil(); err != nil {
		return BlogInput{}, err
	}
	return in, nil
}

func checkLen(ve *ValidationError, field, v string, min, max int) {
	n := utf8.RuneCountInString(v)
	switch {
	case n < min && min == 1:
		ve.add(field, "is required")
	case n > max:
		ve.add(field, "is too long (max "+itoa(max)+" characters)")
	}
}

// PageRequest is a validated pagination request.
type PageRequest struct {
	Page  int
	Limit int
}

// NewPageRequest validates strictly; callers apply defaults before calling it.
func NewPageRequest(page, limit int) (PageRequest, error) {
	ve := &ValidationError{}
	if page < 1 {
		ve.add("page", "must be >= 1")
	}
	if limit < 1 || limit > MaxLimit {
		ve.add("limit", "must be between 1 and "+itoa(MaxLimit))
	}
	if err := ve.orNil(); err != nil {
		return PageRequest{}, err
	}
	return PageRequest{Page: page, Limit: limit}, nil
}

// Offset is the SQL OFFSET for the page.
func (p PageRequest) Offset() int { return (p.Page - 1) * p.Limit }

// BlogPage is one page of list results.
type BlogPage struct {
	Items   []BlogSummary
	Page    int
	Limit   int
	HasNext bool
}

func itoa(n int) string {
	// Small local helper to keep this package free of fmt/strconv noise.
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
