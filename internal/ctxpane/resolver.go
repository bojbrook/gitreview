package ctxpane

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// PerSectionTimeout caps how long any single section may take to compute.
// On timeout, that section is rendered as Status=Loading with a "…" item
// and the rest of the pane still draws.
const PerSectionTimeout = 300 * time.Millisecond

// Resolve computes the Payload for the given cursor. Each section runs in its
// own goroutine with PerSectionTimeout enforced independently, so a slow blame
// or grep cannot stall the rest of the pane. Sections that complete as
// StatusEmpty are filtered out, except for SectionWhere which is always kept.
//
// The returned Payload always has at least Section{Kind: SectionWhere}.
func Resolve(ctx context.Context, cur Cursor) Payload {
	tasks := []func(context.Context) Section{
		func(c context.Context) Section { return buildWhereSection(cur) },
		func(c context.Context) Section { return buildSymbolSection(c, cur) },
		func(c context.Context) Section { return buildCrossFileSection(c, cur) },
		func(c context.Context) Section { return buildBlameSection(c, cur) },
		func(c context.Context) Section { return buildHistorySection(c, cur) },
		func(c context.Context) Section { return buildCommentsSection(cur) },
	}
	out := make([]Section, len(tasks))
	var wg sync.WaitGroup
	for i, t := range tasks {
		i, t := i, t
		wg.Add(1)
		go func() {
			defer wg.Done()
			subCtx, cancel := context.WithTimeout(ctx, PerSectionTimeout)
			defer cancel()
			done := make(chan Section, 1)
			go func() { done <- t(subCtx) }()
			select {
			case s := <-done:
				out[i] = s
			case <-subCtx.Done():
				out[i] = Section{Kind: kindFor(i), Status: StatusLoading}
			}
		}()
	}
	wg.Wait()
	// Filter out fully-empty sections except Where (always kept).
	var sections []Section
	for _, s := range out {
		if s.Kind == SectionWhere {
			sections = append(sections, s)
			continue
		}
		if s.Status == StatusEmpty {
			continue
		}
		sections = append(sections, s)
	}
	return Payload{Sections: sections}
}

// kindFor maps a task-slice index to the SectionKind it produces. Must be
// kept in sync with the tasks slice in Resolve. Panics on unknown index to
// fail loudly rather than silently mislabel a section.
func kindFor(i int) SectionKind {
	switch i {
	case 0:
		return SectionWhere
	case 1:
		return SectionSymbol
	case 2:
		return SectionCrossFile
	case 3:
		return SectionBlame
	case 4:
		return SectionHistory
	case 5:
		return SectionComments
	}
	panic(fmt.Sprintf("kindFor: no SectionKind for task index %d — update kindFor when adding tasks", i))
}
