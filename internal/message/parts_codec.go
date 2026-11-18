package message

// DecodeParts decodes a stored parts column into content parts, as the
// message service does when it loads a message. It fails on a part type
// it does not know.
func DecodeParts(data []byte) ([]ContentPart, error) {
	return unmarshalParts(data)
}

// EncodeParts encodes content parts the way the message service stores
// them.
func EncodeParts(parts []ContentPart) ([]byte, error) {
	return marshalParts(parts)
}
