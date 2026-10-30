package message

import (
	"cmp"
	"slices"
	"strings"
)

type Attachment struct {
	FilePath string
	FileName string
	MimeType string
	Content  []byte
}

// Ref returns the attachment's stable identifier: the file path when the
// attachment has one, otherwise the file name. Both the live request and the
// replayed history use Ref, so the identifier the model sees does not change
// between the turn that sends the attachment and every later turn that
// replays it. A mismatch there would break the provider's prompt cache.
func (a Attachment) Ref() string { return cmp.Or(a.FilePath, a.FileName) }

func (a Attachment) IsText() bool     { return strings.HasPrefix(a.MimeType, "text/") }
func (a Attachment) IsImage() bool    { return strings.HasPrefix(a.MimeType, "image/") }
func (a Attachment) IsAudio() bool    { return strings.HasPrefix(a.MimeType, "audio/") }
func (a Attachment) IsMarkdown() bool { return a.MimeType == "text/markdown" }

// ContainsTextAttachment returns true if any of the attachments is a text attachment.
func ContainsTextAttachment(attachments []Attachment) bool {
	return slices.ContainsFunc(attachments, func(a Attachment) bool {
		return a.IsText()
	})
}
