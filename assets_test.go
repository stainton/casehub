package main

import (
	"bytes"
	"casehub/internal/core"
	"casehub/internal/store"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"testing"
)

// multipartUpload builds the body/content-type for a POST /api/assets request the way a browser's
// <form>/FormData would: an optional "type"/"name" field plus one "file" part.
func multipartUpload(t *testing.T, assetType, name, filename string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	if assetType != "" {
		_ = w.WriteField("type", assetType)
	}
	if name != "" {
		_ = w.WriteField("name", name)
	}
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return body, w.FormDataContentType()
}

func TestAssetsUploadListDownloadDelete(t *testing.T) {
	handler := routes(&api{service: core.NewService(store.NewMemory())})
	upload := func(assetType, name, filename string, data []byte) *httptest.ResponseRecorder {
		t.Helper()
		body, contentType := multipartUpload(t, assetType, name, filename, data)
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/assets", body)
		r.Header.Set("Content-Type", contentType)
		handler.ServeHTTP(w, r)
		return w
	}
	get := func(method, path string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w
	}

	out := upload("image", "", "logo.png", []byte{1, 2, 3, 4})
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	var meta struct{ ID, Type, Name, MimeType string }
	if err := json.Unmarshal(out.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	// No name given: falls back to the uploaded filename.
	if meta.ID == "" || meta.Type != "image" || meta.Name != "logo.png" {
		t.Fatalf("unexpected metadata: %+v", meta)
	}

	if out = upload("bogus", "x", "x.bin", []byte{1}); out.Code != 400 {
		t.Fatal("invalid asset type accepted")
	}
	if out = upload("image", "x", "x.png", nil); out.Code != 400 {
		t.Fatal("empty upload accepted")
	}

	if out = upload("video", "片段", "clip.mp4", []byte{5, 6}); out.Code != 200 {
		t.Fatal(out.Body.String())
	}

	// Listing: all types, then filtered.
	if out = get("GET", "/api/assets"); out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	var all []map[string]any
	if err := json.Unmarshal(out.Body.Bytes(), &all); err != nil || len(all) != 2 {
		t.Fatalf("list all = %v (%v)", all, err)
	}
	if out = get("GET", "/api/assets?type=video"); out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	var videos []map[string]any
	if err := json.Unmarshal(out.Body.Bytes(), &videos); err != nil || len(videos) != 1 || videos[0]["name"] != "片段" {
		t.Fatalf("list by type = %v (%v)", videos, err)
	}
	if out = get("GET", "/api/assets?type=bogus"); out.Code != 400 {
		t.Fatal("unknown type filter accepted")
	}

	// Download: raw bytes, correct content type, unknown id is 404.
	if out = get("GET", "/api/assets/"+meta.ID); out.Code != 200 || out.Body.String() != "\x01\x02\x03\x04" {
		t.Fatalf("download mismatch: status=%d body=%q", out.Code, out.Body.String())
	}
	if ct := out.Header().Get("Content-Type"); ct != "image/png" && ct == "" {
		t.Fatalf("missing content type: %q", ct)
	}
	if out = get("GET", "/api/assets/does-not-exist"); out.Code != 404 {
		t.Fatal("missing asset id accepted")
	}

	// Delete: removed for good, a second delete is 404.
	if out = get("DELETE", "/api/assets/"+meta.ID); out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	if out = get("GET", "/api/assets/"+meta.ID); out.Code != 404 {
		t.Fatal("deleted asset still downloadable")
	}
	if out = get("DELETE", "/api/assets/"+meta.ID); out.Code != 404 {
		t.Fatal("deleting twice not reported as missing")
	}
	if out = get("GET", "/api/assets"); out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	var remaining []map[string]any
	if err := json.Unmarshal(out.Body.Bytes(), &remaining); err != nil || len(remaining) != 1 {
		t.Fatalf("delete left the wrong count: %v (%v)", remaining, err)
	}
}

// The proxy's asset source resolves ids into refs carrying the hash and size the agent verifies the
// pushed bytes against (never an address), skipping ids that no longer exist, and streams the bytes.
func TestAssetSourceResolvesRefsAndOpensBytes(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	meta, err := svc.SaveAsset(context.Background(), "image", "logo.png", "image/png", []byte{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	source := assetSource(svc)
	refs, err := source.Lookup(context.Background(), []string{meta.ID, "missing"})
	if err != nil || len(refs) != 1 {
		t.Fatalf("lookup = %+v (%v)", refs, err)
	}
	if refs[0].ID != meta.ID || refs[0].Name != "logo.png" || refs[0].Type != "image" || refs[0].SHA256 != meta.SHA256 || refs[0].Size != 2 {
		t.Fatalf("resolved ref wrong: %+v", refs[0])
	}
	body, err := source.Open(context.Background(), meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(body)
	if !bytes.Equal(data, []byte{1, 2}) {
		t.Fatalf("opened bytes = %v", data)
	}
}
