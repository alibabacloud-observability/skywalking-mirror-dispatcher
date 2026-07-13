# Validation status

## Automated verification

The repository verifies the pinned official goapi version, the 16-service/29-RPC descriptor surface, the 20/9 routing split, representative real gRPC transport calls, OAP authority, metadata isolation, bounded ARMS failure behavior, health/readiness and graceful drain.

Validated on 2026-07-13:

- `go list -m skywalking.apache.org/repo/goapi` returned `v0.0.0-20260521015734-5c05525a3cce`.
- Formatting, `go test ./...`, `go test -race ./...` and `go vet ./...` passed. The Darwin race build emitted linker warnings about `LC_DYSYMTAB`; all test binaries completed successfully.
- The generated-client integration tests passed for unary, client-streaming, OAP-only and ARMS error paths. These tests are the minimal Agent-to-mirror-to-fake-OAP/ARMS traffic check.
- The pinned multi-stage image built and ran as `65532:65532`; both listeners, `/healthz`, `/readyz` and the fixed-label metrics endpoint responded.
- `kubeconform v0.7.0 -strict` reported all three Kubernetes resources valid.
- A source, manifest and log-oriented credential pattern scan found no embedded Alibaba Cloud access key, private key or ARMS token value. Third-party module checksums are excluded because random checksum text is not credential material.

## Real ARMS endpoint smoke

Status: `NOT-RUN`

Date: 2026-07-13

Reason: no authorized ARMS endpoint/token and isolated test application were supplied to this workspace. The real smoke is intentionally not a code-completion gate. When credentials are available, run one `TraceSegmentReportService/collect` client stream and one `ManagementService/reportInstanceProperties` unary call, recording only normalized gRPC status codes and no endpoint or token values.
