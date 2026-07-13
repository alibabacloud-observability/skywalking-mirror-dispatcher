# SkyWalking Mirror Dispatcher — Technical Design

[English](technical-design.md) | [简体中文](technical-design.zh-CN.md) | [README](../README.md)

## 1. Purpose

A SkyWalking Agent normally has one `collector.backend_service`. This service lets the Agent keep the customer OAP as its authoritative backend while sending a best-effort copy of supported SkyWalking v3 data to the standard Alibaba Cloud ARMS SkyWalking endpoint.

The design is based on two invariants:

1. The Agent observes only the OAP response, metadata, status and `Commands`.
2. ARMS work has bounded concurrency and memory and never applies backpressure to the OAP path.

The service is a stateless protocol relay. It does not participate in the parent repository's raw store, pipeline, SLS routing or Gateway data model.

## 2. Scope

The first version supports exactly 16 generated SkyWalking v3 services and 29 unary or client-streaming RPCs. It uses the official `skywalking.apache.org/repo/goapi` module pinned at `v0.0.0-20260521015734-5c05525a3cce`.

It intentionally excludes:

- SkyWalking v2, package-less compatibility APIs, v10 and HTTP ingestion;
- OTLP conversion or payload transformation;
- server-streaming and bidirectional-streaming compatibility promises;
- application retries, WAL, compensation and exactly-once delivery;
- multiple OAP/ARMS targets or tenant-based routing;
- mTLS, custom SNI and arbitrary metadata rewriting.

## 3. Architecture

```text
                                  authoritative
                              +--------------------+
                              |   customer OAP     |
                              +--------------------+
                                       ^
                                       | generated gRPC client
                                       |
+------------------+  generated  +-----+-------------------+
| SkyWalking Agent | ----------> | skywalking-mirror       |
+------------------+    v3       |                       +--+---- bounded worker/queue
                                  +-----------------------+  |
                                                             v
                                                  +--------------------+
                                                  | Alibaba Cloud ARMS |
                                                  +--------------------+
                                                     best-effort copy
```

The binary has four main layers:

- `internal/policy`: the fixed 29-method routing policy;
- `internal/proxy`: generated service adapters and shared typed relay helpers;
- `internal/upstream`: static OAP/ARMS gRPC connections and transport credentials;
- `internal/app`: gRPC/Admin listeners, metrics and process lifecycle.

There is no generic unknown-service proxy. Every accepted method is registered through the official generated `Register*Server` function and calls the matching generated client method.

## 4. Typed forwarding semantics

Inbound protobuf messages are decoded by the official generated server interface and passed to the corresponding generated OAP/ARMS client. The mirror does not intentionally modify the message.

The contract preserves business semantics, not serialized bytes. A normal protobuf decode followed by encode may produce different wire bytes because field order and other legal encoding details are not stable. The requirement is that generated gRPC/protobuf peers parse the same typed values. No raw codec, `ForceCodec`, byte comparison or extra unknown-field compatibility layer is used.

Unknown or unregistered methods are rejected by grpc-go with `UNIMPLEMENTED` before any upstream RPC is created.

## 5. Routing policy

Twenty data and metadata RPCs use OAP plus an ARMS best-effort copy:

| Service | Mirrored methods |
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

The table is explicit so that a future goapi upgrade cannot silently send a new control method to ARMS.

## 6. OAP authoritative path

For a unary RPC, the handler starts an optional ARMS worker, calls the matching OAP generated client and returns only the OAP response. OAP application headers, trailers and final gRPC status are propagated to the Agent.

For a client stream, every message is sent to OAP in receive order. After Agent EOF, the relay half-closes the OAP stream with `CloseAndRecv` and returns only its response. Agent cancellation cancels the OAP context. An OAP failure cancels the associated ARMS work and is returned directly to the Agent.

grpc-go resolver/service-config retry is disabled, and the relay performs no application-level retry. One inbound RPC therefore creates at most one logical OAP RPC.

## 7. ARMS best-effort path

ARMS isolation uses two bounded resources:

- a process-wide non-blocking semaphore limits concurrent mirrored RPCs;
- each admitted client stream owns one bounded typed message queue.

Unary requests are handed to an independent worker with one `ARMS_FINISH_TIMEOUT`. The handler never waits for that worker before returning the OAP result.

For client streaming, a message is sent to OAP first and then offered to the ARMS queue without blocking. A full queue cancels only the ARMS stream and records `dropped`; later messages continue to OAP. When Agent EOF is observed, the queue is closed and one finish timeout bounds remaining `Send` and `CloseAndRecv` work.

If the semaphore is full, the copy is `skipped`. An ARMS RPC error is `failed`; a completed copy is `succeeded`. ARMS responses are read to finish the HTTP/2 stream and then discarded. There is no retry, persistence or replay.

## 8. Metadata, authentication and TLS

The three legs deliberately have different trust rules:

| Leg | Transport and metadata behavior |
|---|---|
| Agent → mirror | Plaintext or server TLS. The mirror does not authenticate the Agent, so the listener must remain in a trusted network. |
| mirror → OAP | Plaintext or TLS with optional custom CA. Non-reserved incoming application metadata, including Agent `Authentication`, is copied directly. There is no separate OAP token setting. |
| mirror → ARMS | TLS is mandatory. The only outgoing application metadata is `Authentication=<ARMS_AUTHENTICATION>`. Incoming authentication, authorization, cookies and other metadata are not copied. |

The ARMS token is required at startup but is represented in configuration summaries only as a boolean “set” flag. Tokens and private keys are not logged or used as metric labels. Kubernetes injects endpoints through a ConfigMap and the ARMS token through a Secret reference.

## 9. Resource limits and lifecycle

A separate non-blocking inbound semaphore is acquired before an upstream is created. When saturated, a new registered RPC receives `RESOURCE_EXHAUSTED`; existing calls continue.

Startup validates required endpoints/token, listener addresses, enabled certificate files, message-size limits, concurrency/queue values and timeouts before opening listeners.

On SIGTERM:

1. readiness changes to not ready and new RPCs are rejected;
2. the gRPC server drains accepted calls within `DRAIN_TIMEOUT`;
3. remaining ARMS workers are canceled;
4. Admin and upstream connections are closed.

The Kubernetes termination grace period is longer than the default drain timeout.

## 10. Health and observability

- `/healthz` reports process/server liveness.
- `/readyz` reports that listeners are bound and the process is not draining.
- `/metrics` exposes Prometheus metrics.

Readiness does not depend on OAP or ARMS channel state. An unavailable OAP must remain visible as an authoritative Agent error rather than causing every replica to flap out of service; an unavailable ARMS must not affect Agent traffic.

The low-cardinality metric set is:

- `skywalking_mirror_oap_rpc_total{method,code}`
- `skywalking_mirror_arms_rpc_total{method,result}`
- `skywalking_mirror_inflight{target}`
- `skywalking_mirror_oap_duration_seconds{method}`

Method labels can only come from the fixed registry. Endpoints, tokens and user-supplied values are never labels.

## 11. Failure behavior

| Failure | Agent-visible result | Copy behavior |
|---|---|---|
| OAP returns an error | The same OAP status/details/trailer | ARMS work is canceled when still active |
| ARMS returns `UNIMPLEMENTED` or authentication/network error | Unchanged OAP result | Record `failed`, no retry |
| ARMS concurrency is full | Unchanged OAP result | Record `skipped` |
| ARMS stream queue is full | Unchanged OAP result | Record `dropped`, cancel only that copy |
| Agent cancels | OAP call is canceled | Associated ARMS worker is canceled |
| Process drains | Accepted OAP calls get the drain budget | Residual ARMS work is canceled before exit |

The accepted trade-off is incomplete ARMS copies during overload or failure. If eventual delivery becomes a requirement, it needs a separate persistent-outbox design rather than extending this relay implicitly.

## 12. Deployment and upgrade

The supplied image is a pinned multi-stage build with a statically linked binary and a non-root distroless runtime. The default Kubernetes Service is `ClusterIP` and exposes only the Agent-facing gRPC port; the Admin port remains Pod-local for probes and scraping.

Protocol upgrades are explicit:

1. update the pinned official goapi version;
2. review generated service descriptors and RPC cardinality changes;
3. update registrations and the 20/9 routing policy intentionally;
4. run descriptor, transport, failure-isolation, race and deployment tests;
5. publish the child repository commit before updating the parent gitlink.

Real ARMS support for non-trace methods depends on the selected regional endpoint. Smoke results describe observed support but do not change the rule that ARMS failure is isolated from OAP.
