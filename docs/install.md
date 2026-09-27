# KnowForge 安装指南

KnowForge 编译为**单个二进制文件**，内嵌 Next.js SSR 与 Node.js 24 运行时，默认零配置使用 SQLite，启动后通过图形化安装向导完成初始化。本文覆盖四种安装方式：Docker（推荐）、二进制发布包、源码构建、开发模式，以及生产环境的 Nginx / systemd 配置。

---

## 目录

- [环境要求](#环境要求)
- [方式一：Docker Compose（推荐）](#方式一docker-compose推荐)
- [方式二：Docker 单容器](#方式二docker-单容器)
- [方式三：二进制发布包](#方式三二进制发布包)
- [方式四：源码构建](#方式四源码构建)
- [初始化安装向导](#初始化安装向导)
- [配置项参考](#配置项参考)
- [数据库切换](#数据库切换)
- [生产部署（systemd + Nginx + TLS）](#生产部署systemd--nginx--tls)
- [多实例部署](#多实例部署)
- [升级](#升级)
- [故障排查](#故障排查)

---

## 环境要求

| 项目 | 要求 |
|------|------|
| 操作系统 | Linux（amd64 / arm64）、macOS、Windows |
| 内存 | ≥ 512 MB（内嵌 Node.js SSR 运行时） |
| 磁盘 | ≥ 200 MB + 数据空间（数据库与上传文件） |
| 端口 | 默认 `6969`，对外提供服务 |

- **Docker 方式**：Docker 20+ 与 Docker Compose v2，无其他依赖
- **二进制方式**：无任何运行时依赖（Node.js 已内嵌）
- **源码构建**：Go 1.25+、Node.js **24.20.0**（精确版本，构建脚本会校验）、pnpm 9、make

---

## 方式一：Docker Compose（推荐）

默认使用零配置 SQLite，数据（数据库、上传文件、配置）持久化在命名卷 `knowforge-data`。

```bash
git clone https://github.com/devlive-community/knowforge.git
cd knowforge
docker compose up -d
```

查看日志确认启动完成：

```bash
docker compose logs -f knowforge
```

浏览器访问 `http://<主机IP>:6969/install` 完成初始化（见[安装向导](#初始化安装向导)）。

**使用官方预构建镜像**：编辑 `docker-compose.yml`，删除 `build:` 段并将 `image:` 改为：

```yaml
    image: ghcr.io/devlive-community/knowforge:latest
```

---

## 方式二：Docker 单容器

```bash
docker run -d \
  --name knowforge \
  --restart unless-stopped \
  -p 6969:6969 \
  -v knowforge-data:/data \
  ghcr.io/devlive-community/knowforge:latest
```

数据全部落在 `/data` 卷中；升级时拉取新镜像重建容器即可，数据不丢失。

---

## 方式三：二进制发布包

从 [Releases](https://github.com/devlive-community/knowforge/releases) 下载对应平台的压缩包（`knowforge-server-linux-amd64` / `arm64` / macOS / Windows），解压后直接运行：

```bash
chmod +x knowforge-server
./knowforge-server -port 6969
```

默认数据目录为当前目录下的 `./data`，通过环境变量 `KNOWFORGE_DATA` 指定持久化路径：

```bash
KNOWFORGE_DATA=/var/lib/knowforge ./knowforge-server -port 6969
```

---

## 方式四：源码构建

源码构建会将 Next.js SSR 产物与 Node.js 24 运行时打包进单个 Go 二进制。

```bash
git clone https://github.com/devlive-community/knowforge.git
cd knowforge

# 1. 安装前端依赖（要求 Node.js 24.20.0）
make web-install

# 2. 构建本机平台的单个二进制
make build
```

产物为 `bin/knowforge-server`。交叉编译 Linux amd64 发布包用 `make release-linux`。

启动：

```bash
KNOWFORGE_DATA=./data ./bin/knowforge-server -port 6969
```

---

## 初始化安装向导

首次启动后访问 `http://<主机>:6969/install`，向导分两步：

1. **数据库配置**
   - **SQLite**（默认）：零配置，指定数据库文件路径（留空使用默认 `data/knowforge.db`）
   - **MySQL / PostgreSQL**：填写主机、端口、库名、用户名、密码
2. **管理员账户**：设置管理员用户名、邮箱与密码（密码至少 6 位）

完成后自动跳转登录页。用刚才创建的管理员账户登录，即可进入管理后台进行站点设置（站点名称、Logo、站点访问地址等）、邮件服务（SMTP）与用户管理等配置。

> 安装向导只在系统未初始化时可用；重复访问 `/install` 会跳转到首页。

---

## 配置项参考

配置通过环境变量注入，持久化配置保存在数据目录的 `config.json`。

| 环境变量 | 默认值 | 说明 |
|---------|--------|------|
| `KNOWFORGE_DATA` | `./data` | 数据目录（数据库、上传文件、config.json） |
| `KNOWFORGE_PORT` | `6969` | 服务监听端口（也可用 `-port` 参数） |
| `KNOWFORGE_WEB_PORT` | `6900` | 内嵌 Next.js SSR 内部端口，一般无需配置 |
| `KNOWFORGE_TRUSTED_PROXIES` | 空 | 受信任反向代理 IP 列表（逗号分隔），用于解析真实客户端 IP；本机 nginx 填 `127.0.0.1,::1` |
| `KNOWFORGE_SITE_URL` | 空 | 站点对外地址（如 `https://kb.example.com`），用于邮件链接与 sitemap 生成；nginx 已设置 `X-Forwarded-Host` 时可不配 |
| `KNOWFORGE_STATIC_ROOT` | 内置 | Next.js 静态资源目录；跨版本部署时指向共享目录可避免旧页面 404 |
| `KNOWFORGE_UPGRADE` | 空 | 在线升级开关，设为 `enabled` 启用 |
| `KNOWFORGE_UPSTREAM_REPO` | `devlive-community/knowforge` | 在线升级源仓库 |

数据目录结构：

```
data/
├── config.json        # 持久化配置（数据库、密钥等）
├── knowforge.db      # SQLite 数据库（使用 SQLite 时）
├── uploads/           # 上传文件
└── sitemaps/          # 后台定时生成的 sitemap 静态文件
```

---

## 数据库切换

安装向导中可直接选择 SQLite / MySQL / PostgreSQL。三种数据库均由 GORM 支持自动迁移，无需手工建表。

**MySQL 连接示例**（安装向导中填写）：

| 字段 | 示例值 |
|------|--------|
| 主机 | `127.0.0.1` |
| 端口 | `3306` |
| 数据库 | `knowforge` |
| 用户名 / 密码 | 自行创建 |

**PostgreSQL**：端口默认 `5432`，其余同理。

> 建议提前在数据库中创建库并授权：`CREATE DATABASE knowforge CHARACTER SET utf8mb4;`（MySQL）或 `CREATE DATABASE knowforge;`（PostgreSQL）。

---

## 生产部署（systemd + Nginx + TLS）

参考 `deploy/` 目录下的示例文件。

### 1. systemd 服务

```bash
# 放置二进制
sudo mkdir -p /var/www/knowforge/current
sudo cp bin/knowforge-server /var/www/knowforge/current/

# 环境变量文件
sudo mkdir -p /etc/knowforge
sudo cp deploy/knowforge.env.example /etc/knowforge/knowforge.env
sudo nano /etc/knowforge/knowforge.env   # 按需修改

# 数据目录授权（服务以 www-data 运行）
sudo mkdir -p /var/lib/knowforge
sudo chown www-data:www-data /var/lib/knowforge

# 注册服务
sudo cp deploy/knowforge-api.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now knowforge
```

### 2. Nginx 反向代理

```bash
sudo cp deploy/nginx.conf.example /etc/nginx/sites-available/knowforge
sudo ln -s /etc/nginx/sites-available/knowforge /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
```

### 3. TLS 证书

DNS 解析指向服务器后，用 certbot 一键签发并配置 80→443 跳转：

```bash
sudo certbot --nginx -d kb.example.com
```

证书自动续期由 `certbot.timer` 负责。

### 4. 后台站点设置

登录管理后台完成两件事，否则部分功能不完整：

- **站点设置**：填写「站点访问地址」（`https://kb.example.com`）——邮件中的链接与每日自动生成的 `sitemap.xml` 都依赖它
- **邮件服务**：配置 SMTP 发信地址、端口、账号与密码（用于注册验证、密码重置等邮件）

---

## 多实例部署

访问量较大或需要高可用时，可以在负载均衡后运行多个 KnowForge 服务实例。各实例通过共享数据库自动协作：

- 站内通知、问答、写作助手、整本翻译、章节导读、审核队列、插件安装日志等实时推送，送达连在任何实例上的页面；
- 问答、写作助手、整本翻译等后台任务可从任何实例取消或暂停；中途打开的页面会从执行任务的实例补齐已生成的内容；
- 后台任务队列由所有实例共同处理，长任务有心跳，只有执行它的实例退出后才会由其他实例接手；
- 周期巡检（到期提醒、索引补建、中断任务处理等）只由一个实例执行，该实例下线后由其他实例接手；
- 插件启用/禁用后所有实例同步权限，缺少插件文件（如 PDF 导出组件）的实例会自动补齐。

部署要求：

| 要求 | 说明 |
| --- | --- |
| 数据库 | PostgreSQL 或 MySQL（SQLite 不支持多个服务同时写入） |
| 数据目录 | 所有实例挂载同一个共享存储卷（NFS、云盘文件存储等）作为数据目录，保存上传文件、导出结果、导入源文件与插件文件；上传也可在后台改用对象存储 |
| 配置 | 所有实例使用相同的 `config.json`（同一数据库与 `secret`），否则登录状态与事件流凭证无法互认 |
| 负载均衡 | 不需要会话保持；事件流（SSE）连接可落在任意实例。代理需允许长连接：Nginx 默认遵循服务端返回的 `X-Accel-Buffering: no` 关闭缓冲，读超时需大于 25 秒的心跳间隔（默认 60 秒即可） |
| 升级 | 后台的「在线升级」只会升级处理该请求的实例；多实例请用镜像或发布流程逐台升级，保持各实例版本一致 |

Nginx 负载均衡示例：

```nginx
upstream knowforge {
    server 10.0.0.11:6969;
    server 10.0.0.12:6969;
}
server {
    # …其余配置同单实例示例，把 proxy_pass 改为 http://knowforge
    location / {
        proxy_pass http://knowforge;
        proxy_http_version 1.1;
        proxy_read_timeout 120s;
        # …proxy_set_header 同单实例示例
    }
}
```

部署后在管理后台「系统概览 → 服务实例」查看在线实例；数据库不支持、数据目录未共享或版本不一致时会给出提示。

## 升级

- **Docker**：`docker compose pull && docker compose up -d`，数据在卷中不受影响
- **在线升级**（需 `KNOWFORGE_UPGRADE=enabled`）：管理后台一键升级，自动下载校验、替换二进制、重启服务，失败自动回滚
- **二进制**：下载新版本替换 `knowforge-server` 后 `sudo systemctl restart knowforge`
- **数据库迁移**：升级后首次启动自动执行，无需手工操作

---

## 故障排查

| 现象 | 排查方向 |
|------|---------|
| 访问 `/install` 跳转首页 | 系统已初始化过；如需重装，清空数据目录（会丢失全部数据） |
| 端口被占用 | `KNOWFORGE_PORT` 或 `-port` 参数换端口 |
| sitemap.xml 返回 404 | 未在「站点设置」配置站点访问地址，或后台任务尚未运行（每日一次；保存站点地址后会立即触发） |
| nginx 后客户端 IP 显示异常 | 设置 `KNOWFORGE_TRUSTED_PROXIES=127.0.0.1,::1` |
| 上传大文件失败 | 检查 nginx `client_max_body_size`（示例配置为 12m） |
| 容器健康检查失败 | `docker compose logs knowforge` 查看启动日志；首次启动需等待约 40 秒 |
| 升级后旧页面静态资源 404 | 配置 `KNOWFORGE_STATIC_ROOT` 指向跨版本共享的静态目录（见 `deploy/nginx.conf.example`） |

---

## 开发模式

前后端分离运行，代码改动即时生效：

```bash
# 终端 1：Go API（数据落在 ./data）
make dev-server

# 终端 2：Next.js 前端（连接本机 API）
make dev-web
```

前端开发服务地址 `http://localhost:3000`，API 直连 `http://localhost:6969`。

---

## 相关链接

- 项目主页：<https://github.com/devlive-community/knowforge>
- API 文档：[`docs/api.md`](api.md)
- 旧数据迁移：[`docs/migrate-legacy.md`](migrate-legacy.md)
