package claudeagent

import (
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/stretchr/testify/require"
)

var opusPricing = catwalk.Pricing{
	Input:  5,
	Output: 25,
}

func anthropicLookup(models ...catwalk.Model) func(string) (catwalk.Model, bool) {
	index := make(map[string]catwalk.Model, len(models))
	for _, model := range models {
		index[model.ID] = model
	}
	return func(id string) (catwalk.Model, bool) {
		model, ok := index[id]
		return model, ok
	}
}

func TestProviderEntryContextWindow(t *testing.T) {
	t.Parallel()

	lookup := anthropicLookup(catwalk.Model{
		ID:            "claude-opus-5-5",
		ContextWindow: 1_000_000,
		Pricing:       opusPricing,
	})

	tests := []struct {
		name   string
		model  CatalogModel
		lookup func(string) (catwalk.Model, bool)
		want   int64
	}{
		{
			name:   "anthropic catalog supplies the window",
			model:  CatalogModel{ID: "opus", Resolved: "claude-opus-5-5"},
			lookup: lookup,
			want:   1_000_000,
		},
		{
			name:  "1m suffix on the id without a lookup",
			model: CatalogModel{ID: "opus[1m]", Resolved: "claude-opus-9-9"},
			want:  1_000_000,
		},
		{
			name:  "1m suffix on the resolved name without a lookup",
			model: CatalogModel{ID: "custom", Resolved: "claude-opus-9-9[1m]"},
			want:  1_000_000,
		},
		{
			name:   "cli reported window wins",
			model:  CatalogModel{ID: "opus[1m]", Resolved: "claude-opus-5-5", ContextWindow: 300_000},
			lookup: lookup,
			want:   300_000,
		},
		{
			name:  "unknown model without a lookup",
			model: CatalogModel{ID: "mystery", Resolved: "claude-mystery-1"},
			want:  defaultContextWindow,
		},
		{
			name:   "unknown model with a lookup",
			model:  CatalogModel{ID: "mystery", Resolved: "claude-mystery-1"},
			lookup: lookup,
			want:   defaultContextWindow,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			entry := ProviderEntry(&Catalog{Models: []CatalogModel{tt.model}}, tt.lookup)
			require.Len(t, entry.Models, 1)
			require.Equal(t, tt.want, entry.Models[0].ContextWindow)
		})
	}
}

func TestProviderEntryPricing(t *testing.T) {
	t.Parallel()

	lookup := anthropicLookup(catwalk.Model{
		ID:            "claude-opus-5-5",
		ContextWindow: 1_000_000,
		Pricing:       opusPricing,
	})

	tests := []struct {
		name  string
		model CatalogModel
		want  catwalk.Pricing
	}{
		{
			name:  "resolved name matches",
			model: CatalogModel{ID: "opus", Resolved: "claude-opus-5-5"},
			want:  opusPricing,
		},
		{
			name:  "1m variant shares the base prices",
			model: CatalogModel{ID: "opus[1m]", Resolved: "claude-opus-5-5[1m]"},
			want:  opusPricing,
		},
		{
			name:  "provider prefix is trimmed",
			model: CatalogModel{ID: "opus", Resolved: "anthropic.claude-opus-5-5"},
			want:  opusPricing,
		},
		{
			name:  "unknown model has no prices",
			model: CatalogModel{ID: "mystery", Resolved: "claude-mystery-1"},
			want:  catwalk.Pricing{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			entry := ProviderEntry(&Catalog{Models: []CatalogModel{tt.model}}, lookup)
			require.Len(t, entry.Models, 1)
			require.Equal(t, tt.want, entry.Models[0].Pricing)
		})
	}

	entry := ProviderEntry(&Catalog{Models: []CatalogModel{{ID: "opus", Resolved: "claude-opus-5-5"}}}, nil)
	require.Equal(t, catwalk.Pricing{}, entry.Models[0].Pricing)
}

func TestProviderEntryName(t *testing.T) {
	t.Parallel()

	lookup := anthropicLookup(
		catwalk.Model{ID: "claude-opus-5-5", Name: "Claude Opus 5.5"},
		catwalk.Model{ID: "claude-fable-5-1", Name: "Claude Fable 5.1"},
	)

	tests := []struct {
		name   string
		model  CatalogModel
		lookup func(string) (catwalk.Model, bool)
		want   string
	}{
		{
			name:   "anthropic catalog supplies the version",
			model:  CatalogModel{ID: "opus", Name: "Opus", Resolved: "claude-opus-5-5"},
			lookup: lookup,
			want:   "Opus 5.5",
		},
		{
			name:   "1m variant takes the base model name",
			model:  CatalogModel{ID: "claude-fable-5-1[1m]", Name: "Fable", Resolved: "claude-fable-5-1"},
			lookup: lookup,
			want:   "Fable 5.1",
		},
		{
			name:  "cli name without a lookup",
			model: CatalogModel{ID: "opus", Name: "Opus", Resolved: "claude-opus-5-5"},
			want:  "Opus",
		},
		{
			name:   "cli name for an unknown model",
			model:  CatalogModel{ID: "mystery", Name: "Mystery", Resolved: "claude-mystery-1"},
			lookup: lookup,
			want:   "Mystery",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			entry := ProviderEntry(&Catalog{Models: []CatalogModel{tt.model}}, tt.lookup)
			require.Len(t, entry.Models, 1)
			require.Equal(t, tt.want, entry.Models[0].Name)
		})
	}
}
