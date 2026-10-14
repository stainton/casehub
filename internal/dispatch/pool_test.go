package dispatch

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestPoolForms(t *testing.T) {
	if p, err := ParsePool(""); p != nil || err != nil {
		t.Fatal("an empty address disables the service")
	}
	p, err := ParsePool("http://a:4501, http://b:4501/")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Nodes(context.Background()); !reflect.DeepEqual(got, []string{"http://a:4501", "http://b:4501"}) {
		t.Fatalf("nodes = %v", got)
	}
	if got := p.Nodes(context.Background()); got[0] != "http://b:4501" {
		t.Fatal("successive calls start from different nodes")
	}
	for _, bad := range []string{"a:4501", "ftp://a", "dns+planner:4501", "dns+http://"} {
		if _, err := ParsePool(bad); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
	d, err := ParsePool("dns+http://planner-headless:4501")
	if err != nil {
		t.Fatal(err)
	}
	lookups := 0
	d.lookup = func(_ context.Context, host string) ([]string, error) {
		lookups++
		if host != "planner-headless" {
			t.Fatalf("looked up %q", host)
		}
		return []string{"10.0.0.7", "fd00::1"}, nil
	}
	if got := d.Nodes(context.Background()); !reflect.DeepEqual(got, []string{"http://10.0.0.7:4501", "http://[fd00::1]:4501"}) {
		t.Fatalf("nodes = %v", got)
	}
	d.Nodes(context.Background())
	if lookups != 1 {
		t.Fatal("a lookup is cached briefly")
	}
	d.cachedAt = d.cachedAt.Add(-4 * time.Second)
	d.lookup = func(context.Context, string) ([]string, error) { return nil, errors.New("no such host") }
	if got := d.Nodes(context.Background()); len(got) != 0 {
		t.Fatal("a failed lookup means no nodes for now (scaled to zero, or not up yet)")
	}
}
