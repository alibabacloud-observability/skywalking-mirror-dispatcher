# Deploy the SkyWalking demo stack

This guide brings up a complete, self-contained SkyWalking environment and a
SkyWalking-instrumented demo app, then relays the agent traffic through
`skywalking-mirror` to both the local OAP (authoritative) and Alibaba Cloud ARMS
(best-effort copy).

It is location-agnostic: no machine IP or host name is baked in. It runs on any
Docker host and has been used behind restricted (China) networks via the
override notes below.

```text
SkyWalking Java Agent (demo app)
        |  gRPC :11800
        v
   skywalking-mirror ----> OAP (authoritative) --> BanyanDB --> UI
        |
        +-----------------> Alibaba Cloud ARMS (best-effort copy)
```

## Components

| Service    | Image                                   | Purpose                                    | Ports (host) |
|------------|-----------------------------------------|--------------------------------------------|--------------|
| `banyandb` | `apache/skywalking-banyandb:0.7.0`      | Storage (standalone)                       | 17912, 17913 |
| `oap`      | `apache/skywalking-oap-server:10.1.0`   | OAP server (gRPC in-network, REST/GraphQL) | 12800        |
| `ui`       | `apache/skywalking-ui:10.1.0`           | Web UI                                      | 13800 -> 8080 |
| `mirror`   | built from repo root `Dockerfile`       | Agent entry; relays to OAP + ARMS           | 11800, 127.0.0.1:18081 |
| `demo`     | built from `deploy/demo/app`            | Spring Boot app + SkyWalking Java agent     | 18090 -> 8080 |

The OAP gRPC port `11800` is intentionally **not** published on the host so the
`mirror` can own `host:11800` as the single agent entry point. Agents connect to
`<host>:11800`.

## Files

```
deploy/demo/
├── docker-compose.yaml     # the whole stack (profiles: mirror, demo)
├── mirror.env.template     # ARMS credentials template (copy to mirror.env)
└── app/                    # SkyWalking-instrumented demo application
    ├── Dockerfile          # multi-stage build; agent downloaded via build args
    ├── pom.xml
    ├── settings.xml        # optional Maven mirror
    └── src/main/java/com/example/demo/DemoApplication.java
```

## Prerequisites

- Docker Engine + Docker Compose v2
- Outbound access to the container registries and the SkyWalking agent tarball,
  or the mirror overrides in [Restricted networks](#restricted-networks)
- ARMS SkyWalking endpoint + token (only if you enable the `mirror` profile)

## Quick start

Run from `deploy/demo/`.

### 1. Backend (BanyanDB + OAP + UI)

```bash
docker compose up -d banyandb
# wait for BanyanDB to accept gRPC (a few seconds), then:
docker compose up -d oap ui
```

Starting BanyanDB first avoids OAP retry churn while storage initialises.

Verify:

```bash
# OAP GraphQL is up (405 on GET means the endpoint is alive; it expects POST)
curl -s -o /dev/null -w '%{http_code}\n' http://<host>:12800/graphql
# UI
curl -s -o /dev/null -w '%{http_code}\n' http://<host>:13800/
```

Open the UI at `http://<host>:13800`.

### 2. Mirror (optional: relay to ARMS)

```bash
cp mirror.env.template mirror.env
# edit mirror.env on the host and fill ARMS_ENDPOINT / ARMS_AUTHENTICATION
docker compose --profile mirror up -d --build mirror
```

Check health and metrics (admin port is bound to localhost only):

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18081/healthz   # 200
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18081/readyz    # 200
curl -s http://127.0.0.1:18081/metrics | grep '^skywalking_mirror'
```

If you do not need ARMS, skip the mirror and instead publish OAP gRPC directly
by uncommenting `- "11800:11800"` under the `oap` service.

### 3. Demo app

```bash
docker compose --profile demo up -d --build demo
```

The demo reports to `mirror:11800` (in-network). If the mirror is disabled, set
`SW_AGENT_COLLECTOR_BACKEND_SERVICES: "oap:11800"` on the `demo` service instead.

### One-liner (unrestricted network)

```bash
docker compose --profile mirror --profile demo up -d --build
```

## Validate the full chain

### Generate test traffic

Each `/hello` produces a multi-span trace: `/hello` server span -> RestTemplate
client span -> `/work` server span.

```bash
# quick burst
for i in $(seq 1 150); do curl -s -o /dev/null http://<host>:18090/hello; done
```

The `demo` container must be up first (`curl http://<host>:18090/hello` returns
`200`); a freshly recreated app needs a few seconds to boot before the agent
connects.

For a stable service/topology in the UI, keep a light, sustained stream running
for a minute or two rather than a single burst:

```bash
# sustained traffic for ~120s (~10 req/s)
end=$((SECONDS+120))
while [ $SECONDS -lt $end ]; do curl -s -o /dev/null http://<host>:18090/hello; sleep 0.1; done
```

> **Aggregation delay.** OAP needs ~30s to register a service and roll up the
> first metrics window, so a new service will not appear immediately. If you
> renamed the service (`SW_AGENT_NAME`) and recreated the `demo`, the old name
> lingers in query results until its time window expires — both names coexisting
> for a while is expected.

1. **UI**: `http://<host>:13800` -> the `skywalking-mirror-demo` service appears under Services,
   with a topology node and traces (`GET:/hello`, `GET:/work`).

2. **OAP via GraphQL** (run inside the compose network):

   ```bash
   docker run --rm --network skywalking-stack curlimages/curl:8.10.1 -s \
     -X POST http://oap:12800/graphql -H 'Content-Type: application/json' \
     -d '{"query":"query{getAllServices(duration:{start:\"2026-01-01 0000\",end:\"2030-01-01 0000\",step:MINUTE}){id name}}"}'
   ```

   Expect `skywalking-mirror-demo` in the result.

3. **Mirror metrics** confirm both legs:

   ```bash
   curl -s http://127.0.0.1:18081/metrics | grep -E '^skywalking_mirror_(oap|arms)_rpc_total'
   ```

   - `skywalking_mirror_oap_rpc_total{code="OK",...}` increasing -> OAP leg works.
   - `skywalking_mirror_arms_rpc_total{result="succeeded",...}` increasing -> ARMS leg works.

## Restricted networks

The defaults pull from `docker.io` and the Apache CDN. When those are slow or
blocked (e.g. in China), override without editing tracked files.

- **Container images**: configure Docker registry mirrors in
  `/etc/docker/daemon.json` (e.g. DaoCloud, Aliyun ACR) and restart Docker.
- **SkyWalking agent tarball**: pass a regional mirror at build time. The Apache
  CDN keeps only the latest release; for pinned versions use a mirror or
  `archive.apache.org`.

  ```bash
  docker compose --profile demo build \
    --build-arg SW_AGENT_URL=https://mirrors.tuna.tsinghua.edu.cn/apache/skywalking/java-agent/9.6.0/apache-skywalking-java-agent-9.6.0.tgz
  ```

  Regional Apache mirrors that work well: `mirrors.tuna.tsinghua.edu.cn`,
  `mirrors.bfsu.edu.cn`, `mirrors.aliyun.com` (path prefix `/apache/skywalking/...`).

- **Maven**: `app/settings.xml` already uses the Aliyun public mirror; edit or
  remove it as needed.
- **Base images**: override `MAVEN_IMAGE` / `JRE_IMAGE` build args with
  mirror-prefixed tags if `docker.io` is unreachable.
- **Mirror image build**: the repo root `Dockerfile` uses a `gcr.io` distroless
  base and the Go module proxy. Behind a firewall, build with mirror-prefixed
  bases / `GOPROXY` overrides (see the repo `Dockerfile` and `Makefile`).

## Troubleshooting

- **ARMS `Unavailable` for every method, OAP is `OK`.** The ARMS transport mode
  does not match the endpoint. The mirror uses **plaintext** gRPC to ARMS by
  default, so `ARMS_TLS` should be omitted; Alibaba Cloud's SkyWalking v3 endpoint
  is plaintext on port `8000` (e.g. `tracing-analysis-dc-<region>.aliyuncs.com:8000`). Set
  `ARMS_TLS=true` only for a TLS-enabled endpoint (and use its TLS port).
  A TLS/plaintext mismatch in either direction yields `Unavailable`.
  Probe the endpoint:

  ```bash
  openssl s_client -connect <arms-host>:<port> -servername <arms-host> </dev/null
  # "unknown protocol" / "no peer certificate" -> plaintext endpoint  -> omit ARMS_TLS
  # a real certificate is presented                -> TLS endpoint     -> ARMS_TLS=true
  ```

- **OAP shows no services.** Confirm the agent reached the entry point:
  `docker logs sw-demo | grep -i skywalking` and check
  `skywalking_mirror_oap_rpc_total` (or, without the mirror, that agents target
  `oap:11800`). Allow ~30s for OAP aggregation before querying.

- **UI empty but services exist.** SkyWalking needs a completed metrics window;
  keep traffic flowing and widen the UI time range.

## Teardown

```bash
docker compose --profile mirror --profile demo down
# add -v to also remove the BanyanDB volume/state
```

## See also

- [deploy-skywalking-demo-validation.md](deploy-skywalking-demo-validation.md) —
  end-to-end validation procedure and conclusions (nonroot mirror, OAP leg, ARMS
  plaintext leg).
