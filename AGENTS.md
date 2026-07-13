# SkyWalking Mirror Dispatcher

## 项目定位

本仓提供一个无状态 SkyWalking v3 gRPC mirror。SkyWalking Agent只连接本服务；客户 OAP是唯一权威后端，阿里云 ARMS只接收有界、best-effort的数据副本。

服务只接收并转发SkyWalking v3 gRPC，不持久化telemetry，也不把payload转换为其他协议或数据模型。

## 不可破坏的语义

1. Agent只能看到 OAP返回的 response、application metadata、gRPC status和 `Commands`。
2. OAP完成后立即向 Agent返回，不得等待 ARMS。
3. ARMS阻塞、失败、限流或不支持某方法时，只影响副本，不得改变 OAP请求或 Agent结果。
4. 所有资源必须有界。不得为 ARMS增加无界 goroutine、无界 channel、WAL、补偿或隐式重试。
5. 首版只支持显式注册的 SkyWalking v3 unary和 client-streaming RPC；未知方法由 grpc-go返回 `UNIMPLEMENTED`。

## 协议实现规则

- 固定使用官方 `skywalking.apache.org/repo/goapi v0.0.0-20260521015734-5c05525a3cce`。
- 使用官方 generated `Register*Server`和对应 generated client；不得增加 `UnknownServiceHandler`、raw codec、`ForceCodec`、手写通用 `StreamDesc`或 raw fallback。
- typed转发保持 protobuf业务语义，不承诺序列化 wire bytes完全一致，也不增加 byte比较、强制 `proto.Clone`或 unknown-field round-trip门禁。
- 不主动修改 payload。一个入站 RPC至多创建一个逻辑 OAP调用和一个逻辑 ARMS调用。
- 禁用 grpc-go resolver/service-config retry，并且不实现应用层重试。
- client-streaming必须保持 message顺序和 half-close；所有 ARMS message必须在对应 message成功发送 OAP后才可尝试入队。

## 固定方法策略

以下20个 RPC发送 OAP并尝试旁路 ARMS：

- `TraceSegmentReportService/collect`
- `TraceSegmentReportService/collectInSync`
- `SpanAttachedEventReportService/collect`
- `ManagementService/reportInstanceProperties`
- `ManagementService/keepAlive`
- `JVMMetricReportService/collect`
- `CLRMetricReportService/collect`
- `MeterReportService/collect`
- `MeterReportService/collectBatch`
- `LogReportService/collect`
- `EventService/collect`
- `BrowserPerfService/collectPerfData`
- `BrowserPerfService/collectWebVitalsPerfData`
- `BrowserPerfService/collectResourcePerfData`
- `BrowserPerfService/collectWebInteractionsPerfData`
- `BrowserPerfService/collectErrorLogs`
- `ServiceMeshMetricService/collect`
- `EBPFAccessLogService/collect`
- `EBPFProcessService/reportProcesses`
- `EBPFProcessService/keepAlive`

以下9个控制或任务耦合 RPC只发送 OAP：

- `ConfigurationDiscoveryService/fetchConfigurations`
- `ProfileTask/getProfileTaskCommands`
- `ProfileTask/collectSnapshot`
- `ProfileTask/goProfileReport`
- `ProfileTask/reportTaskFinish`
- `EBPFProfilingService/queryTasks`
- `EBPFProfilingService/collectProfilingData`
- `ContinuousProfilingService/queryPolicies`
- `ContinuousProfilingService/reportProfilingTask`

新增或删除方法时，必须在同一个改动中更新 generated service注册、`internal/policy`、descriptor测试、中英文 README和中英文技术方案。不得根据方法名通配或自动旁路未来方法。

## OAP与ARMS链路

### OAP权威链路

- 除gRPC保留/transport metadata外，将Agent入站 application metadata直接复制给 OAP，包括Agent提供的 `Authentication`。
- 不提供独立 OAP token，不在mirror内校验或覆盖Agent凭证。
- 传播OAP application header、trailer、status details和最终response。
- OAP失败或Agent取消时，立即结束主调用并取消仍在执行的关联ARMS worker。

### ARMS旁路

- ARMS连接默认plaintext；只有 `ARMS_TLS=true`时使用系统CA启用TLS。无论transport模式如何，都只注入 `Authentication=<ARMS_AUTHENTICATION>`。
- 不得把入站 authentication、authorization、cookie或其他application metadata复制给ARMS。
- 使用进程级non-blocking semaphore限制ARMS RPC并发；无容量时记录 `skipped`。
- 每个准入的client stream使用独立有界typed queue；queue满时取消该副本并记录 `dropped`，OAP继续。
- unary dispatch或client-stream EOF后只使用一个 `ARMS_FINISH_TIMEOUT`限制收尾。
- ARMS response必须读取后丢弃，不能合并或返回Agent。

## 传输、安全与配置

- Agent listener、OAP和ARMS三段gRPC链路都默认plaintext；服务默认只能部署在客户可信网络，Kubernetes Service保持 `ClusterIP`。
- listener通过证书/私钥启用TLS，OAP通过 `OAP_TLS=true`启用TLS及可选CA，ARMS通过 `ARMS_TLS=true`启用使用系统CA的TLS。
- 核心环境变量是 `OAP_ENDPOINT`、`ARMS_ENDPOINT`和 `ARMS_AUTHENTICATION`。
- endpoint放入ConfigMap，只有 `ARMS_AUTHENTICATION`通过Secret引用注入。
- 日志、错误、指标、测试输出和manifest不得包含token、authorization、cookie、私钥或其他凭证明文。
- 配置摘要只能记录secret是否已设置，不得记录secret值。

## 资源、健康检查与停机

- 在创建upstream前通过non-blocking入站semaphore准入；饱和时返回 `RESOURCE_EXHAUSTED`。
- `/healthz`只表示进程/server存活。
- `/readyz`只表示listener已绑定且进程未drain，不跟随OAP或ARMS channel状态。
- `/metrics`只暴露当前四类低基数指标；method标签只能来自固定29方法注册表。
- SIGTERM顺序必须是：NotReady并拒绝新RPC、在drain timeout内等待OAP在途调用、取消残留ARMS worker、关闭Admin和upstream连接。

## 目录职责

```text
cmd/skywalking-mirror/  进程入口与signal处理
internal/app/           listener、Admin server与生命周期
internal/config/        环境变量解析、校验与脱敏摘要
internal/logging/       zap结构化日志与lumberjack文件轮转
internal/policy/        固定29方法路由表
internal/proxy/         generated clients、service adapters与typed relay
internal/telemetry/     Prometheus指标
internal/upstream/      OAP/ARMS连接与TLS
deploy/                 Kubernetes清单
docs/                   中英文技术方案
Makefile                本地构建、测试、镜像和清单校验入口
```

共享转发语义放在typed helper中，每个generated service adapter只做类型绑定。不要为每个方法复制一套转发状态机。

## 日志约定

- 业务代码统一使用 `*zap.Logger`和typed field，不得混用 `log`、`slog`或SugaredLogger。
- 二进制同目录的 `skywalking-mirror.log`始终是主输出；仅当 `LOG_STDOUT=true`时额外写stdout，默认值为 `false`。
- 文件使用lumberjack按100 MiB轮转，保留5个备份、最长30天并压缩；日志目录不可写时必须启动失败。
- 文件日志不增加回放或投递承诺；容器部署可显式启用stdout供集中采集，Kubernetes示例必须启用。
- Kubernetes因该约束允许可写root filesystem，但必须继续保持非root、禁止提权、drop capabilities和runtime-default seccomp。

## 开发与验证

提交前至少运行：

```bash
make check
```

`make check`必须保持为格式检查、固定goapi版本检查、单元/集成测试、race detector和 `go vet`的统一门禁。不要让README、CI和人工命令各自维护不同的检查集合。

协议或路由改动还必须确认：

- `go list -m skywalking.apache.org/repo/goapi`仍是固定版本；
- descriptor测试仍精确覆盖16个service、29个RPC和20/9分组；
- representative unary、client-streaming、OAP-only transport测试通过；
- ARMS阻塞、queue满、semaphore满、`UNIMPLEMENTED`、鉴权失败和不可达不改变OAP结果；
- `make docker-build`生成的镜像仍为非root；
- `make kube-validate`通过，Kubernetes清单仍不通过Service暴露Admin端口。

不要新增逐方法重复E2E、固定P99/吞吐门禁或没有真实生产依据的复杂watchdog。

## 文档约定

- `README.md`与 `README.zh-CN.md`内容必须保持等价，并保留语言导航。
- `docs/technical-design.md`与 `docs/technical-design.zh-CN.md`内容必须同步更新。
- README面向使用者，技术方案记录架构理由和故障语义；仓库维护和代理执行规则只写在本文件，不在README重复。
- 删除或重命名文档时，必须清理全部交叉引用。

## Git

- 不修改仓库现有author/committer身份；所有已配置remote使用相同commit历史和提交信息。
- 不配置隐式fan-out push；是否推送某个remote必须是显式操作。
- 不改写已经发布的 `main` 历史，除非用户明确授权force-push及其影响范围。
