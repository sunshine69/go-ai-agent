// Package domainconfig defines the domain hierarchy, sub-categories, and
// associated keywords used by the context builder to route queries to MCP tools.
//
// It mirrors src/backend/app/domain_config.py. Domains may be defined in a
// domains.yaml file in the current working directory; if that file is missing
// (or contains no usable domains) the built-in generic set is used so the app
// always has something to offer. Each domain carries a display_name, icon,
// keyword list, Confluence spaces, and sub-categories. The confluence_spaces
// list is authoritative for scoping confluence_search calls and must be kept
// byte-identical to the Python source.
package domainconfig

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SubCategory is one sub-category within a domain.
type SubCategory struct {
	DisplayName string      `yaml:"display_name"`
	Icon        string      `yaml:"icon"`
	Keywords    []string    `yaml:"keywords"`
	Tools       []ToolEntry `yaml:"tools"`
}

// ToolEntry is a {tool: name, args: {...}} entry (or a [name, args] pair).
type ToolEntry struct {
	Tool string         `yaml:"tool"`
	Args map[string]any `yaml:"args"`
}

// Domain is one top-level domain entry.
type Domain struct {
	DisplayName      string                 `yaml:"display_name"`
	Icon             string                 `yaml:"icon"`
	Keywords         []string               `yaml:"keywords"`
	ConfluenceSpaces []string               `yaml:"confluence_spaces"`
	SubCategories    map[string]SubCategory `yaml:"sub_categories"`
}

// Domains is the full, coerced domain map keyed by domain key.
type Domains map[string]Domain

// YAMLPath returns the path to domains.yaml in the current working directory.
func YAMLPath() string {
	wd, err := os.Getwd()
	if err != nil {
		return "domains.yaml"
	}
	return filepath.Join(wd, "domains.yaml")
}

// Load reads domains from domains.yaml in the CWD, falling back to the built-in
// generic set. This mirrors domain_config.py::DOMAINS.
func Load() Domains {
	raw := loadFromYAML()
	if raw == nil {
		return genericDomains()
	}
	return coerce(raw)
}

// genericDomains returns the built-in generic domains used when no
// domains.yaml is present. This is the literal port of the Python
// _load_generic_domains(), including the confluence_spaces arrays (empty =
// unscoped) and every keyword literal.
func genericDomains() Domains {
	return Domains{
		"IT": {
			DisplayName:      "IT & Technology",
			Icon:             "💻",
			ConfluenceSpaces: []string{},
			Keywords: []string{
				"IT", "information technology", "technology", "software", "hardware",
				"network", "helpdesk", "ticket", "account", "system", "application",
			},
			SubCategories: map[string]SubCategory{
				"getting-started": {
					DisplayName: "Getting Started",
					Icon:        "🚀",
					Keywords:    []string{"onboarding", "getting started", "new", "new hire", "induction", "first day", "orientation"},
					Tools: []ToolEntry{
						{Tool: "process_search", Args: map[string]any{"keyword": "onboard*"}},
						{Tool: "confluence_search", Args: map[string]any{"keyword": "onboarding"}},
					},
				},
				"skills-training": {
					DisplayName: "Skills & Training",
					Icon:        "📚",
					Keywords:    []string{"skill", "competency", "training", "assessment", "certification", "qualification", "course"},
					Tools: []ToolEntry{
						{Tool: "skills_search", Args: map[string]any{"keyword": "*"}},
						{Tool: "skills_list", Args: map[string]any{}},
					},
				},
				"forms-templates": {
					DisplayName: "Forms & Templates",
					Icon:        "📄",
					Keywords:    []string{"form", "template", "request", "sheet", "checklist"},
					Tools: []ToolEntry{
						{Tool: "forms_search", Args: map[string]any{"keyword": "*"}},
						{Tool: "forms_list", Args: map[string]any{}},
					},
				},
				"processes-workflows": {
					DisplayName: "Processes & Workflows",
					Icon:        "🔄",
					Keywords:    []string{"process", "workflow", "procedure", "step", "lifecycle"},
					Tools: []ToolEntry{
						{Tool: "process_search", Args: map[string]any{"keyword": "*"}},
						{Tool: "confluence_search", Args: map[string]any{"keyword": "procedure"}},
					},
				},
				"systems-tools": {
					DisplayName: "Systems & Tools",
					Icon:        "🖥️",
					Keywords:    []string{"system", "software", "application", "platform", "database", "tool", "licence", "license"},
					Tools: []ToolEntry{
						{Tool: "confluence_search", Args: map[string]any{"keyword": "system"}},
						{Tool: "documents_search", Args: map[string]any{"keyword": "*"}},
					},
				},
				"policies-guidelines": {
					DisplayName: "Policies & Guidelines",
					Icon:        "📋",
					Keywords:    []string{"policy", "guideline", "standard", "SOP", "compliance"},
					Tools: []ToolEntry{
						{Tool: "confluence_search", Args: map[string]any{"keyword": "policy"}},
						{Tool: "documents_search", Args: map[string]any{"keyword": "policy"}},
					},
				},
				"support-contacts": {
					DisplayName: "Support & Contacts",
					Icon:        "📞",
					Keywords:    []string{"contact", "support", "help", "who", "owner", "manager", "team", "department", "helpdesk"},
					Tools: []ToolEntry{
						{Tool: "process_search", Args: map[string]any{"keyword": "owner"}},
						{Tool: "skills_search", Args: map[string]any{"keyword": "contact"}},
					},
				},
			},
		},
		"Science & Technology": {
			DisplayName:      "Science & Technology",
			Icon:             "🔬",
			ConfluenceSpaces: []string{},
			Keywords: []string{
				"science", "technology", "research", "laboratory", "lab", "analysis",
				"experiment", "specimen", "sample", "test", "innovation", "R&D", "RnD",
			},
			SubCategories: map[string]SubCategory{
				"getting-started": {
					DisplayName: "Getting Started",
					Icon:        "🚀",
					Keywords:    []string{"onboarding", "getting started", "new", "new hire"},
					Tools: []ToolEntry{
						{Tool: "process_search", Args: map[string]any{"keyword": "onboard*"}},
						{Tool: "confluence_search", Args: map[string]any{"keyword": "onboarding"}},
					},
				},
				"forms-templates": {
					DisplayName: "Forms & Templates",
					Icon:        "📄",
					Keywords:    []string{"form", "template", "request", "sample", "collection"},
					Tools: []ToolEntry{
						{Tool: "forms_search", Args: map[string]any{"keyword": "*"}},
						{Tool: "forms_list", Args: map[string]any{}},
					},
				},
				"processes-workflows": {
					DisplayName: "Processes & Workflows",
					Icon:        "🔄",
					Keywords:    []string{"process", "workflow", "procedure", "SOP", "step"},
					Tools: []ToolEntry{
						{Tool: "process_search", Args: map[string]any{"keyword": "*"}},
						{Tool: "confluence_search", Args: map[string]any{"keyword": "procedure"}},
					},
				},
				"systems-tools": {
					DisplayName: "Systems & Tools",
					Icon:        "🖥️",
					Keywords:    []string{"system", "software", "LIS", "platform", "instrument", "application"},
					Tools: []ToolEntry{
						{Tool: "confluence_search", Args: map[string]any{"keyword": "system"}},
						{Tool: "documents_search", Args: map[string]any{"keyword": "LIS"}},
					},
				},
				"policies-guidelines": {
					DisplayName: "Policies & Guidelines",
					Icon:        "📋",
					Keywords:    []string{"policy", "guideline", "standard", "SOP", "quality", "accreditation"},
					Tools: []ToolEntry{
						{Tool: "confluence_search", Args: map[string]any{"keyword": "policy"}},
						{Tool: "documents_search", Args: map[string]any{"keyword": "quality"}},
					},
				},
				"support-contacts": {
					DisplayName: "Support & Contacts",
					Icon:        "📞",
					Keywords:    []string{"contact", "support", "help", "who", "owner", "manager"},
					Tools: []ToolEntry{
						{Tool: "process_search", Args: map[string]any{"keyword": "owner"}},
						{Tool: "skills_search", Args: map[string]any{"keyword": "contact"}},
					},
				},
			},
		},
		"Operations & Logistics": {
			DisplayName:      "Operations & Logistics",
			Icon:             "⚙️",
			ConfluenceSpaces: []string{},
			Keywords: []string{
				"operations", "operational", "logistics", "facility", "site",
				"management", "supply", "chain", "dispatch", "fleet", "warehouse",
			},
			SubCategories: map[string]SubCategory{
				"getting-started": {
					DisplayName: "Getting Started",
					Icon:        "🚀",
					Keywords:    []string{"onboarding", "getting started", "new", "new hire"},
					Tools: []ToolEntry{
						{Tool: "process_search", Args: map[string]any{"keyword": "onboard*"}},
						{Tool: "confluence_search", Args: map[string]any{"keyword": "onboarding"}},
					},
				},
				"forms-templates": {
					DisplayName: "Forms & Templates",
					Icon:        "📄",
					Keywords:    []string{"form", "template", "request", "report", "log", "checklist"},
					Tools: []ToolEntry{
						{Tool: "forms_search", Args: map[string]any{"keyword": "*"}},
						{Tool: "forms_list", Args: map[string]any{}},
					},
				},
				"processes-workflows": {
					DisplayName: "Processes & Workflows",
					Icon:        "🔄",
					Keywords:    []string{"process", "workflow", "procedure", "SOP", "step"},
					Tools: []ToolEntry{
						{Tool: "process_search", Args: map[string]any{"keyword": "*"}},
						{Tool: "confluence_search", Args: map[string]any{"keyword": "procedure"}},
					},
				},
				"systems-tools": {
					DisplayName: "Systems & Tools",
					Icon:        "🖥️",
					Keywords:    []string{"system", "software", "application", "platform", "IT", "database"},
					Tools: []ToolEntry{
						{Tool: "confluence_search", Args: map[string]any{"keyword": "database"}},
						{Tool: "documents_search", Args: map[string]any{"keyword": "*"}},
					},
				},
				"policies-guidelines": {
					DisplayName: "Policies & Guidelines",
					Icon:        "📋",
					Keywords:    []string{"policy", "guideline", "standard", "SOP", "operations manual"},
					Tools: []ToolEntry{
						{Tool: "confluence_search", Args: map[string]any{"keyword": "operations manual"}},
						{Tool: "documents_search", Args: map[string]any{"keyword": "SOP"}},
					},
				},
				"support-contacts": {
					DisplayName: "Support & Contacts",
					Icon:        "📞",
					Keywords:    []string{"contact", "support", "help", "who", "owner", "manager"},
					Tools: []ToolEntry{
						{Tool: "process_search", Args: map[string]any{"keyword": "owner"}},
						{Tool: "skills_search", Args: map[string]any{"keyword": "contact"}},
					},
				},
			},
		},
		"Finance & Admin": {
			DisplayName:      "Finance & Admin",
			Icon:             "💰",
			ConfluenceSpaces: []string{},
			Keywords: []string{
				"finance", "financial", "accounts", "payroll", "billing", "invoice",
				"admin", "administrative", "procurement", "purchasing", "budget",
			},
			SubCategories: map[string]SubCategory{
				"getting-started": {
					DisplayName: "Getting Started",
					Icon:        "🚀",
					Keywords:    []string{"onboarding", "getting started", "new", "new hire"},
					Tools: []ToolEntry{
						{Tool: "process_search", Args: map[string]any{"keyword": "onboard*"}},
						{Tool: "confluence_search", Args: map[string]any{"keyword": "onboarding"}},
					},
				},
				"forms-templates": {
					DisplayName: "Forms & Templates",
					Icon:        "📄",
					Keywords:    []string{"form", "template", "request", "expense", "reimbursement"},
					Tools: []ToolEntry{
						{Tool: "forms_search", Args: map[string]any{"keyword": "*"}},
						{Tool: "forms_list", Args: map[string]any{}},
					},
				},
				"processes-workflows": {
					DisplayName: "Processes & Workflows",
					Icon:        "🔄",
					Keywords:    []string{"process", "workflow", "procedure", "SOP", "approval"},
					Tools: []ToolEntry{
						{Tool: "process_search", Args: map[string]any{"keyword": "*"}},
						{Tool: "confluence_search", Args: map[string]any{"keyword": "procedure"}},
					},
				},
				"policies-guidelines": {
					DisplayName: "Policies & Guidelines",
					Icon:        "📋",
					Keywords:    []string{"policy", "guideline", "standard", "SOP", "spend"},
					Tools: []ToolEntry{
						{Tool: "confluence_search", Args: map[string]any{"keyword": "policy"}},
						{Tool: "documents_search", Args: map[string]any{"keyword": "policy"}},
					},
				},
				"support-contacts": {
					DisplayName: "Support & Contacts",
					Icon:        "📞",
					Keywords:    []string{"contact", "support", "help", "who", "owner", "manager"},
					Tools: []ToolEntry{
						{Tool: "process_search", Args: map[string]any{"keyword": "owner"}},
						{Tool: "skills_search", Args: map[string]any{"keyword": "contact"}},
					},
				},
			},
		},
	}
}

// loadFromYAML reads domains from domains.yaml in the CWD, or nil if unavailable.
func loadFromYAML() map[string]map[string]any {
	path := YAMLPath()
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw struct {
		Domains map[string]map[string]any `yaml:"domains"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil
	}
	if raw.Domains == nil {
		return nil
	}
	return raw.Domains
}

// coerce converts the raw YAML map into a typed Domains value, mirroring
// domain_config.py::_coerce_domain.
func coerce(raw map[string]map[string]any) Domains {
	out := Domains{}
	for key, info := range raw {
		out[key] = coerceDomain(info, key)
	}
	return out
}

func coerceDomain(raw map[string]any, key string) Domain {
	displayName, _ := raw["display_name"].(string)
	if displayName == "" {
		displayName = key
	}
	icon, _ := raw["icon"].(string)
	if icon == "" {
		icon = "📁"
	}

	keywords := toStringSlice(raw["keywords"])
	confluenceSpaces := toStringSlice(raw["confluence_spaces"])

	sub := coerceSubCategories(raw["sub_categories"])

	return Domain{
		DisplayName:      displayName,
		Icon:             icon,
		Keywords:         keywords,
		ConfluenceSpaces: confluenceSpaces,
		SubCategories:    sub,
	}
}

func coerceSubCategories(raw any) map[string]SubCategory {
	out := map[string]SubCategory{}
	inner, ok := raw.(map[string]any)
	if !ok {
		return out
	}
	for subKey, subRaw := range inner {
		m, ok := subRaw.(map[string]any)
		if !ok {
			m = map[string]any{}
		}
		disp, _ := m["display_name"].(string)
		if disp == "" {
			disp = subKey
		}
		icon, _ := m["icon"].(string)
		if icon == "" {
			icon = "📁"
		}
		tools := coerceTools(m["tools"])
		out[subKey] = SubCategory{
			DisplayName: disp,
			Icon:        icon,
			Keywords:    toStringSlice(m["keywords"]),
			Tools:       tools,
		}
	}
	return out
}

// coerceTools converts a YAML list of [tool, args] / {tool, args} entries.
func coerceTools(raw any) []ToolEntry {
	tools := []ToolEntry{}
	list, ok := raw.([]any)
	if !ok {
		return tools
	}
	for _, entry := range list {
		switch e := entry.(type) {
		case map[string]any:
			tool, _ := e["tool"].(string)
			if tool == "" {
				continue
			}
			argsMap, _ := e["args"].(map[string]any)
			tools = append(tools, ToolEntry{Tool: tool, Args: argsMap})
		case []any:
			if len(e) == 0 {
				continue
			}
			tool, _ := e[0].(string)
			if tool == "" {
				continue
			}
			argsMap := map[string]any{}
			if len(e) > 1 {
				if m, ok := e[1].(map[string]any); ok {
					argsMap = m
				}
			}
			tools = append(tools, ToolEntry{Tool: tool, Args: argsMap})
		}
	}
	return tools
}

func toStringSlice(v any) []string {
	out := []string{}
	if v == nil {
		return out
	}
	list, ok := v.([]any)
	if !ok {
		return out
	}
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
