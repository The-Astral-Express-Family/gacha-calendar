# gacha-calendar 部署

单二进制部署：前端构建产物经 `go:embed` 嵌入 Go 二进制，nginx 只做 TLS 终止与反代。

## 布局

| 路径 | 内容 |
|---|---|
| `/opt/gacha-calendar/gacha-calendar` | 服务二进制（含嵌入前端） |
| `/etc/gacha-calendar/gacha-calendar.env` | EnvironmentFile（root:root 0600，含 ADMIN_TOKEN） |
| `/var/lib/gacha-calendar/` | SQLite（WAL）与 covers/、backups/，属主 gacha |
| `/etc/nginx/sites-available/gacha-origin.modenc.top` | nginx 站点（ccg-origin 通配符证书） |
| systemd | `gacha-calendar.service` + `gacha-calendar-backup.{service,timer}` |

## 采集调度（自适应）

调度按"事件窗口"分级，无需手工配置；如 env 文件里显式设置了 `REFRESH_INTERVAL=6h`，
建议删除以启用新的 3h 默认值：

| 变量 | 默认 | 含义 |
|---|---|---|
| `REFRESH_INTERVAL` | `3h` | 平静期每游戏的基础轮询间隔 |
| `WATCH_INTERVAL` | `30m` | 事件窗口内（预告发布前 5–7 天至事件次日）的轮询间隔 |
| `PRIME_INTERVAL` | `15m` | 事件窗口内且处于发布黄金时段（07:00–14:00 / 18:00–23:00 国服时间）的轮询间隔 |
| `TICK_INTERVAL` | `5m` | 调度器唤醒粒度 |
| `BOOTSTRAP_ON_START` | `true` | 版本历史不足时启动自动回填（历史公告 → 校准统计） |

首次部署（或库太薄）时服务会自动执行引导回填：拉取各官号约 18 个月的更新公告重建
`versions` 表、回填历史前瞻时间，并推导每游戏的真实节奏（周期天数、更新星期、前瞻
提前天数等）供推测使用。可用 `POST /api/admin/bootstrap` 手动重跑，进度看
`/api/meta` 的 `bootstrap` 字段。

## 域名与缓存

- 源站：`gacha-origin.modenc.top`（CDN 回源）
- CDN：`gacha.modenc.top`；**不缓存** `/api`、`/api/*`、`OPTIONS`、`/healthz`，其余路径缓存
- 应用自身输出 `Cache-Control`：`/api`、`/healthz` → `no-store`；`/assets/*` → `public, max-age=31536000, immutable`；`index.html` → `public, max-age=60, must-revalidate`；`/covers/*`、`/fallback/*` → `public, max-age=86400`。nginx 原样透传。
- 发布新版本后需在 CDN 控制台刷新 `index.html`（哈希资源名变更，无需刷新）。

## 证书

`/etc/nginx/certs/ccg-origin.modenc.top/`（ZeroSSL，`*.modenc.top`）。
部署前已验证：SAN 覆盖、证书链（3 张）、私钥模数匹配、有效期。

## 构建

推送到 `main` 后 CI（`.github/workflows/build-linux.yml`）自动完成完整构建
（前端 → embed → Linux amd64 二进制）并覆盖发布到滚动 Release（tag `build`，
即 releases/latest）。也可在 Actions 页面对任意历史 commit 手动触发
`workflow_dispatch` 来重发布旧版本（用于回滚）。

本地构建仅在 CI 不可用时需要：

```bash
./build.sh                                    # 本地完整构建
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -ldflags "-X main.version=<ver>" \
  -o backend/gacha-calendar-linux-amd64 ./backend/cmd/gacha-calendar
```

## 首次部署（root@modenc.top）

```bash
# 用户与目录
useradd --system --home /var/lib/gacha-calendar --shell /usr/sbin/nologin gacha
mkdir -p /opt/gacha-calendar /var/lib/gacha-calendar/backups
# 上传二进制与配置（见下方 scp）
install -m 0755 /tmp/gacha-calendar /opt/gacha-calendar/gacha-calendar
install -m 0600 /tmp/gacha-calendar.env /etc/gacha-calendar/gacha-calendar.env
chown -R gacha:gacha /var/lib/gacha-calendar

# systemd
install -m 0644 deploy/gacha-calendar.service /etc/systemd/system/
install -m 0644 deploy/gacha-calendar-backup.service /etc/systemd/system/
install -m 0644 deploy/gacha-calendar-backup.timer /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now gacha-calendar.service gacha-calendar-backup.timer

# nginx
install -m 0644 deploy/nginx-gacha-origin.modenc.top.conf \
  /etc/nginx/sites-available/gacha-origin.modenc.top
ln -s /etc/nginx/sites-available/gacha-origin.modenc.top /etc/nginx/sites-enabled/
nginx -t && systemctl reload nginx
```

## 升级

从 CI Release 拉取（固定 URL 始终指向最新构建，仓库公开无需 token）：

```bash
ssh root@modenc.top
cd /tmp
base=https://github.com/The-Astral-Express-Family/gacha-calendar/releases/latest/download
curl -fLO "$base/gacha-calendar-linux-amd64"
curl -fLO "$base/gacha-calendar-linux-amd64.sha256"
sha256sum -c gacha-calendar-linux-amd64.sha256
install -m 0755 gacha-calendar-linux-amd64 /opt/gacha-calendar/gacha-calendar
systemctl restart gacha-calendar
```

无 CI 时（或热修复未推送）仍可本地 scp：

```bash
scp backend/gacha-calendar-linux-amd64 root@modenc.top:/opt/gacha-calendar/gacha-calendar.new
ssh root@modenc.top 'cd /opt/gacha-calendar && mv gacha-calendar.new gacha-calendar && systemctl restart gacha-calendar'
```

重启后观察：`journalctl -u gacha-calendar -f`、`curl -s localhost:8790/healthz`。

## 备份

- `gacha-calendar-backup.timer` 每天 04:30（随机延迟 10 分钟内）通过
  `VACUUM INTO` 写快照到 `/var/lib/gacha-calendar/backups/gacha-<日期>.db`，保留最近 14 份。
- SQLite 已开启 WAL + `busy_timeout=5000`，单写连接（GORM pool=1）。

## ADMIN_TOKEN

`/api/admin/refresh` 与 `/api/admin/overrides/*` 需要 `Authorization: Bearer <token>`；
生成方式：`openssl rand -hex 24`。dsh 插件端通过
`globalThis.__GACHA_CALENDAR__ = { baseUrl, token }` 注入。
