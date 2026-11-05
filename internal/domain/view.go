package domain

import (
	"time"

	"github.com/google/uuid"
)

// IPHashSize is the stored length of the HMAC'd client IP (bytes).
const IPHashSize = 16

// IPHash is the truncated HMAC of a client IP. Raw IPs are never stored.
type IPHash [IPHashSize]byte

// ViewDate returns the UTC calendar date (midnight UTC) used as the daily
// dedup key. Always derive it through this function, never from DB now().
func ViewDate(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// ViewKey identifies one unique daily view: blog + UTC date + client IP.
type ViewKey struct {
	BlogID uuid.UUID
	Date   time.Time
	IPHash IPHash
}

// NewViewKey builds the key for a view happening at now.
func NewViewKey(blogID uuid.UUID, now time.Time, ip IPHash) ViewKey {
	return ViewKey{BlogID: blogID, Date: ViewDate(now), IPHash: ip}
}
