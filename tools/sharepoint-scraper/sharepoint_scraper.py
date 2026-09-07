#!/usr/bin/env python3
"""
Web/SharePoint Site Crawler for SuperSonicIQ

Crawls a website or SharePoint site using Playwright (browser automation),
converts pages to markdown, and saves them to local file system.

Also downloads PDFs found on pages and saves them for RAG indexing.

Authentication is optional:
    --user   : username
    --passw  : password
    --authtype : authentication type (basic, basic-auth, sharepoint — default: none)

SharePoint mode (--sitetype sharepoint) uses Playwright's context.request for
PDF downloads (avoids browser PDF navigation issues) and filters .aspx links.

Generic web mode (--sitetype web) does a standard crawl of HTML pages.

Usage:
    # Generic web site, no auth:
    python3 sharepoint_scraper.py \
        --sitetype web \
        --url https://example.com \
        --output-dir resources/documents/web/example \
        --rag-dir resources/rag_documents/web/example

    # SharePoint site, with credentials:
    python3 sharepoint_scraper.py \
        --sitetype sharepoint \
        --url http://sitedge.au.int.sonichealthcare/ES/HR/Pages/Home.aspx \
        --output-dir resources/documents/sharepoint/hr \
        --rag-dir resources/rag_documents/hr \
        --user sitsxk5 \
        --passw '6P*M++_Qe^Lv-j'

    # Generic web site with basic auth:
    python3 sharepoint_scraper.py \
        --sitetype web \
        --authtype basic-auth \
        --url https://example.com \
        --user myuser \
        --passw mypass \
        --output-dir resources/documents/web/example \
        --rag-dir resources/rag_documents/web/example

    # Also accepts --user / --passw from .env file (see below).
    # For SharePoint, SP_USERNAME and SP_PASSWORD env vars are still respected.

Run from project root:
    python3 tools/sharepoint-scraper/sharepoint_scraper.py
"""

import argparse
import asyncio
import logging
import os
import re
import sys
import urllib.parse
from pathlib import Path

import playwright
from playwright.async_api import async_playwright, Browser, BrowserContext, Page
from bs4 import BeautifulSoup

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
)
logger = logging.getLogger(__name__)

# ── Constants ──────────────────────────────────────────────────────────
MAX_PAGES = 100
MAX_PDFS = 10

# Page extensions to crawl per sitetype
WEB_PAGE_EXTENSIONS = ('.html', '.htm', '/')
SHAREPOINT_PAGE_EXTENSIONS = ('.aspx', '.html', '/')

# Auth type mapping — extend this dict for new auth types
AUTH_TYPE_MAP = {
    'basic': 'basic',          # alias for basic-auth
    'basic-auth': 'basic',     # Playwright's http_credentials uses Basic auth
    'sharepoint': 'sharepoint',# SharePoint uses form-based auth via browser,
                               # so we pass credentials to browser instead
}

# ── Helpers ────────────────────────────────────────────────────────────

def make_md_filename(url: str) -> str:
    """
    Convert a URL to a .md filename.

    SharePoint: http://sitedge.au.int.sonichealthcare/ES/HR/Pages/Home.aspx
      → sitedge.au.int.sonichealthcare/ES/HR/Pages/Home.md

    Generic web: https://example.com/blog/post-name
      → example.com_blog_post-name.md
    """
    parsed = urllib.parse.urlparse(url)
    path = parsed.path
    # Strip extension
    for ext in ('.aspx', '.html', '.htm'):
        if path.lower().endswith(ext):
            path = path[:-len(ext)]
            break
    # Replace forward slashes with underscores, except the first segment
    segments = path.lstrip("/").split("/")
    filename = f"{segments[0]}.md"
    if len(segments) > 1:
        filename = f"{segments[0]}_{'_'.join(segments[1:])}.md"
    return filename


def clean_html(html_content: str) -> str:
    """
    Clean HTML content before converting to Markdown.
    Removes script tags, style tags, and other unnecessary elements.
    """
    soup = BeautifulSoup(html_content, "html.parser")

    # Remove script and style tags
    for tag in soup.find_all(["script", "style", "link", "meta", "noscript"]):
        tag.decompose()

    # Remove inline styles (simplifies HTML)
    for tag in soup.find_all(True):
        if tag.has_attr("style"):
            del tag["style"]

    return str(soup)


async def convert_to_markdown(html_content: str, page_url: str) -> str:
    """Convert HTML content to Markdown using html2text library."""
    import html2text

    html_content = clean_html(html_content)
    h = html2text.HTML2Text()
    h.ignore_links = False
    h.ignore_images = False
    h.body_width = 0
    h.wrap_links = True
    h.wrap_list_items = True
    h.unicode_snob = True

    markdown_content = h.handle(html_content)

    # Add source URL at the top for tracking
    source_header = f"---\nsource_url: {page_url}\n---\n\n"
    return source_header + markdown_content


# ── PDF Download ───────────────────────────────────────────────────────

def _is_pdf_url(url: str) -> bool:
    """Check whether a URL points to a PDF, ignoring query string/fragment."""
    return urllib.parse.urlparse(url).path.lower().endswith(".pdf")


async def extract_pdf_links(page: Page, url: str) -> list[str]:
    """Extract all PDF links from the current page."""
    pdf_links = []

    # Find all <a> tags with href ending in .pdf (case-insensitive)
    for a_tag in await page.query_selector_all("a"):
        href = await a_tag.get_attribute("href")
        if href:
            abs_url = urllib.parse.urljoin(url, href)
            if _is_pdf_url(abs_url) and abs_url not in pdf_links:
                pdf_links.append(abs_url)

    # Also check <object> and <embed> tags
    for tag_name in ["object", "embed"]:
        for tag in await page.query_selector_all(tag_name):
            data = await tag.get_attribute("data")
            if data:
                abs_url = urllib.parse.urljoin(url, data)
                if _is_pdf_url(abs_url) and abs_url not in pdf_links:
                    pdf_links.append(abs_url)

    return pdf_links


async def download_pdf(context: BrowserContext, pdf_url: str, output_dir: Path, filename: str | None = None) -> bool:
    """
    Download a PDF using the context's APIRequestContext (context.request).

    This avoids navigating a Page to the PDF URL: browsers treat a direct
    navigation to a PDF as either a file download (which aborts Playwright's
    page.goto() with "net::ERR_ABORTED") or hand it to the built-in PDF
    viewer (which can hang wait_until="networkidle" indefinitely). The
    APIRequestContext instead performs a plain HTTP GET and shares the
    context's http_credentials/cookies automatically, so auth still works.
    """
    if filename is None:
        # Generate filename from URL
        filename = pdf_url.split("/")[-1].split("?")[0]
        if not filename.endswith(".pdf"):
            filename += ".pdf"

    output_path = output_dir / filename
    if output_path.exists():
        logger.info(f"PDF already exists, skipping: {filename}")
        return True

    logger.info(f"Downloading PDF: {filename}")

    try:
        response = await context.request.get(pdf_url, timeout=60000)
        if not response.ok:
            logger.warning(f"PDF download failed with status {response.status}: {pdf_url}")
            return False

        body = await response.body()
        if not body:
            logger.warning(f"PDF response had no body: {pdf_url}")
            return False

        output_path.write_bytes(body)
        logger.info(f"Saved PDF: {output_path} ({len(body)} bytes)")
        return True
    except Exception as e:
        logger.error(f"Error downloading PDF {pdf_url}: {e}")
        return False


# ── Page Crawling ──────────────────────────────────────────────────────

async def crawl_page(page: Page, url: str, output_dir: Path, pdfs_dir: Path) -> dict:
    """Crawl a single page: convert to markdown, extract PDFs and links."""
    result = {
        "url": url,
        "success": False,
        "markdown_saved": False,
        "pdfs_downloaded": 0,
        "pdf_links_found": 0,
        "pdf_download_failures": 0,
        "links_found": 0,
        "error": None,
    }

    # Navigate to the page
    logger.info(f"Navigating to: {url}")
    try:
        await page.goto(url, timeout=30000)
    except playwright._impl._errors.TimeoutError:
        result["error"] = "Navigation timeout"
        logger.warning(f"Timeout navigating to: {url}")
        return result
    except Exception as e:
        result["error"] = str(e)
        logger.error(f"Failed to navigate to {url}: {e}")
        return result

    # Wait for content to load
    try:
        await page.wait_for_load_state("domcontentloaded", timeout=15000)
        logger.info("DOM loaded, waiting for content...")
    except Exception:
        logger.warning("Could not wait for content load")

    # Wait a bit more for JS rendering
    await page.wait_for_timeout(2000)

    # Get page content
    try:
        html_content = await page.evaluate("() => document.documentElement.outerHTML")
    except Exception as e:
        result["error"] = f"Failed to get page content: {e}"
        logger.error(f"Error getting content from {url}: {e}")
        return result

    # Convert to markdown
    try:
        markdown_content = await convert_to_markdown(html_content, url)
    except Exception as e:
        result["error"] = f"Failed to convert to markdown: {e}"
        logger.error(f"Error converting to markdown for {url}: {e}")
        return result

    # Save markdown
    try:
        filename = make_md_filename(url)
        output_path = output_dir / filename
        if output_path.exists():
            logger.info(f"Markdown already exists, skipping: {filename}")
            return result
        output_path.write_text(markdown_content, encoding="utf-8")
        result["markdown_saved"] = True
        result["success"] = True
        logger.info(f"Saved markdown: {output_path}")
    except Exception as e:
        result["error"] = f"Failed to save markdown: {e}"
        logger.error(f"Error saving markdown for {url}: {e}")
        return result

    # Extract PDF links
    try:
        pdf_links = await extract_pdf_links(page, url)
        result["pdf_links_found"] = len(pdf_links)
        logger.info(f"Found {len(pdf_links)} PDF links on {url}")
    except Exception as e:
        logger.error(f"Error extracting PDF links from {url}: {e}")
        pdf_links = []

    # Extract all links for navigation
    try:
        links = await page.query_selector_all("a")
        result["links_found"] = len(links)
    except Exception as e:
        logger.error(f"Error getting links from {url}: {e}")
        links = []

    # Download PDFs
    pdf_downloads = []
    for i, pdf_url in enumerate(pdf_links):
        if i >= MAX_PDFS:
            logger.warning(f"Max PDF limit ({MAX_PDFS}) reached for {url}")
            break

        # Generate safe filename
        safe_filename = re.sub(r'[^a-zA-Z0-9_\-\.]', '_', pdf_url.split("/")[-1].split("?")[0])
        if not safe_filename.endswith(".pdf"):
            safe_filename += ".pdf"

        pdf_downloads.append((pdf_url, safe_filename))

    for pdf_url, pdf_filename in pdf_downloads:
        if await download_pdf(page.context, pdf_url, pdfs_dir, pdf_filename):
            result["pdfs_downloaded"] += 1
        else:
            result["pdf_download_failures"] += 1

    return result


# ── Main Crawler ───────────────────────────────────────────────────────

async def crawl_site(start_url: str, output_dir: Path,
                     max_pages: int, sitetype: str, auth_type: str,
                     username: str | None, password: str | None) -> dict:
    """Crawl the entire site starting from the given URL."""

    # Validate sitetype
    if sitetype not in ('web', 'sharepoint'):
        logger.error(f"Invalid sitetype: {sitetype} (must be 'web' or 'sharepoint')")
        sys.exit(1)

    # Validate auth type
    if auth_type and auth_type not in AUTH_TYPE_MAP:
        logger.error(f"Invalid authtype: {auth_type} (must be one of {list(AUTH_TYPE_MAP.keys())})")
        sys.exit(1)

    # Check auth credentials
    has_auth = username and password
    if has_auth and not auth_type:
        auth_type = 'basic'  # default to basic auth when credentials provided
    if has_auth and auth_type == 'sharepoint' and not (username and password):
        logger.error("SharePoint auth type requires --user and --passw")
        sys.exit(1)

    # Page extension filter based on sitetype
    if sitetype == 'sharepoint':
        page_extensions = SHAREPOINT_PAGE_EXTENSIONS
    else:
        page_extensions = WEB_PAGE_EXTENSIONS

    # Create directories - markdown in root, PDFs in sub-dir
    output_dir.mkdir(parents=True, exist_ok=True)
    pdfs_dir = output_dir / "pdfs"
    pdfs_dir.mkdir(parents=True, exist_ok=True)

    logger.info(f"Starting crawl from: {start_url}")
    logger.info(f"Sitetype: {sitetype}")
    logger.info(f"Auth type: {auth_type or 'none'}")
    logger.info(f"Output directory: {output_dir}")
    logger.info(f"  PDFs sub-directory: {pdfs_dir}")
    logger.info(f"Max pages: {max_pages}")
    logger.info(f"Max PDFs per page: {MAX_PDFS}")
    logger.info(f"Page extensions to crawl: {page_extensions}")

    results = {
        "start_url": start_url,
        "sitetype": sitetype,
        "auth_type": auth_type or "none",
        "pages_crawled": 0,
        "pages_failed": 0,
        "total_markdown_saved": 0,
        "total_pdfs_downloaded": 0,
        "total_pdf_links_found": 0,
        "total_links_found": 0,
        "max_pages_reached": False,
        "errors": [],
    }

    async with async_playwright() as p:
        # Launch browser - use system Chrome since it's already installed
        logger.info("Launching browser...")
        chrome_path = os.environ.get("CHROME_PATH") or os.environ.get("CHROME_EXECUTABLE") or None
        if not chrome_path:
            for candidate in ["/usr/bin/google-chrome", "/usr/bin/google-chrome-stable", "/usr/bin/chromium-browser", "/usr/bin/chromium"]:
                if os.path.exists(candidate):
                    chrome_path = candidate
                    break
        if chrome_path:
            logger.info(f"Using system Chrome: {chrome_path}")
            browser: Browser = await p.chromium.launch(
                headless=True,
                executable_path=chrome_path,
                args=[
                    "--disable-web-security",
                    "--disable-features=IsolateOrigins,site-per-process",
                    "--disable-site-isolation-trials",
                    "--no-sandbox",
                ],
            )
        else:
            logger.warning("System Chrome not found, using Playwright bundled Chromium")
            browser: Browser = await p.chromium.launch(
                headless=True,
                args=[
                    "--disable-web-security",
                    "--disable-features=IsolateOrigins,site-per-process",
                    "--disable-site-isolation-trials",
                    "--no-sandbox",
                ],
            )

        # Build context options based on auth type
        context_kwargs = dict(
            bypass_csp=True,
            ignore_https_errors=True,
            viewport={"width": 1920, "height": 1080},
        )

        if auth_type == 'basic':
            # Basic auth: Playwright passes credentials via http_credentials
            context_kwargs["http_credentials"] = {"username": username, "password": password}
            logger.info(f"Auth: Basic auth with username: {username}")
        elif auth_type == 'sharepoint':
            # SharePoint: pass credentials to browser for form-based auth
            context_kwargs["http_credentials"] = {"username": username, "password": password}
            logger.info(f"Auth: SharePoint form-based auth with username: {username}")
        else:
            logger.info("Auth: none")

        context: BrowserContext = await browser.new_context(**context_kwargs)

        # Create a new page
        page: Page = await context.new_page()

        try:
            # Crawl the start URL
            page_results = await crawl_page(page, start_url, output_dir, pdfs_dir)
            results["pages_crawled"] = 1
            results["total_markdown_saved"] = 1 if page_results["markdown_saved"] else 0
            results["total_pdfs_downloaded"] = page_results["pdfs_downloaded"]
            results["total_pdf_links_found"] = page_results["pdf_links_found"]
            results["total_links_found"] = page_results["links_found"]
            if not page_results["success"]:
                results["pages_failed"] = 1
                results["errors"].append({
                    "url": start_url,
                    "error": page_results["error"],
                })

            # Collect links for crawling
            next_urls = set()
            links = await page.query_selector_all("a")
            for link in links:
                href = await link.get_attribute("href")
                if href:
                    # Convert relative URL to absolute
                    abs_url = urllib.parse.urljoin(start_url, href)

                    # Check if it's a crawlable page based on sitetype
                    is_page = (
                        abs_url.lower().endswith(page_extensions) and
                        not abs_url.lower().startswith('#') and
                        abs_url not in next_urls and
                        abs_url != start_url
                    )
                    if is_page:
                        next_urls.add(abs_url)

            logger.info(f"Found {len(next_urls)} page links to crawl from: {start_url}")

            # Crawl next pages
            # Convert to list to avoid RuntimeError from set mutation during iteration
            urls_to_crawl = list(next_urls)
            for i, url in enumerate(urls_to_crawl):
                if i >= max_pages:
                    results["max_pages_reached"] = True
                    logger.warning(f"Max pages ({max_pages}) reached — still has more pages to crawl!")
                    break

                logger.info(f"Crawling page {i + 2}/{max_pages}: {url}")
                try:
                    page_results = await crawl_page(page, url, output_dir, pdfs_dir)
                    results["pages_crawled"] += 1
                    results["total_markdown_saved"] += 1 if page_results["markdown_saved"] else 0
                    results["total_pdfs_downloaded"] += page_results["pdfs_downloaded"]
                    results["total_pdf_links_found"] += page_results["pdf_links_found"]
                    results["total_links_found"] += page_results["links_found"]

                    if not page_results["success"]:
                        results["pages_failed"] += 1
                        results["errors"].append({
                            "url": url,
                            "error": page_results["error"],
                        })

                    # Collect links from this page
                    page_links = await page.query_selector_all("a")
                    for link in page_links:
                        href = await link.get_attribute("href")
                        if href:
                            abs_url = urllib.parse.urljoin(url, href)
                            if (abs_url.lower().endswith(page_extensions) and
                                not abs_url.lower().startswith('#') and
                                abs_url not in urls_to_crawl):
                                urls_to_crawl.append(abs_url)

                except Exception as e:
                    logger.error(f"Error crawling {url}: {e}")
                    results["pages_failed"] += 1
                    results["errors"].append({
                        "url": url,
                        "error": str(e),
                    })

        finally:
            await browser.close()

    # Print summary
    print("\n" + "=" * 60)
    print("CRAWL SUMMARY")
    print("=" * 60)
    print(f"Start URL: {start_url}")
    print(f"Sitetype: {results['sitetype']}")
    print(f"Auth type: {results['auth_type']}")
    print(f"Pages crawled: {results['pages_crawled']}")
    print(f"Pages failed: {results['pages_failed']}")
    print(f"Markdown files saved: {results['total_markdown_saved']}")
    print(f"PDFs downloaded: {results['total_pdfs_downloaded']}")
    print(f"PDF links found: {results['total_pdf_links_found']}")
    print(f"Total links found: {results['total_links_found']}")
    if results["max_pages_reached"]:
        print(f"⚠️  WARNING: Reached max page limit — still has more pages to crawl!")
    print("=" * 60)

    if results["errors"]:
        print("\nErrors:")
        for error in results["errors"]:
            print(f"  - {error['url']}: {error['error']}")

    return results


# ── CLI ────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(
        description="Web/SharePoint Site Crawler for SuperSonicIQ"
    )
    parser.add_argument(
        "--sitetype",
        default="sharepoint",
        choices=["web", "sharepoint"],
        help="Site type: 'web' for generic websites, 'sharepoint' for SharePoint sites (default: sharepoint)"
    )
    parser.add_argument(
        "--url",
        default="http://sitedge.au.int.sonichealthcare/ES/HR/Pages/Home.aspx",
        help="Start URL for the site to crawl"
    )
    parser.add_argument(
        "--output-dir",
        default="resources/documents/sharepoint/hr",
        help="Output directory - markdown saved to the dir, PDFs to {output-dir}/pdfs/ subdirectory"
    )
    parser.add_argument(
        "--max-pages",
        type=int,
        default=MAX_PAGES,
        help="Maximum number of pages to crawl"
    )
    # Authentication options
    parser.add_argument(
        "--user",
        default=None,
        help="Username for authentication (also read from .env if not provided)"
    )
    parser.add_argument(
        "--passw",
        default=None,
        help="Password for authentication (also read from .env if not provided)"
    )
    parser.add_argument(
        "--authtype",
        default=None,
        choices=list(AUTH_TYPE_MAP.keys()),
        help="Authentication type: 'basic' or 'basic-auth' for HTTP basic auth, 'sharepoint' for SharePoint form auth, or none (default: auto-detected)"
    )

    args = parser.parse_args()

    # Load .env from this script's directory
    script_dir = Path(__file__).parent
    from dotenv import load_dotenv
    load_dotenv(script_dir / ".env")

    # Get credentials: .env only applies for SharePoint sitetype.
    # For generic web, the user must provide --user/--passw explicitly.
    if args.sitetype == "sharepoint":
        username = args.user or os.getenv("SP_USERNAME")
        password = args.passw or os.getenv("SP_PASSWORD")
    else:
        # Generic web — .env SP_ vars don't apply; require explicit --user/--passw
        username = args.user
        password = args.passw

    # Resolve output directory relative to project root (parent of tools/)
    project_root = script_dir.parent.parent
    output_dir = Path(args.output_dir)
    if not output_dir.is_absolute():
        output_dir = project_root / output_dir

    if not username or not password:
        logger.info("No authentication provided — running without credentials.")
        logger.info("  Set --user/--passw (for basic auth) or SP_USERNAME/SP_PASSWORD in .env (for SharePoint) to enable auth.")

    logger.info(f"Username: {username}")
    logger.info(f"Password: {'*' * (len(password) if password else 0)}")
    logger.info(f"Authtype: {args.authtype or 'none (auto)'}")
    logger.info(f"Output dir: {output_dir}")

    # Run the crawler
    results = asyncio.run(crawl_site(
        start_url=args.url,
        output_dir=output_dir,
        max_pages=args.max_pages,
        sitetype=args.sitetype,
        auth_type=args.authtype,
        username=username,
        password=password,
    ))

    if results["pages_failed"] > 0:
        sys.exit(1)


if __name__ == "__main__":
    main()
