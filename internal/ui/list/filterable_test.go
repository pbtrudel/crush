package list

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// countingFilterItem counts Filter calls so filter memoization is
// observable from the outside.
type countingFilterItem struct {
	*Versioned
	filter string
	calls  int
}

func newCountingFilterItem(filter string) *countingFilterItem {
	return &countingFilterItem{Versioned: NewVersioned(), filter: filter}
}

func (i *countingFilterItem) Filter() string          { i.calls++; return i.filter }
func (i *countingFilterItem) Render(width int) string { return i.filter }
func (i *countingFilterItem) Finished() bool          { return true }

func TestFilterableListMemoizesFiltering(t *testing.T) {
	t.Parallel()

	a := newCountingFilterItem("alpha")
	b := newCountingFilterItem("beta")
	f := NewFilterableList(a, b)

	// Empty query: no filtering work, result is all items.
	require.Len(t, f.FilteredItems(), 2)
	require.Zero(t, a.calls+b.calls)

	f.SetFilter("a")
	require.Len(t, f.FilteredItems(), 2)
	calls := a.calls + b.calls
	require.Positive(t, calls)

	// Repeated calls with the same query hit the cache.
	require.Len(t, f.FilteredItems(), 2)
	require.Equal(t, calls, a.calls+b.calls)

	// Rendering applies the filtered items without re-filtering.
	f.SetSize(40, 10)
	require.Contains(t, f.Render(), "alpha")
	require.Contains(t, f.Render(), "beta")
	require.Equal(t, calls, a.calls+b.calls)

	// A new query re-filters.
	f.SetFilter("alp")
	require.Len(t, f.FilteredItems(), 1)
	require.Greater(t, a.calls+b.calls, calls)

	// Changing the items re-filters.
	c := newCountingFilterItem("gamma")
	f.SetItems(a, c)
	require.Len(t, f.FilteredItems(), 1)
}

func BenchmarkFilterableListRender(b *testing.B) {
	items := make([]FilterableItem, 200)
	for i := range items {
		items[i] = newCountingFilterItem(fmt.Sprintf("item-%d", i))
	}
	f := NewFilterableList(items...)
	f.SetSize(80, 24)
	f.SetFilter("item-1")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = f.Render()
	}
}
