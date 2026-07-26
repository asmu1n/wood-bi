# RabbitMQ 使用说明

本文描述 wood-bi 中 **已落地** 的 RabbitMQ 设计：拓扑、分层、生产/消费封装、业务接入与本地运维。  
对齐原 yubi-backend 的 BI 任务队列语义（命令消息 = `chartId`），**不是**跨模块领域事件总线。

---

## 1. 总览：MQ 在本项目中的角色

| 角色 | 用途 | 主要代码 |
| ---- | ---- | -------- |
| **任务队列** | 图表 AI 异步生成 | `port.ChartGenQueue` + `infra/mq/rabbit` |
| **生产者** | HTTP 异步提交后投递 `chartId` | `publisher` → `EnqueueGen` |
| **消费者** | 后台拉取任务并执行生成 | `Consumer` → `chart.ProcessGenJob` |
| **补偿** | 滞留 wait 补投 / 超时 running 失败 | `chart.CompensateStaleJobs` + cron |

```text
  POST /api/chart/gen/async
           │
           ▼
  chart.GenerateAsync
    落库 status=wait
    queue.EnqueueGen(chartId)  ──►  RabbitMQ bi_queue
           │                              │
           │ 立即返回 { chartId }         │
           ▼                              ▼
  前端轮询 GET /chart/get          Consumer（同进程后台）
                                         │
                                         ▼
                                  ProcessGenJob
                                    wait/running → AI → succeed|failed
```

要点：

- 消息体很小，**真相在 Postgres**（状态、CSV、生成结果）。
- 业务只依赖 **`port.ChartGenQueue`**；不 import `amqp`。
- **API 与 Worker 同进程**：`cmd/server` 既监听 HTTP，也 `Consumer.Start`。
- 启动时 **Dial 失败即 Fatal**（与 AI、DB、Redis 同属核心依赖）。

---

## 2. 分层与依赖方向

```text
cmd/server
  Dial → NewPublisher → NewService(..., pub)
       → NewConsumer(conn, cfg, chartSvc.ProcessGenJob) → Start
       │
       ▼
module/chart          只依赖 port.ChartGenQueue / ProcessGenJob
       │
       ▼
internal/port         ChartGenQueue.EnqueueGen
       ▲
       │ 实现
infra/mq/rabbit       Connection / Channel / 拓扑 / ack
```

| 规则 | 说明 |
| ---- | ---- |
| module 不 import rabbit | 发消息走端口；消费走回调函数 |
| infra 不 import chart | `GenJobFunc` 由 main 注入 `ProcessGenJob` |
| Connection 共享 | Publisher 与 Consumer 共用 `Dial` 得到的一条连接，各用各的 Channel |

---

## 3. 拓扑与配置

### 3.1 拓扑（默认名，可环境变量覆盖）

| 资源 | 默认 | 类型/属性 |
| ---- | ---- | --------- |
| Exchange | `bi_exchange` | direct, durable |
| Queue | `bi_queue` | durable |
| Routing key | `bi_routingKey` | 绑定 exchange → queue |
| 消息体 | `{"chartId":123}` | JSON；消费端兼容纯数字字符串 |
| 消息持久化 | `DeliveryMode: Persistent` | 配合 durable 队列 |

生产/消费启动时都会 **幂等** `declareTopology`（Declare + Bind），无需单独 InitMain。

### 3.2 环境变量

见 `.env.example`：

| 变量 | 含义 | 默认示例 |
| ---- | ---- | -------- |
| `RABBITMQ_URL` | AMQP 连接串 | `amqp://guest:guest@localhost:5672/` |
| `RABBITMQ_BI_EXCHANGE` | 交换机名 | `bi_exchange` |
| `RABBITMQ_BI_QUEUE` | 队列名 | `bi_queue` |
| `RABBITMQ_BI_ROUTING_KEY` | 路由键 | `bi_routingKey` |
| `RABBITMQ_PREFETCH` | 消费 QoS：未 ack 上限 | `2` |

Compose 内 app 使用 `RABBITMQ_URL=amqp://...@rabbitmq:5672/`；本机 Go 进程用 `localhost`。

### 3.3 Docker

- `docker-compose.yml`：`rabbitmq` 服务（含 management 镜像时）
- `docker-compose.dev.yml`：暴露 `5672`（AMQP）、`15672`（管理台，视镜像而定）

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d
```

---

## 4. 代码地图

| 路径 | 职责 |
| ---- | ---- |
| `internal/port/queue.go` | `ChartGenQueue` 端口 |
| `internal/infra/mq/rabbit/config.go` | 读环境、校验 |
| `internal/infra/mq/rabbit/dial.go` | `Dial` → Connection + Config |
| `internal/infra/mq/rabbit/topology.go` | 声明 exchange / queue / bind |
| `internal/infra/mq/rabbit/publisher.go` | 实现 `EnqueueGen` |
| `internal/infra/mq/rabbit/consumer.go` | 消费循环、手动 ack |
| `internal/module/chart/service.go` | `GenerateAsync` / `ProcessGenJob` / `CompensateStaleJobs` |
| `cmd/server/main.go` | 装配与生命周期 |

---

## 5. 连接（Dial）

```go
conn, cfg, err := rabbit.Dial()
// Fatal if err
defer conn.Close()
```

1. `loadConfig` + `validate`
2. `amqp.Dial(URL)`：TCP + AMQP 握手
3. 返回 **Connection** 与 **同一份 Config**（保证生产/消费拓扑名一致）

**Connection** = 进程级长连接（贵）；**Channel** = 连接上的逻辑会话（相对便宜）。  
Publisher / Consumer 各自持有 Channel，不共用同一条 Channel 发收。

---

## 6. 生产者（Publisher）

### 6.1 职责

实现 `port.ChartGenQueue`：

```go
EnqueueGen(ctx context.Context, chartID int64) error
```

向 `bi_exchange` 按 `routingKey` 发布持久化 JSON 消息。

### 6.2 初始化与 Channel

- `NewPublisher(conn, cfg)`：保存连接与配置；当前实现 **不在构造时强制建 channel**（首次发送时 `lazyChannel`）。
- `lazyChannel`（持锁）：
  - 若 `ch` 可用则复用
  - 否则 `conn.Channel()` → `declareTopology` → 赋给 `p.ch`

### 6.3 并发：短临界区

多个 HTTP 请求会并发 `EnqueueGen`，对共享字段 `p.ch` 用 `sync.Mutex`：

```go
// 1. 锁内确保 / 创建 channel
p.lazyChannel()

// 2. 短锁只拷贝指针，避免把网络 IO 放进临界区
p.mu.Lock()
ch := p.ch
p.mu.Unlock()

// 3. 锁外 Publish
ch.PublishWithContext(...)
```

| 要点 | 说明 |
| ---- | ---- |
| 为何加锁 | 防止并发读写 `p.ch` 的 data race |
| 为何短锁 | `Publish` 可能阻塞；持锁发送会串行化所有请求 |
| 局部 `ch` | 指针副本；之后 `p.ch` 被 Close 置 nil 不影响局部变量指向，但对象已关则 Publish 可能失败 |

`Close()` 只关 Publisher 的 channel，**不关**共享 Connection。

### 6.4 业务调用

`chart.GenerateAsync`：

1. 校验、限流、Excel→CSV  
2. 落库 `status=wait`  
3. `EnqueueGen`  
4. 投递失败 → 更新 `failed` 并返回业务错误  
5. 成功 → 立即返回 `{ chartId }`（HTTP 不等待 AI）

---

## 7. 消费者（Consumer）

### 7.1 两阶段生命周期

| 阶段 | 作用 |
| ---- | ---- |
| `NewConsumer(conn, cfg, handle)` | 只保存依赖，**不**拉消息 |
| `Start(ctx)` | 后台 goroutine 跑 `loop` |
| `Close()` | 取消 ctx → 等 loop 结束 → 关 channel |

`handle` 类型：

```go
type GenJobFunc func(ctx context.Context, chartID int64) error
```

main 注入：`chartSvc.ProcessGenJob`。

### 7.2 为何是 `loop` + `consumeOnce`（而不是一次 Consume 到底）

`Channel().Consume()` 返回的 `<-chan Delivery` 绑定在 **某一次 AMQP Channel 会话** 上。  
连接抖动、channel 关闭、broker 重启都会导致该 Go channel 关闭，`for range` 结束。

| 层次 | 含义 |
| ---- | ---- |
| **`consumeOnce`** | 一次会话：开 ch → 拓扑 → Qos → Consume → `select` 读 deliveries |
| **`loop`** | 会话失败则指数退避（1s…30s）再开下一局；`ctx` 取消则退出 |

内层仍然是 **channel 持续接收**；外层负责 **会话级重连**。  
没有外层循环 = 第一次断线后异步消费永久静默失败。

### 7.3 QoS（Prefetch）

```go
ch.Qos(prefetch, 0, false)  // 默认 2
```

限制「未 ack」消息数量，避免 AI 慢时堆积过多 in-flight 任务。

### 7.4 单条消息：ack 策略

`handleDelivery`：

| 情况 | 动作 |
| ---- | ---- |
| body 无法解析 | `Nack(requeue=false)` 丢弃，防毒消息死循环 |
| `handle` 返回 **error** | `Nack(requeue=true)` 瞬时故障重试 |
| `handle` 返回 **nil** | `Ack`（含业务失败已写入 DB 的情况） |

单任务 `context` 超时默认 **3 分钟**。

### 7.5 与 `ProcessGenJob` 的约定（关键）

| `ProcessGenJob` 返回 | 含义 | MQ |
| -------------------- | ---- | -- |
| `nil` + succeed | 生成成功 | Ack |
| `nil` + 已 `failJob` | AI/数据等业务失败，已标 failed | Ack（**不要 requeue**） |
| `nil` + 已是 succeed/failed | 幂等跳过 | Ack |
| `error` | DB 等瞬时错误 | Nack requeue |

状态在 DB，消息只带 ID → 消费可幂等。

### 7.6 `Close` 与并发

- `mu` 保护 `cancel`、当前 `ch`（与 Start/loop 交错）  
- `WaitGroup` 等待后台 `loop` 退出  
- 取消 `runCtx` 使 `select` 从读 deliveries 中醒来  

---

## 8. 补偿任务

`cmd/server` 注册 cron（每分钟）：

```go
chartSvc.CompensateStaleJobs(ctx)
```

| 状态 | 条件（约） | 动作 |
| ---- | ---------- | ---- |
| `running` | `updated_at` 过旧 | 标 `failed`（执行超时） |
| `wait` | `updated_at` 过旧 | 再次 `EnqueueGen` 补投 |

用于：投递后消费未执行、进程崩溃、消息丢失等。

---

## 9. 装配顺序（cmd/server）

必须按依赖顺序：

```text
1. Dial
2. NewPublisher
3. NewService(..., mqPub)          // 需要 queue
4. NewConsumer(..., ProcessGenJob) // 需要 Service 方法
5. Consumer.Start(ctx)
6. 注册 CompensateStaleJobs cron
7. HTTP Listen
```

关停建议顺序（defer 栈 LIFO）：

```text
Consumer.Close → Publisher.Close → Connection.Close
```

核心依赖失败（含 Dial）→ `logger.Fatal`，不做「无 MQ 半残启动」。

---

## 10. API 与前端约定

| 接口 | 行为 |
| ---- | ---- |
| `POST /api/chart/gen/async` | 受理任务，返回 `{ "chartId": n }` |
| `GET /api/chart/get?id=` | 查 `status` / `genChart` / `genResult` / `execMessage` |

状态：`wait` → `running` → `succeed` | `failed`。  
**写接口不阻塞等 AI**；结果靠轮询（或后续可加 SSE）。

同步接口 `POST /api/chart/gen` 不走 MQ。

---

## 11. 与原 Java 项目对照

| 项 | Java (yubi) | Go (wood-bi) |
| -- | ----------- | ------------ |
| 拓扑名 | bi_exchange / bi_queue / bi_routingKey | 同默认 |
| 消息 | chartId 字符串 | JSON `chartId`（兼容数字串） |
| 生产 | Controller → BiMessageProducer | Service → port → Publisher |
| 消费 | `@RabbitListener` 同进程 | Consumer.Start 同进程 |
| 编排 | 大量逻辑在 Consumer | 编排在 `ProcessGenJob` |
| 建拓扑 | 手写 BiInitMain | 运行时 declareTopology |

---

## 12. 排障速查

| 现象 | 可能原因 |
| ---- | -------- |
| 启动 Fatal dial | Rabbit 未起、URL/端口/账号错误 |
| 异步一直 wait | Consumer 未 Start、队列名不一致、prefetch/处理卡住 |
| 反复 requeue | `ProcessGenJob` 持续返回 error（查 DB/日志） |
| 直接 failed | 投递失败、AI 失败、补偿超时 |
| 管理台无队列 | 尚未有进程 declare；先成功启动 app 或手动声明 |

日志 event 示例：`rabbit.ready`、`rabbit.consume_start`、`rabbit.job_requeue`、`chart.gen_async_ok`、`chart.job_succeed`。

---

## 13. 演进备忘（非当前实现）

- 拆 `cmd/worker`：仅 API 发消息，Worker 只消费  
- 死信队列（DLX）替代无限 requeue  
- Connection 级自动重拨（当前侧重 channel 会话重建）  
- 跨模块 **领域事件** 总线（与当前「图表任务命令队列」分层，勿混为一谈）  

更细的互斥锁 / 短临界区笔记见博客项目木屑：  
`AsMuin_WebSite/sawdust/go-mutex-short-critical-section.md`（路由 `/sawdust/go-mutex-short-critical-section`）。

---

## 14. 相关文档

- [REDIS_CACHE.md](./REDIS_CACHE.md) — Redis 角色划分  
- [REFACTORING_PLAN.md](./REFACTORING_PLAN.md) — 重构与 Phase 分期  
- `.env.example` — 环境变量模板  
- `docker-compose.yml` / `docker-compose.dev.yml` — RabbitMQ 服务与端口  

