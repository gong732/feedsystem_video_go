# VideoFeed · Go 视频 Feed 流系统

基于 Go 从零实现的短视频 Feed 流后端，覆盖 **发布 → 关注流 → 发现页 → 详情 → 热榜** 完整业务闭环。针对 Feed 流三大核心难题（时间线、缓存治理、增量同步）做了实战级设计，以 Docker Compose 三件套容器化交付，一条命令拉起。

---

## 核心亮点

| # | 亮点 | 解决的问题 |
|---|---|---|
| 1 | **关注流冷热分离**：Redis ZSet 热区（每用户 200 条）+ MySQL 冷区 | 大 V 粉丝看关注流不能全量打 MySQL |
| 2 | **双锚增量**：`since` 拉新 + `cursor` 翻旧 | 刷新不重复、翻页不丢不重（B 站式） |
| 3 | **缓存三件套**：空值缓存 / singleflight / TTL 抖动 | 缓存穿透、击穿、雪崩 |
| 4 | **分级限流**：Redis 固定窗口计数 + 进程内令牌桶降级 | 防脚本刷接口，Redis 故障不连带服务 |
| 5 | **容器化**：Dockerfile 多阶段 + Compose 三服务 | 一条命令拉起 MySQL+Redis+应用 |

## 技术栈

```
Go 1.26  ·  Gin v1.12  ·  GORM v1.31 + MySQL 8.0  ·  go-redis v9 + Redis 7
JWT (HS256 + TokenVersion)  ·  bcrypt  ·  singleflight (golang.org/x/sync)
令牌桶 (golang.org/x/time/rate)  ·  Docker + Docker Compose  ·  GitHub Actions CI
```

## 功能总览

| 模块 | 功能 |
|---|---|
| 账号 | 注册/登录（bcrypt + JWT）、登出（TokenVersion 踢旧会话）、用户信息 |
| 视频 | 发布、发现页游标时间线、详情（缓存三件套） |
| 上传 | 视频/封面上传，每用户每日额度限制 |
| 点赞 | 点赞/取消 toggle、状态查询、复合唯一索引防重复 |
| 评论 | 发布/列表/删除、删除校验归属（越权防护） |
| 关注 | 关注/取关、粉丝/关注列表、是否关注 |
| Feed | 关注流（冷热分离 + 双锚） |
| 热榜 | 播放量 TopN（ZSet 原子自增） |

---

## 架构设计

### 整体链路

```
浏览器 / 客户端
     │  HTTP (8081)
     ▼
┌─ middleware ──────────────────────────────┐
│ ① 分级限流（全局 30/s + 分接口独立限额）    │
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
- **复合唯一索引防重复点赞/关注**：`(user_id, follow_user_id)` / `(user_id, video_id)` 唯一索引，重复操作第二次 `Create` 撞 MySQL 错误 1062 → 走取关/取消分支。数据库层强制幂等，不用先查再写，天然防并发双插。
- **逻辑外键**：所有 `user_id` 关联在代码层回填（`WHERE id IN (?)` 批量查 + map 组装），不建物理外键——性能好、灵活，数据一致性由应用层保证。
- **created_ts 统一毫秒时间戳**：不依赖 DATETIME（时区敏感、秒级精度不够），全链路排序/游标/边界都走 `created_ts`（`gorm:"autoCreateTime:milli"`）。

### 安全设计

- **JWT TokenVersion 踢旧会话**：登出时 `token_version + 1`，旧 token 立即失效——不需要引入 Redis 黑名单即可主动踢掉已签发 token。
- **防算法混淆攻击**：解析 token 时强制校验签名算法必须是 HMAC，拒绝 `none` / 非对称算法注入。
- **防 XFF 伪造绕过限流**：`SetTrustedProxies(nil)`，ClientIP 直接取 `RemoteAddr`，不信任任何 `X-Forwarded-For`。
- **越权防护**：评论删除前校验归属，非本人返回 403。
- **密码安全**：bcrypt 哈希存储，`json:"-"` 保证 password 永不序列化返回。

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
| `VIDEO_JWT_SECRET` | `video-feed-secret` | JWT 签名密钥（生产须改） |
| `VIDEO_JWT_EXPIRE_HOURS` | `24` | JWT 过期小时数 |
| `VIDEO_UPLOAD_DAILY_MB` | `2048` | 每用户每天上传总量（MB） |
| `VIDEO_LLM_*` | DeepSeek 占位 | 后续 AI 能力预留 |

## API 一览（统一 `{code, msg, data}`，code=0 成功）

### 账号 `/api/users`

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| POST | `/register` | - | 注册（bcrypt） |
| POST | `/login` | - | 登录（签发 JWT） |
| POST | `/logout` | 必须 | 登出（TokenVersion +1，旧 token 失效） |
| GET | `/:id` | 可选 | 用户信息 |

### 视频 `/api/videos`

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| GET | `/videos?cursor=&limit=` | 可选 | 发现页游标时间线 |
| GET | `/videos/:id` | 可选 | 详情（缓存三件套） |
| POST | `/videos` | 必须 | 发布（body: video_url） |
| POST | `/upload/video` | 必须 | 上传视频（限流 5 次/时/账号） |
| POST | `/upload/cover` | 必须 | 上传封面 |

### 点赞 `/api/videos/:id`

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| POST | `/videos/:id/like` | 必须 | 点赞/取消 toggle |
| GET | `/videos/:id/like` | 可选 | 点赞状态 |

### 评论 `/api`

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| POST | `/videos/:id/comments` | 必须 | 发布评论 |
| GET | `/videos/:id/comments` | 可选 | 评论列表 |
| DELETE | `/comments/:id` | 必须 | 删除评论（校验归属） |

### 关注 `/api`

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| POST | `/follow/:user_id` | 必须 | 关注/取关 toggle |
| GET | `/following/:user_id` | 必须 | 关注列表 |
| GET | `/followers/:user_id` | 必须 | 粉丝列表 |
| GET | `/is_following/:user_id` | 必须 | 是否关注 |

### Feed / 热榜 `/api`

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| GET | `/feed?cursor=&limit=&since=` | 必须 | 关注流（拉新翻旧） |
| GET | `/hot?limit=` | 可选 | 热榜（播放量 TopN） |
| GET | `/ping` | - | 健康检查 |

## 限流设计

限流分两级：**全局**（所有 `/api` 30 req/s/IP）+ **分接口**独立限额。

| 接口 | 限额 | 维度 |
|---|---|---|
| 全局限流 | 30 req/s | IP |
| 注册 | 5 次/时 | IP |
| 登录 | 10 次/分 | IP |
| 上传视频/封面 | 5 次/时 | 账号 |
| 点赞 | 30 次/分 | 账号 |
| 评论 | 10 次/分 | 账号 |
| 关注 | 20 次/分 | 账号 |

实现上，Redis 可用时用 **固定窗口计数**（`INCR + EXPIRE`，按 `scope + 维度 + 窗口` 分桶）；Redis 不可用时**降级为进程内令牌桶**（`golang.org/x/time/rate`），保证单机仍有基本保护——限流组件故障不会把整个服务变成"无限制"或"全拒绝"。

## 实测验证（诚实数据，可复现）

- **缓存**：详情二次命中直接返回（播放量经 ZScore 实时同步）；请求不存在的 id 返回 404 且不穿透 DB（空值标记生效）；
- **发现页游标**：13 条数据 3 页翻完，不重不漏（`[27..22] → [21..6] → [5]`，`next_cursor=0` 到底）；
- **热区回填**：`feed:hot:{uid}` 保留最新 200 条，倒序毫秒精确；
- **降级**：Redis 断连后服务照常可用（详情回源 MySQL、限流切本地令牌桶、关注流退化冷区直查）。

## 目录结构

```
video-feed/
├── main.go              # 入口：配置加载、连库、AutoMigrate、Redis 初始化、路由、优雅关闭
├── config/              # 环境变量加载（VIDEO_ 前缀）
├── database/            # MySQL/Redis 连接 + Redis 业务封装（热榜/时间线/缓存/回填）
├── models/              # GORM 模型（User/Video/Like/Follow，created_ts 毫秒时间戳）
├── handlers/            # HTTP 处理（users/videos/feed/follow/like/comment/upload）
├── service/             # 业务逻辑（关注流/发现页/详情缓存/点赞评论计数一致性）
├── repository/          # 数据访问（Redis 时间线 + MySQL 查询）
├── middleware/          # JWT 鉴权 + AuthOptional 软鉴权 + 分级限流 + BodyLimit
├── routes/              # 路由注册
├── utils/               # JWT 签发/解析
├── apierror/            # 统一错误封装（{code, msg, data}）
├── static/              # 前端单页 + 本地素材（进镜像）
├── Dockerfile           # 多阶段构建（builder 编译 → alpine 运行）
├── docker-compose.yml   # MySQL + Redis + App 三服务编排
└── .github/workflows/   # CI：gofmt / go vet / go test -race / go build
```

## Roadmap

- [ ] 消息队列异步 fanout（大 V 发布不阻塞）
- [ ] 对象存储文件上传（OSS/S3 + CDN）
- [ ] K8s 部署（StatefulSet + ConfigMap）
- [ ] 可观测性（Prometheus + Grafana + 结构化日志）
- [ ] 加权推荐流（ZSet score 融合点赞/完播）
