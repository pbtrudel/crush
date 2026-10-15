package list

import (
	"github.com/sahilm/fuzzy"
)

// FilterableItem is an item that can be filtered via a query.
type FilterableItem interface {
	Item
	// Filter returns the value to be used for filtering.
	Filter() string
}

// MatchSettable is an interface for items that can have their match indexes
// and match score set.
type MatchSettable interface {
	SetMatch(fuzzy.Match)
}

// FilterableList is a list that takes filterable items that can be filtered
// via a settable query.
type FilterableList struct {
	*List
	items []FilterableItem
	query string

	// itemsVersion bumps whenever the item set changes.
	itemsVersion int
	// The filter cache memoizes FilteredItems, keyed by the query and
	// the items version, so repeated renders and draws do not re-run
	// the fuzzy pass.
	filterValid bool
	lastQuery   string
	lastVersion int
	filtered    []Item
}

// NewFilterableList creates a new filterable list.
func NewFilterableList(items ...FilterableItem) *FilterableList {
	f := &FilterableList{
		List:  NewList(),
		items: items,
	}
	f.RegisterRenderCallback(FocusedRenderCallback(f.List))
	f.SetItems(items...)
	return f
}

// SetItems sets the list items and updates the filtered items.
func (f *FilterableList) SetItems(items ...FilterableItem) {
	f.items = items
	f.itemsVersion++
	fitems := make([]Item, len(items))
	for i, item := range items {
		fitems[i] = item
	}
	f.List.SetItems(fitems...)
}

// AppendItems appends items to the list and updates the filtered items.
func (f *FilterableList) AppendItems(items ...FilterableItem) {
	f.items = append(f.items, items...)
	f.itemsVersion++
	itms := make([]Item, len(f.items))
	for i, item := range f.items {
		itms[i] = item
	}
	f.List.SetItems(itms...)
}

// PrependItems prepends items to the list and updates the filtered items.
func (f *FilterableList) PrependItems(items ...FilterableItem) {
	f.items = append(items, f.items...)
	f.itemsVersion++
	itms := make([]Item, len(f.items))
	for i, item := range f.items {
		itms[i] = item
	}
	f.List.SetItems(itms...)
}

// SetFilter sets the filter query and updates the list items.
func (f *FilterableList) SetFilter(q string) {
	f.query = q
	f.applyFiltered()
	f.ScrollToTop()
}

// FilterableItemsSource is a type that implements [fuzzy.Source] for filtering
// [FilterableItem]s.
type FilterableItemsSource []FilterableItem

// Len returns the length of the source.
func (f FilterableItemsSource) Len() int {
	return len(f)
}

// String returns the string representation of the item at index i.
func (f FilterableItemsSource) String(i int) string {
	return f[i].Filter()
}

// FilteredItems returns the visible items after filtering. The result
// is memoized on the (query, items) pair: repeated calls while neither
// changed return the cached slice without re-running the fuzzy pass.
func (f *FilterableList) FilteredItems() []Item {
	if f.filterValid && f.query == f.lastQuery && f.itemsVersion == f.lastVersion {
		return f.filtered
	}

	var result []Item
	if f.query == "" {
		result = make([]Item, len(f.items))
		for i, item := range f.items {
			if ms, ok := item.(MatchSettable); ok {
				ms.SetMatch(fuzzy.Match{})
				item = ms.(FilterableItem)
			}
			result[i] = item
		}
	} else {
		items := FilterableItemsSource(f.items)
		matches := fuzzy.FindFrom(f.query, items)
		result = make([]Item, 0, len(matches))
		for i := range matches {
			match := matches[i]
			item := items[match.Index]
			if ms, ok := item.(MatchSettable); ok {
				ms.SetMatch(match)
				item = ms.(FilterableItem)
			}
			result = append(result, item)
		}
	}

	f.filterValid = true
	f.lastQuery = f.query
	f.lastVersion = f.itemsVersion
	f.filtered = result
	return result
}

// applyFiltered pushes the current filtered items into the inner list,
// unless they are already applied. Skipping the SetItems call keeps the
// scroll position stable across renders.
func (f *FilterableList) applyFiltered() {
	filtered := f.FilteredItems()
	if !sameItemSlice(f.List.items, filtered) {
		f.List.SetItems(filtered...)
	}
}

// sameItemSlice reports whether two item slices share the same backing
// array. Two empty slices count as equal.
func sameItemSlice(a, b []Item) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	return &a[0] == &b[0]
}

// Render renders the filterable list.
func (f *FilterableList) Render() string {
	f.applyFiltered()
	return f.List.Render()
}
