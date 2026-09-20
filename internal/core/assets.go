package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Assets are files a person uploads once and later attaches to a design task (aigc用例设计 needs a
// real image/video/audio sometimes — a QR code to scan, a file to upload in the flow it is exploring).
// CaseHub stores them; the agent never gets direct DB or filesystem access, only the download URL
// upstream/proxy.go resolves the task's chosen asset ids into, and fetches/caches them itself.
const (
	AssetImage = "image"
	AssetVideo = "video"
	AssetAudio = "audio"
)

func ValidAssetType(t string) bool { return t == AssetImage || t == AssetVideo || t == AssetAudio }

// Per-type ceilings: images are typically small fixtures, audio/video fixtures can be legitimately
// larger. Chosen generously for a self-hosted internal tool, not for public upload abuse.
const (
	maxImageBytes = 15 << 20
	maxAudioBytes = 50 << 20
	maxVideoBytes = 200 << 20
	maxAssetName  = 200
)

func maxAssetBytes(assetType string) int64 {
	switch assetType {
	case AssetImage:
		return maxImageBytes
	case AssetAudio:
		return maxAudioBytes
	case AssetVideo:
		return maxVideoBytes
	}
	return 0
}
func humanMB(n int64) string { return fmt.Sprintf("%d MB", n/(1<<20)) }

// AssetMeta is everything about an asset except its bytes: what a list, the assets dialog and the AI
// 设计 asset picker need. Asset adds the bytes, read only for a download or a delete's own bookkeeping.
type AssetMeta struct {
	ID, Type, Name, MimeType string
	Size                     int64
	SHA256                   string
	CreatedAt                time.Time
}

// Newest first, ID as a stable tiebreaker for assets saved in the same millisecond.
func sortAssetsNewestFirst(list []AssetMeta) {
	sort.Slice(list, func(i, j int) bool {
		if !list[i].CreatedAt.Equal(list[j].CreatedAt) {
			return list[i].CreatedAt.After(list[j].CreatedAt)
		}
		return list[i].ID < list[j].ID
	})
}

type Asset struct {
	AssetMeta
	Data []byte
}

func (s *Service) ListAssets(ctx context.Context, assetType string) ([]AssetMeta, error) {
	if assetType != "" && !ValidAssetType(assetType) {
		return nil, errors.New("未知资产类型")
	}
	list, err := s.repo.ListAssets(ctx, assetType)
	if err != nil {
		return nil, err
	}
	sortAssetsNewestFirst(list)
	return list, nil
}

// AssetMeta reads only the metadata upstream/proxy.go needs to build a download URL: never the bytes,
// so resolving the assets a task carries costs nothing proportional to how large those assets are.
func (s *Service) AssetMeta(ctx context.Context, id string) (AssetMeta, error) {
	return s.repo.GetAssetMeta(ctx, id)
}

// Asset reads the full asset including its bytes, for the download endpoint the agent (and the
// browser preview) fetches from.
func (s *Service) Asset(ctx context.Context, id string) (Asset, error) {
	return s.repo.GetAsset(ctx, id)
}

func (s *Service) SaveAsset(ctx context.Context, assetType, name, mimeType string, data []byte) (AssetMeta, error) {
	if !ValidAssetType(assetType) {
		return AssetMeta{}, errors.New("资产类型必须是 image、video 或 audio")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "未命名资产"
	}
	if len([]rune(name)) > maxAssetName {
		return AssetMeta{}, errors.New("资产名称过长")
	}
	if len(data) == 0 {
		return AssetMeta{}, errors.New("上传内容为空")
	}
	if limit := maxAssetBytes(assetType); int64(len(data)) > limit {
		return AssetMeta{}, fmt.Errorf("该类型资产不能超过 %s", humanMB(limit))
	}
	mimeType = strings.TrimSpace(mimeType)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	sum := sha256.Sum256(data)
	meta := AssetMeta{ID: ID(), Type: assetType, Name: name, MimeType: mimeType,
		Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), CreatedAt: now()}
	if err := s.repo.SaveAsset(ctx, Asset{AssetMeta: meta, Data: data}); err != nil {
		return AssetMeta{}, err
	}
	return meta, nil
}
func (s *Service) DeleteAsset(ctx context.Context, id string) error {
	return s.repo.DeleteAsset(ctx, id)
}
