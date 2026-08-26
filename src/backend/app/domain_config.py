"""
Domain configuration for GenIQ.

This module defines the domain hierarchy, sub-categories, and associated keywords
used to build domain-specific search context.

Domains are defined in ``domains.yaml`` in the **current working directory** so they
can be configured without code changes. If that file is missing (or contains no
usable domains) the module falls back to a small built-in generic set so the app
always has something to offer.

Each domain has:
- ``display_name``  human-readable name
- ``icon``          emoji icon
- ``sub_categories`` map of sub-category key -> sub-category definition

Each sub-category has:
- ``display_name``          human-readable name
- ``icon``                  emoji icon
- ``keywords``              domain/sub-category keywords for the search terms
- ``tools``                 optional list of ``(tool_name, args)`` pairs that override
                            the ContextBuilder's default "search everything" behaviour

Configuring domains is as simple as dropping a ``domains.yaml`` next to the backend
and editing it — no code changes required. Example::

    domains:
      IT:
        display_name: "IT & Technology"
        icon: "💻"
        keywords: ["it", "technology", "software", "network", "helpdesk"]
        sub_categories:
          "Getting Started":
            display_name: "Getting Started"
            icon: "🚀"
            keywords: ["onboarding", "getting started", "new"]
            tools:
              - [confluence_search, {keyword: "onboarding"}]
              - [skills_search, {"keyword": "*"}]

If the file is absent, the following generic domains are used instead:
IT, Science & Technology, Operations & Logistics, Finance & Admin.
"""

import os

import yaml

# domains.yaml is read from the current working directory (the backend is launched
# from src/backend, so this resolves to src/backend/domains.yaml).
_DOMAINS_YAML_PATH = os.path.join(os.getcwd(), "domains.yaml")


def _coerce_domain(raw: dict, key: str) -> dict:
    """Validate / normalise a single domain entry from YAML.

    Accepts both fully-specified dicts and loose/invalid input without crashing.
    Returns a normalised ``{display_name, icon, keywords, sub_categories}`` dict.
    """
    if not isinstance(raw, dict):
        raw = {}

    display_name = str(raw.get("display_name", key))
    icon = str(raw.get("icon", "📁"))
    keywords = [str(k) for k in (raw.get("keywords") or [])]
    confluence_spaces = [str(s) for s in (raw.get("confluence_spaces") or [])]

    raw_subcats = raw.get("sub_categories") or {}
    if not isinstance(raw_subcats, dict):
        raw_subcats = {}

    sub_categories: dict = {}
    for sub_key, sub_raw in raw_subcats.items():
        if not isinstance(sub_raw, dict):
            sub_raw = {}
        sub_categories[str(sub_key)] = {
            "display_name": str(sub_raw.get("display_name", sub_key)),
            "icon": str(sub_raw.get("icon", "📁")),
            "keywords": [str(k) for k in (sub_raw.get("keywords") or [])],
            "tools": _coerce_tools(sub_raw.get("tools")),
        }

    return {"display_name": display_name, "icon": icon, "keywords": keywords,
            "confluence_spaces": confluence_spaces, "sub_categories": sub_categories}


def _coerce_tools(raw_tools) -> list[tuple[str, dict]]:
    """Coerce a YAML list of ``[tool, args]`` entries into ``(tool_name, args)`` tuples."""
    tools: list[tuple[str, dict]] = []
    if not isinstance(raw_tools, list):
        return tools
    for entry in raw_tools:
        if not isinstance(entry, (list, tuple)) or not entry:
            continue
        tool_name = str(entry[0])
        args = entry[1] if len(entry) > 1 else {}
        if not isinstance(args, dict):
            args = {}
        tools.append((tool_name, {str(k): v for k, v in args.items()}))
    return tools


def _load_generic_domains() -> dict:
    """Return the built-in generic domains used when no ``domains.yaml`` is found."""
    return {
        "IT": {
            "display_name": "IT & Technology",
            "icon": "💻",
            # Left empty (unscoped) so confluence_search can match any configured space.
            "confluence_spaces": [],
            "keywords": [
                "IT", "information technology", "technology", "software", "hardware",
                "network", "helpdesk", "ticket", "account", "system", "application",
            ],
            "sub_categories": {
                "getting-started": {
                    "display_name": "Getting Started",
                    "icon": "🚀",
                    "keywords": ["onboarding", "getting started", "new", "new hire",
                                 "induction", "first day", "orientation"],
                    "tools": [
                        ["process_search", {"keyword": "onboard*"}],
                        ["confluence_search", {"keyword": "onboarding"}],
                    ],
                },
                "skills-training": {
                    "display_name": "Skills & Training",
                    "icon": "📚",
                    "keywords": ["skill", "competency", "training", "assessment",
                                 "certification", "qualification", "course"],
                    "tools": [
                        ["skills_search", {"keyword": "*"}],
                        ["skills_list", {}],
                    ],
                },
                "forms-templates": {
                    "display_name": "Forms & Templates",
                    "icon": "📄",
                    "keywords": ["form", "template", "request", "sheet", "checklist"],
                    "tools": [
                        ["forms_search", {"keyword": "*"}],
                        ["forms_list", {}],
                    ],
                },
                "processes-workflows": {
                    "display_name": "Processes & Workflows",
                    "icon": "🔄",
                    "keywords": ["process", "workflow", "procedure", "step", "lifecycle"],
                    "tools": [
                        ["process_search", {"keyword": "*"}],
                        ["confluence_search", {"keyword": "procedure"}],
                    ],
                },
                "systems-tools": {
                    "display_name": "Systems & Tools",
                    "icon": "🖥️",
                    "keywords": ["system", "software", "application", "platform",
                                 "database", "tool", "licence", "license"],
                    "tools": [
                        ["confluence_search", {"keyword": "system"}],
                        ["documents_search", {"keyword": "*"}],
                    ],
                },
                "policies-guidelines": {
                    "display_name": "Policies & Guidelines",
                    "icon": "📋",
                    "keywords": ["policy", "guideline", "standard", "SOP", "compliance"],
                    "tools": [
                        ["confluence_search", {"keyword": "policy"}],
                        ["documents_search", {"keyword": "policy"}],
                    ],
                },
                "support-contacts": {
                    "display_name": "Support & Contacts",
                    "icon": "📞",
                    "keywords": ["contact", "support", "help", "who", "owner",
                                 "manager", "team", "department", "helpdesk"],
                    "tools": [
                        ["process_search", {"keyword": "owner"}],
                        ["skills_search", {"keyword": "contact"}],
                    ],
                },
            },
        },
        "Science & Technology": {
            "display_name": "Science & Technology",
            "icon": "🔬",
            # Left empty (unscoped) so confluence_search can match any configured space.
            "confluence_spaces": [],
            "keywords": [
                "science", "technology", "research", "laboratory", "lab", "analysis",
                "experiment", "specimen", "sample", "test", "innovation", "R&D", "RnD",
            ],
            "sub_categories": {
                "getting-started": {
                    "display_name": "Getting Started",
                    "icon": "🚀",
                    "keywords": ["onboarding", "getting started", "new", "new hire"],
                    "tools": [
                        ["process_search", {"keyword": "onboard*"}],
                        ["confluence_search", {"keyword": "onboarding"}],
                    ],
                },
                "forms-templates": {
                    "display_name": "Forms & Templates",
                    "icon": "📄",
                    "keywords": ["form", "template", "request", "sample", "collection"],
                    "tools": [
                        ["forms_search", {"keyword": "*"}],
                        ["forms_list", {}],
                    ],
                },
                "processes-workflows": {
                    "display_name": "Processes & Workflows",
                    "icon": "🔄",
                    "keywords": ["process", "workflow", "procedure", "SOP", "step"],
                    "tools": [
                        ["process_search", {"keyword": "*"}],
                        ["confluence_search", {"keyword": "procedure"}],
                    ],
                },
                "systems-tools": {
                    "display_name": "Systems & Tools",
                    "icon": "🖥️",
                    "keywords": ["system", "software", "LIS", "platform", "instrument",
                                 "application"],
                    "tools": [
                        ["confluence_search", {"keyword": "system"}],
                        ["documents_search", {"keyword": "LIS"}],
                    ],
                },
                "policies-guidelines": {
                    "display_name": "Policies & Guidelines",
                    "icon": "📋",
                    "keywords": ["policy", "guideline", "standard", "SOP", "quality",
                                 "accreditation"],
                    "tools": [
                        ["confluence_search", {"keyword": "policy"}],
                        ["documents_search", {"keyword": "quality"}],
                    ],
                },
                "support-contacts": {
                    "display_name": "Support & Contacts",
                    "icon": "📞",
                    "keywords": ["contact", "support", "help", "who", "owner", "manager"],
                    "tools": [
                        ["process_search", {"keyword": "owner"}],
                        ["skills_search", {"keyword": "contact"}],
                    ],
                },
            },
        },
        "Operations & Logistics": {
            "display_name": "Operations & Logistics",
            "icon": "⚙️",
            # Left empty (unscoped) so confluence_search can match any configured space.
            "confluence_spaces": [],
            "keywords": [
                "operations", "operational", "logistics", "facility", "site",
                "management", "supply", "chain", "dispatch", "fleet", "warehouse",
            ],
            "sub_categories": {
                "getting-started": {
                    "display_name": "Getting Started",
                    "icon": "🚀",
                    "keywords": ["onboarding", "getting started", "new", "new hire"],
                    "tools": [
                        ["process_search", {"keyword": "onboard*"}],
                        ["confluence_search", {"keyword": "onboarding"}],
                    ],
                },
                "forms-templates": {
                    "display_name": "Forms & Templates",
                    "icon": "📄",
                    "keywords": ["form", "template", "request", "report", "log", "checklist"],
                    "tools": [
                        ["forms_search", {"keyword": "*"}],
                        ["forms_list", {}],
                    ],
                },
                "processes-workflows": {
                    "display_name": "Processes & Workflows",
                    "icon": "🔄",
                    "keywords": ["process", "workflow", "procedure", "SOP", "step"],
                    "tools": [
                        ["process_search", {"keyword": "*"}],
                        ["confluence_search", {"keyword": "procedure"}],
                    ],
                },
                "systems-tools": {
                    "display_name": "Systems & Tools",
                    "icon": "🖥️",
                    "keywords": ["system", "software", "application", "platform", "IT", "database"],
                    "tools": [
                        ["confluence_search", {"keyword": "database"}],
                        ["documents_search", {"keyword": "*"}],
                    ],
                },
                "policies-guidelines": {
                    "display_name": "Policies & Guidelines",
                    "icon": "📋",
                    "keywords": ["policy", "guideline", "standard", "SOP", "operations manual"],
                    "tools": [
                        ["confluence_search", {"keyword": "operations manual"}],
                        ["documents_search", {"keyword": "SOP"}],
                    ],
                },
                "support-contacts": {
                    "display_name": "Support & Contacts",
                    "icon": "📞",
                    "keywords": ["contact", "support", "help", "who", "owner", "manager"],
                    "tools": [
                        ["process_search", {"keyword": "owner"}],
                        ["skills_search", {"keyword": "contact"}],
                    ],
                },
            },
        },
        "Finance & Admin": {
            "display_name": "Finance & Admin",
            "icon": "💰",
            # Left empty (unscoped) for confluence_search.
            "confluence_spaces": [],
            "keywords": [
                "finance", "financial", "accounts", "payroll", "billing", "invoice",
                "admin", "administrative", "procurement", "purchasing", "budget",
            ],
            "sub_categories": {
                "getting-started": {
                    "display_name": "Getting Started",
                    "icon": "🚀",
                    "keywords": ["onboarding", "getting started", "new", "new hire"],
                    "tools": [
                        ["process_search", {"keyword": "onboard*"}],
                        ["confluence_search", {"keyword": "onboarding"}],
                    ],
                },
                "forms-templates": {
                    "display_name": "Forms & Templates",
                    "icon": "📄",
                    "keywords": ["form", "template", "request", "expense", "reimbursement"],
                    "tools": [
                        ["forms_search", {"keyword": "*"}],
                        ["forms_list", {}],
                    ],
                },
                "processes-workflows": {
                    "display_name": "Processes & Workflows",
                    "icon": "🔄",
                    "keywords": ["process", "workflow", "procedure", "SOP", "approval"],
                    "tools": [
                        ["process_search", {"keyword": "*"}],
                        ["confluence_search", {"keyword": "procedure"}],
                    ],
                },
                "policies-guidelines": {
                    "display_name": "Policies & Guidelines",
                    "icon": "📋",
                    "keywords": ["policy", "guideline", "standard", "SOP", "spend"],
                    "tools": [
                        ["confluence_search", {"keyword": "policy"}],
                        ["documents_search", {"keyword": "policy"}],
                    ],
                },
                "support-contacts": {
                    "display_name": "Support & Contacts",
                    "icon": "📞",
                    "keywords": ["contact", "support", "help", "who", "owner", "manager"],
                    "tools": [
                        ["process_search", {"keyword": "owner"}],
                        ["skills_search", {"keyword": "contact"}],
                    ],
                },
            },
        },
    }


def _load_domains_from_yaml() -> dict | None:
    """Load domains from ``domains.yaml`` in the CWD, or ``None`` if unavailable."""
    if not os.path.isfile(_DOMAINS_YAML_PATH):
        return None
    try:
        with open(_DOMAINS_YAML_PATH, encoding="utf-8") as f:
            data = yaml.safe_load(f)
    except (OSError, yaml.YAMLError):
        return None

    domains = (data or {}).get("domains") if isinstance(data, dict) else None
    if not isinstance(domains, dict) or not domains:
        return None
    return domains


# Load domains at import time from the CWD, falling back to the built-in generic set.
_DOMAINS_RAW = _load_domains_from_yaml()
DOMAINS: dict[str, dict] = {
    key: _coerce_domain(info, key)
    for key, info in (_DOMAINS_RAW or _load_generic_domains()).items()
}
