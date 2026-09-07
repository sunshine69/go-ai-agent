# Web/SharePoint Site Crawler

Crawls a website or SharePoint site using Playwright (browser automation), converts pages to markdown, and saves them to local file system. Also extracts PDFs and saves them for RAG indexing.

## Prerequisites

### 1. Install Python dependencies

```bash
cd tools/sharepoint-scraper
pip3 install -r requirements.txt
```

### 2. Install Playwright browsers

```bash
playwright install chromium
```

### 3. Set up credentials

Copy `.env.example` to `.env` and fill in your credentials:

```bash
cp .env.example .env
# Edit .env with your SharePoint credentials
```

Credentials are read from `.env` for **SharePoint** mode. For **generic web** mode, provide `--user`/`--passw` on the command line.

## Usage

### Generic web site, no auth

```bash
python3 sharepoint_scraper.py \
    --sitetype web \
    --url https://example.com \
    --output-dir resources/documents/web/example
```

### SharePoint site, with credentials from .env

```bash
python3 sharepoint_scraper.py \
    --sitetype sharepoint \
    --url http://sitedge.au.int.sonichealthcare/ES/HR/Pages/Home.aspx \
    --output-dir resources/documents/sharepoint/hr
```

### SharePoint site, with inline credentials

```bash
python3 sharepoint_scraper.py \
    --sitetype sharepoint \
    --url http://sitedge.au.int.sonichealthcare/ES/HR/Pages/Home.aspx \
    --output-dir resources/documents/sharepoint/hr \
    --user sitsxk5 \
    --passw '6P*M++_Qe^Lv-j'
```

### Generic web site with basic auth

```bash
python3 sharepoint_scraper.py \
    --sitetype web \
    --authtype basic-auth \
    --url https://example.com \
    --user myuser \
    --passw mypass \
    --output-dir resources/documents/web/example
```

### Crawl with limit

```bash
python3 sharepoint_scraper.py \
    --sitetype web \
    --url https://www.dhm.com.au/clinicians/test-ordering-and-collection/test-advice/ \
    --output-dir resources/rag_documents/dhm/test-advice \
    --max-pages 10
```

## How it works

1. **Navigate** — Uses Playwright to open the starting page in a headless browser
2. **Convert** — Converts HTML to markdown using `html2text`
3. **Save** — Saves markdown to local files in `{output-dir}/`
4. **Extract PDFs** — Finds PDF links on each page
5. **Download PDFs** — Downloads PDFs to `{output-dir}/pdfs/` subdirectory

## Output structure

```
{output-dir}/
├── Home.md
├── Policies/
│   └── Onboarding.md
├── pdfs/
│   ├── training_manual.pdf
│   ├── safety_policy.pdf
│   └── ...
└── ...
```

**Note:** All output (markdown + PDFs) goes into the same `--output-dir`. PDFs are stored in a `pdfs/` subdirectory inside it.

## Command-line options

| Flag | Description | Default |
|------|-------------|---------|
| `--sitetype` | Site type: `web` for generic websites, `sharepoint` for SharePoint sites | `sharepoint` |
| `--url` | Starting URL (required) | — |
| `--output-dir` | Output directory — markdown files saved here, PDFs saved to `{output-dir}/pdfs/` | `resources/documents/sharepoint/hr` |
| `--max-pages` | Maximum number of pages to crawl | `100` |
| `--user` | Username for authentication (overrides `.env` if provided) | — |
| `--passw` | Password for authentication (overrides `.env` if provided) | — |
| `--authtype` | Auth type: `basic` or `basic-auth` for HTTP basic auth, `sharepoint` for SharePoint form auth | auto (none) |

## Authentication

- **SharePoint** (`--sitetype sharepoint`): Reads `SP_USERNAME` and `SP_PASSWORD` from `.env` in this directory. Can also override with `--user`/`--passw`.
- **Basic auth** (`--authtype basic-auth`): Provide `--user` and `--passw`. Credentials must be passed explicitly; the `.env` SharePoint vars do **not** apply.
- **No auth**: Simply don't provide `--user`/`--passw`. The crawler will attempt to access pages without authentication.
