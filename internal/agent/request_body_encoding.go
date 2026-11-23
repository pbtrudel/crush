package agent

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"

	brrr "github.com/molecule-man/go-brrr"
)

// requestBodyCodec decodes and re-encodes one Content-Encoding.
type requestBodyCodec struct {
	decode func([]byte) ([]byte, error)
	encode func([]byte) ([]byte, error)
}

// requestBodyCodecs are the request encodings the fork transports can see
// through. fantasy compresses Hyper bodies with brotli or gzip outside these
// transports, so without this they would be handed bytes they cannot read.
var requestBodyCodecs = map[string]requestBodyCodec{
	"gzip": {decode: gunzipBody, encode: gzipBody},
	"br":   {decode: unbrotliBody, encode: brotliBody},
}

// readRequestBody reads and closes req.Body and returns the plain body with
// the encoding it arrived in. A body with no encoding, an unknown or stacked
// one, or one that does not decode comes back as the wire bytes with an empty
// encoding, so requestWithBody sends it on untouched.
func readRequestBody(req *http.Request) ([]byte, string, error) {
	raw, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, "", err //nolint:wrapcheck // callers add their own context
	}
	encoding := strings.ToLower(strings.TrimSpace(req.Header.Get("Content-Encoding")))
	codec, ok := requestBodyCodecs[encoding]
	if !ok {
		return raw, "", nil
	}
	plain, err := codec.decode(raw)
	if err != nil {
		return raw, "", nil
	}
	return plain, encoding, nil
}

// requestWithBody rebuilds req around body, encoding it with encoding first
// when that is one readRequestBody decoded. ContentLength and GetBody always
// describe the bytes that go on the wire. An empty encoding leaves the
// headers as they were, which keeps an encoding we could not read intact.
func requestWithBody(req *http.Request, body []byte, encoding string) *http.Request {
	clone := req.Clone(req.Context())
	if codec, ok := requestBodyCodecs[encoding]; ok {
		if encoded, err := codec.encode(body); err == nil {
			body = encoded
			clone.Header.Set("Content-Encoding", encoding)
		} else {
			// Plain JSON is always a body the server can read.
			clone.Header.Del("Content-Encoding")
		}
	}
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	clone.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	clone.TransferEncoding = nil
	return clone
}

func gunzipBody(raw []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err //nolint:wrapcheck // readRequestBody only checks for failure
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

func gzipBody(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err //nolint:wrapcheck // requestWithBody only checks for failure
	}
	if _, err := zw.Write(body); err != nil {
		return nil, err //nolint:wrapcheck // requestWithBody only checks for failure
	}
	if err := zw.Close(); err != nil {
		return nil, err //nolint:wrapcheck // requestWithBody only checks for failure
	}
	return buf.Bytes(), nil
}

func unbrotliBody(raw []byte) ([]byte, error) {
	return io.ReadAll(brrr.NewReader(bytes.NewReader(raw)))
}

// brotliBody uses the quality fantasy compresses Hyper requests with.
func brotliBody(body []byte) ([]byte, error) {
	return brrr.Compress(body, 1) //nolint:wrapcheck // requestWithBody only checks for failure
}
