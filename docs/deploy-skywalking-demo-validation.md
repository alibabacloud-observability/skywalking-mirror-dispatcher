# SkyWalking demo stack — validation record

This records how the [deploy-skywalking-demo](deploy-skywalking-demo.md) stack
was validated end to end, and the conclusions. It is location-agnostic; the
procedure reproduces on any Docker host.

## Scope

Confirm the full path works:

```text
SkyWalking Java Agent (demo) -> skywalking-mirror -> OAP (authoritative) -> BanyanDB -> UI
                                       \-----------> Alibaba Cloud ARMS (best-effort copy)
```

Specifically:

- the `mirror` image starts and stays up as the **nonroot** distroless user;
- the **OAP leg** (authoritative) accepts every SkyWalking v3 method;
- the **ARMS leg** works over **plaintext** gRPC on port `8000`;
- the demo service and its traces show up via OAP GraphQL and the UI.

## Environment

- Linux x86_64 Docker host, Docker Engine + Compose v2, behind a restricted
  (China) network — images pulled through registry mirrors, the mirror image
  built locally (see [Restricted networks](deploy-skywalking-demo.md#restricted-networks)).
- Backend: `banyandb:0.7.0`, `oap-server:10.1.0`, `ui:10.1.0`.
- `mirror` built from the repo root `Dockerfile` (binary at `/app`, user `65532`).
- `demo` with the SkyWalking Java agent, `SW_AGENT_NAME=skywalking-mirror-demo`,
  reporting to `mirror:11800`.
- ARMS: SkyWalking v3 endpoint `tracing-analysis-dc-<region>.aliyuncs.com:8000`,
  **plaintext** (`ARMS_TLS` left unset — default `false`).

## Procedure

1. Bring up the stack per the deploy guide: backend, then `mirror` (with
   `mirror.env` filled on the host), then `demo`.
2. Confirm the mirror process identity and health:

   ```bash
   docker inspect -f '{{.Config.User}} {{.Config.WorkingDir}} {{json .Config.Entrypoint}}' skywalking-mirror:local
   # expect: 65532:65532 /app ["/app/skywalking-mirror"]
   docker compose --profile mirror ps mirror      # STATUS: Up (not Restarting)
   curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18081/healthz   # 200
   ```

3. Generate traffic (see
   [Generate test traffic](deploy-skywalking-demo.md#generate-test-traffic)),
   then query OAP and the mirror metrics.

## Verification & results

| Check | Command / signal | Result |
|-------|------------------|--------|
| Mirror runs as nonroot, no crash-loop | `docker compose ps mirror` + `docker inspect .Config.User` | `Up`, user `65532:65532`, entrypoint `/app/skywalking-mirror`, `healthz=200` |
| OAP leg (authoritative) | `skywalking_mirror_oap_rpc_total` | every method `code="OK"` (TraceSegmentReport, JVMMetricReport, MeterReport, EventService, ConfigurationDiscovery, ProfileTask) |
| ARMS leg (plaintext `:8000`) | `skywalking_mirror_arms_rpc_total{result="succeeded"}` | TraceSegmentReport and JVMMetricReport `succeeded` and increasing (e.g. 381 / 322 after sustained traffic) |
| Service registered | GraphQL `getAllServices` | returns `skywalking-mirror-demo` |
| Traces present | UI / trace query | `GET:/hello`, `GET:/work` per request |

## Conclusions

- **The stack is correct end to end.** OAP is the authoritative leg (all `OK`),
  and the ARMS best-effort copy succeeds for trace and JVM-metric reporting.
- **ARMS uses plaintext gRPC on port `8000`.** No `ARMS_TLS` is needed for the
  Alibaba Cloud v3 endpoint; leaving it unset (default `false`) is correct.
  Setting `ARMS_TLS=true` against the plaintext port — or omitting it against a
  TLS port — produces `Unavailable` for every method. This was the root cause of
  the earlier all-`Unavailable` ARMS symptom.
- **The mirror runs as the nonroot distroless user.** The repo `Dockerfile`
  places the binary in `/app` (chowned to `65532`), so the file log at
  `/app/skywalking-mirror.log` is writable and startup succeeds without any root
  override. A custom image that puts the binary at `/` reintroduces the
  crash-loop — see [Troubleshooting](deploy-skywalking-demo.md#troubleshooting).

## Non-blocking observations

- **`EventService` / `MeterReportService` occasionally show ARMS `failed`** (a
  single count) while the same methods are `OK` on OAP. This is an ARMS-side
  rejection of those payloads, not a transport/TLS problem; trace and JVM metrics
  are unaffected.
- **Aggregation delay (~30s).** A newly registered service is not visible
  immediately; OAP needs one metrics window. If `SW_AGENT_NAME` was changed and
  the demo recreated, the previous name lingers in query results until its time
  window expires — both names coexisting for a while is expected.

## See also

- [deploy-skywalking-demo.md](deploy-skywalking-demo.md) — deployment and
  verification guide (with restricted-network overrides and troubleshooting).
