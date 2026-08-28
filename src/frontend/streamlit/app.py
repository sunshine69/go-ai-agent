#!/usr/bin/env python3
"""
SupersonicIQ - AI-Powered Knowledge Assistant
Streamlit frontend with domain-aware workflow

Workflow:
1. User selects a domain (HR, Pathology, Radiology, Operations)
2. Domain shows sub-category pills (inline, no rerun)
3. User types a question in the chat input - always available at the bottom
4. System builds context using domain-specific keywords
   - No domain: global search
   - Domain selected, no sub-category: domain-level search
   - Both selected: domain + sub-category search
"""

import streamlit as st
import requests
import os
import json
from dotenv import load_dotenv

# Load .env file from the frontend directory
load_dotenv(os.path.join(os.path.dirname(os.path.abspath(__file__)), ".env"))

# Constants
BACKEND_URL = os.getenv("BACKEND_URL", "http://localhost:8000")
API_BASE = f"{BACKEND_URL}/api"
API_TIMEOUT = int(os.getenv("API_TIMEOUT", "30"))
# TODO: replace with the real feedback/data-collection form URL once available
CONTRIBUTE_FORM_URL = os.getenv("CONTRIBUTE_FORM_URL", "https://forms.office.com/")

# Page config
st.set_page_config(
    page_title="SupersonicIQ - Sonic Healthcare Knowledge Assistant",
    page_icon="supersonicIQ.png",
    layout="wide",
)

# PWA Manifest + Service Worker - injected into parent window's <head> via JS
# This makes the app detectable by Chrome as installable
MANIFEST_DATA = {
    "name": "SupersonicIQ - Sonic Healthcare Knowledge Assistant",
    "short_name": "SupersonicIQ",
    "description": "AI-Powered Knowledge Assistant for Sonic Healthcare",
    "start_url": "/",
    "display": "standalone",
    "background_color": "#111827",
    "theme_color": "#4c8bf5",
    "icons": [
        {
            "src": "http://localhost:8501/supersonicIQ-192.png",
            "sizes": "192x192",
            "type": "image/png"
        },
        {
            "src": "http://localhost:8501/supersonicIQ-512.png",
            "sizes": "512x512",
            "type": "image/png"
        }
    ]
}
manifest_string = json.dumps(MANIFEST_DATA)

# Service worker code - Chrome requires a service worker for PWA installability
SW_CODE = """
const CACHE_NAME = "supersoniciq-v1";
const STATIC_ASSETS = [
    "/",
    "/manifest.json",
    "/sw.js",
    "/supersonicIQ-192.png",
    "/supersonicIQ-512.png",
];

self.addEventListener("install", (event) => {
    event.waitUntil(
        caches.open(CACHE_NAME).then((cache) => {
            return cache.addAll(STATIC_ASSETS);
        })
    );
    self.skipWaiting();
});

self.addEventListener("activate", (event) => {
    event.waitUntil(
        caches.keys().then((keys) => {
            return Promise.all(
                keys
                    .filter((key) => key !== CACHE_NAME)
                    .map((key) => caches.delete(key))
            );
        })
    );
    self.clients.claim();
});

self.addEventListener("fetch", (event) => {
    if (event.request.url.includes("/api/")) {
        event.respondWith(fetch(event.request));
        return;
    }
    event.respondWith(
        caches.match(event.request).then((cached) => {
            return cached || fetch(event.request);
        })
    );
});
"""

js_injector = f"""
<script>
    // Inject PWA manifest into parent window's head
    const manifestStr = {repr(manifest_string)};
    const manifestBlob = new Blob([manifestStr], {{type: 'application/json'}});
    const manifestURL = URL.createObjectURL(manifestBlob);
    const parentHead = window.parent.document.getElementsByTagName('head')[0];
    const oldManifest = parentHead.querySelector('link[rel="manifest"]');
    if (oldManifest) oldManifest.remove();
    const link = window.parent.document.createElement('link');
    link.rel = 'manifest';
    link.href = manifestURL;
    parentHead.appendChild(link);

    // Register service worker via blob URL (Chrome requires SW for PWA installability)
    const swCode = {repr(SW_CODE)};
    const swBlob = new Blob([swCode], {{type: 'application/javascript'}});
    const swURL = URL.createObjectURL(swBlob);
    if ('serviceWorker' in window.parent.navigator) {{
        window.parent.navigator.serviceWorker.register(swURL).then((reg) => {{
            console.log('PWA ServiceWorker registered', reg);
        }}).catch((err) => {{
            console.error('PWA ServiceWorker registration failed:', err);
        }});
    }}
</script>
"""
st.html(js_injector)

# Session state initialization
if "messages" not in st.session_state:
    st.session_state.messages = []
if "conversations" not in st.session_state:
    st.session_state.conversations = []
if "current_conversation" not in st.session_state:
    st.session_state.current_conversation = None
if "selected_domain" not in st.session_state:
    st.session_state.selected_domain = None
if "selected_sub_category" not in st.session_state:
    st.session_state.selected_sub_category = None
if "domains" not in st.session_state:
    st.session_state.domains = None

# --- API helpers ---

def get_api_request(path, params=None):
    """Make GET request to API"""
    try:
        response = requests.get(f"{API_BASE}{path}", params=params, timeout=API_TIMEOUT)
        response.raise_for_status()
        return response.json()
    except requests.exceptions.RequestException as e:
        return {"error": str(e)}

def post_api_request(path, data=None):
    """Make POST request to API"""
    try:
        response = requests.post(
            f"{API_BASE}{path}",
            json=data,
            headers={"Content-Type": "application/json"},
            timeout=API_TIMEOUT,
        )
        response.raise_for_status()
        return response.json()
    except requests.exceptions.RequestException as e:
        return {"error": str(e)}

def delete_api_request(path):
    """Make DELETE request to API (used to clear/delete conversations)."""
    try:
        response = requests.delete(
            f"{API_BASE}{path}",
            headers={"Content-Type": "application/json"},
            timeout=API_TIMEOUT,
        )
        response.raise_for_status()
        return response.json()
    except requests.exceptions.RequestException as e:
        return {"error": str(e)}

def load_domains():
    """Load domain configuration from the API."""
    if st.session_state.domains is None:
        result = get_api_request("/domains")
        if "error" not in result:
            st.session_state.domains = result.get("domains", [])

def get_selected_domain_info():
    """Get the selected domain and sub-category info from the API."""
    load_domains()
    domains = st.session_state.domains or []
    if not st.session_state.selected_domain:
        return None, None, None

    selected_domain = next((d for d in domains if d["key"] == st.session_state.selected_domain), None)
    if not selected_domain:
        return None, None, None

    sub_cat_info = {}
    if st.session_state.selected_sub_category:
        sub_cats = selected_domain.get("sub_categories", {})
        sub_cat_info = sub_cats.get(st.session_state.selected_sub_category, {})

    return selected_domain, sub_cat_info, selected_domain.get("sub_categories", {})

def show_rag_sources(sources):
    """Render RAG document references returned by the backend as clickable links."""
    if not sources:
        return
    # Filter out non-RAG sources (e.g., "Direct Answer") and Confluence sources
    rag_sources = [s for s in sources if s not in ["Direct Answer"] and not s.startswith("Documents Search") and not s.startswith("Skills Search") and not s.startswith("Forms Search") and not s.startswith("Process Search") and not s.startswith("Confluence Search")]
    if not rag_sources:
        return
    with st.expander(f"📄 RAG document sources ({len(rag_sources)})"):
        for src in rag_sources:
            st.markdown(f"- {src}")

def show_rag_sources(sources):
    """Render RAG document references returned by the backend as clickable links."""
    if not sources:
        return
    with st.expander(f"📄 RAG document sources ({len(sources)})"):
        for src in sources:
            st.markdown(f"- {src}")

def show_confluence_links(confluence_links):
    """Render Confluence page references returned by the backend as clickable links."""
    if not confluence_links:
        return
    with st.expander(f"📎 Confluence sources ({len(confluence_links)})"):
        for link in confluence_links:
            st.markdown(f"- [{link['title']}]({link['url']})")

def show_contribute_button(key):
    """Show a button linking to the feedback/data-collection form for an assistant answer."""
    st.link_button(
        "⚠️ Missing information or not what you expected? Please contribute",
        CONTRIBUTE_FORM_URL,
        key=key,
    )

def build_history():
    """Build the prior-turn history payload for cross-turn context.

    Skips the synthetic intro messages shown on domain/sub-category selection
    (they start with a known phrase and are display-only), and keeps only the
    assistant turns. This mirrors the payload the Wails frontend sends.
    """
    history = []
    for msg in st.session_state.messages:
        if msg.get("role") != "assistant":
            continue
        content = msg.get("content", "")
        # Skip the synthetic intro messages (domain/sub-category selection)
        if content.startswith("Great! I'm ready to help"):
            continue
        if content.startswith("Perfect! Let me help you with"):
            continue
        history.append({"role": "assistant", "content": content})
    return history


def send_message(prompt):
    """Send a message to the backend with domain context."""
    data = {"message": prompt}
    if st.session_state.current_conversation:
        data["conversation_id"] = st.session_state.current_conversation
    data["history"] = build_history()

    selected_domain, sub_cat_info, _ = get_selected_domain_info()
    if selected_domain:
        data["domain"] = st.session_state.selected_domain
        if sub_cat_info:
            data["sub_category"] = st.session_state.selected_sub_category

    result = post_api_request("/messages", data)
    return result

def reset_selection():
    """Reset domain and sub-category selection."""
    st.session_state.selected_domain = None
    st.session_state.selected_sub_category = None
    st.session_state.messages = []

def start_new_conversation():
    """Start a brand-new conversation in the backend and display it immediately.

    Creates the conversation in the backend right away so it shows up in the
    sidebar "Conversations" list (with an "Untitled" title) the moment "New
    Conversation" is clicked, rather than waiting for the first message.
    """
    result = post_api_request("/conversations", {})
    if "error" not in result and isinstance(result, dict):
        st.session_state.current_conversation = result.get("id")
    else:
        st.session_state.current_conversation = None
    st.session_state.selected_domain = None
    st.session_state.selected_sub_category = None
    st.session_state.messages = []

def select_conversation(conv_id):
    """Load an existing conversation's history into the chat and make it active.

    Fetches the full conversation from the backend and restores its user + assistant
    turns so prior context shows up in the window. Leaves the current domain /
    sub-category selection untouched so the RAG search scope stays consistent.
    """
    if not conv_id:
        return
    result = get_api_request(f"/conversations/{conv_id}")
    if "error" in result or not isinstance(result, dict):
        return

    stored = result.get("messages", [])
    messages = []
    for m in stored:
        role = m.get("role")
        if role == "assistant":
            messages.append({
                "role": "assistant",
                "content": m.get("content", ""),
                "confluence_links": m.get("confluence_links", []),
                "rag_sources": m.get("rag_sources", []),
            })
        elif role == "user":
            messages.append({"role": "user", "content": m.get("content", "")})

    st.session_state.current_conversation = conv_id
    st.session_state.messages = messages

def show_welcome():
    """Show welcome screen with domain selection pills."""
    load_domains()
    domains = st.session_state.domains or []

    if not domains:
        st.warning("Failed to load domains. Please try again later.")
        return

    st.markdown("## Welcome to SupersonicIQ")
    st.markdown("I can help you find information quickly. Select an area to get started:")

    # Use pills for domain selection - native Streamlit element
    # pills disappear after selection, which is perfect for onboarding
    selected = st.pills(
        "Select area:",
        options=[f"{d['icon']} {d['display_name']}" for d in domains],
        default=None,
        label_visibility="collapsed",
        key="domain_pills"
    )

    if selected:
        # Guard against rerun loop: only process if domain not already selected
        if st.session_state.selected_domain:
            st.warning("Domain already selected. Use sidebar to change.")
        else:
            # Only process selection once to prevent rerun loop
            for d in domains:
                label = f"{d['icon']} {d['display_name']}"
                if label == selected:
                    st.session_state.selected_domain = d["key"]
                    st.session_state.selected_sub_category = None
                    st.session_state.messages.append({
                        "role": "assistant",
                        "content": f"Great! I'm ready to help with {d['display_name']}. What would you like to know?",
                    })
                    st.rerun()

def show_sub_categories():
    """Show sub-category pills based on the selected domain."""
    selected_domain, sub_cat_info, sub_cats = get_selected_domain_info()
    if not selected_domain:
        st.warning("Domain not found. Please select a domain again.")
        return

    st.markdown(f"## How can I help with {selected_domain['display_name']}?")

    # Use pills for sub-category selection - no rerun until selected
    selected = st.pills(
        "Select category:",
        options=[f"{info['icon']} {info['display_name']}" for info in sub_cats.values()],
        default=None,
        label_visibility="collapsed",
        key="subcat_pills"
    )

    if selected and not st.session_state.selected_sub_category:
        # Only process selection once to prevent rerun loop
        for key, info in sub_cats.items():
            label = f"{info['icon']} {info['display_name']}"
            if label == selected:
                st.session_state.selected_sub_category = key
                st.session_state.messages.append({
                    "role": "assistant",
                    "content": f"Perfect! Let me help you with {info['display_name']}. What would you like to know?",
                })
                st.rerun()

    # Back button to change domain
    if st.button("← Back to domains", key="back_to_domains"):
        reset_selection()
        st.rerun()

def show_chat_input():
    """Show the chat history and input at the bottom of the page.

    Available at all times - search scope depends on session state:
    - No domain: global search
    - Domain selected, no sub-category: domain-level search
    - Both selected: domain + sub-category search
    """
    selected_domain, sub_cat_info, _ = get_selected_domain_info()

    # Determine placeholder text based on scope
    if not selected_domain:
        placeholder = "Ask me anything..."
        show_domain_label = False
    elif not sub_cat_info:
        placeholder = f"Ask about {selected_domain['display_name']}..."
        show_domain_label = False
    else:
        placeholder = f"Ask about {selected_domain['display_name']} > {sub_cat_info['display_name']}..."
        show_domain_label = True

    # Show domain label only when both domain and sub-category are selected
    if show_domain_label:
        label = f"{selected_domain['icon']} {selected_domain['display_name']}"
        label += f" > {sub_cat_info['display_name']}"
        st.markdown(f"**{label}**")

    # Display chat history - user messages appear IMMEDIATELY because they're in session_state
    for idx, message in enumerate(st.session_state.messages):
        avatar = "supersonicIQ.png" if message["role"] == "assistant" else None
        with st.chat_message(message["role"], avatar=avatar):
            st.markdown(message["content"])
            show_rag_sources(message.get("rag_sources"))
            show_confluence_links(message.get("confluence_links"))
            if message["role"] == "assistant":
                show_contribute_button(key=f"contribute_{idx}")

    # Chat input - disabled during API call to prevent interruptions
    if prompt := st.chat_input(placeholder, submit_mode="disable"):
        # Add user message to history BEFORE API call so it shows immediately
        st.session_state.messages.append({
            "role": "user",
            "content": prompt,
        })

        with st.chat_message("user"):
            st.markdown(prompt)

        # Show loading state for assistant, then display the result
        with st.chat_message("assistant", avatar="supersonicIQ.png"):
            with st.spinner("Thinking..."):
                result = send_message(prompt)

            if "error" in result:
                st.error(f"Error: {result['error']}")
            else:
                response_content = result.get("answer", "No response")
                confluence_links = result.get("confluence_links") or []
                rag_sources = result.get("sources") or []
                st.markdown(response_content)
                show_rag_sources(rag_sources)
                show_confluence_links(confluence_links)
                show_contribute_button(key=f"contribute_new_{len(st.session_state.messages)}")
            st.session_state.messages.append({
                "role": "assistant",
                "content": response_content,
                "confluence_links": confluence_links,
                "rag_sources": rag_sources,
            })

            if st.session_state.current_conversation is None and result.get("conversation_id"):
                st.session_state.current_conversation = result["conversation_id"]

# --- Sidebar ---

def show_sidebar():
    """Show sidebar with navigation."""
    with st.sidebar:
        st.header("SupersonicIQ")

        if st.button("🆕 New Conversation", key="new_conversation", use_container_width=True):
            start_new_conversation()
            st.rerun()

        st.divider()

        # Domain selection indicator
        if st.session_state.selected_domain:
            selected_domain, sub_cat_info, _ = get_selected_domain_info()
            if selected_domain:
                label = f"{selected_domain['icon']} {selected_domain['display_name']}"
                if sub_cat_info:
                    label += f" > {sub_cat_info['display_name']}"
                st.markdown(f"**{label}**")

                if st.button("Change selection", key="change_selection"):
                    reset_selection()
                    st.rerun()

        st.divider()

        # Quick actions - click to switch domain
        with st.expander("Quick Actions"):
            load_domains()
            for domain in st.session_state.domains or []:
                if st.button(f"{domain['icon']} {domain['display_name']}", key=f"quick_{domain['key']}"):
                    st.session_state.selected_domain = domain["key"]
                    st.session_state.selected_sub_category = None
                    st.session_state.messages = []
                    st.rerun()

        st.divider()

        # Conversations
        with st.expander("Conversations"):
            conversations = get_api_request("/conversations")
            if "error" in conversations:
                st.caption("Could not load conversations")
            elif len(conversations) == 0:
                st.caption("No conversations yet")
                st.button("🆕 Create one", key="empty_start_new",
                          use_container_width=True,
                          on_click=start_new_conversation)
            else:
                for conv in conversations:
                    conv_id = conv.get("id")
                    title = conv.get("title", "Untitled")
                    is_active = conv_id == st.session_state.current_conversation

                    # Per-conversation row: [title] [select] [delete]
                    c_title, c_open, c_del = st.columns([5, 1, 1])
                    with c_title:
                        st.caption(f"{title} {'✅' if is_active else ''}")
                    with c_open:
                        if st.button("📂", key=f"conv_open_{conv_id}"):
                            select_conversation(conv_id)
                            st.rerun()
                    with c_del:
                        if st.button("🗑️", key=f"conv_del_{conv_id}"):
                            was_active = conv_id == st.session_state.current_conversation
                            if was_active:
                                st.session_state.messages = []
                                st.session_state.current_conversation = None
                            result = delete_api_request(f"/conversations/{conv_id}")
                            if "error" not in result:
                                st.rerun()

                st.divider()
                # Clear every conversation in one shot.
                if st.button("🧹 Clear all", key="clear_all_conversations",
                              use_container_width=True):
                    delete_api_request("/conversations")
                    st.session_state.messages = []
                    st.session_state.current_conversation = None
                    st.rerun()

def main():
    """Main application."""
    show_sidebar()

    # Show pills based on selection state
    if not st.session_state.selected_domain:
        # No domain selected: show welcome + domain pills
        show_welcome()
    else:
        # Domain selected: show sub-category pills
        show_sub_categories()

    # Chat input is always available - search scope depends on session state
    show_chat_input()

if __name__ == "__main__":
    main()
