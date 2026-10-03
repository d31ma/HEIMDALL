package api

import "testing"

// The query descriptions are keyed by route, and a key that no longer names a
// route is a description of nothing — the realistic way this table rots is a
// route being renamed or dropped while its entry stays behind, documenting a
// parameter no caller can send.
func TestEveryDescribedQueryBelongsToARoute(t *testing.T) {
	routes := map[string]bool{}
	for _, route := range (&Server{}).Routes() {
		routes[route.Method+" "+route.Pattern] = true
	}
	for key, parameters := range routeQuery {
		if !routes[key] {
			t.Errorf("routeQuery describes %q, which is not in the route table", key)
		}
		for _, parameter := range parameters {
			if parameter.name == "" || parameter.description == "" {
				t.Errorf("%s: a query parameter needs a name and a description", key)
			}
		}
	}
}
