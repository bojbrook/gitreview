package ctxpane

import (
	"context"
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
		func(c context.Context) Section { return buildBlameSection(c, cur) },
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

// kindFor returns the SectionKind associated with task index i. Keep this in
// the same order as the tasks slice in Resolve.
func kindFor(i int) SectionKind {
	switch i {
	case 0:
		return SectionWhere
	case 1:
		return SectionBlame
	case 2:
		return SectionSymbol
	case 3:
		return SectionCrossFile
	case 4:
		return SectionHistory
	}
	return SectionWhere
}
