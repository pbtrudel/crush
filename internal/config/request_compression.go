package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Request body encodings a provider can list in request_compression.
const (
	RequestCompressionBrotli = "br"
	RequestCompressionGzip   = "gzip"
)

// ValidateRequestCompression checks that every provider's
// request_compression names only encodings Crush can send, so a typo is a
// load error instead of a provider that silently sends plain JSON.
func (c *Config) ValidateRequestCompression() error {
	if c.Providers == nil {
		return nil
	}
	providers := c.Providers.Copy()
	for _, id := range slices.Sorted(maps.Keys(providers)) {
		p := providers[id]
		for _, encoding := range p.RequestCompression {
			switch encoding {
			case RequestCompressionBrotli, RequestCompressionGzip:
			default:
				return fmt.Errorf("provider %s: request_compression: unknown encoding %q (expected %s)",
					id, encoding, strings.Join([]string{RequestCompressionBrotli, RequestCompressionGzip}, " or "))
			}
		}
	}
	return nil
}
