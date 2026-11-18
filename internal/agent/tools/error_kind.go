package tools

import (
	"charm.land/fantasy"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ErrorKind classifies a failed tool result for the session log. It is
// stored under "error_kind" in the result's metadata, which is never part
// of a provider request. The list is closed: a failure outside it carries
// no kind, and adding one is a code change with a test.
type ErrorKind string

const (
	// ErrorKindNotRead is an edit or write refused because the file was
	// never read in the session.
	ErrorKindNotRead ErrorKind = "not_read"
	// ErrorKindChangedSinceRead is an edit or write refused because the
	// file changed after it was last read.
	ErrorKindChangedSinceRead ErrorKind = "changed_since_read"
	// ErrorKindTextNotFound is a find-and-replace whose text is not in the
	// file.
	ErrorKindTextNotFound ErrorKind = "text_not_found"
	// ErrorKindMultipleMatches is a find-and-replace whose text appears more
	// than once while replace_all is off.
	ErrorKindMultipleMatches ErrorKind = "multiple_matches"
	// ErrorKindPermissionDenied is a call the user refused permission for.
	ErrorKindPermissionDenied ErrorKind = "permission_denied"
)

const errorKindKey = "error_kind"

// setErrorKind returns metadata with kind stored under "error_kind",
// keeping every other key. Metadata that is not a JSON object is returned
// unchanged, because a stamp must never alter or break a result.
func setErrorKind(metadata string, kind ErrorKind) string {
	if metadata == "" {
		metadata = "{}"
	}
	if !gjson.Valid(metadata) || !gjson.Parse(metadata).IsObject() {
		return metadata
	}
	updated, err := sjson.Set(metadata, errorKindKey, string(kind))
	if err != nil {
		return metadata
	}
	return updated
}

// withErrorKind records kind in a tool response's metadata. Call it after
// any fantasy.WithResponseMetadata, which replaces the metadata whole.
func withErrorKind(resp fantasy.ToolResponse, kind ErrorKind) fantasy.ToolResponse {
	resp.Metadata = setErrorKind(resp.Metadata, kind)
	return resp
}
