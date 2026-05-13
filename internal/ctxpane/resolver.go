package ctxpane

import (
	"context"
	"time"
)

// PerSectionTimeout caps how long any single section may take to compute.
// On timeout, that section is rendered as Status=Loading with a "…" item
// and the rest of the pane still draws.
const PerSectionTimeout = 300 * time.Millisecond

// Resolve computes the Payload for the given cursor. Each section runs in
// its own goroutine; errors and timeouts are isolated to that section. The
// returned Payload always has at least Section{Kind: SectionWhere}.
func Resolve(ctx context.Context, cur Cursor) Payload {
	return Payload{
		Sections: []Section{
			buildWhereSection(cur),
		},
	}
}
