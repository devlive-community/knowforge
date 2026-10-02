<div align="center">

<img width="96" src="app/web/public/logo.png" alt="KnowForge" />

# KnowForge

**An open-source, self-hosted platform for knowledge management and collaborative documentation**

English | [简体中文](README_zh.md)

Turn scattered knowledge into searchable, collaborative, shareable "books": organize content as books and chapters, write and read together online, and keep full ownership of your data.

[![version](https://img.shields.io/github/v/release/devlive-community/infosphere?label=version&color=blue)](https://github.com/devlive-community/infosphere/releases/latest)
![go](https://img.shields.io/badge/Go-1.26-00ADD8)
![next](https://img.shields.io/badge/Next.js-14-black)
![license](https://img.shields.io/badge/license-MIT-green)

</div>

---

## What is KnowForge

KnowForge is a knowledge base / documentation site / e-book platform you deploy on your own server. Organize knowledge like writing a book, in a multi-level "book → chapter" structure, write in the built-in Markdown workbench, and publish with one click as a public site that is friendly to both readers and search engines. You can also invite collaborators to maintain content together, and let readers comment, bookmark, and track their reading progress.

Use it to build: **documentation sites for teams and open-source projects**, **personal blogs or e-books**, **product manuals and knowledge bases**, and **technical tutorial sites**.

The whole system compiles into **a single binary** with the frontend and runtime embedded. It runs on SQLite with zero configuration, and you can switch to MySQL / PostgreSQL at any time.

## Why KnowForge

- **🗂 Own your data** — Fully self-hosted: content, uploads, and the database all live on your own server, with no dependency on third-party SaaS.
- **📦 Minimal deployment** — A single binary embeds Next.js SSR and the Node.js runtime. Start it with one `docker run` or one binary; SQLite works with zero configuration, and a graphical setup wizard handles initialization.
- **🔎 SEO first** — All public pages are server-side rendered with real HTML plus dynamic `title`/`description`/Open Graph/canonical/JSON-LD, and `sitemap.xml` and `robots.txt` are generated automatically so search engines actually index your content.
- **✍️ Great writing and reading experience** — Markdown workbench (paste/drag image upload, shortcuts, code blocks/tables/task lists), drag-and-drop chapter tree, web/PDF/ZIP import, and version history. Readers get a table of contents, resume reading, annotations, and reading progress statistics.
- **👥 Collaboration and community** — Book collaborators (editor/read-only), comments, likes and favorites, and real-time in-site notifications (SSE) turn documentation into an interactive community.
- **🔐 Production-grade security** — JWT + bcrypt, two-factor authentication (with step-up verification for sensitive operations), login lockout and password policy, captcha, audit logs, rate limiting, and a recycle bin. Sensitive values are stored as hashes in multi-instance deployments.
- **🖥 Multi-platform** — One self-hosted service with Web, desktop (Tauri), and Android clients.
- **🔄 Smooth upgrades** — One-click online upgrade from the admin console (automatic download and verification, replace, restart, rollback on failure), or pull a new Docker image and recreate the container.

## Features

**Content and writing**
- Multi-level book / chapter tree with ordering rules and chapter prefixes; draft / published / archived status
- Markdown workbench: image upload, shortcuts, code/table/task-list toolbar, local draft fallback
- Drag-and-drop chapter tree (including moving across levels as a child chapter) and context-menu actions
- Collect from web pages, import PDF and ZIP; export Markdown / DOCX / PDF, including batch export
- Chapter version history; multiple versions and languages per book
- Tags and popular searches; full-text search across books and chapters (including within a book)

**Reading experience**
- SSR public pages: home / explore / book / chapter / user profile
- Reading progress: cross-book "Reading now", resume at scroll position, total reading time, streaks and daily goals
- Chapter annotations, remembered font size, and read markers in the table of contents

**Collaboration and community**
- Book collaborators (editor / viewer) and sharing of private books
- Nested comments with permissions, likes and favorites
- In-site notifications (comments/likes/collaboration/upgrades) with real-time SSE push and a navigation bell

**Accounts and security**
- Sign up / sign in and third-party login (GitHub / Google / GitLab, multiple can be linked at once)
- Two-factor authentication (TOTP + backup codes) and step-up verification for sensitive operations
- Login lockout, password policy, captcha, and password recovery
- Profile, theme settings, invitation codes, and self-service account deletion (with an admin-configurable cooling-off period)

**Admin console**
- Graphical setup wizard (database → site → administrator)
- Site settings (name/description/logo/favicon/keywords/footer/ICP filing, site-wide announcement)
- Storage drivers (local disk / Qiniu object storage) and mail (SMTP / log)
- User and content management, audit logs, rate limiting, recycle bin, and one-click online upgrade

**Clients**
- **Web**: Next.js 14 + TypeScript + Tailwind, server-side rendered
- **Desktop**: Tauri 2 (macOS / Windows / Linux), multiple servers, in-app OAuth
- **Android**: Kotlin + Jetpack Compose, sign-in, search, reading, offline cache

## Quick start

### Docker (recommended)

```bash
# Official image (GitHub Packages / GHCR, published for amd64/arm64 with every v* release)
docker run -d --name knowforge -p 6969:6969 -v knowforge-data:/data \
  ghcr.io/devlive-community/knowforge:latest

# Or build from source locally with Docker Compose
docker compose up -d --build
```

After it starts, open `http://<host>:6969/install` to finish the setup wizard. All data (database, uploads, configuration) lives in `/data` inside the container (the `knowforge-data` volume).

- Port: `KNOWFORGE_PORT` (default `6969`)
- Data directory: fixed at `/data` inside the container; mount a volume or host directory to persist it
- Trusted proxies: behind nginx / a gateway, set `KNOWFORGE_TRUSTED_PROXIES` to the proxy IPs/CIDRs (comma-separated)
- External database: choose MySQL / PostgreSQL in the setup wizard and fill in the connection details
- Upgrade: pull the new image and recreate the container (`docker compose pull && docker compose up -d`)
- Image tags: `latest` and specific versions (e.g. `ghcr.io/devlive-community/knowforge:1.2.3`, `1.2`)

### Build from source

```bash
make build          # bin/knowforge-server (embeds Next.js SSR + Node.js 24)
make test           # the same quality gates as CI (vet/test/tsc/lint)
```

### Local development

```bash
make dev-server     # Go API (:6969, data written to server/data)
make dev-web        # Next.js SSR (:3000, talks to :6969 directly)
```

## Tech stack and architecture

- **Server**: Go + Gin + GORM, REST API (`/api/v1/*`), JWT + bcrypt, health checks and online upgrade
- **Database**: SQLite (zero-config default) / MySQL / PostgreSQL, chosen at install time, with idempotent GORM auto-migration
- **Frontend**: Next.js 14 + TypeScript + Tailwind, SSR standalone, SEO first
- **Packaging**: the frontend and Node.js runtime are embedded in the Go binary and shipped as a single file
- **CI/CD**: GitHub Actions quality gates → automatic deployment from the `dev` branch → `v*` tags publish multi-arch binaries and Docker images

```
knowforge/
├── server/               # Go server (single binary with embedded frontend build)
│   └── internal/
│       ├── app/          # HTTP routes, handlers, embedded web runtime management
│       ├── auth/         # JWT issuing and verification
│       ├── config/       # Persisted install configuration (data/config.json)
│       ├── database/     # SQLite / MySQL / PostgreSQL support
│       ├── models/       # GORM models and auto-migration
│       └── plugins/      # Feature plugins (tags, versions, translations, content collect, ...)
├── app/
│   ├── web/              # Next.js 14 + TypeScript + Tailwind frontend (SSR)
│   ├── desktop/          # Tauri 2 desktop client (macOS / Windows / Linux)
│   └── android/          # Android client (Kotlin + Jetpack Compose)
├── deploy/               # systemd unit / nginx config / sudoers / release scripts
├── Dockerfile · docker-compose.yml
└── Makefile
```

## Deployment and releases

### Production deployment (automated by CI)

Pushing to the `dev` branch triggers [deploy.yml](.github/workflows/deploy.yml): the frontend SSR, Node.js 24, and the Go API are built into one binary, uploaded via scp, atomically switched into `releases/<sha>`, and the systemd service is restarted. Old releases are cleaned up once the health check passes.

```
nginx (:80/:443)
 └─ /* → knowforge-api (Go, 127.0.0.1:6969)
          └─ hosts the embedded Next.js + Node.js 24 (internal 127.0.0.1:6900)
```

### Releasing a new version

```bash
deploy/release.sh        # tag and push the current version in handler_setup.go, then open and commit the next version (not pushed)
deploy/new-version.sh    # open a new version manually: bump versions everywhere and draft the CHANGELOG
```

[release.yml](.github/workflows/release.yml) builds multi-arch single-file binaries with the full web runtime, publishes a GitHub Release, and pushes multi-arch Docker images to GitHub Packages (GHCR). Administrators can also upgrade online from the "System" page.

## Clients

**Desktop**

```bash
cd app/desktop && pnpm install
pnpm dev            # development mode
pnpm build          # build installers
```

**Android**

```bash
cd app/android && ./gradlew assembleDebug
```

On first launch, both clients ask for the KnowForge server address and remember it.

## Data migration

Migrate historical data from the legacy version (Node.js/Express/MySQL):

```bash
go run ./cmd/migrate-legacy -legacy-dsn "user:pass@tcp(127.0.0.1:3306)/knowforge" [-dry-run]
```

## API

The REST API is mounted at `/api/v1/*` and covers the setup wizard, authentication, books and documents, search, comments, notifications, collaboration, OAuth, import/export, and site/storage/mail settings. See [docs/api.md](docs/api.md) for the full list of endpoints, requests/responses, and permissions.

## Acknowledgements

[JetBrains](https://www.jetbrains.com/) · [Tailwind CSS](https://tailwindcss.com/)
