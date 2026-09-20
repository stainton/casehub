package core

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// Business defaults are shared through CaseHub's configured repository.
// The agent's own setting.json is edited separately, not copied into State.
type AgentSettings struct {
	BaseURL        string `json:"baseUrl"`
	Instructions   string `json:"instructions"`
	TestAccount    string `json:"testAccount"`
	TestSecret     string `json:"testSecret"`
	TimeoutMinutes int    `json:"timeoutMinutes"`
}

func (a AgentSettings) WithDefaults() AgentSettings {
	if a.TimeoutMinutes == 0 {
		a.TimeoutMinutes = 15
	}
	return a
}

func (s *Service) SaveAgentSettings(ctx context.Context, id string, a AgentSettings) (AgentSettings, error) {
	if id != "playwright" {
		return AgentSettings{}, errors.New("未知 agent")
	}
	a.BaseURL = strings.TrimSpace(a.BaseURL)
	if a.BaseURL != "" {
		u, err := url.Parse(a.BaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return AgentSettings{}, errors.New("被测系统 URL 必须是有效的 HTTP 或 HTTPS 地址")
		}
	}
	if a.TimeoutMinutes < 1 || a.TimeoutMinutes > 240 {
		return AgentSettings{}, errors.New("任务超时时间需为 1–240 分钟的整数")
	}
	if len(a.Instructions) > 200000 || len(a.TestAccount) > 2000 || len(a.BaseURL) > 4096 || len(a.TestSecret) > 8192 {
		return AgentSettings{}, errors.New("配置字段过长")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.repo.Load(ctx)
	if errors.Is(err, ErrNotFound) {
		st = Seed()
	} else if err != nil {
		return AgentSettings{}, err
	}
	if st.AgentSettings == nil {
		st.AgentSettings = map[string]AgentSettings{}
	}
	st.AgentSettings[id] = a
	if err = s.repo.Save(ctx, st); err != nil {
		return AgentSettings{}, err
	}
	return a, nil
}
