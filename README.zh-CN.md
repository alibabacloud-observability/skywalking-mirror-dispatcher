# SkyWalking Mirror Dispatcher

[English](README.md) | [简体中文](README.zh-CN.md)

`skywalking-mirror` 是一个精简的 SkyWalking v3 gRPC 转发服务。客户本地 OAP 是权威后端，阿里云 ARMS 接收一份有界、尽力而为的数据副本。

```text
SkyWalking Agent
       |
       v
skywalking-mirror ------> 客户 OAP（权威 response/Commands）
       |
       +---------------> 阿里云 ARMS（best-effort 副本）
```

服务使用官方 [`skywalking.apache.org/repo/goapi`](https://github.com/apache/skywalking-goapi) generated 接口。它不把数据转换成 OTLP，不实现 raw codec，不重试应用 RPC，不持久化 WAL，也不合并 ARMS response。

## 行为语义

- Agent 只能收到 OAP 的 response、application metadata、status 和 `Commands`。
- OAP 成功后立即向 Agent 返回，不等待 ARMS。
- ARMS 并发数和每个镜像 client stream 的队列都有上限。容量耗尽时跳过或丢弃副本，不对 OAP 产生反压。
- Agent application metadata（包括 `Authentication`）会转发给 OAP。mirror 不鉴权，也没有独立 OAP token 配置。
- ARMS 只收到 `Authentication=<ARMS_AUTHENTICATION>`；Agent/OAP 凭证及其他 application metadata 不会复制到 ARMS。
- listener 必须部署在客户可信网络内。随附 Kubernetes Service 为 `ClusterIP`，不能作为公网无鉴权入口。
- 只支持下方已注册的 SkyWalking v3 unary 和 client-streaming 方法。其他 v2、compatibility、v10、HTTP 或未来方法返回 `UNIMPLEMENTED`。

## 方法策略

以下 20 个数据和元数据 RPC 同时转发给 OAP，并尽力旁路到 ARMS：

| Service | Methods |
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

以下 9 个控制或任务耦合 RPC 只转发给 OAP：

- `ConfigurationDiscoveryService/fetchConfigurations`
- `ProfileTask/getProfileTaskCommands`
- `ProfileTask/collectSnapshot`
- `ProfileTask/goProfileReport`
- `ProfileTask/reportTaskFinish`
- `EBPFProfilingService/queryTasks`
- `EBPFProfilingService/collectProfilingData`
- `ContinuousProfilingService/queryPolicies`
- `ContinuousProfilingService/reportProfilingTask`

trace 以外方法是否被 ARMS 支持取决于所选 ARMS endpoint。ARMS 返回 `UNIMPLEMENTED` 或其他错误时只记录并忽略，不改变 OAP 结果。

## 配置

所有配置都来自环境变量，不加载配置文件。

| 变量 | 必填 | 默认值 | 含义 |
|---|---:|---|---|
| `OAP_ENDPOINT` | 是 | — | 客户 OAP gRPC 地址，通常为 `host:11800` |
| `ARMS_ENDPOINT` | 是 | — | 从 ARMS 控制台复制的 SkyWalking gRPC endpoint |
| `ARMS_AUTHENTICATION` | 是 | — | ARMS SkyWalking authentication token |
| `LISTEN_ADDR` | 否 | `:11800` | 面向 Agent 的 gRPC 监听地址 |
| `ADMIN_ADDR` | 否 | `:8080` | 健康、就绪和 Prometheus 监听地址 |
| `LISTENER_TLS_CERT_FILE` | 否 | — | listener TLS 证书，必须与 key 同时配置 |
| `LISTENER_TLS_KEY_FILE` | 否 | — | listener TLS 私钥，必须与证书同时配置 |
| `OAP_TLS` | 否 | `false` | OAP 连接是否使用 TLS |
| `OAP_CA_FILE` | 否 | — | 可选 OAP CA bundle，要求 `OAP_TLS=true` |
| `GRPC_MAX_MESSAGE_BYTES` | 否 | `52428800` | 入站和出站 gRPC message 大小上限 |
| `MAX_INFLIGHT_RPCS` | 否 | `1024` | 进程级 Agent RPC 并发上限 |
| `ARMS_MAX_CONCURRENT_RPCS` | 否 | `64` | 进程级 ARMS 镜像 RPC 并发上限 |
| `ARMS_STREAM_QUEUE_SIZE` | 否 | `128` | 每个镜像 client-streaming RPC 的队列大小 |
| `ARMS_FINISH_TIMEOUT` | 否 | `5s` | ARMS unary 生命周期或 stream 收尾时间上限 |
| `DRAIN_TIMEOUT` | 否 | `30s` | 进程优雅停机预算 |
| `LOG_STDOUT` | 否 | `false` | 是否把结构化文件日志额外复制到 stdout |

ARMS 始终使用 TLS。token 不会出现在配置摘要、日志或指标标签中。

阿里云 ARMS SkyWalking 接入文档：[通过 SkyWalking 客户端上报 Java 应用数据](https://help.aliyun.com/zh/arms/tracing-analysis/use-skywalking-to-report-java-application-data)。

## 本地运行

```bash
export OAP_ENDPOINT=customer-oap.example:11800
export ARMS_ENDPOINT='<endpoint-from-arms-console>'
export ARMS_AUTHENTICATION='<token-from-arms-console>'
go run ./cmd/skywalking-mirror
```

把 Agent 指向 mirror：

```properties
collector.backend_service=skywalking-mirror.example:11800
```

如果客户 OAP 开启了 SkyWalking token 鉴权，继续在 Agent 配置 `agent.authentication`。mirror 会把该 metadata 转发给 OAP，不会把它复用到 ARMS。

管理端点：

- `GET /healthz`：进程和 server 存活状态；
- `GET /readyz`：listener 已绑定，且进程未进入 drain；
- `GET /metrics`：Prometheus 指标。

readiness 不跟随 OAP 或 ARMS channel 状态变化。

## 日志

服务使用 zap 输出结构化 JSON 日志，主输出始终是运行中二进制所在目录的 `skywalking-mirror.log`。设置 `LOG_STDOUT=true` 后，才会把相同结构化事件额外复制到 stdout；默认不写 stdout。lumberjack 在文件达到 100 MiB 时轮转，最多保留 5 个备份、最长保留 30 天，并压缩备份。二进制目录不可写时，进程会在启动阶段失败。

该文件是本地运维日志，不是持久化 telemetry 存储。容器部署可以启用 stdout 交给常规日志采集器；随附 Kubernetes 清单已显式启用。镜像中的文件路径为 `/app/skywalking-mirror.log`，Kubernetes 容器文件系统因此保持可写，Pod 删除时该文件也会删除。

## Docker

```bash
docker build -t skywalking-mirror-dispatcher:local .
docker run --rm \
  -e OAP_ENDPOINT=customer-oap.example:11800 \
  -e ARMS_ENDPOINT='<endpoint-from-arms-console>' \
  -e ARMS_AUTHENTICATION='<token-from-arms-console>' \
  -e LOG_STDOUT=true \
  -p 11800:11800 -p 127.0.0.1:8080:8080 \
  skywalking-mirror-dispatcher:local
```

最终镜像为 distroless，只包含静态链接服务和 CA bundle，并以 uid/gid `65532` 运行。非 root 用户拥有 `/app`，因此服务可以在二进制旁创建并轮转 `/app/skywalking-mirror.log`。

## Kubernetes

修改 `deploy/kubernetes.yaml` 中的 endpoint，然后创建 token Secret 并应用清单：

```bash
kubectl create secret generic skywalking-mirror-secrets \
  --from-literal=ARMS_AUTHENTICATION='<token-from-arms-console>'
kubectl apply -f deploy/kubernetes.yaml
```

gRPC Service 为 `ClusterIP`，Admin 端口仅在 Pod 内使用。容器根文件系统只因需要写入二进制同目录的轮转日志而保持可写，其他容器安全配置继续启用。回滚时让 Agent 重新指向原 OAP endpoint，然后删除 Deployment。服务无状态，不需要迁移或回放数据。

## 指标

- `skywalking_mirror_oap_rpc_total{method,code}`
- `skywalking_mirror_arms_rpc_total{method,result}`，其中 result 为 `succeeded`、`failed`、`skipped` 或 `dropped`
- `skywalking_mirror_inflight{target}`
- `skywalking_mirror_oap_duration_seconds{method}`

method 标签只来自固定的 29 个方法。用户输入、endpoint 和 token 不会成为指标标签。

## 开发与验证

Makefile是本地开发的统一入口：

```bash
make help
make check
make build
```

`make check`依次执行格式检查、固定goapi版本检查、单元/集成测试、race detector和 `go vet`。`make build`将二进制写入 `bin/skywalking-mirror`。

其他目标：

```bash
make run                                      # 使用当前环境变量
make docker-build IMAGE=skywalking-mirror:dev
make kube-validate
```

测试使用官方 generated client 和 typed fake server，覆盖代表性的 unary、client-streaming 和 OAP-only 调用，还覆盖 OAP metadata/status 权威语义、取消、过载、ARMS 阻塞/失败、队列溢出和 worker 有界退出。

架构与设计细节：[English](docs/technical-design.md) | [简体中文](docs/technical-design.zh-CN.md)。
