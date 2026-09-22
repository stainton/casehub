package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
)

// An agent's configuration is CaseHub state, stored in the repository (a table of its own in
// PostgreSQL) rather than in a file beside the agent. CaseHub sends it with every request it proxies
// to that agent, and the agent overwrites its own setting.json whenever Revision changes. The agent
// keeps no copy that must be kept in sync: a rebuilt container, a rolled-back image or a hand-edited
// file all converge on what CaseHub holds the next time a task starts.
type AgentSettings struct {
	BaseURL        string `json:"baseUrl"`
	Instructions   string `json:"instructions"`
	TestAccount    string `json:"testAccount"`
	TestSecret     string `json:"testSecret"`
	TimeoutMinutes int    `json:"timeoutMinutes"`
}

type AgentConfig struct {
	// Defaults are the business defaults a new task is prefilled with.
	Defaults AgentSettings `json:"defaults"`
	// Settings is the agent's setting.json text, kept verbatim so every field stays editable,
	// including Claude options CaseHub does not know about. Empty means CaseHub has never been given
	// one: nothing is sent and the agent keeps the configuration built into its image.
	Settings string `json:"settings"`
	// Revision advances only when Settings changes. It is what an agent compares against to decide
	// whether to rewrite its file, and what the editor sends back to detect a concurrent save.
	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Agents CaseHub stores configuration for: the auto-test planner behind 需求管理's "AI 设计", the
// generator behind 用例管理's "脚本生成", and the standalone general-agent. Each is configured independently.
var agentNames = map[string]string{"playwright": "AI 设计", "generator": "脚本生成", "general-agent": "通用 AI"}

func KnownAgent(id string) bool { _, ok := agentNames[id]; return ok }

var ErrRevisionConflict = errors.New("配置已被其他设备或窗口修改，请重新读取后再保存")

const agentSettingsMaxChars = 256 * 1024

// ValidateAgentSettingsJSON accepts any valid Claude settings object, checking only the two fields
// whose shape the agents depend on. Unknown fields pass through untouched.
func ValidateAgentSettingsJSON(content string) error {
	if len(content) > agentSettingsMaxChars {
		return errors.New("setting.json 过长")
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &settings); err != nil {
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
		if json.Unmarshal(raw, &model) != nil || len(strings.TrimSpace(model)) == 0 {
			return errors.New("model 必须是非空字符串")
		}
	}
	return nil
}

func (a AgentSettings) WithDefaults() AgentSettings {
	if a.TimeoutMinutes == 0 {
		a.TimeoutMinutes = 15
	}
	return a
}

func validateAgentDefaults(a AgentSettings) (AgentSettings, error) {
	a.BaseURL = strings.TrimSpace(a.BaseURL)
	if a.BaseURL != "" {
		u, err := url.Parse(a.BaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return a, errors.New("被测系统 URL 必须是有效的 HTTP 或 HTTPS 地址")
		}
	}
	if a.TimeoutMinutes < 1 || a.TimeoutMinutes > 240 {
		return a, errors.New("任务超时时间需为 1–240 分钟的整数")
	}
	if len(a.Instructions) > 200000 || len(a.TestAccount) > 2000 || len(a.BaseURL) > 4096 || len(a.TestSecret) > 8192 {
		return a, errors.New("配置字段过长")
	}
	return a, nil
}

// AgentConfig returns one agent's stored configuration. An agent configured before the
// configuration moved into its own table keeps its business defaults: they are read once from the
// state document and written into the table, and the state copy is left alone as a fallback.
func (s *Service) AgentConfig(ctx context.Context, id string) (AgentConfig, error) {
	if !KnownAgent(id) {
		return AgentConfig{}, errors.New("未知 agent")
	}
	cfg, err := s.repo.AgentConfig(ctx, id)
	if !errors.Is(err, ErrNotFound) {
		return cfg, err
	}
	legacy, err := s.legacyAgentDefaults(ctx, id)
	if err != nil || legacy == (AgentSettings{}) {
		return AgentConfig{Defaults: legacy}, err
	}
	migrated, err := s.repo.UpdateAgentConfig(ctx, id, func(current AgentConfig) (AgentConfig, error) {
		if current.Defaults != (AgentSettings{}) {
			return current, nil
		}
		current.Defaults = legacy
		return current, nil
	})
	if err != nil {
		return AgentConfig{Defaults: legacy}, nil // Readable without being writable; nothing is lost.
	}
	return migrated, nil
}

func (s *Service) legacyAgentDefaults(ctx context.Context, id string) (AgentSettings, error) {
	st, err := s.repo.Load(ctx)
	if errors.Is(err, ErrNotFound) {
		return AgentSettings{}, nil
	}
	if err != nil {
		return AgentSettings{}, err
	}
	return st.AgentSettings[id], nil
}

func (s *Service) SaveAgentDefaults(ctx context.Context, id string, a AgentSettings) (AgentConfig, error) {
	if !KnownAgent(id) {
		return AgentConfig{}, errors.New("未知 agent")
	}
	a, err := validateAgentDefaults(a)
	if err != nil {
		return AgentConfig{}, err
	}
	// Business defaults never move the revision: an agent must not rewrite its setting.json because
	// somebody changed a default URL.
	return s.repo.UpdateAgentConfig(ctx, id, func(current AgentConfig) (AgentConfig, error) {
		current.Defaults = a
		current.UpdatedAt = now()
		return current, nil
	})
}

// SaveAgentSettings replaces the agent's setting.json text. revision is the one the editor last read;
// a different stored revision means somebody else saved in between and this write is refused.
func (s *Service) SaveAgentSettings(ctx context.Context, id, content string, revision int64) (AgentConfig, error) {
	if !KnownAgent(id) {
		return AgentConfig{}, errors.New("未知 agent")
	}
	if err := ValidateAgentSettingsJSON(content); err != nil {
		return AgentConfig{}, err
	}
	return s.repo.UpdateAgentConfig(ctx, id, func(current AgentConfig) (AgentConfig, error) {
		if current.Revision != revision {
			return current, ErrRevisionConflict
		}
		if current.Settings == content {
			return current, nil
		}
		current.Settings = content
		current.Revision++
		current.UpdatedAt = now()
		return current, nil
	})
}
