package routing

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/protocol/common"
	"github.com/zbss/airoute/internal/protocol/ir"
)

type Input struct {
	Request  *ir.Request
	Protocol ir.Protocol
	Headers  map[string]string
}
type Decision struct {
	RouteID     string           `json:"route_id"`
	Targets     []ResolvedTarget `json:"targets"`
	Explanation []string         `json:"explanation"`
}
type ResolvedTarget struct {
	Provider config.Provider `json:"provider"`
	Model    string          `json:"model"`
}

func Resolve(c *config.Config, in Input) (Decision, error) {
	rules := append([]config.Route(nil), c.Routes...)
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Priority != rules[j].Priority {
			return rules[i].Priority > rules[j].Priority
		}
		return specificity(rules[i]) > specificity(rules[j])
	})
	for _, r := range rules {
		ok, reasons := matches(r.Match, in)
		if ok {
			return build(c, r.ID, r.Targets, append([]string{"matched route " + r.ID}, reasons...))
		}
	}
	if c.DefaultRoute != nil {
		return build(c, "default", c.DefaultRoute.Targets, []string{"no explicit route matched", "selected default route"})
	}
	return Decision{}, fmt.Errorf("no route matched model %q", in.Request.Model)
}

func build(c *config.Config, id string, targets []config.RouteTarget, explanation []string) (Decision, error) {
	d := Decision{RouteID: id, Explanation: explanation}
	for _, t := range targets {
		var p *config.Provider
		for i := range c.Providers {
			if c.Providers[i].ID == t.Provider {
				p = &c.Providers[i]
				break
			}
		}
		if p == nil {
			return Decision{}, fmt.Errorf("route %q references unknown provider %q", id, t.Provider)
		}
		d.Targets = append(d.Targets, ResolvedTarget{Provider: *p, Model: t.Model})
	}
	return d, nil
}
func matches(m config.RouteMatch, in Input) (bool, []string) {
	var why []string
	if m.Model != "" {
		ok, _ := path.Match(m.Model, in.Request.Model)
		if !ok {
			return false, nil
		}
		why = append(why, "model matched "+m.Model)
	}
	if m.Protocol != "" && m.Protocol != in.Protocol {
		return false, nil
	} else if m.Protocol != "" {
		why = append(why, "protocol matched "+string(m.Protocol))
	}
	if m.Stream != nil && *m.Stream != in.Request.Stream {
		return false, nil
	}
	if m.Tools != nil && *m.Tools != (len(in.Request.Tools) > 0) {
		return false, nil
	}
	hasImage := common.HasType(in.Request, "image_url") || common.HasType(in.Request, "image_base64")
	if m.Image != nil && *m.Image != hasImage {
		return false, nil
	}
	for k, v := range m.Headers {
		if !strings.EqualFold(in.Headers[strings.ToLower(k)], v) {
			return false, nil
		}
	}
	return true, why
}
func specificity(r config.Route) int {
	n := 0
	if r.Match.Model != "" {
		if strings.ContainsAny(r.Match.Model, "*?[") {
			n += 10
		} else {
			n += 100
		}
	}
	if r.Match.Protocol != "" {
		n++
	}
	if r.Match.Stream != nil {
		n++
	}
	if r.Match.Tools != nil {
		n++
	}
	if r.Match.Image != nil {
		n++
	}
	n += len(r.Match.Headers)
	return n
}
