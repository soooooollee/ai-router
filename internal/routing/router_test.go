package routing

import (
	"testing"

	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/protocol/ir"
)

func TestResolvePrioritySpecificityAndFallback(t *testing.T) {
	tools := true
	c := &config.Config{Providers: []config.Provider{{ID: "a"}, {ID: "b"}}, Routes: []config.Route{{ID: "generic", Priority: 10, Match: config.RouteMatch{Model: "code-*"}, Targets: []config.RouteTarget{{Provider: "a", Model: "m1"}}}, {ID: "tools", Priority: 20, Match: config.RouteMatch{Model: "code-*", Tools: &tools}, Targets: []config.RouteTarget{{Provider: "b", Model: "m2"}}}}, DefaultRoute: &config.RouteTargetList{Targets: []config.RouteTarget{{Provider: "a", Model: "default"}}}}
	d, e := Resolve(c, Input{Request: &ir.Request{Model: "code-fast", Tools: []ir.Tool{{Name: "x"}}}})
	if e != nil {
		t.Fatal(e)
	}
	if d.RouteID != "tools" || d.Targets[0].Provider.ID != "b" {
		t.Fatalf("unexpected decision %#v", d)
	}
	d, e = Resolve(c, Input{Request: &ir.Request{Model: "other"}})
	if e != nil || d.RouteID != "default" {
		t.Fatalf("fallback failed %#v %v", d, e)
	}
}

func TestExactModelWinsOverGlobAtEqualPriority(t *testing.T) {
	c := &config.Config{Providers: []config.Provider{{ID: "exact"}, {ID: "glob"}}, Routes: []config.Route{
		{ID: "glob", Priority: 10, Match: config.RouteMatch{Model: "code-*"}, Targets: []config.RouteTarget{{Provider: "glob", Model: "m"}}},
		{ID: "exact", Priority: 10, Match: config.RouteMatch{Model: "code-fast"}, Targets: []config.RouteTarget{{Provider: "exact", Model: "m"}}},
	}}
	decision, err := Resolve(c, Input{Request: &ir.Request{Model: "code-fast"}})
	if err != nil || decision.RouteID != "exact" {
		t.Fatalf("exact route did not win: %#v %v", decision, err)
	}
}
