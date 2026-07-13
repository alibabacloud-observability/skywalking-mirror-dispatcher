# SkyWalking Mirror Dispatcher

[English](README.md) | [简体中文](README.zh-CN.md)

`skywalking-mirror` is a small SkyWalking v3 gRPC relay. A customer OAP is the authoritative backend; Alibaba Cloud ARMS receives a bounded, best-effort copy.

```text
SkyWalking Agent
       |
       v
skywalking-mirror ------> customer OAP (authoritative response/Commands)
       |
       +---------------> Alibaba Cloud ARMS (best-effort copy)
```

The relay uses the official [`skywalking.apache.org/repo/goapi`](https://github.com/apache/skywalking-goapi) generated interfaces. It does not translate data to OTLP, implement a raw codec, retry application RPCs, persist a WAL, or merge ARMS responses.

## Behavior

- The Agent receives only the OAP response, application metadata, status and `Commands`.
- A successful OAP response is returned without waiting for ARMS.
- ARMS concurrency and each mirrored client stream queue are bounded. Saturated copies are skipped or dropped without applying backpressure to OAP.
- Agent application metadata, including `Authentication`, is forwarded to OAP. The mirror does not authenticate Agents and has no OAP token setting.
- ARMS receives only `Authentication=<ARMS_AUTHENTICATION>`; Agent/OAP credentials and other application metadata are not copied to ARMS.
- The listener must stay inside the customer trusted network. The supplied Kubernetes Service is `ClusterIP` and is not a public unauthenticated endpoint.
- Only the registered SkyWalking v3 unary and client-streaming methods below are supported. Other v2, compatibility, v10, HTTP or future methods return `UNIMPLEMENTED`.

## Method policy

Twenty data and metadata RPCs are mirrored to OAP and ARMS:

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

Nine control or task-coupled RPCs go only to OAP:

- `ConfigurationDiscoveryService/fetchConfigurations`
- `ProfileTask/getProfileTaskCommands`
- `ProfileTask/collectSnapshot`
- `ProfileTask/goProfileReport`
- `ProfileTask/reportTaskFinish`
- `EBPFProfilingService/queryTasks`
- `EBPFProfilingService/collectProfilingData`
- `ContinuousProfilingService/queryPolicies`
- `ContinuousProfilingService/reportProfilingTask`

ARMS support outside trace ingestion depends on the selected ARMS endpoint. An ARMS `UNIMPLEMENTED` or other error is recorded and ignored; it never changes the OAP result.

## Configuration

All settings are environment variables. No configuration file is loaded.

| Variable | Required | Default | Meaning |
|---|---:|---|---|
| `OAP_ENDPOINT` | yes | — | Customer OAP gRPC target, normally `host:11800` |
| `ARMS_ENDPOINT` | yes | — | SkyWalking gRPC endpoint copied from the ARMS console |
| `ARMS_AUTHENTICATION` | yes | — | ARMS SkyWalking authentication token |
| `LISTEN_ADDR` | no | `:11800` | Agent-facing gRPC listen address |
| `ADMIN_ADDR` | no | `:8080` | Health, readiness and Prometheus listen address |
| `LISTENER_TLS_CERT_FILE` | no | — | Server TLS certificate; requires the key variable |
| `LISTENER_TLS_KEY_FILE` | no | — | Server TLS key; requires the certificate variable |
| `OAP_TLS` | no | `false` | Use TLS for the OAP connection |
| `OAP_CA_FILE` | no | — | Optional OAP CA bundle; requires `OAP_TLS=true` |
| `ARMS_TLS` | no | `false` | Use TLS with system CAs for the ARMS connection |
| `GRPC_MAX_MESSAGE_BYTES` | no | `52428800` | Inbound and outbound gRPC message limit |
| `MAX_INFLIGHT_RPCS` | no | `1024` | Process-wide Agent RPC limit |
| `ARMS_MAX_CONCURRENT_RPCS` | no | `64` | Process-wide mirrored RPC limit |
| `ARMS_STREAM_QUEUE_SIZE` | no | `128` | Per mirrored client-streaming RPC queue |
| `ARMS_FINISH_TIMEOUT` | no | `5s` | Maximum ARMS unary lifetime or stream finish time |
| `DRAIN_TIMEOUT` | no | `30s` | Process graceful shutdown budget |
| `LOG_STDOUT` | no | `false` | Also copy structured file logs to stdout |

All three gRPC legs use plaintext by default. Listener TLS is enabled by the certificate/key pair, OAP TLS by `OAP_TLS=true`, and ARMS TLS by `ARMS_TLS=true`. Leave `ARMS_TLS=false` for plaintext ARMS ports such as `8000` or `8090`; use `true` only with a TLS endpoint such as port `443`. The token is never included in configuration summaries, logs or metric labels.

Alibaba Cloud ARMS SkyWalking access guide: [Report Java application data with SkyWalking agent](https://www.alibabacloud.com/help/en/arms/tracing-analysis/use-skywalking-to-report-java-application-data).

## Run locally

```bash
export OAP_ENDPOINT=customer-oap.example:11800
export ARMS_ENDPOINT='<endpoint-from-arms-console>'
export ARMS_AUTHENTICATION='<token-from-arms-console>'
go run ./cmd/skywalking-mirror
```

Point the Agent at the mirror:

```properties
collector.backend_service=skywalking-mirror.example:11800
```

If the customer OAP enables SkyWalking token authentication, keep `agent.authentication` configured on the Agent. The mirror forwards that metadata to OAP and does not reuse it for ARMS.

Admin endpoints:

- `GET /healthz`: process/server liveness
- `GET /readyz`: listener is bound and the process is not draining
- `GET /metrics`: Prometheus metrics

Readiness deliberately does not follow OAP or ARMS channel state.

## Logs

The service uses zap for structured JSON logging. The primary output is always `skywalking-mirror.log` in the running binary's directory. Set `LOG_STDOUT=true` to copy the same structured events to stdout; stdout is disabled by default. The file is rotated by lumberjack at 100 MiB; five backups are retained for up to 30 days and compressed. The process fails at startup if the binary directory is not writable.

The file is local operational storage, not durable telemetry storage. Container deployments can enable stdout for their normal log collector; the supplied Kubernetes manifest does so explicitly. In the supplied image the file path is `/app/skywalking-mirror.log`; the Kubernetes container filesystem is writable for this reason, and the file is discarded with the Pod.

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

The final image is distroless, contains only the statically linked service and CA bundle, and runs as uid/gid `65532`. The non-root user owns `/app`, so the service can create and rotate `/app/skywalking-mirror.log` beside the binary.

## Kubernetes

Edit the endpoint values in `deploy/kubernetes.yaml`, then create the token Secret and apply the manifests:

```bash
kubectl create secret generic skywalking-mirror-secrets \
  --from-literal=ARMS_AUTHENTICATION='<token-from-arms-console>'
kubectl apply -f deploy/kubernetes.yaml
```

The gRPC Service is `ClusterIP`; the Admin port is Pod-only. The container root filesystem remains writable solely for the binary-adjacent rotating log file; all other container hardening settings remain enabled. To roll back, point Agents back to the original OAP endpoint and remove the Deployment. The service is stateless and has no data migration or replay step.

## Metrics

- `skywalking_mirror_oap_rpc_total{method,code}`
- `skywalking_mirror_arms_rpc_total{method,result}` where result is `succeeded`, `failed`, `skipped` or `dropped`
- `skywalking_mirror_inflight{target}`
- `skywalking_mirror_oap_duration_seconds{method}`

Method labels come only from the fixed 29-method registry. User values, endpoints and tokens are never metric labels.

## Development and verification

The Makefile is the canonical local entry point:

```bash
make help
make check
make build
```

`make check` runs formatting verification, the pinned goapi check, unit/integration tests, the race detector and `go vet`. `make build` writes `bin/skywalking-mirror`.

Additional targets:

```bash
make run                                      # uses the current environment
make docker-build IMAGE=skywalking-mirror:dev
```

The tests use official generated clients and typed fake servers for representative unary, client-streaming and OAP-only calls. They also cover OAP metadata/status authority, cancellation, saturation, ARMS blocking/failure, queue overflow and bounded worker exit.

Architecture and design details: [English](docs/technical-design.md) | [简体中文](docs/technical-design.zh-CN.md).
