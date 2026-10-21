# VideoFeed · Go 高并发视频流系统

一个从零实现的短视频 Feed 流后端，覆盖 **发布 → 关注流 → 发现页 → 详情 → 热榜** 完整业务闭环，针对 Feed 流三大核心难题（时间线、缓存治理、增量同步）做了实战级设计，最终以 Docker Compose 三件套容器化交付。

---

## 核心亮点

| # | 亮点 | 解决的问题 |
|---|---|---|
| 1 | **关注流冷热分离**：Redis ZSet 热区（每用户 200 条）+ MySQL 冷区 | 大 V 粉丝看关注流不能全量打 MySQL |
| 2 | **双锚增量**：`since` 拉新 + `cursor` 翻旧 | 刷新不再重复、翻页不丢不重（B 站式） |
| 3 | **缓存三件套**：空值缓存 / singleflight / TTL 抖动 | 缓存穿透、击穿、雪崩 |
| 4 | **令牌桶限流**（每 IP 10/s，突发 20） | 防脚本刷接口，实测放行 20 / 拒绝 10 |
| 5 | **容器化**：Dockerfile 多阶段 + Compose 三服务 | 一条命令拉起 MySQL+Redis+应用 |

## 技术栈

```
Go 1.26  ·  Gin v1.12  ·  GORM v1.31 + MySQL 8.0  ·  go-redis v9 + Redis 7
JWT (HS256, 24h)  ·  singleflight (golang.org/x/sync)  ·  令牌桶 (golang.org/x/time/rate)
Docker + Docker Compose
```

---

## 架构设计

### 整体链路

```
浏览器 / 客户端
     │  HTTP (8081)
     ▼
┌─ middleware ──────────────────────────────┐
│ ① 令牌桶限流 (每IP 10/s, 突发20)          │
│ ② JWT 鉴权 (Auth 必须 / AuthOptional 软鉴权)│
└──────────────┬────────────────────────────┘
               ▼
┌─ handlers ────────────────────────────────┐
│ 关注流 GetFeed  │ 发现页 GetVideoList     │
│ 详情 GetDetail  │ 热榜 GetHot             │
│ 发布 Create     │ 关注 Toggle             │
└───┬──────────┬──────────┬─────────────┬───┘
    ▼          ▼          ▼             ▼
 Redis       Redis      MySQL         MySQL
 ZSet 热区    缓存/热榜   冷区查询      逻辑外键回填
```

### Redis 数据结构设计（全项目只用 4 种结构）

| Key | 类型 | Score 含义 | 用途 |
|---|---|---|---|
| `feed:hot:{uid}` | ZSet | 视频 `created_ts`（Unix 毫秒） | 关注流热区：发布即 fanout，留最新 200 条 |
| `all_videos` | ZSet | 视频 `created_ts` | 发现页全站时间线（游标翻页） |
| `hot_videos` | ZSet | 播放量 | 热榜：`ZIncrBy` 原子自增 |
| `video:info:{id}` / `video:null:{id}` | String | — | 详情缓存 / 空值防穿透标记 |

### 关注流：冷热分离 + 双锚（核心设计）

**为什么用 ZSet 而不用 List？**
1. **List 没有时间戳**：无法按"某时间之后"定位，做不到游标翻页；
2. **List 不幂等**：重复插入会重复，ZSet 按成员去重；
3. **ZSet score 可加权**：未来做"加权推荐流"（点赞/完播加权）不用换结构。

**冷热分离**：发布时 fanout 写入关注者热区（ZSet，`ZRemRangeByRank` 只留 200 条）；读关注流先走热区，不足 20 条再查 MySQL 冷区（`created_ts < 热区最老` 二分回填）。

**双锚增量（B 站式拉新翻旧）**：
- `since`：刷新时拿"当前列表最新一条的 created_ts"，`created_ts > since` 拉到新视频，插到顶部；
- `cursor`：向下滚动时拿"当前列表最老一条的 created_ts"，`created_ts < cursor` 翻旧；
- 两个锚点互不干扰：刷新不会吞掉还没看的旧内容，翻旧不会重复已看的新内容。
- 关键取舍：用**时间戳绝对坐标**而非页码相对坐标——新增内容不会让页码偏移导致重复/丢失。

### 发现页：全站一条时间线

全站一个 ZSet，`ZRevRangeWithScores`（cursor=0）或 `ZRevRangeByScore` 开区间（`Max="(cursor"`）取下一页 → 回表 MySQL `WHERE id IN (?)` → 按 score 重排（MySQL IN 不保证顺序）→ 返回 `next_cursor`。无页码、无重复。

### 详情接口：缓存三件套

```
请求 /api/videos/:id
  ① 查 video:info:{id}  → 命中：ZScore 取实时播放量，返回
  ② 查 video:null:{id}  → 命中：直接 404（防穿透，DB 无此记录不再打 DB）
  ③ singleflight 合并回源（同 id 并发只放 1 个查 DB）
     查 DB → 回填缓存（TTL 300s + rand(0,60)s 抖动，防雪崩）
     DB 无 → 写空值标记（60s）
  ④ 播放量是高频写，不进缓存：ZIncrBy 原子自增，读取时 ZScore 实时取
```

### 其他设计点

- **播放量自增与缓存拷贝分离**：singleflight 返回共享指针，自增是写操作不能合并，先值拷贝再 `UpdateColumn("views", views+1)`（不会把所有并发打到同一个指针上）。
- **复合唯一索引防重复点赞/关注**：`(user_id, follow_user_id)` 唯一索引，重复关注第二次 `Create` 撞 MySQL 错误 1062 → 走取关分支。数据库层强制幂等，不用先查再写，天然防并发双插。
- **逻辑外键**：所有 `user_id` 关联在代码层回填（`WHERE id IN (?)` 批量查 + map 组装），不建物理外键——性能好、灵活，数据一致性由应用层保证。
- **created_ts 统一毫秒时间戳**：不依赖 DATETIME（时区敏感、秒级精度不够），全链路排序/游标/边界都走 `created_ts`（`gorm:"autoCreateTime:milli"`）。

---

## 快速开始

前置：Docker Desktop。

```bash
# 一键启动三件套（自动构建镜像）
docker compose up -d --build

# 打开前端
open http://localhost:8081
```

| 命令 | 作用 |
|---|---|
| `docker compose up -d --build` | 启动/重建（改代码后用它） |
| `docker compose down` | 停止（数据保留） |
| `docker compose down -v` | 停止并清数据（危险） |
| `docker compose logs -f app` | 应用日志 |
| `docker exec -it video-mysql mysql -uroot -p123456` | 进 MySQL |
| `docker exec -it video-redis redis-cli` | 进 Redis |

容器架构：`video-feed-app`(8081) 通过 compose 内部 DNS 以服务名 `mysql`/`redis` 连接数据库；MySQL 挂外部命名卷 `blog_mysql_data`（数据持久化，容器销毁不丢）。

## 本地开发模式

```bash
docker compose up -d mysql redis   # 只起数据库
go run .                            # 本机编译，连 127.0.0.1:3306/6379
```

## 配置（环境变量，均有默认值）

| 变量 | 默认值 | 说明 |
|---|---|---|
| `VIDEO_SERVER_HOST` / `VIDEO_SERVER_PORT` | `0.0.0.0` / `8081` | 监听地址 |
| `VIDEO_DB_USER` / `VIDEO_DB_PASSWORD` | `root` / `123456` | MySQL 账号 |
| `VIDEO_DB_HOST` / `VIDEO_DB_PORT` | `127.0.0.1` / `3306` | 容器内填 `mysql` |
| `VIDEO_DB_NAME` | `video_feed` | 库名 |
| `VIDEO_REDIS_HOST` / `VIDEO_REDIS_PORT` | `127.0.0.1` / `6379` | 容器内填 `redis` |
| `VIDEO_JWT_SECRET` | `video-feed-secret` | JWT 签名密钥 |
| `VIDEO_LLM_*` | DeepSeek 占位 | 后续 AI 能力预留 |

## API 一览（统一 `{code, msg, data}`，code=0 成功）

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| POST | `/api/users/register` | - | 注册（bcrypt） |
| POST | `/api/users/login` | - | 登录（签发 JWT） |
| GET | `/api/videos?cursor=&limit=` | 可选 | 发现页游标时间线 |
| GET | `/api/videos/:id` | 可选 | 详情（缓存三件套） |
| POST | `/api/videos` | 必须 | 发布（body: video_url） |
| GET | `/api/feed?cursor=&limit=&since=` | 必须 | 关注流（拉新翻旧） |
| GET | `/api/hot?limit=` | - | 热榜（播放量 TopN） |
| POST | `/api/follow/:user_id` | 必须 | 关注/取关 toggle |
| GET | `/api/following` / `/api/followers` | 必须 | 关注/粉丝列表 |
| GET | `/ping` | - | 健康检查 |

## 实测验证（诚实数据，可复现）

- **限流**：Python 并发 30 请求 → 恰好放行 20（桶容量）、拒绝 10（429），随后按 10/s 恢复；
- **缓存**：详情二次命中直接返回（播放量经 ZScore 实时同步）；请求不存在的 id 返回 404 且不穿透 DB（空值标记生效）；
- **发现页游标**：13 条数据 3 页翻完，不重不漏（`[27..22] → [21..6] → [5]`，`next_cursor=0` 到底）；
- **热区回填**：`feed:hot:{uid}` 保留最新 200 条，倒序毫秒精确。

## 目录结构

```
video-feed/
├── main.go              # 入口：配置加载、连库、AutoMigrate、Redis 初始化、路由
├── config/              # 环境变量加载（VIDEO_ 前缀）
├── database/            # MySQL/Redis 连接 + Redis 业务封装（热榜/时间线/缓存/回填）
├── models/              # GORM 模型（User/Video/Follow，created_ts 毫秒时间戳）
├── handlers/            # HTTP 处理（users/videos/feed/follow）
├── middleware/          # JWT 鉴权 + AuthOptional 软鉴权 + 令牌桶限流
├── routes/              # 路由注册
├── utils/               # JWT 签发/解析
├── static/              # 前端单页 + 本地素材（进镜像）
├── Dockerfile           # 多阶段构建（builder 编译 → alpine 运行）
└── docker-compose.yml   # MySQL + Redis + App 三服务编排
```

## Roadmap

- [ ] 消息队列异步 fanout（大 V 发布不阻塞）
- [ ] 对象存储文件上传（OSS/S3 + CDN）
- [ ] K8s 部署（StatefulSet + ConfigMap）
- [ ] 可观测性（Prometheus + Grafana + 结构化日志）
- [ ] 加权推荐流（ZSet score 融合点赞/完播）
