package tools

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFetchToolDecodesLegacyEncoding(t *testing.T) {
	t.Parallel()

	// "café" in ISO-8859-1: 0xe9 is not valid UTF-8.
	body := []byte("<html><body><p>caf\xe9</p></body></html>")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	resp, err := runFetchTool(t, server.Client(), server.URL)
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "café")
}

func TestFetchURLAndConvertDecodesLegacyEncoding(t *testing.T) {
	t.Parallel()

	body := []byte("<html><body><p>caf\xe9</p></body></html>")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	content, err := FetchURLAndConvert(t.Context(), server.Client(), server.URL)
	require.NoError(t, err)
	require.Contains(t, content, "café")
}

func TestFetchToolDecodesMetaTagEncoding(t *testing.T) {
	t.Parallel()

	body := []byte(`<html><head><meta charset="windows-1252"></head><body><p>caf` + "\xe9" + `</p></body></html>`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	resp, err := runFetchTool(t, server.Client(), server.URL)
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "café")
}

func TestFetchToolKeepsValidUTF8WithMultipleEncodingLabels(t *testing.T) {
	t.Parallel()

	body := []byte(`<html><head><meta charset="utf-8, iso-8859-1"></head><body><p>café</p></body></html>`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8, iso-8859-1")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	resp, err := runFetchTool(t, server.Client(), server.URL)
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "café")
}
