package routers

import (
	"net/http"
	"sort"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/domainconfig"
)

// domainJSON is the serialized shape the Wails frontend reads for /api/domains.
// Field names must match the Python FastAPI contract exactly.
type domainJSON struct {
	Key           string                     `json:"key"`
	DisplayName   string                     `json:"display_name"`
	Icon          string                     `json:"icon"`
	SubCategories map[string]subCategoryJSON `json:"sub_categories"`
}

type subCategoryJSON struct {
	DisplayName string `json:"display_name"`
	Icon        string `json:"icon"`
}

type domainsHandler struct{}

func newDomainsHandler() *domainsHandler { return &domainsHandler{} }

func (h *domainsHandler) handle(w http.ResponseWriter, r *http.Request) {
	domains := domainconfig.Load()
	ordered := make([]domainJSON, 0, len(domains))
	// Map iteration in Go is unordered; sort by key so responses are stable
	// across requests (the Python dict preserves insertion order).
	keys := make([]string, 0, len(domains))
	for k := range domains {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		d := domains[k]
		subs := make(map[string]subCategoryJSON, len(d.SubCategories))
		for sk, sv := range d.SubCategories {
			subs[sk] = subCategoryJSON{DisplayName: sv.DisplayName, Icon: sv.Icon}
		}
		ordered = append(ordered, domainJSON{
			Key:           k,
			DisplayName:   d.DisplayName,
			Icon:          d.Icon,
			SubCategories: subs,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"domains": ordered})
}
