// Package dispatch hands CaseHub's tasks to auto-test nodes. A node (planner, generator/healer, executor) is a
// stateless executor; CaseHub owns each task's record (internal/tasks), keeps the request in memory until it is
// done, picks an idle node, pushes what the task needs to that node, follows its progress there, collects the
// result, the planner session to continue and a failed run's diagnostics, and then releases it on the node.
package dispatch

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Pool is the set of nodes serving one kind of task. Two forms, so the same CaseHub runs beside plain
// processes and in Kubernetes:
//
//	http://a:4501,http://b:4501        a fixed list (one URL is a pool of one, as before)
//	dns+http://planner-headless:4501   every address the name resolves to (a headless Service: its ready pods)
type Pool struct {
	static []string
	dns    *url.URL
	lookup func(ctx context.Context, host string) ([]string, error)

	mu       sync.Mutex
	cached   []string
	cachedAt time.Time
	next     int
}

func ParsePool(spec string) (*Pool, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	if rest, ok := strings.CutPrefix(spec, "dns+"); ok {
		u, err := url.Parse(rest)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return nil, fmt.Errorf("invalid node address %q: expected dns+http://host:port", spec)
		}
		return &Pool{dns: u, lookup: net.DefaultResolver.LookupHost}, nil
	}
	p := &Pool{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimRight(strings.TrimSpace(part), "/")
		if part == "" {
			continue
		}
		u, err := url.Parse(part)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("invalid node address %q", part)
		}
		p.static = append(p.static, part)
	}
	if len(p.static) == 0 {
		return nil, nil
	}
	return p, nil
}

// Nodes lists the pool's current members, rotated so successive callers start from different nodes.
// A DNS lookup is cached briefly; a failed one returns no nodes (tasks wait in the queue meanwhile).
func (p *Pool) Nodes(ctx context.Context) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	nodes := p.static
	if p.dns != nil {
		if time.Since(p.cachedAt) > 3*time.Second {
			lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			addrs, err := p.lookup(lookupCtx, p.dns.Hostname())
			cancel()
			p.cachedAt = time.Now()
			p.cached = nil
			if err == nil {
				for _, addr := range addrs {
					host := addr
					if port := p.dns.Port(); port != "" {
						host = net.JoinHostPort(addr, port)
					} else if strings.Contains(addr, ":") {
						host = "[" + addr + "]"
					}
					p.cached = append(p.cached, p.dns.Scheme+"://"+host)
				}
			}
		}
		nodes = p.cached
	}
	if len(nodes) == 0 {
		return nil
	}
	start := p.next % len(nodes)
	p.next++
	return append(append([]string(nil), nodes[start:]...), nodes[:start]...)
}

// Describe is the configured form, for logs.
func (p *Pool) Describe() string {
	if p == nil {
		return "disabled"
	}
	if p.dns != nil {
		return "dns+" + p.dns.String()
	}
	return strings.Join(p.static, ",")
}
