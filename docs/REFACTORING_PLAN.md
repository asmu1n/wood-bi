# wood-bi 重构方案与技术选型

> 源项目：`_reference/yubi-backend-master`（鱼皮 Spring Boot 模板 + 鱼 BI 业务）  
> 目标工程：本仓库 Go Web 模板（Gin + Ent + Postgres + Redis）  
> 文档性质：技术方向与分期实施蓝图（选型已确认；业务代码按 Phase 分期落地）

---

## 0. 已确认决策（基线）

| # | 议题 | 结论 |
| - | ---- | ---- |
| 1 | AI 提供商 | **OpenAI 兼容协议**（可配 BaseURL / APIKey / Model）；**不兼容**原项目鱼聪明 SDK |
| 2 | 异步任务 | **RabbitMQ** 作为异步主方案（对齐原 `bizmq` 语义） |
| 3 | API / 响应兼容 | **不要求字段 100% 兼容**原 Java；在模板 `pkg/response`、`pkg/page` 基础上承载业务数据；路径与核心语义对齐即可 |
| 4 | AI 输出协议 | **JSON**（如 `option` + `conclusion`）；不再使用 `【【【【【` 分隔符 |
| 5 | 数据与持久化 | **Postgres + Ent 全新库**；不做 MySQL 历史数据 / 密码迁移 |

以上决策覆盖后文所有选型冲突点；若与早期草稿不一致，**以本节为准**。

---

## 1. 原项目定位与技术方向

### 1.1 产品定位

**yubi-backend** 本质是 **AI 智能 BI（数据分析 + 图表生成）** 后端，而不是纯内容社区。

核心用户故事：

1. 用户注册 / 登录
2. 上传 Excel 表格 + 填写分析目标与图表类型
3. 后端调用 AI，生成：
   - **ECharts V5 option**（前端可直接渲染）
   - **自然语言分析结论**
4. 任务可 **同步返回**，也可 **异步排队**（线程池 / RabbitMQ），用户轮询任务状态
5. 图表任务支持列表、详情、编辑、删除（本人或管理员）

数据模型上真正与产品相关的表只有两张：

| 表 | 职责 |
| -- | ---- |
| `user` | 账号、密码、昵称、头像、角色 |
| `chart` | 分析目标、原始 CSV、图表类型、AI 生成结果、任务状态 |

`post` / 点赞 / 收藏 / ES 同步等来自 Spring 初始模板的**示例业务**，与 BI 主线无关。

### 1.2 技术栈全景（原项目）

| 层次 | 技术 | 在项目中的实际用途 |
| ---- | ---- | ------------------ |
| 语言 / 运行时 | Java 8 + Spring Boot 2.7 | Web 框架与依赖注入 |
| Web | Spring MVC | REST API，`context-path=/api` |
| ORM | MyBatis + MyBatis-Plus | CRUD、分页、逻辑删除 |
| DB | MySQL | 业务持久化 |
| 缓存 / 会话 | Redis + Spring Session | 可选分布式 Session |
| 限流 | Redisson `RRateLimiter` | AI 生成接口按用户限流（约 2 次/秒） |
| 异步 | `ThreadPoolExecutor` + `CompletableFuture` | 异步图表任务 |
| 消息队列 | RabbitMQ（AMQP） | 异步 MQ 版 AI 任务（`bi_queue`） |
| AI | 鱼聪明 SDK（`yucongming-java-sdk`） | `doChat(modelId, message)` |
| 表格 | EasyExcel | xlsx → CSV 文本压缩后喂给 AI |
| 对象存储 | 腾讯云 COS | 通用文件上传（非 BI 主链路） |
| 搜索 | Elasticsearch | 帖子检索（模板示例，非 BI） |
| 微信 | wx-java（开放平台 / 公众号） | 第三方登录与公众号（模板示例） |
| 文档 | Knife4j (Swagger) | 接口调试 |
| 工具 | Hutool / Gson / Commons Lang / Lombok | 工具与样板代码 |
| 定时任务 | Spring Scheduler | 帖子全量/增量同步 ES |

### 1.3 架构分层（原项目）

```text
controller  →  service / manager  →  mapper(MyBatis) → MySQL
                    │
                    ├─ AiManager          → 鱼聪明 AI
                    ├─ RedisLimiterManager → Redisson
                    ├─ BiMessageProducer  → RabbitMQ
                    └─ CosManager         → 腾讯云 COS

bizmq.BiMessageConsumer  ← RabbitMQ ← chart 异步任务
job.*                    → ES 同步（帖子示例，可忽略）
```

特点：

- **Controller 偏胖**：图表生成、校验、限流、拼 Prompt、落库、异步调度都在 `ChartController`
- **Service 偏薄**：`ChartServiceImpl` 几乎只有 MyBatis-Plus 默认 CRUD
- **横切能力**靠 AOP（权限注解、请求日志）与全局异常处理

### 1.4 核心业务链路

#### A. 同步 AI 分析 `POST /chart/gen`

```text
上传 xlsx + goal/name/chartType
  → 校验（目标非空、文件 ≤1MB、后缀 xlsx）
  → 登录用户 + 限流
  → Excel → CSV
  → 拼 Prompt → AI → **解析 JSON**（option + conclusion）→ genChart / genResult
  → 落库 chart → 返回业务 data（外层走模板 response）
```

#### B. 异步（MQ）`POST /chart/gen/async`（Go 目标主路径；原项目另有线程池版可忽略）

```text
校验 + 限流 + Excel→CSV
  → 先落库 status=wait
  → 投递 chartId 到 RabbitMQ
  → Consumer：wait→running → AI → succeed/failed
  → 接口立即返回 chartId（前端轮询 GET）
```

> 原项目还有线程池版 `/gen/async` 与 MQ 版 `/gen/async/mq` 两套。**Go 重写只保留一套异步：RabbitMQ。**

图表状态机：

```text
wait → running → succeed
               ↘ failed（附 execMessage）
```

#### D. 用户与权限

- Session 存登录态（`USER_LOGIN_STATE`）
- 角色：`user` / `admin`
- 密码：MD5(SALT + password)（**安全较弱，Go 重写应升级**）
- 注解 `@AuthCheck(mustRole=admin)` + AOP 拦截

### 1.5 原项目中应「继承 / 改造 / 丢弃」的部分

| 类别 | 内容 | 建议 |
| ---- | ---- | ---- |
| **核心产品** | 用户注册登录、图表 CRUD、AI 同步/异步生成、限流、Excel 解析、任务状态 | **必须重写** |
| **工程能力** | 统一响应、错误码、分页、CORS、全局异常、Swagger、逻辑删除 | **对齐现有 Go 模板能力** |
| **可替换基础设施** | MySQL→Postgres、鱼聪明→OpenAI 兼容、Redisson→Redis 限流、EasyExcel→excelize；**RabbitMQ 保留** | **按已确认选型替换** |
| **模板示例业务** | Post / 点赞 / 收藏、ES 同步、微信登录/公众号 | **不迁移** |
| **学习用 MQ 样例** | `mq/` 下 Direct/Fanout/Topic/TTL/DLX 等演示 | **不迁移** |
| **可选增强** | COS 文件上传、队列监控 `QueueController` | **二期按需** |

---

## 2. 目标工程现状（Go 模板）

本仓库已具备可复用骨架，**业务 module 为空**。

| 能力 | 现状 | 对应原项目 |
| ---- | ---- | ---------- |
| HTTP | Gin + `/api` + Swagger | Spring MVC + Knife4j |
| ORM / DB | Ent + **Postgres** | MyBatis-Plus + MySQL |
| Session | gin-sessions + Redis | Spring Session Redis |
| 缓存 | `port.Cache`（Redis + L1 TinyLFU） | 部分 Redis 用法 |
| 分布式锁 | `port.Locker` | Redisson 锁（原项目主用限流） |
| 定时任务 | robfig/cron | Spring Scheduler |
| 鉴权中间件 | `AuthRequired`（session 存 userID） | Session + `@AuthCheck` |
| 统一响应 / 错误码 | `pkg/response` | BaseResponse / ErrorCode |
| 分页 | `pkg/page` | PageRequest / MyBatis-Plus Page |
| 业务模块约定 | `internal/module/<name>/` 垂直切片 | controller/service/mapper 水平分层 |
| 限流 | **未内置** | Redisson RateLimiter |
| 异步任务队列 | **未内置（将接 RabbitMQ）** | ThreadPool / RabbitMQ |
| AI 客户端 | **未内置（将接 OpenAI 兼容）** | 鱼聪明 SDK |
| Excel | **未内置** | EasyExcel |

依赖方向（必须遵守）：

```text
cmd → httpapi → module/*/http → module/*
module → port、pkg
infra 实现 port（module 不直接依赖 infra）
```

---

## 3. 技术选型对照与决策

### 3.1 总原则

1. **优先复用模板已有能力**（Gin / Ent / Postgres / Redis Session / Cache / Lock / Cron / Swagger / `pkg/response` / `pkg/page`）。
2. **职责拆分中间件**：Redis = Session + 缓存 + 限流 + 锁；**RabbitMQ = 异步图表任务**（与原 yubi 一致）。
3. **AI 走 OpenAI 兼容协议**：抽象 `port.AI`，环境变量配置 BaseURL/APIKey/Model；**不对接鱼聪明**。
4. **安全与模型优于「行为复制」**：bcrypt；状态枚举；Handler 瘦、用例在 Service。
5. **示例业务不移植**：只做「用户 + 图表 BI」闭环。
6. **全新数据**：Postgres + Ent；无 MySQL 迁移。

### 3.2 选型决策表

| 能力域 | 原 Java | Go 选型 | 决策说明 |
| ------ | ------- | ------- | -------- |
| Web 框架 | Spring MVC | **Gin**（已有） | 与模板一致 |
| ORM | MyBatis-Plus | **Ent**（已有） | Schema 驱动、类型安全、迁移内置 |
| 主库 | MySQL | **Postgres**（已有） | **已确认**全新库；snake_case；软删按 Ent 约定 |
| Session | Spring Session Redis | **gin-contrib/sessions + Redis**（已有） | 已对齐 |
| 业务缓存 | Redis 零散用法 | **port.Cache**（已有） | 用户信息、图表详情等按需缓存 |
| 限流 | Redisson RateLimiter | **Redis 限流**（新增 `port.RateLimiter`） | `redis_rate` 或自研 Lua |
| 同步 AI | Controller 内阻塞调用 | Service 同步调 `port.AI` | 保留 `POST /chart/gen`，超时可配置 |
| 异步 AI | ThreadPool + RabbitMQ 两套 | **仅 RabbitMQ**（`port.ChartGenQueue` + consumer） | **已确认**；不引入 Asynq 作为主路径 |
| AI 服务 | 鱼聪明 SDK | **OpenAI 兼容 Client** + `port.AI` | **已确认**；Prompt 自建 |
| AI 输出 | 分隔符切分 | **JSON** `option` + `conclusion` | **已确认** |
| Excel | EasyExcel | **excelize** | xlsx → CSV 文本喂给 AI |
| 对象存储 | 腾讯云 COS | **暂缓** | 头像用 URL 字段即可 |
| 搜索 / 微信 | ES / wx-java | **不做** | 非 BI 主线 |
| API 文档 | Knife4j | **swaggo**（已有） | 保持 |
| 响应 / 分页 | BaseResponse + MP Page | **`pkg/response` + `pkg/page`** | **已确认**；见 §3.6 |
| 配置 | application.yml | **环境变量 + godotenv**（已有） | 12-factor |
| 密码 | MD5+盐 | **bcrypt** | 无历史密码迁移 |
| ID | 雪花 ASSIGN_ID | **Ent 自增 int64（默认）** | 对外 JSON 用 number/int64 |
| 日志 | slf4j | **pkg/logger**（已有） | module + event |
| 定时任务 | Spring Scheduler | **robfig/cron**（已有） | 超时 `running` 扫描、wait 补投 |
| 部署 | Dockerfile | compose + Dockerfile | **增加 RabbitMQ 服务** |

### 3.3 异步方案（RabbitMQ，已确认）

```text
                    ┌──────────────┐
  /gen (sync)  ───► │ ChartService │ ──► AI(JSON) ──► DB succeed
                    └──────┬───────┘
  /gen/async ──────► 落库 wait ──► RabbitMQ Publish(chartId)
                           │                │
                           │                ▼
                           │         Consumer（手动 ack）
                           │                │
                           │                ▼
                           │         GenerateChart 同一用例
                           ▼
                    前端按 chartId 轮询 GET
```

**拓扑（对齐原 bizmq，名称可配置）：**

| 资源 | 默认名 |
| ---- | ------ |
| Exchange | `bi_exchange`（direct, durable） |
| Queue | `bi_queue`（durable） |
| Routing key | `bi_routingKey` |
| 消息体 | chartId（JSON 数字或字符串，实现时统一） |
| QoS | prefetch 可配置（控制并发 AI 调用数） |

**可靠消费约定：**

- 消费端 **手动 ack**
- 业务失败（AI 解析失败等）：写 `status=failed` + `exec_message` 后 **ack**（避免死循环）
- 瞬时基础设施错误：有限次 requeue 或进 DLX（P2 硬化）
- 幂等：已 `succeed` 直接 ack；`running` 按策略允许重入或跳过
- 投递顺序：**先 DB wait，再 Publish**；失败可用 cron 扫长时间 `wait` 补投

**分层：**

```text
port.ChartGenQueue.EnqueueGen(ctx, chartID)
infra/mq/rabbit  — Publisher 实现 port
infra/mq/rabbit  — Consumer 调 chart.Service.ProcessGenJob
cmd/server 或 cmd/worker — 装配连接与拓扑声明
```

**分期：**

| 阶段 | 内容 |
| ---- | ---- |
| Phase 含异步时 | compose 加 RabbitMQ；Publish + 同进程或 `cmd/worker` Consumer；sync + async 两接口 |
| 硬化 | DLX、补投 cron、prefetch/并发配置、连接自动重连 |

> Asynq / 进程内 worker 仅作对照思路，**不作为本项目默认实现**。

### 3.4 AI Prompt 与输出协议（JSON，已确认）

服务端维护完整 system/user Prompt，要求模型 **只输出 JSON**（可配合 `response_format` 若供应商支持）：

```json
{
  "option": { },
  "conclusion": "……"
}
```

| 字段 | 含义 | 落库 |
| ---- | ---- | ---- |
| `option` | ECharts V5 option 对象 | `gen_chart`（序列化为 JSON 文本） |
| `conclusion` | 分析结论文本 | `gen_result` |

解析失败或缺少字段 → `status=failed`，`exec_message` 记录原因。  
可对模型输出做轻量清洗（去掉 markdown 代码围栏）再 `json.Unmarshal`。

### 3.5 新增依赖清单（建议）

| 依赖 | 用途 | 阶段 |
| ---- | ---- | ---- |
| `github.com/xuri/excelize/v2` | Excel 解析 | 图表同步 AI |
| `golang.org/x/crypto/bcrypt` | 密码哈希 | 用户 |
| OpenAI 兼容 client（thin HTTP 或 `go-openai`） | Chat Completions | AI |
| Redis 限流库或自研 Lua | 生成接口限流 | AI |
| `github.com/rabbitmq/amqp091-go` | RabbitMQ | 异步 |
| 对象存储 SDK | 文件上传 | 按需 |

### 3.6 响应与分页（模板优先，语义对齐）

**不要求**与 Java `BaseResponse` / MyBatis-Plus `Page` 字段 100% 一致；统一走模板约定：

**外层响应**（`pkg/response`）：

```json
{
  "code": 0,
  "data": {},
  "message": "ok"
}
```

错误码使用模板 `response.Code`（如 `40000` 参数错误、`40100` 未登录等），可按业务需要扩展（如限流 `429xx`），不必复刻 Java 的每一个 ErrorCode 数值。

**分页**（`pkg/page`）：

| 方向 | 字段 |
| ---- | ---- |
| 请求 | `pageNum`、`pageSize`（可嵌入业务 Query） |
| 响应 data | `records`、`total`、`pageSize`、`pageNum` |

相对原项目 `current` / `size` / `records` 等命名，**以模板为准**；业务 Query 其它过滤字段（name、goal、chartType、userId）按需保留。

**典型 data 形状（语义对齐，字段名可微调）：**

| 场景 | data 内容 |
| ---- | --------- |
| 登录 | 用户公开信息 VO（id、account、name、avatar、role 等，**不含密码**） |
| 同步生成成功 | `{ chartId, genChart, genResult }` 或等价 |
| 异步提交 | `{ chartId }` |
| 图表详情 | chart 实体/VO（含 status、execMessage 等） |

---

## 4. 领域模块划分（落在 Go 模板上）

```text
internal/module/
  user/          # 注册、登录、注销、资料、管理员用户管理
    http/
    repo/
  chart/         # 图表 CRUD + AI 生成用例 + 状态机
    http/
    repo/

internal/port/
  cache.go       # 已有
  lock.go        # 已有
  rate_limit.go  # 新增
  ai.go          # 新增：Chat Completions（OpenAI 兼容）
  queue.go       # 新增：ChartGenQueue.EnqueueGen

internal/infra/
  cache/         # 已有
  lock/          # 已有
  ratelimit/     # 新增 Redis 限流
  ai/            # 新增 OpenAI 兼容客户端
  mq/rabbit/     # 新增 Publisher + Consumer + 拓扑声明
```

### 4.1 模块职责

**user**

- 注册 / 登录 / 注销 / 当前用户 / 更新自己
- 管理员：增删改查用户（对应原 `UserController` 管理端）
- Session 写入 `userID`（与现有 `middleware.AuthRequired` 对齐）
- 角色校验：在 middleware 增加 `AdminRequired`，或 Service 内校验

**chart**

- CRUD + 分页列表 / 我的列表
- `GenerateSync` / `GenerateAsync`（async 只走 RabbitMQ）
- `ProcessGenJob(chartID)`：供 Consumer 调用的同一套 AI 用例
- Excel→CSV、拼 Prompt、调 AI、**JSON 解析**、状态更新
- 所有写操作鉴权；删改校验本人或 admin

**不建 module：** post、favour、thumb、wx、file（一期）、es

### 4.2 Ent Schema 草案

**user**

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| id | int64 | PK |
| account | string | unique，原 userAccount |
| password_hash | string | bcrypt |
| name | string | 昵称 |
| avatar | string | URL |
| role | enum/string | user / admin |
| created_at / updated_at | time | |
| deleted_at | optional | 软删（Ent 注解或字段） |

**chart**

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| id | int64 | PK |
| name | string | 图表名称 |
| goal | text | 分析目标 |
| chart_data | text | CSV 原始数据 |
| chart_type | string | 如折线图 |
| gen_chart | text | ECharts option JSON/文本 |
| gen_result | text | 分析结论 |
| status | enum | wait / running / succeed / failed |
| exec_message | text | 失败信息 |
| user_id | int64 | FK → user |
| created_at / updated_at | time | |
| deleted_at | optional | 软删 |

索引建议：`chart(user_id, created_at)`、`user(account)`。

### 4.3 API 草图（与原接口大致对齐，便于前端迁移）

| 方法 | 路径 | 说明 | 鉴权 |
| ---- | ---- | ---- | ---- |
| POST | `/api/user/register` | 注册 | 否 |
| POST | `/api/user/login` | 登录 | 否 |
| POST | `/api/user/logout` | 注销 | 是 |
| GET | `/api/user/current` | 当前用户 | 是 |
| POST | `/api/user/update/my` | 更新自己 | 是 |
| POST | `/api/chart/add` | 创建 | 是 |
| POST | `/api/chart/delete` | 删除 | 是 |
| POST | `/api/chart/edit` | 编辑 | 是 |
| GET | `/api/chart/get` | 详情 | 是 |
| POST | `/api/chart/list/page` | 分页（管理/全量视权限） | 是 |
| POST | `/api/chart/my/list/page` | 我的分页 | 是 |
| POST | `/api/chart/gen` | 同步 AI 生成 | 是 |
| POST | `/api/chart/gen/async` | 异步 AI 生成（RabbitMQ） | 是 |

管理员用户管理接口按原项目保留，路径可放在 `/api/user/*` 并加 Admin 中间件。

**响应约定见 §3.6**：外层 `pkg/response`，分页 `pkg/page`；`data` 内业务字段语义对齐原项目，命名以 Go 模板与 JSON 惯例为准（不必 100% 同名）。

---

## 5. 架构目标态

```text
                    ┌─────────────────────────────────────────┐
                    │     cmd/server（及可选 cmd/worker）       │
                    │  装配 DB / Redis / AI / RateLimit /     │
                    │  RabbitMQ Pub+Sub / Cron / Router       │
                    └───────────────────┬─────────────────────┘
                                        │
              ┌─────────────────────────┼─────────────────────┐
              ▼                         ▼                     ▼
         httpapi                   module/*               infra/*
     (路由/中间件)              (user, chart)         (实现 port)
              │                         │
              │                         ├── port.Cache
              │                         ├── port.Locker
              │                         ├── port.RateLimiter
              │                         ├── port.AI
              │                         └── port.ChartGenQueue
              ▼                         ▼
           Gin                      Service 用例
                                      │
                                      ▼
                                   repo + ent → Postgres
```

与原项目的关键差异：

| 点 | 原项目 | 目标 |
| -- | ------ | ---- |
| 分层 | Controller 承载主流程 | Service 承载用例；Handler 只做绑定与响应 |
| 中间件 | 注解 AOP | Gin middleware + 显式依赖注入 |
| 异步 | 线程池 + RabbitMQ 两套 | **仅 RabbitMQ 一套** |
| DB | MySQL 驼峰 | **Postgres + Ent**，全新库 |
| AI | 鱼聪明 SDK + 分隔符 | **OpenAI 兼容 + JSON** |
| 响应 | Java BaseResponse / MP Page | **模板 response + page** |

---

## 6. 分期实施计划

### Phase 0 — 基线与选型（已完成）

- [x] 梳理原技术栈与业务边界
- [x] 对照 Go 模板能力缺口
- [x] 技术选型与模块划分
- [x] **关键决策已确认**（见 §0）

### Phase 1 — 用户闭环 + Schema ✅

1. [x] Ent schema：`user`、`chart`（Postgres）
2. [x] `module/user`：注册、登录、注销、当前用户、更新资料 + 管理端 CRUD
3. [x] Session 与 `AuthRequired` / `AdminRequired` 打通
4. [x] bcrypt；Service 单测

**验收：** 可注册登录，Swagger 可调，Session 跨请求有效（需本地 Postgres + Redis）。

### Phase 2 — 图表 CRUD + 同步 AI ✅

1. [x] `module/chart` CRUD 与分页（`pkg/page`，含 my list）
2. [x] `port.AI` + `infra/ai`（OpenAI 兼容）
3. [x] Excelize：xlsx → CSV
4. [x] `POST /chart/gen`：Prompt + **JSON 解析** + 落库
5. [x] `port.RateLimiter` 挂生成接口

**验收：** 配置 `AI_API_KEY` 后上传样例 Excel，返回 ECharts option 与 conclusion 并落库。

### Phase 3 — RabbitMQ 异步

1. compose 增加 RabbitMQ；拓扑声明（exchange/queue/bind）
2. `port.ChartGenQueue` + `infra/mq/rabbit` Publisher
3. Consumer 调 `ProcessGenJob`；手动 ack；状态机完整
4. `POST /chart/gen/async`；可选 `cmd/worker` 拆分
5. Cron：长时间 `running` / 滞留 `wait` 的补偿

**验收：** 异步提交立即返回 chartId；轮询至 succeed/failed；重启后未 ack 消息可再投。

### Phase 4 — 硬化与运维

1. 环境变量文档（AI、RabbitMQ、限流、文件大小、超时）
2. DLX / 补投策略完善；日志 event
3. 集成测试（mock AI + 可选 testcontainers MQ）
4. （可选）对象存储、管理端能力

---

## 7. 已关闭的决策 & 实现期参数

### 7.1 已关闭（见 §0）

不再讨论：鱼聪明兼容、Asynq 主路径、字段级 100% 兼容、分隔符输出、MySQL 迁移。

### 7.2 实现期默认可配置参数（编码时用合理默认即可）

| 参数 | 建议默认 |
| ---- | -------- |
| 上传文件大小 | 1MB |
| 允许后缀 | xlsx（可加 xls） |
| 限流 | 每用户约 2 次/秒（可调） |
| AI 超时 | 60–120s（可调） |
| RabbitMQ prefetch | 2–4（对齐原线程池并发量级） |
| gen 接口 | **sync + async 两套** |

---

## 8. 小结

| 维度 | 结论 |
| ---- | ---- |
| 原项目本质 | AI BI 图表生成服务，外裹 Spring 教学模板 |
| 迁移范围 | **用户 + 图表 + AI + 限流 + Excel + RabbitMQ 异步**；丢弃帖子/ES/微信/演示 MQ |
| 技术主轴 | Gin + Ent + **Postgres** + Redis（Session/Cache/Lock/限流）+ **RabbitMQ** |
| AI | **OpenAI 兼容** + 服务端 Prompt + **JSON 输出** |
| API | 路径语义对齐；**response / page 以模板为准** |
| 数据 | **全新库**，bcrypt，无 MySQL 迁移 |
| 实施顺序 | Schema/用户 → 同步 AI → RabbitMQ 异步 → 硬化 |

---

## 9. 下一步

选型已锁定，可直接进入 **Phase 1 编码**：

1. Ent schema（`user`、`chart`）+ generate  
2. `module/user` 闭环  
3. 随后 Phase 2 `chart` + AI，Phase 3 RabbitMQ  

如需，可先补一版 **接口字段级草案**（Request/VO 与错误码扩展表），再写代码。
