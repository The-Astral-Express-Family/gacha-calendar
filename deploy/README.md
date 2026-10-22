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

## 迁移服务器

既有凭证全部随文件走，正常情况**无需重新扫码登录**：

```bash
# 旧机：停服务，打包状态（DB、covers、会话文件都在 DATA_DIR）
systemctl stop gacha-calendar
tar -C /var/lib -czf /tmp/gacha-data.tgz gacha-calendar
scp /tmp/gacha-data.tgz root@<new-host>:/tmp/
scp /etc/gacha-calendar/gacha-calendar.env root@<new-host>:/tmp/   # ADMIN_TOKEN 等

# 新机：按「首次部署」建好用户/目录/systemd/nginx，然后还原状态
tar -C /var/lib -xzf /tmp/gacha-data.tgz
install -m 0600 /tmp/gacha-calendar.env /etc/gacha-calendar/gacha-calendar.env
chown -R gacha:gacha /var/lib/gacha-calendar
systemctl restart gacha-calendar
```

要点：

- `bili-session.json` 在 DATA_DIR 里，随包迁移即带走登录；服务启动即恢复会话，
  12 小时内的例行探测会自动续期。若迁移时会话已临近过期，可马上
  `POST /api/admin/bili/login/refresh` 主动换发一轮确认存活。
- 旧实例**尽快停用**：共用同一会话不会互相踢，但任何一方完成一次 cookie 轮换
  （confirm 步会吊销旧凭证）后，另一台内存里的旧 cookie 会立刻失效报 -101。
  迁移时以新机为准，旧机停服即可。
- 版本历史、校准统计、采集状态都在 SQLite 里，covers/ 里是本地化图片，全部
  随 DATA_DIR 迁移；env 文件单独拷。

## 备份

- `gacha-calendar-backup.timer` 每天 04:30（随机延迟 10 分钟内）通过
  `VACUUM INTO` 写快照到 `/var/lib/gacha-calendar/backups/gacha-<日期>.db`，保留最近 14 份。
- SQLite 已开启 WAL + `busy_timeout=5000`，单写连接（GORM pool=1）。

## ADMIN_TOKEN

`/api/admin/*`（refresh/bootstrap/overrides）需要 `Authorization: Bearer <token>`；
生成方式：`openssl rand -hex 24`。dsh 插件端通过
`globalThis.__GACHA_CALENDAR__ = { baseUrl, token }` 注入。

安全行为（2026-10 起）：

- token 为空时 admin 路由整体不挂载（请求 404，fail-closed），不会退化为无鉴权。
- token 校验为常数时间比较；同一客户端 IP 在 15 分钟内认证失败 5 次会被封禁
  15 分钟（期间即使 token 正确也返回 429）。认证成功即清零失败计数。
  误封（例如自己连续输错）等 15 分钟自动解封，或 `systemctl restart gacha-calendar`。
- 认证失败会记日志（含来源 IP，经 nginx 的 X-Real-IP 还原真实地址）。
- token 会进入 dsh 插件的浏览器 JS 上下文，存在被本机其他脚本读取的理论暴露面，
  建议**定期轮换**：改 `/etc/gacha-calendar/gacha-calendar.env` 中的 `ADMIN_TOKEN`
  → `systemctl restart gacha-calendar` → 同步更新 dsh 插件注入值。

## B 站扫码登录（采集会话）

登录后的 SESSDATA 拼进采集请求，可绕开动态流接口对机房 IP 的 -352 风控，
也能看到个人认证号（星铁/鸣潮）未登录时被登录墙挡住的动态。扫码流程
只有 generate/poll 两个 passport 接口，**不涉及任何人机验证**。

会话存 `$DATA_DIR/bili-session.json`（默认 `/opt/gacha-calendar/data/`），
0600、独立于 DB（不进 VACUUM 备份）。SESSDATA 名义有效期约半年，但服务内置
**自动续期**：启动约 1 分钟后做首次探测，之后每 12 小时 nav 校验一次会话；进入
最后 30 天窗口时自动走 passport 的 cookie 轮换（凭当前 SESSDATA+bili_jct 换发
新 cookie，无需扫码、无人机验证），新凭证写回会话文件并即时生效。正常运行情况
下**永远不需要重新扫码**；只有会话被登出/风控吊销（日志出现 "dead_session" 或
"session rejected (-101)"）才需重扫。

手动触发一次续期（迁移后确认、或后台续期报错后重试）：

- `POST /api/admin/bili/login/refresh` — 强制跑一轮 nav 校验 + passport 轮换，
  返回 `outcome`：`rotated`（已换发）/ `not_due`（有效期还长）/ `not_in_window`
  （B 站尚未开放续期窗口）/ `dead_session`（已失效，只能重扫）等

服务器上执行（推荐，SSH 里直接出二维码）。注意 SESSION_PATH 按 DATA_DIR
解析，务必在服务的工作目录下运行，或显式指定 `DATA_DIR=/opt/gacha-calendar/data`：

```sh
cd /opt/gacha-calendar
sudo -u gacha ./gacha-calendar bili-login   # 终端出二维码，手机 B 站 App 扫码确认
sudo systemctl restart gacha-calendar       # 服务启动时会加载会话并打日志
```

或走 admin API（SSH 端口转发后浏览器/脚本操作）：

- `POST /api/admin/bili/login` — 生成二维码（返回 qrcode_key）
- `GET  /api/admin/bili/login/qr.png` — 二维码图片
- `POST /api/admin/bili/login/poll` — 轮询状态（waiting/scanned/success/expired），
  success 时会话即刻生效，无需重启
- `GET  /api/admin/bili/login` — 会话状态（含 nav 实时校验 logged_in）
- `DELETE /api/admin/bili/login` — 登出并删除会话文件

注意：cookie 内容绝不经 API 返回；若账号在别处被登出或风控失效（接口报 -101），
采集会自动降级匿名继续，日志出现 "session rejected (-101)"，此时重新扫码。

