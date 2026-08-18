// Package client 是 case API 的 HTTP 客户端：manager 通过它创建/查询测试用例，
// 不直接访问 case API 拥有的数据库表（那些表只属于 case API 自己）。
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/stainton/casehub/pkg/api"
	"github.com/stainton/casehub/pkg/model"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{}}
}

// UpsertCase 创建或编辑一个测试用例：case_id 已存在时是编辑（新增一个 revision），
// 不存在时是创建，语义与 case API 的 POST /api/cases 一致。
func (c *Client) UpsertCase(ctx context.Context, tc *model.TestCase) (*model.TestCase, error) {
	method, path := splitRoute(api.RouteCreateCase)
	var out model.TestCase
	if err := c.doJSON(ctx, method, path, tc, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// QueryCases 按条件查询最新版本的测试用例，语义与 case API 的 GET /api/cases 一致，
// 参见 api.QueryCaseFields 支持的过滤字段。
func (c *Client) QueryCases(ctx context.Context, filters map[string]string) ([]*model.TestCase, error) {
	_, path := splitRoute(api.RouteListCases)

	q := url.Values{}
	for k, v := range filters {
		if v != "" {
			q.Set(k, v)
		}
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	var out []*model.TestCase
	if err := c.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetCase 查询单个用例的最新版本，语义与 case API 的 GET /api/cases/{id} 一致。
func (c *Client) GetCase(ctx context.Context, uid int64) (*model.TestCase, error) {
	var out model.TestCase
	if err := c.get(ctx, pathWithID(api.RouteGetCase, uid), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetCaseHistory 查询单个用例的全部版本，语义与 case API 的
// GET /api/cases/{id}/history 一致。
func (c *Client) GetCaseHistory(ctx context.Context, uid int64) ([]*model.TestCase, error) {
	var out []*model.TestCase
	if err := c.get(ctx, pathWithID(api.RouteGetCaseHistory, uid), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateExecution 新增一条测试执行记录，语义与 case API 的
// POST /api/cases/{id}/executions 一致。
func (c *Client) CreateExecution(ctx context.Context, uid int64, body api.CreateExecutionRequest) (*model.TestExecution, error) {
	method, _ := splitRoute(api.RouteCreateExecution)
	var out model.TestExecution
	if err := c.doJSON(ctx, method, pathWithID(api.RouteCreateExecution, uid), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListExecutions 查询某个用例(uid+revision)的全部执行记录，语义与 case API 的
// GET /api/cases/{id}/executions 一致。
func (c *Client) ListExecutions(ctx context.Context, uid, revision int64) ([]*model.TestExecution, error) {
	q := url.Values{}
	q.Set(api.RevisionQueryParam, strconv.FormatInt(revision, 10))

	var out []*model.TestExecution
	if err := c.get(ctx, pathWithID(api.RouteListExecutions, uid)+"?"+q.Encode(), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// get 向 path 发送一个 GET 请求，并将响应解码进 out。
func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	return c.send(req, out)
}

// doJSON 向 method+path 发送 body 的 JSON 编码，并将响应解码进 out。
func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.send(req, out)
}

func (c *Client) send(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("case api %s %s: %s: %s", req.Method, req.URL.Path, resp.Status, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// splitRoute 将 "METHOD /path" 格式的路由常量（见 pkg/api）拆分成 method 和 path。
func splitRoute(route string) (method, path string) {
	method, path, _ = strings.Cut(route, " ")
	return method, path
}

// pathWithID 取出路由常量的 path 部分，并将其中的 {id} 替换成具体的 uid。
func pathWithID(route string, uid int64) string {
	_, path := splitRoute(route)
	return strings.Replace(path, "{id}", strconv.FormatInt(uid, 10), 1)
}
