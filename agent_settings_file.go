package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

type agentSettingsFile struct {
	path string
	mu   sync.Mutex
}

func newAgentSettingsFile(path string) *agentSettingsFile { return &agentSettingsFile{path: path} }
func settingsRevision(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func readSettingsFile(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []byte("{}"), false, nil
	}
	return data, true, err
}
func validateSettingsJSON(data []byte) error {
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		return errors.New("setting.json 必须是有效的 JSON 对象")
	}
	if settings == nil {
		return errors.New("setting.json 必须是 JSON 对象")
	}
	if raw, ok := settings["env"]; ok {
		var env map[string]json.RawMessage
		if json.Unmarshal(raw, &env) != nil || env == nil {
			return errors.New("env 必须是对象，且所有值必须是字符串")
		}
		for _, value := range env {
			var text string
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &text) != nil {
				return errors.New("env 中的值必须是字符串")
			}
		}
	}
	if raw, ok := settings["model"]; ok {
		var model string
		if json.Unmarshal(raw, &model) != nil || len(bytes.TrimSpace([]byte(model))) == 0 {
			return errors.New("model 必须是非空字符串")
		}
	}
	return nil
}
func (f *agentSettingsFile) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.PathValue("id") != "playwright" {
		jsonOut(w, 404, map[string]string{"error": "未知 agent"})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	current, exists, err := readSettingsFile(f.path)
	if err != nil {
		jsonOut(w, 500, map[string]string{"error": "无法读取 setting.json，请检查配置路径和文件权限"})
		return
	}
	if r.Method == http.MethodGet {
		// Return the original text so every field is editable, including unknown future settings.
		jsonOut(w, 200, map[string]any{"content": string(current), "revision": settingsRevision(current), "exists": exists, "path": f.path})
		return
	}
	var input struct {
		Content  string `json:"content"`
		Revision string `json:"revision"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*1024*1024))
	dec.DisallowUnknownFields()
	if dec.Decode(&input) != nil {
		jsonOut(w, 400, map[string]string{"error": "无效的配置请求"})
		return
	}
	if input.Revision != settingsRevision(current) {
		jsonOut(w, 409, map[string]string{"error": "文件已被其他设备或程序修改，请重新读取后再保存"})
		return
	}
	if err = validateSettingsJSON([]byte(input.Content)); err != nil {
		jsonOut(w, 400, map[string]string{"error": err.Error()})
		return
	}
	// Resolve symlinks before atomic replacement, so a shared config link remains intact.
	target := f.path
	if resolved, e := filepath.EvalSymlinks(target); e == nil {
		target = resolved
	}
	if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		jsonOut(w, 500, map[string]string{"error": "无法创建配置目录"})
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".setting-*.json")
	if err != nil {
		jsonOut(w, 500, map[string]string{"error": "无法写入配置目录"})
		return
	}
	defer os.Remove(tmp.Name())
	_, err = io.WriteString(tmp, input.Content)
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), target)
	}
	if err != nil {
		jsonOut(w, 500, map[string]string{"error": "保存 setting.json 失败，请检查目录写权限（容器需共享可写目录）"})
		return
	}
	jsonOut(w, 200, map[string]any{"content": input.Content, "revision": settingsRevision([]byte(input.Content)), "exists": true, "path": f.path})
}
