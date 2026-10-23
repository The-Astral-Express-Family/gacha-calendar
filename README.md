# gacha-calendar

二游版本更新与前瞻直播时间表服务：采集六款二游官方 B 站账号的版本公告，
推算/记录下一版本的**版本更新时间**与**前瞻直播时间**，提供网页可视化与公开 API。

跟踪游戏：原神 / 崩坏：星穹铁道 / 绝区零 / 鸣潮 / 明日方舟：终末地 / 异环。

## 架构

- **后端** `backend/`：Go + GORM + SQLite。定时采集 B 站官方专栏/合集/动态，
  纯规则解析公告中的维护时段与版本结束时间，推算下一版本计划并落库；
  同时保存原始公告（帖子）供 Agent 与前端查询。
- **前端** `frontend/`：Vite + TypeScript，构建产物嵌入 Go 二进制。
  `/` 提供页面，`/api` 提供接口，`/healthz` 健康检查。

## 时间口径

- 业务口径统一为国服时间 **Asia/Shanghai（UTC+8，无夏令时）**。
- 数据库与 API 一律存储/返回 **UTC RFC3339**；排序、分页、时间范围过滤按 UTC 比较。
- API 附带时区元数据；本地化展示由前端完成（支持本地时区/国服/UTC 切换）。

## 本地开发

```bash
# 后端（默认监听 127.0.0.1:8787，数据库 ./data/gacha.db）
cd backend && go run ./cmd/gacha-calendar

# 前端（Vite dev server，/api、/covers 等代理到后端）
# 代理目标读 API_ORIGIN，默认 http://127.0.0.1:8791；按上面默认端口起后端时需显式指定
cd frontend && pnpm install && API_ORIGIN=http://127.0.0.1:8787 pnpm dev

# 测试
cd backend && go test ./...

# 构建单二进制（前端 → 拷入 backend/web/assets/dist → go build 嵌入）
./build.sh    # Windows 用 build.ps1
```

## API 概览

| 路由 | 说明 |
|---|---|
| `GET /api/games` | 游戏列表与采集状态、主视觉图路径 |
| `GET /api/predictions` | 预测更新/前瞻时间分页列表（按游戏/时间范围筛选、排序） |
| `GET /api/predictions/:id` | 单条预测 |
| `GET /api/posts` | 原始公告/动态/视频分页列表（按游戏/时间范围筛选、排序） |
| `GET /api/posts/:id` | 单条原始公告（含正文） |
| `GET /api/meta` | 时区元数据与服务状态 |
| `GET /healthz` | 健康检查（不缓存） |
| `POST /api/admin/refresh` | 触发采集（Bearer Token） |
| `PUT/DELETE /api/admin/overrides/:game` | 官方校正覆盖层（Bearer Token） |

所有列表接口支持 `page` / `page_size` 并返回分页元信息。

## 部署

`deploy/` 内含 systemd 单元、Nginx 参考配置与部署说明（详见 `deploy/README.md`）；
线上源站 `gacha-origin.modenc.top`，CDN `gacha.modenc.top`（`/api`、`/healthz` 不缓存，其余缓存）。

> CDN 注意：`gacha.modenc.top` 需接入**标准 Web CDN/EdgeOne**（回源 `gacha-origin.modenc.top`）。
> 若 CNAME 指向腾讯云 VOD 点播加速域名（`*.tdnsvod1.cn`），所有普通路径会被节点以 514 直接拒绝。
