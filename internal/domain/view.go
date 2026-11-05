package domain

import (
	"net/netip"
	"time"

	"github.com/google/uuid"
)

// ViewDate returns the UTC calendar date (midnight UTC) used as the daily dedup key.
// Always derive it through this function, never from database now().
func ViewDate(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// ViewKey identifies one unique daily view: blog + UTC date + client IP.
type ViewKey struct {
	BlogID uuid.UUID
	Date   time.Time
	IP     netip.Addr
}

// NewViewKey builds the key for a view occurring at now.
func NewViewKey(blogID uuid.UUID, now time.Time, ip netip.Addr) ViewKey {
	return ViewKey{BlogID: blogID, Date: ViewDate(now), IP: ip.WithZone("").Unmap()}
}
