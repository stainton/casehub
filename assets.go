package main

import (
	"bytes"
	"casehub/internal/core"
	"casehub/internal/upstream"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Requests above this are rejected before any part of them is read. Per-type ceilings (core.go) are
// tighter; this is only the outer bound multipart's own field overhead sits inside.
const maxAssetUploadBytes = 210 << 20

func assetMetaOut(m core.AssetMeta) map[string]any {
	return map[string]any{"id": m.ID, "type": m.Type, "name": m.Name, "mimeType": m.MimeType,
		"size": m.Size, "createdAt": m.CreatedAt}
}

// GET lists uploaded assets (optionally ?type=image|video|audio), newest first, metadata only.
// POST uploads one: multipart/form-data with a "type" field and a "file" part; "name" defaults to the
// uploaded filename.
func (a *api) assets(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		list, err := a.service.ListAssets(r.Context(), r.URL.Query().Get("type"))
		if err != nil {
			jsonOut(w, 400, map[string]string{"error": err.Error()})
			return
		}
		out := make([]map[string]any, len(list))
		for i, m := range list {
			out[i] = assetMetaOut(m)
		}
		jsonOut(w, 200, out)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAssetUploadBytes)
	// 32MB kept in memory; a larger upload spills to a temp file ParseMultipartForm cleans up itself.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		jsonOut(w, 400, map[string]string{"error": "上传请求过大或格式无效"})
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	file, header, err := r.FormFile("file")
	if err != nil {
		jsonOut(w, 400, map[string]string{"error": "缺少上传文件"})
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		jsonOut(w, 400, map[string]string{"error": "读取上传文件失败"})
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = header.Filename
	}
	meta, err := a.service.SaveAsset(r.Context(), r.FormValue("type"), name, header.Header.Get("Content-Type"), data)
	if err != nil {
		jsonOut(w, 400, map[string]string{"error": err.Error()})
		return
	}
	jsonOut(w, 200, assetMetaOut(meta))
}

// GET serves the raw bytes for the assets dialog's preview (agents never call this: CaseHub pushes
// assets to them). DELETE removes it permanently.
func (a *api) assetByID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r.Method == http.MethodDelete {
		w.Header().Set("Cache-Control", "no-store")
		err := a.service.DeleteAsset(r.Context(), id)
		if errors.Is(err, core.ErrNotFound) {
			jsonOut(w, 404, map[string]string{"error": "资产不存在"})
			return
		}
		if err != nil {
			jsonOut(w, 500, map[string]string{"error": "删除资产失败"})
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
		return
	}
	asset, err := a.service.Asset(r.Context(), id)
	if errors.Is(err, core.ErrNotFound) {
		jsonOut(w, 404, map[string]string{"error": "资产不存在"})
		return
	}
	if err != nil {
		jsonOut(w, 500, map[string]string{"error": "读取资产失败"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", asset.MimeType)
	w.Header().Set("Content-Disposition", `inline; filename="`+sanitizeFilename(asset.Name)+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(asset.Size, 10))
	_, _ = w.Write(asset.Data)
}

// A filename never carries a quote or a newline into a header value.
func sanitizeFilename(name string) string {
	return strings.NewReplacer(`"`, "'", "\r", " ", "\n", " ").Replace(name)
}

// assetSource gives the proxy CaseHub's side of handing an asset to an agent. CaseHub pushes the bytes
// to the agent itself (upstream/proxy.go), so the agent is never given an address to call back, a
// credential or any access to CaseHub's database. Assets that no longer exist are skipped rather than
// failing the whole task.
func assetSource(service *core.Service) *upstream.Assets {
	return &upstream.Assets{
		Lookup: func(ctx context.Context, ids []string) ([]upstream.AssetRef, error) {
			refs := make([]upstream.AssetRef, 0, len(ids))
			for _, id := range ids {
				meta, err := service.AssetMeta(ctx, id)
				if errors.Is(err, core.ErrNotFound) {
					continue
				}
				if err != nil {
					return nil, err
				}
				refs = append(refs, upstream.AssetRef{ID: meta.ID, Name: meta.Name, Type: meta.Type,
					MimeType: meta.MimeType, SHA256: meta.SHA256, Size: meta.Size})
			}
			return refs, nil
		},
		Open: func(ctx context.Context, id string) (io.ReadCloser, error) {
			asset, err := service.Asset(ctx, id)
			if err != nil {
				return nil, err
			}
			return io.NopCloser(bytes.NewReader(asset.Data)), nil
		},
	}
}
