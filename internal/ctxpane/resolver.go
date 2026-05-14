package ctxpane

import (
	"context"
	"time"
)

// PerSectionTimeout caps how long any single section may take to compute.
// On timeout, that section is rendered as Status=Loading with a "…" item
// and the rest of the pane still draws.
const PerSectionTimeout = 300 * time.Millisecond

// Resolve computes the Payload for the given cursor. In v0 (this task),
// sections are computed sequentially. A later task will fan out per goroutine
// with PerSectionTimeout enforced per section so a slow blame or grep can't
// stall the whole pane.
//
// The returned Payload always has at least Section{Kind: SectionWhere}.
func Resolve(ctx context.Context, cur Cursor) Payload {
	return Payload{
		Sections: []Section{
			buildWhereSection(cur),
		},
	}
}
