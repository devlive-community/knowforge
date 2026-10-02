<div align="center">

<img width="96" src="app/web/public/logo.png" alt="KnowForge" />

# KnowForge

**开源、自托管的知识管理与文档协作平台**

[English](README.md) | 简体中文

把零散的知识沉淀成一本本可检索、可协作、可分享的「书」——以书籍与章节组织内容，在线协作写作与阅读，数据完全掌握在自己手里。

[![version](https://img.shields.io/github/v/release/devlive-community/infosphere?label=version&color=blue)](https://github.com/devlive-community/infosphere/releases/latest)
![go](https://img.shields.io/badge/Go-1.26-00ADD8)
![next](https://img.shields.io/badge/Next.js-14-black)
![license](https://img.shields.io/badge/license-MIT-green)

</div>

---

## KnowForge 是什么

KnowForge 是一个可以部署在自己服务器上的知识库 / 文档站 / 电子书平台。你可以像写书一样把知识整理成「书籍 → 章节」的多级结构，用内置的 Markdown 写作台创作，一键发布成对读者友好、对搜索引擎友好的公开站点；也可以邀请协作者共同维护、让读者评论与收藏、追踪自己的阅读进度。

它适合用来搭建：**团队/开源项目的文档站**、**个人博客或电子书**、**产品手册与知识库**、**技术教程站点**。

整套系统编译为**一个二进制文件**，内置前端与运行时，零配置即可用 SQLite 跑起来——也可以随时切换到 MySQL / PostgreSQL。

## 为什么选择 KnowForge

- **🗂 数据自主可控** —— 完全自托管，内容、上传、数据库都在你自己的服务器上，不依赖任何第三方 SaaS。
- **📦 部署极简** —— 单文件二进制内嵌 Next.js SSR 与 Node.js 运行时；一条 `docker run` 或一个二进制即可启动，默认零配置 SQLite，图形化安装向导完成初始化。
- **🔎 SEO 一等公民** —— 公开页面全部服务端渲染，输出真实 HTML + 动态 `title`/`description`/Open Graph/canonical/JSON-LD，自动生成 `sitemap.xml` 与 `robots.txt`，让内容被搜索引擎真正收录。
- **✍️ 好用的写作与阅读体验** —— Markdown 写作台（图片粘贴/拖拽上传、快捷键、代码块/表格/任务列表）、章节树拖拽排序、网页/PDF/ZIP 导入、版本历史；读者侧有目录、续读、笔记标注与阅读进度统计。
- **👥 协作与社区** —— 书籍协作者（编辑/只读）、评论、点赞收藏、站内实时通知（SSE），把文档变成可互动的社区。
- **🔐 面向生产的安全能力** —— JWT + bcrypt、两步验证（含敏感操作二次认证）、登录失败锁定与密码策略、验证码、审计日志、限流、回收站，多实例部署时敏感值以哈希存储。
- **🖥 多端覆盖** —— 同一套自托管服务，配套 Web、桌面端（Tauri）与 Android 客户端。
- **🔄 平滑升级** —— 管理后台一键在线升级（自动下载校验、替换、重启、失败回滚），或 Docker 拉取新镜像重建。

## 功能一览

**内容与写作**
- 书籍 / 章节多级树，排序规则与章节前缀；草稿 / 发布 / 归档状态
- Markdown 写作台：图片上传、快捷键、代码/表格/任务列表工具栏、本地草稿兜底
- 章节树拖拽排序（含跨层级成为子章节）、右键菜单快捷操作
- 从网页采集、PDF、ZIP 导入；导出 Markdown / DOCX / PDF，支持批量导出
- 章节版本历史；书籍多版本与多语言
- 标签体系与热门检索、全文搜索（书籍与章节，支持本书内检索）

**阅读体验**
- SSR 公开页：首页 / 发现 / 书籍 / 章节 / 用户主页
- 阅读进度：跨书「我在读」、滚动位置续读、累计时长、连续打卡与每日目标
- 章节笔记标注、字号记忆、目录进度标记

**协作与社区**
- 书籍协作者（editor / viewer）、私有书共享
- 多级评论与权限、点赞与收藏
- 站内通知（评论/点赞/协作/升级）+ SSE 实时推送 + 导航铃铛

**账户与安全**
- 注册/登录、第三方登录（GitHub / Google / GitLab，可同时绑定多个）
- 两步验证（TOTP + 备份码）与敏感操作二次认证
- 登录失败锁定、密码策略、验证码、找回密码
- 个人资料、主题设置、邀请码、账号自助注销（管理员可配置冷静期）

**管理后台**
- 图形化安装向导（数据库 → 站点 → 管理员）
- 站点设置（名称/描述/Logo/Favicon/关键词/页脚/备案、全站公告）
- 存储驱动（本地磁盘 / 七牛云对象存储）、邮件（SMTP / 日志）
- 用户与内容管理、审计日志、限流、回收站、一键在线升级

**多端客户端**
- **Web**：Next.js 14 + TypeScript + Tailwind，服务端渲染
- **桌面端**：Tauri 2（macOS / Windows / Linux），多服务器切换、应用内 OAuth
- **Android**：Kotlin + Jetpack Compose，登录、搜索、阅读、离线缓存

## 快速开始

### Docker 部署（推荐）

```bash
# 使用官方发布的镜像（GitHub Packages / GHCR，随每个 v* 版本自动发布 amd64/arm64）
docker run -d --name knowforge -p 6969:6969 -v knowforge-data:/data \
  ghcr.io/devlive-community/knowforge:latest

# 或用 Docker Compose 从源码本地构建
docker compose up -d --build
```

启动后访问 `http://<主机>:6969/install` 完成安装向导。数据（数据库、上传、配置）都在容器内 `/data`（对应数据卷 `knowforge-data`）。

- 端口：`KNOWFORGE_PORT`（默认 `6969`）
- 数据目录：容器内固定为 `/data`，挂载数据卷或宿主目录持久化
- 受信代理：经 nginx / 网关部署时设 `KNOWFORGE_TRUSTED_PROXIES` 为代理 IP/CIDR（逗号分隔）
- 外接数据库：安装向导中选择 MySQL / PostgreSQL 并填写连接信息即可
- 升级：拉取新镜像重建容器（`docker compose pull && docker compose up -d`）
- 镜像标签：`latest` 及具体版本（如 `ghcr.io/devlive-community/knowforge:1.2.3`、`1.2`）

### 从源码构建

```bash
make build          # bin/knowforge-server（内嵌 Next.js SSR + Node.js 24）
make test           # 与 CI 一致的质量门禁（vet/test/tsc/lint）
```

### 本地开发

```bash
make dev-server     # Go API（:6969，数据写入 server/data）
make dev-web        # Next.js SSR（:3000，直连 :6969）
```

## 技术栈与架构

- **服务端**：Go + Gin + GORM，REST API（`/api/v1/*`），JWT + bcrypt，健康检查与在线升级
- **数据库**：SQLite（默认零配置）/ MySQL / PostgreSQL，安装时选择，GORM 幂等自动迁移
- **前端**：Next.js 14 + TypeScript + Tailwind，SSR standalone，SEO 一等公民
- **打包**：前端与 Node.js 运行时内嵌进 Go 二进制，单文件交付
- **CI/CD**：GitHub Actions 质量门禁 → `dev` 分支自动部署 → `v*` 标签发布多架构二进制与 Docker 镜像

```
knowforge/
├── server/               # Go 服务端（单文件二进制，内嵌前端产物）
│   └── internal/
│       ├── app/          # HTTP 路由、处理器、内嵌 Web 运行时管理
│       ├── auth/         # JWT 签发与校验
│       ├── config/       # 安装配置持久化（data/config.json）
│       ├── database/     # SQLite / MySQL / PostgreSQL 多数据库支持
│       ├── models/       # GORM 模型与自动迁移
│       └── plugins/      # 特性插件（标签、版本、多语言、内容采集等）
├── app/
│   ├── web/              # Next.js 14 + TypeScript + Tailwind 前端（SSR）
│   ├── desktop/          # Tauri 2 桌面客户端（macOS / Windows / Linux）
│   └── android/          # Android 客户端（Kotlin + Jetpack Compose）
├── deploy/               # systemd unit / nginx 配置 / sudoers / 发布脚本
├── Dockerfile · docker-compose.yml
└── Makefile
```

## 部署与发布

### 生产部署（CI 自动化）

`push` 到 `dev` 分支即触发 [deploy.yml](.github/workflows/deploy.yml)：前端 SSR、Node.js 24 与 Go API 构建成一个二进制，经 scp 上传，原子切换 `releases/<sha>` 并重启 systemd 服务，健康检查通过后清理旧版本。

```
nginx (:80/:443)
 └─ /* → knowforge-api（Go, 127.0.0.1:6969）
          └─ 托管内嵌 Next.js + Node.js 24（内部 127.0.0.1:6900）
```

### 发布新版本

```bash
deploy/release.sh        # 为 handler_setup.go 中的当前版本打 v 标签并推送，发布后自动开启并提交下一版本（不推送）
deploy/new-version.sh    # 手动开启新版本：统一 bump 各处版本号并生成 CHANGELOG 草稿
```

[release.yml](.github/workflows/release.yml) 会构建包含完整 Web 运行时的多架构单文件二进制、发布 GitHub Release，并推送多架构 Docker 镜像到 GitHub Packages（GHCR）。线上管理员也可在「系统管理」页一键在线升级。

## 客户端

**桌面端**

```bash
cd app/desktop && pnpm install
pnpm dev            # 开发模式
pnpm build          # 打包安装程序
```

**Android**

```bash
cd app/android && ./gradlew assembleDebug
```

两端首次启动均填写 KnowForge 服务器地址后接入，地址会被记住。

## 数据迁移

从旧版（Node.js/Express/MySQL）迁移历史数据：

```bash
go run ./cmd/migrate-legacy -legacy-dsn "user:pass@tcp(127.0.0.1:3306)/knowforge" [-dry-run]
```

## API

REST API 挂载在 `/api/v1/*`，覆盖安装向导、认证、书籍与文档、搜索、评论、通知、协作、OAuth、导入导出、站点/存储/邮件设置等。完整端点、请求/响应与权限清单见 [docs/api.md](docs/api.md)。

## 鸣谢

[JetBrains](https://www.jetbrains.com/) · [Tailwind CSS](https://tailwindcss.com/)
