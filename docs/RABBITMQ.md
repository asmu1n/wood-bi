# RabbitMQ 使用说明

本文描述 wood-bi 中 **已落地** 的 RabbitMQ 设计：拓扑、分层、生产/消费封装、业务接入与本地运维。  
对齐原 yubi-backend 的 BI 任务队列语义（命令消息 = `chartId`），**不是**跨模块领域事件总线。

---

## 1. 总览：MQ 在本项目中的角色

| 角色 | 用途 | 主要代码 |
| ---- | ---- | -------- |
| **任务队列** | 图表 AI 异步生成 | `port.ChartGenQueue` + `infra/mq/rabbit` |
| **生产者** | HTTP 异步提交后投递 `chartId` | `publisher` → `EnqueueGen` |
| **消费者** | 多 Channel 竞争拉取并执行生成 | `Consumer` → `chart.ProcessGenJob` |
| **补偿** | 滞留 wait 补投 / 超时 running 失败 | `chart.CompensateStaleJobs` + cron |

```text
  POST /api/chart/gen/async          cmd/server（API）
           │
           ▼
  chart.GenerateAsync
    落库 status=wait
    queue.EnqueueGen(chartId)  ──►  RabbitMQ bi_queue
           │                              │
           │ 立即返回 { chartId }         │ 竞争投递
           ▼                              ▼
  前端轮询 GET /chart/get          cmd/worker（独立进程）
                                    Consumer：worker0..N-1 各 1 条 Channel
                                         │ 串行 handle + ack
                                         ▼
                                  ProcessGenJob
                                    wait/running → AI → succeed|failed

  cmd/worker 另跑 cron：CompensateStaleJobs（滞留 wait 补投 / 超时 running 失败）
```

要点：

- 消息体很小，**真相在 Postgres**（状态、CSV、生成结果）。
- 业务只依赖 **`port.ChartGenQueue`**；不 import `amqp`。
- **API 与 Worker 分进程**：`cmd/server` 只 HTTP + 投递；`cmd/worker` 只消费 + 补偿。
- **并行模型 = 多 Channel 竞争消费（方案 C）**：每个消费会话独立 Channel，本 Channel 内串行处理与 ack；**不用**业务线程池在共享 Channel 上并发 ack。跨进程再叠加多副本 `worker` 水平扩容。
- 启动时 **Dial 失败即 Fatal**（API 无 Publisher / Worker 无 Consumer 均视为不可用）。

---

## 2. 分层与依赖方向

```text
cmd/server                         cmd/worker
  Dial → NewPublisher                Dial → NewPublisher（补偿补投）
       → NewService(..., pub)             → NewService(..., pub)  // limiter=nil
       → HTTP Listen                      → NewConsumer(..., ProcessGenJob) → Start
                                          → cron CompensateStaleJobs
       │                                  │
       └──────────────┬───────────────────┘
                      ▼
               module/chart
                 只依赖 port.ChartGenQueue / ProcessGenJob
                      │
                      ▼
               internal/port  ChartGenQueue.EnqueueGen
                      ▲
                      │ 实现
               infra/mq/rabbit  Connection / Channel / 拓扑 / ack
```

| 规则 | 说明 |
| ---- | ---- |
| module 不 import rabbit | 发消息走端口；消费走回调函数 |
| infra 不 import chart | `GenJobFunc` 由 `cmd/worker` 注入 `ProcessGenJob` |
| 进程职责 | **server**：迁移 + HTTP + Publish；**worker**：Consume + 补偿（及补偿所需 Publish） |
| Connection | 每个进程各自 `Dial` 一条连接；进程内 Publisher/Consumer 可共享该连接 |
| Channel 按角色拆分 | Publisher 1 条懒创建 Channel；Consumer **每个消费会话 1 条** Channel |

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

生产/消费启动时都会 **幂等** `declareTopology`（Declare + Bind），无需单独运维脚本预建（多 worker 会各自 declare，参数一致即可）。

### 3.2 环境变量

见 `.env.example`：

| 变量 | 含义 | 默认 |
| ---- | ---- | ---- |
| `RABBITMQ_URL` | AMQP 连接串 | `amqp://guest:guest@localhost:5672/` |
| `RABBITMQ_BI_EXCHANGE` | 交换机名 | `bi_exchange` |
| `RABBITMQ_BI_QUEUE` | 队列名 | `bi_queue` |
| `RABBITMQ_BI_ROUTING_KEY` | 路由键 | `bi_routingKey` |
| `RABBITMQ_PREFETCH` | **每个**消费 Channel 的 QoS（未 ack 上限） | `1` |
| `RABBITMQ_WORKERS` | 并行消费会话数（独立 Channel 数） | `2` |

Compose 内 app 使用 `RABBITMQ_URL=amqp://...@rabbitmq:5672/`；本机 Go 进程用 `localhost`。

#### Prefetch × Workers

```text
进程内最大未确认消息数 ≈ RABBITMQ_WORKERS × RABBITMQ_PREFETCH
进程内并行 AI 任务数   ≈ RABBITMQ_WORKERS
                         （每 worker 串行处理；prefetch>1 只是管道预取）
```

| 场景建议 | Workers | Prefetch | 说明 |
| -------- | ------- | -------- | ---- |
| 默认 / 轻量 | 2 | 1 | 公平分发，易推理 |
| 提高吞吐 | 4～8 | 1 | 先加 worker，再考虑 prefetch |
| 单 worker 调试 | 1 | 1 | 行为最简单 |

AI 调用重、耗时长时，优先用 **Workers** 表达并行度，**Prefetch 保持 1** 通常足够。

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
| `internal/infra/mq/rabbit/config.go` | 读环境（含 Workers/Prefetch）、校验 |
| `internal/infra/mq/rabbit/dial.go` | `Dial` → Connection + Config |
| `internal/infra/mq/rabbit/topology.go` | 声明 exchange / queue / bind |
| `internal/infra/mq/rabbit/publisher.go` | 实现 `EnqueueGen`（懒 Channel） |
| `internal/infra/mq/rabbit/consumer.go` | 多 worker 循环、手动 ack |
| `internal/module/chart/service.go` | `GenerateAsync` / `ProcessGenJob` / `CompensateStaleJobs` |
| `cmd/server/main.go` | API：迁移、HTTP、Publisher |
| `cmd/worker/main.go` | Worker：Consumer、补偿 cron |

---

## 5. 连接（Dial）与 Connection / Channel

```go
conn, cfg, err := rabbit.Dial()
// Fatal if err
defer conn.Close()
```

1. `loadConfig` + `validate`
2. `amqp.Dial(URL)`：TCP + AMQP 握手
3. 返回 **Connection** 与 **同一份 Config**（保证生产/消费拓扑名一致）

| 对象 | 含义 | 本项目用法 |
| ---- | ---- | ---------- |
| **Connection** | 进程级 TCP 长连接（相对贵） | `Dial` 一次，main `defer Close` |
| **Channel** | 连接上的逻辑会话（相对便宜） | Pub 1 条；Consumer **每 worker 1 条** |

约定：

- **关掉 Channel ≠ 关掉 Connection**
- **关掉 Connection = 其上所有 Channel 失效**
- Publisher / 各 Consumer worker **不共享**同一条 Channel（避免并发打同一 Channel）
- 当前 **不做 Connection 级自动重拨**；Consumer 在已有 conn 上做 **Channel 会话** 退避重建。conn 彻底断开时需进程重启（或后续增强）。

---

## 6. 生产者（Publisher）

### 6.1 职责

实现 `port.ChartGenQueue`：

```go
EnqueueGen(ctx context.Context, chartID int64) error
```

向 `bi_exchange` 按 `routingKey` 发布持久化 JSON：`{"chartId":n}`。

### 6.2 行为摘要

| 点 | 行为 |
| -- | ---- |
| Channel | `lazyChannel`：无或已关则新建 + `declareTopology` |
| 并发 | `mu` 保护 channel 指针的创建/关闭；发送路径需注意与 Channel 非并发安全约束 |
| 持久化 | `DeliveryMode: Persistent` |
| Close | 只关 publish Channel，**不关**共享 Connection |
| Confirm | **未**启用 publisher confirm（入队成功以写出帧为准；可靠性另靠 DB 状态 + 补偿） |

业务侧：`GenerateAsync` 若 `EnqueueGen` 失败，会将任务标 `failed` 并返回错误。

---

## 7. 消费者（Consumer）— 多 Channel 并行

### 7.1 模型（方案 C）

```text
                    ┌─ worker 0: Channel + Consume + 串行 handle/ack ─┐
  bi_queue ─────────┼─ worker 1: Channel + Consume + 串行 handle/ack ─┼─► ProcessGenJob
  (竞争投递)         └─ worker N-1: …                                  ┘
         ▲
         │ 共享
   *amqp.Connection（Dial）
```

| 设计点 | 说明 |
| ------ | ---- |
| 并行单元 | **AMQP 消费会话（Channel）**，不是共享 Channel 上的业务线程池 |
| 每 worker | 独立 `loop` → `consumeOnce` → 独立 Channel / QoS / Consume |
| 处理 | 同 goroutine 内 `handleDelivery` → `Ack`/`Nack`（Channel 安全） |
| consumer tag | 传空字符串，由 broker 生成，避免多实例/重连冲突 |
| 拓扑 | 每会话启动时幂等 `declareTopology` |

**刻意不做的：**

- 单 Channel + worker pool 业务并行再回流 ack（方案 B）— 复杂度高，AI 场景收益有限  
- 旁路 goroutine 在 cancel 时强关 Channel — 业务已全程透传 ctx，靠 `select` + 下游取消即可  

### 7.2 生命周期

```go
c, err := rabbit.NewConsumer(conn, cfg, chartSvc.ProcessGenJob)
// workers = cfg.Workers（<=0 则 1）
err = c.Start(ctx)   // 拉起 N 个后台 loop
defer c.Close()      // cancel + wg.Wait，等所有 worker（含 in-flight）退出
```

| API | 行为 |
| --- | ---- |
| `NewConsumer` | 校验 conn/handle；规范化 workers |
| `Start` | 只允许一次；派生 `runCtx`；`for id := 0..workers-1` 启动 `loop` |
| `Close` | 锁外取出 `cancel` → `cancel()` → **同步** `wg.Wait()`；不关 Connection |

关停路径：

```text
Close/cancel
  → 各 worker 若在 select 等消息：<-ctx.Done() 退出会话
  → 若在 handle：jobCtx 随父 ctx 取消 → DB/HTTP 返回 → Nack(requeue) 或收尾
  → defer ch.Close()
  → wg.Done；全部结束后 Close 返回
```

要求：`ProcessGenJob` 及 repo/HTTP **遵守 ctx**，关停才不会长时间卡住。

### 7.3 单 worker 会话（`loop` / `consumeOnce`）

```text
loop:
  consumeOnce
    Channel() → declareTopology → Qos(prefetch) → Consume
    for select:
      ctx.Done           → return nil（正常停）
      deliveries 关闭
        · ctx 已取消     → return nil
        · 否则           → return error → loop 退避重连
      消息               → handleDelivery（同步）
  若 error 且 ctx 未取消:
    会话曾稳定运行 >30s → backoff 重置为 1s
    sleep backoff（指数，上限 30s）再开会话
```

说明：

- **重的是 Channel 会话重建**，不是 `amqp.Dial` 重连。  
- `deliveries` 因 broker/网络异常关闭 → 打 `rabbit.consume_error` 后退避再 `consumeOnce`。  

### 7.4 单条消息（`handleDelivery`）

| 步骤 | 行为 |
| ---- | ---- |
| 解析 body | JSON `chartId` 或纯数字；失败 → `Nack(requeue=false)` 丢弃 |
| 超时 | `context.WithTimeout(ctx, 3m)` 包一层再调 `handle` |
| `handle` 返回 nil | `Ack` |
| `handle` 返回 error 且父 ctx 未取消 | 日志 `rabbit.job_requeue` + `Nack(requeue=true)` |
| `handle` 返回 error 且正在关停 | 静默 `Nack(requeue=true)`，减少噪音 |

### 7.5 与 `ProcessGenJob` 的 ack 约定

| 回调结果 | 含义 | Consumer 动作 |
| -------- | ---- | ------------- |
| `nil` + succeed | 生成成功 | Ack |
| `nil` + 已 `failJob` | AI/数据等业务失败，已标 failed | Ack（**不要 requeue**） |
| `nil` + 已是 succeed/failed | 幂等跳过 | Ack |
| `error` | DB 等瞬时错误（或 ctx 取消） | Nack requeue |

状态在 DB，消息只带 ID → 消费可幂等；补偿任务兜底「wait 滞留 / running 超时」。

### 7.6 日志 event（消费侧）

| event | 含义 |
| ----- | ---- |
| `rabbit.consume_workers_start` | N 个 worker 已拉起 |
| `rabbit.consume_start` | 某 worker 会话（Channel）已 Consume |
| `rabbit.consume_error` | 某 worker 会话异常结束，将退避重连 |
| `rabbit.bad_message` | 非法 body，丢弃 |
| `rabbit.job_requeue` | 业务瞬时失败，requeue |
| `rabbit.ack_error` | Ack 失败 |
| `rabbit.publisher_ready` | API 侧 Publisher 就绪 |
| `worker.ready` | Worker 侧 Consumer + 补偿就绪（含 prefetch/**workers**） |

---

## 8. 补偿任务

`cmd/worker` 注册 cron（每分钟）：

```go
chartSvc.CompensateStaleJobs(ctx)
```

| 状态 | 条件（约） | 动作 |
| ---- | ---------- | ---- |
| `running` | `updated_at` 过旧 | 标 `failed`（执行超时） |
| `wait` | `updated_at` 过旧 | 再次 `EnqueueGen` 补投 |

用于：投递后消费未执行、进程崩溃、关机 requeue 风暴后的兜底等。

> 多副本 `worker` 时补偿可能并发执行；依赖 `ProcessGenJob` 对终态幂等与 DB 更新兜底。若以后要严格单飞，可加分布式锁（现有 `port.Locker`）。

---

## 9. 装配顺序

### 9.1 cmd/server（API）

```text
1. DB.Migrate
2. Dial → NewPublisher
3. NewService(..., mqPub)   // HTTP 异步投递；同步 gen 仍用 AI
4. HTTP Listen
```

关停：`Publisher.Close` → `Connection.Close`（及 DB/Redis）。

### 9.2 cmd/worker（消费）

```text
1. Dial → NewPublisher          // 补偿补投
2. NewService(..., mqPub)       // limiter=nil
3. NewConsumer(..., ProcessGenJob) → Start   // RABBITMQ_WORKERS
4. 注册 CompensateStaleJobs cron
5. 阻塞等待 signal（ctx.Done）
```

关停（defer LIFO）：

```text
Consumer.Close → scheduler.Stop → Publisher.Close → Connection.Close
```

核心依赖失败（含 Dial / AI）→ `logger.Fatal`，不做「无 MQ 半残启动」。

### 9.3 本地双进程

```bash
# 依赖
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d

# 终端 1
go run ./cmd/server

# 终端 2（异步 chart 必需，否则任务会一直 wait，直到补偿也因无 worker 无效）
go run ./cmd/worker
```

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
| 消费 | `@RabbitListener` 同进程 | **独立** `cmd/worker` 多 Channel 消费 |
| 并行 | 视监听容器并发 | `RABBITMQ_WORKERS` × 独立 Channel |
| 编排 | 大量逻辑在 Consumer | 编排在 `ProcessGenJob` |
| 建拓扑 | 手写 BiInitMain | 运行时 `declareTopology` |

---

## 12. 排障速查

| 现象 | 可能原因 |
| ---- | -------- |
| 启动 Fatal dial | Rabbit 未起、URL/端口/账号错误 |
| 异步一直 wait | **未启动 `cmd/worker`**、队列名不一致、workers=0（已兜底为 1）、处理卡住 |
| 并发上不去 | `RABBITMQ_WORKERS` 过小；或误以为只调大 prefetch 就能并行（每 worker 仍串行） |
| 单机 AI 打满 | workers × 任务过重；下调 `RABBITMQ_WORKERS` |
| 反复 requeue | `ProcessGenJob` 持续返回 error（查 DB/日志 `rabbit.job_requeue`） |
| 直接 failed | 投递失败、AI 失败、补偿超时 |
| 关停很慢 | in-flight AI 未及时响应 ctx；查下游是否透传 cancel |
| 管理台无队列 | 尚未有进程 declare；先成功启动 server 或 worker（二者均会 declare） |

---

## 13. 演进备忘（非当前实现）

- [x] 拆 `cmd/worker`：API 只发消息，Worker 只消费 + 补偿  
- 死信队列（DLX）替代无限 requeue  
- **Connection 级**自动重拨 + 安全替换共享 conn  
- Publisher confirm / 发布端更强投递保证  
- 补偿任务多副本单飞（分布式锁）  
- 跨模块 **领域事件** 总线（与当前「图表任务命令队列」分层，勿混为一谈）  

通用任务池（`internal/pkg/worker`）可用于其它批处理场景；**当前 MQ 消费路径不依赖它**（并行已由多 Channel / 多 worker 进程表达）。

---

## 14. 相关文档

- [REDIS_CACHE.md](./REDIS_CACHE.md) — Redis 角色划分  
- [REFACTORING_PLAN.md](./REFACTORING_PLAN.md) — 重构与 Phase 分期  
- `.env.example` — 环境变量模板  
- `docker-compose.yml` / `docker-compose.dev.yml` — RabbitMQ 服务与端口  
