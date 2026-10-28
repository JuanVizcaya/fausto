package check

import (
	"fmt"
	"slices"
	"strings"

	"github.com/JuanVizcaya/fausto/internal/spec"
)

// runtimeRules inspect a component's compose service and return a problem,
// or "" when the rule holds. Requirements opt components into them with
// verified_by: runtime/<rule>.
var runtimeRules = map[string]func(s composeService, c *spec.Component) string{
	"healthcheck": func(s composeService, _ *spec.Component) string {
		switch {
		case s.Healthcheck == nil || len(s.Healthcheck.Test) == 0:
			return "has no health check"
		case s.Healthcheck.Disable || s.Healthcheck.Test[0] == "NONE":
			return "has its health check disabled"
		}
		return ""
	},
	"memory-limit": func(s composeService, _ *spec.Component) string {
		if s.Deploy.Resources.Limits.Memory == "" {
			return "has no memory limit (deploy.resources.limits.memory)"
		}
		return ""
	},
	"hardening": func(s composeService, _ *spec.Component) string {
		var missing []string
		if !s.ReadOnly {
			missing = append(missing, "read_only: true")
		}
		if !slices.ContainsFunc(s.CapDrop, func(c string) bool { return strings.EqualFold(c, "ALL") }) {
			missing = append(missing, "cap_drop: [ALL]")
		}
		if !slices.ContainsFunc(s.SecurityOpt, func(o string) bool {
			return o == "no-new-privileges:true" || o == "no-new-privileges"
		}) {
			missing = append(missing, "security_opt: no-new-privileges:true")
		}
		if len(missing) > 0 {
			return "is missing " + strings.Join(missing, ", ")
		}
		return ""
	},
	"exposure": func(s composeService, c *spec.Component) string {
		if c.Layer != "edge" && slices.ContainsFunc(s.Ports, publicPort) {
			return fmt.Sprintf("publishes ports beyond loopback but is in the %s layer; only edge components may", c.Layer)
		}
		return ""
	},
}

// publicPort reports whether a compose ports entry, short ("8080",
// "127.0.0.1:5432:5432") or long ({target, host_ip}), publishes on an
// address other hosts can reach.
func publicPort(p any) bool {
	var ip string
	switch p := p.(type) {
	case map[string]any:
		ip, _ = p["host_ip"].(string)
	case string:
		p, _, _ = strings.Cut(p, "/")
		if strings.HasPrefix(p, "[") {
			ip, _, _ = strings.Cut(strings.TrimPrefix(p, "["), "]")
		} else if parts := strings.Split(p, ":"); len(parts) == 3 {
			ip = parts[0]
		}
	}
	return ip != "127.0.0.1" && ip != "::1" && ip != "localhost"
}

// RuntimeRules lists the rule names usable in verified_by: runtime/<rule>.
func RuntimeRules() []string {
	return sortedKeys(runtimeRules)
}

// checkRuntime enforces the runtime rules that requirements ask for on
// the active components they apply to.
func checkRuntime(r *Repo) []Finding {
	var out []Finding
	var compose map[string]composeService
	seen := map[string]bool{}
	for _, req := range r.Spec.Requirements.Requirements {
		rule, ok := strings.CutPrefix(req.VerifiedBy, "runtime/")
		check := runtimeRules[rule]
		if !ok || check == nil {
			continue // unknown rules are reported by the schema check
		}
		if compose == nil {
			var err error
			if compose, err = r.loadCompose(); err != nil {
				return []Finding{{Message: err.Error(), Path: r.Spec.Layout.ComposeFile}}
			}
		}
		for i := range r.Spec.System.Components {
			c := &r.Spec.System.Components[i]
			if c.Status != "active" || c.Runtime.Compose == "" || !req.Applies(c) {
				continue
			}
			svc, ok := compose[c.Runtime.Compose]
			if !ok {
				continue // reported by the services check
			}
			key := rule + "/" + c.ID
			if seen[key] {
				continue
			}
			seen[key] = true
			if msg := check(svc, c); msg != "" {
				out = append(out, Finding{
					Message:   fmt.Sprintf("compose service %q %s (requirement %s)", c.Runtime.Compose, msg, req.ID),
					Component: c.ID, Path: svc.File, Rule: rule,
				})
			}
		}
	}
	return out
}
