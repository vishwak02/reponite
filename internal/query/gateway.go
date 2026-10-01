// gateway.go reads a reverse-proxy gateway's routing (Caddy's native JSON
// config) so `routes` can follow a request through it: a UI calls
// "/orders-api/v2/documents/", the gateway forwards "/orders-api*" to
// localhost:8003 (stripping the prefix, or passing it as SCRIPT_NAME), and the
// backend serves "v2/documents/". Without the hop, a client path and a server
// route only meet by their common suffix, and a suffix shared by two backends
// ("v1/operations/") cannot be told apart. Each mount is tied to the indexed
// repo whose name carries the mount's words (/orders-api ->
// …orders_api, /maps -> …maps), or to an explicit upstream=repo mapping.
// Pure, stdlib only (ADR-018).
package query

import (
	"encoding/json"
	"sort"
	"strings"
)

// GatewayMount is one path prefix the gateway forwards to an upstream.
type GatewayMount struct {
	Prefix string   // "/orders-api" (the match path without its trailing *)
	Dial   string   // "localhost:8003"
	Repos  []string // indexed repos serving it (by name, or by explicit mapping)
	Source string   // the config it came from
}

// ParseCaddyJSON lists the reverse_proxy mounts of a Caddy JSON config
// (apps.http.servers.*.routes, subroutes included). Mounts with no path match
// (catch-alls) are skipped: they route nothing a path can tell apart.
func ParseCaddyJSON(content []byte, source string) ([]GatewayMount, error) {
	var cfg struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Routes []caddyRoute `json:"routes"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(content, &cfg); err != nil {
		return nil, err
	}
	var out []GatewayMount
	var walk func(rs []caddyRoute, inherited []string)
	walk = func(rs []caddyRoute, inherited []string) {
		for _, r := range rs {
			paths := inherited
			for _, m := range r.Match {
				if len(m.Path) > 0 {
					paths = m.Path
				}
			}
			for _, h := range r.Handle {
				switch h.Handler {
				case "reverse_proxy":
					for _, p := range paths {
						p = strings.TrimRight(p, "*")
						if p == "" || p == "/" {
							continue
						}
						for _, u := range h.Upstreams {
							out = append(out, GatewayMount{Prefix: strings.TrimRight(p, "/"), Dial: u.Dial, Source: source})
						}
					}
				case "subroute":
					walk(h.Routes, paths)
				}
			}
		}
	}
	for _, name := range sortedKeys(cfg.Apps.HTTP.Servers) {
		walk(cfg.Apps.HTTP.Servers[name].Routes, nil)
	}
	// Longest prefix first: "/maps-bridge" must win over "/maps".
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].Prefix) > len(out[j].Prefix) })
	return out, nil
}

type caddyRoute struct {
	Match []struct {
		Path []string `json:"path"`
	} `json:"match"`
	Handle []struct {
		Handler   string `json:"handler"`
		Upstreams []struct {
			Dial string `json:"dial"`
		} `json:"upstreams"`
		Routes []caddyRoute `json:"routes"`
	} `json:"handle"`
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// words splits a name into lowercase words on - _ / . and space.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return r == '-' || r == '_' || r == '/' || r == '.' || r == ' '
	})
}

// bindMounts ties each mount to the repos serving it: an explicit
// upstream=repo mapping first, else the repos whose name holds every word of
// the prefix with the fewest extra words (/maps -> acme_maps, not
// acme_maps_bridge; /maps-bridge -> acme_maps_bridge).
func bindMounts(mounts []GatewayMount, repos []string, upstreams map[string]string) []GatewayMount {
	for i := range mounts {
		if r, ok := upstreams[mounts[i].Dial]; ok {
			mounts[i].Repos = []string{r}
			continue
		}
		if externalDial(mounts[i].Dial) {
			continue // a hosted service (api.example.com:443): not code in this fleet
		}
		var pw []string
		for _, w := range words(mounts[i].Prefix) {
			if !genericWords[w] {
				pw = append(pw, w)
			}
		}
		if len(pw) == 0 {
			continue
		}
		best := -1
		var hits []string
		for _, r := range repos {
			rw := words(r)
			all := true
			for _, w := range pw {
				if !containsWord(rw, w) {
					all = false
					break
				}
			}
			if !all {
				continue
			}
			extra := len(rw) - len(pw)
			switch {
			case best < 0 || extra < best:
				best, hits = extra, []string{r}
			case extra == best:
				hits = append(hits, r)
			}
		}
		mounts[i].Repos = hits
	}
	// A repo a more specific mount names (/orders-api -> …orders_api)
	// is not also behind a vaguer one (/interface).
	for i := range mounts {
		if len(mounts[i].Repos) < 2 {
			continue
		}
		var keep []string
		for _, r := range mounts[i].Repos {
			taken := false
			for j := range mounts {
				if j != i && len(words(mounts[j].Prefix)) > len(words(mounts[i].Prefix)) && containsStr(mounts[j].Repos, r) {
					taken = true
				}
			}
			if !taken {
				keep = append(keep, r)
			}
		}
		if len(keep) > 0 {
			mounts[i].Repos = keep
		}
	}
	return mounts
}

// genericWords say nothing about which service a prefix names.
var genericWords = map[string]bool{"service": true, "server": true, "api": true, "app": true, "backend": true}

// externalDial: an upstream on another host (a domain name), not a local
// service the fleet's code runs as.
func externalDial(dial string) bool {
	host := dial
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	if host == "localhost" || strings.HasPrefix(host, "127.") || host == "0.0.0.0" || host == "" {
		return false
	}
	return strings.Contains(host, ".") && strings.IndexFunc(host, func(r rune) bool { return r >= 'a' && r <= 'z' }) >= 0
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func containsWord(ws []string, w string) bool {
	for _, x := range ws {
		if x == w {
			return true
		}
	}
	return false
}

// viaGateway finds the mount a client URL goes through: by its path prefix
// ("/maps/v1/spot"), by the upstream it dials directly
// ("http://localhost:8000/v1/spot"), or by the client object's name (an
// `ims.get(…)` client is the one mounted at /ims). It returns the mount and the
// path the backend sees.
func viaGateway(mounts []GatewayMount, rawURL, recv string) (GatewayMount, string, bool) {
	host, path := splitHost(rawURL)
	if host != "" {
		for _, m := range mounts {
			if m.Dial == host || strings.TrimPrefix(m.Dial, "localhost") == strings.TrimPrefix(host, "127.0.0.1") {
				return m, path, true
			}
		}
	}
	p := "/" + strings.TrimLeft(path, "/")
	if strings.HasPrefix(path, "{}") { // ${BASE}/maps/…: the variable is the origin
		p = "/" + strings.TrimLeft(strings.TrimPrefix(path, "{}"), "/")
	}
	for _, m := range mounts {
		if p == m.Prefix || strings.HasPrefix(p, m.Prefix+"/") {
			return m, strings.TrimPrefix(p, m.Prefix), true
		}
	}
	if r := lastSegDot(recv); r != "" {
		for _, m := range mounts {
			if containsWord(words(m.Prefix), strings.ToLower(r)) && len(m.Repos) > 0 {
				return m, path, true
			}
		}
	}
	return GatewayMount{}, path, false
}

func lastSegDot(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}

// splitHost returns "host:port" (default ports dropped) and the path of a URL
// with a scheme; "" host otherwise.
func splitHost(u string) (string, string) {
	i := strings.Index(u, "://")
	if i < 0 {
		return "", u
	}
	rest := u[i+3:]
	j := strings.Index(rest, "/")
	if j < 0 {
		return rest, "/"
	}
	return rest[:j], rest[j:]
}
