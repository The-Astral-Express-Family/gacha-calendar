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

## 域名与缓存

- 源站：`gacha-origin.modenc.top`（CDN 回源）
- CDN：`gacha.modenc.top`；**不缓存** `/api`、`/api/*`、`OPTIONS`、`/healthz`，其余路径缓存
- 应用自身输出 `Cache-Control`：`/api`、`/healthz` → `no-store`；`/assets/*` → `public, max-age=31536000, immutable`；`index.html` → `public, max-age=60, must-revalidate`；`/covers/*`、`/fallback/*` → `public, max-age=86400`。nginx 原样透传。
- 发布新版本后需在 CDN 控制台刷新 `index.html`（哈希资源名变更，无需刷新）。

## 证书

`/etc/nginx/certs/ccg-origin.modenc.top/`（ZeroSSL，`*.modenc.top`）。
部署前已验证：SAN 覆盖、证书链（3 张）、私钥模数匹配、有效期。

## 构建

在开发机仓库根目录：

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
