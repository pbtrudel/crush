package agent

import (
	"errors"

	"charm.land/fantasy"
)

// isContextOverflowError reports whether err is a provider rejection caused
// by the request exceeding the model's context window. fantasy parses the
// provider-specific variants (OpenAI, Anthropic, Google, Kronk) into
// ProviderError.ContextTooLargeErr, so no message matching is needed here.
func isContextOverflowError(err error) bool {
	var providerErr *fantasy.ProviderError
	return errors.As(err, &providerErr) && providerErr.IsContextTooLarge()
}

// shouldRecoverFromOverflow reports whether a context-overflow error should
// trigger the one-time compact-and-requeue recovery. The retried flag bounds
// recovery to a single attempt so an overflow that survives compaction (for
// example a single message larger than the window) fails normally instead
// of looping.
func shouldRecoverFromOverflow(err error, retried bool) bool {
	return !retried && isContextOverflowError(err)
}
