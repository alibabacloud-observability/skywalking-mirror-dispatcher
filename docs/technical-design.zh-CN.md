# SkyWalking Mirror Dispatcher 技术方案

[English](technical-design.md) | [简体中文](technical-design.zh-CN.md) | [README](../README.zh-CN.md)

## 1. 目标

SkyWalking Agent 通常只配置一个 `collector.backend_service`。本服务让 Agent 继续以客户本地 OAP 为权威后端，同时把受支持的 SkyWalking v3 数据尽力复制到阿里云标准 ARMS SkyWalking endpoint。

方案建立在两个不变量上：

1. Agent 只能观察到 OAP 的 response、metadata、status 和 `Commands`。
2. ARMS 工作的并发和内存都有明确上限，不能对 OAP 主链路施加反压。

服务是独立、无状态的协议转发器，不持久化telemetry，也不把payload转换为其他协议或数据模型。

## 2. 范围

首版只支持 16 个 generated SkyWalking v3 service、共 29 个 unary 或 client-streaming RPC。依赖官方 `skywalking.apache.org/repo/goapi`，版本固定为 `v0.0.0-20260521015734-5c05525a3cce`。

明确不支持：

- SkyWalking v2、无 package compatibility API、v10 和 HTTP 接入；
- OTLP 转换或 payload 变换；
- 对 server-streaming 和 bidi-streaming 的兼容承诺；
- 应用层重试、WAL、补偿和 exactly-once；
- 多 OAP/ARMS 目标或按租户路由；
- mTLS、自定义 SNI 和任意 metadata 重写。

## 3. 架构

```text
                                  权威端
                              +----------------+
                              |   客户 OAP     |
                              +----------------+
                                       ^
                                       | generated gRPC client
                                       |
+------------------+  generated  +-----+-------------------+
| SkyWalking Agent | ----------> | skywalking-mirror       |
+------------------+    v3       |                       +--+---- 有界 worker/queue
                                  +-----------------------+  |
                                                             v
                                                  +----------------+
                                                  | 阿里云 ARMS    |
                                                  +----------------+
                                                     best-effort 副本
```

二进制包含四个主要层次：

- `internal/policy`：固定的 29 方法路由策略；
- `internal/proxy`：generated service adapter 和共享 typed relay helper；
- `internal/upstream`：静态 OAP/ARMS gRPC 连接和 transport credentials；
- `internal/app`：gRPC/Admin listener、指标和进程生命周期。

系统没有通用 unknown-service proxy。每个允许的方法都通过官方 generated `Register*Server` 注册，并调用匹配的 generated client 方法。

## 4. Typed 转发语义

入站 protobuf message 由官方 generated server 接口解码，再传给对应的 generated OAP/ARMS client。mirror 不主动修改 message。

协议契约保持业务语义，不保证序列化字节一致。protobuf 正常解码再编码时，字段顺序等合法编码细节可能变化；要求是 generated gRPC/protobuf 对端能解析出相同 typed value。系统不使用 raw codec、`ForceCodec`、byte 比较或额外 unknown-field 兼容层。

未知或未注册方法由 grpc-go 直接返回 `UNIMPLEMENTED`，且不会创建任何 upstream RPC。

## 5. 路由策略

以下 20 个数据和元数据 RPC 使用 OAP 主链路，并尽力复制到 ARMS：

| Service | 旁路方法 |
|---|---|
| `TraceSegmentReportService` | `collect`, `collectInSync` |
| `SpanAttachedEventReportService` | `collect` |
| `ManagementService` | `reportInstanceProperties`, `keepAlive` |
| `JVMMetricReportService` | `collect` |
| `CLRMetricReportService` | `collect` |
| `MeterReportService` | `collect`, `collectBatch` |
| `LogReportService` | `collect` |
| `EventService` | `collect` |
| `BrowserPerfService` | `collectPerfData`, `collectWebVitalsPerfData`, `collectResourcePerfData`, `collectWebInteractionsPerfData`, `collectErrorLogs` |
| `ServiceMeshMetricService` | `collect` |
| `EBPFAccessLogService` | `collect` |
| `EBPFProcessService` | `reportProcesses`, `keepAlive` |

以下 9 个控制或任务耦合 RPC 只发送给 OAP：

- `ConfigurationDiscoveryService/fetchConfigurations`
- `ProfileTask/getProfileTaskCommands`
- `ProfileTask/collectSnapshot`
- `ProfileTask/goProfileReport`
- `ProfileTask/reportTaskFinish`
- `EBPFProfilingService/queryTasks`
- `EBPFProfilingService/collectProfilingData`
- `ContinuousProfilingService/queryPolicies`
- `ContinuousProfilingService/reportProfilingTask`

路由表必须显式维护，避免未来升级 goapi 时把新增控制方法静默发送到 ARMS。

## 6. OAP 权威主链路

对于 unary RPC，handler 启动可选 ARMS worker，调用匹配的 OAP generated client，并且只返回 OAP response。OAP application header、trailer 和最终 gRPC status 都会传播给 Agent。

对于 client stream，每条 message 按接收顺序发送给 OAP。收到 Agent EOF 后，通过 `CloseAndRecv` half-close OAP stream，并且只返回该调用的 response。Agent 取消会取消 OAP context；OAP 失败会取消关联的 ARMS 工作，并把错误直接返回 Agent。

grpc-go resolver/service-config retry 已禁用，relay 也不执行应用层重试。因此一个入站 RPC 至多创建一个逻辑 OAP RPC。

## 7. ARMS Best-effort 旁路

ARMS 隔离使用两类有界资源：

- 进程级 non-blocking semaphore 限制镜像 RPC 并发数；
- 每个准入的 client stream 拥有一个有界 typed message queue。

unary request 交给独立 worker，并使用唯一的 `ARMS_FINISH_TIMEOUT`。handler 返回 OAP 结果前不等待 worker。

对于 client streaming，每条 message 先发送 OAP，然后以 non-blocking 方式尝试放入 ARMS queue。queue 满时只取消 ARMS stream并记录 `dropped`，后续 message 仍继续进入 OAP。观察到 Agent EOF 后关闭 queue，并用一个 finish timeout 限制剩余的 `Send` 和 `CloseAndRecv`。

semaphore 满时副本记为 `skipped`；ARMS RPC 报错记为 `failed`；副本完整结束记为 `succeeded`。ARMS response 会被读取以结束 HTTP/2 stream，随后丢弃。系统不重试、不持久化、不回放。

## 8. Metadata、鉴权与 TLS

三段链路采用不同的信任规则：

| 链路 | Transport 与 metadata 行为 |
|---|---|
| Agent → mirror | 支持 plaintext 或 server TLS。mirror 不鉴权，因此 listener 必须位于可信网络。 |
| mirror → OAP | 支持 plaintext 或 TLS及可选自定义 CA。直接复制非保留入站 application metadata，包括 Agent 的 `Authentication`；没有独立 OAP token 配置。 |
| mirror → ARMS | 强制 TLS。唯一的出站 application metadata 是 `Authentication=<ARMS_AUTHENTICATION>`；不复制入站 authentication、authorization、cookie 或其他 metadata。 |

ARMS token 是启动必填项，但配置摘要只记录它是否已设置。token 和私钥不会写入日志，也不会成为指标标签。Kubernetes 通过 ConfigMap 注入 endpoint，通过 Secret 引用注入 ARMS token。

## 9. 资源上限与生命周期

系统在创建 upstream 前获取独立的 non-blocking 入站 semaphore。达到上限时，新注册 RPC 返回 `RESOURCE_EXHAUSTED`，已有调用继续执行。

进程在打开 listener 前校验必填 endpoint/token、监听地址、已启用证书文件、message size、并发/queue 参数和 timeout。

收到 SIGTERM 后：

1. readiness 变为失败并拒绝新 RPC；
2. gRPC server 在 `DRAIN_TIMEOUT` 内排空已接收调用；
3. 取消残余 ARMS worker；
4. 关闭 Admin server 和 upstream 连接。

Kubernetes termination grace period 大于默认 drain timeout。

## 10. 健康检查与可观测性

- `/healthz` 表示进程和 server 存活；
- `/readyz` 表示 listener 已绑定且进程未进入 drain；
- `/metrics` 暴露 Prometheus 指标。

readiness 不依赖 OAP 或 ARMS channel 状态。OAP 不可用应作为权威 Agent 错误体现，而不是让所有副本同时反复退出服务；ARMS 不可用更不能影响 Agent 流量。

低基数指标包括：

- `skywalking_mirror_oap_rpc_total{method,code}`
- `skywalking_mirror_arms_rpc_total{method,result}`
- `skywalking_mirror_inflight{target}`
- `skywalking_mirror_oap_duration_seconds{method}`

method 标签只能来自固定注册表。endpoint、token 和用户输入不会成为 label。

## 11. 故障行为

| 故障 | Agent 可见结果 | 副本行为 |
|---|---|---|
| OAP 返回错误 | 相同的 OAP status/details/trailer | 仍在执行的 ARMS 工作被取消 |
| ARMS 返回 `UNIMPLEMENTED`、鉴权或网络错误 | OAP 结果不变 | 记录 `failed`，不重试 |
| ARMS 并发已满 | OAP 结果不变 | 记录 `skipped` |
| ARMS stream queue 已满 | OAP 结果不变 | 记录 `dropped`，只取消该副本 |
| Agent 取消 | 取消 OAP 调用 | 取消关联 ARMS worker |
| 进程 drain | 已接收 OAP 调用获得 drain 预算 | 退出前取消残余 ARMS 工作 |

方案明确接受过载或故障时 ARMS 副本不完整。如果未来需要最终送达，应单独设计持久 outbox，而不是隐式扩展这个 relay。

## 12. 部署与升级

随附镜像使用固定 digest 的多阶段构建，最终层只有静态链接二进制和非 root distroless runtime。默认 Kubernetes Service 为 `ClusterIP`，只暴露面向 Agent 的 gRPC 端口；Admin 端口保留在 Pod 内供探针和抓取使用。

协议升级必须显式执行：

1. 更新固定的官方 goapi 版本；
2. 审查 generated service descriptor 和 RPC cardinality 变化；
3. 有意识地更新注册和 20/9 路由策略；
4. 执行 descriptor、transport、故障隔离、race 和部署测试；
5. 所有必需检查通过后再发布协议升级。

ARMS 对 trace 以外方法的实际支持程度取决于所选地域 endpoint。真实 smoke 只描述观察到的支持情况，不改变“ARMS 失败与 OAP 隔离”的规则。
