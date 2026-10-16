# Running USSD Runtime in production

## How fast it is

USSD networks give each reply only a few seconds before dropping the session; Hubtel's SDK, for example, warns to answer within about 5 seconds. The runtime itself takes a millisecond or two. What matters is your backend, which is why actions have a hard `timeout_ms` (2 s default, 4 s maximum).

`cmd/loadtest` plays many callers through a flow at once. These are real numbers.

**Laptop:**
- Machine: Intel Core i7-10850H, 32 GB RAM, Windows 11.
- Setup: runtime, demo backend and load generator on the same machine; in-memory sessions; 50 concurrent callers; 15 s per row.
- Flow: the school-fees payment path (dial, 1, 1001, 1, 250, 1). Two of its six steps call the backend.

| Gateway | Requests/s | p50 | p95 | p99 | Max | Errors |
| --- | --- | --- | --- | --- | --- | --- |
| Africa's Talking | 19,438 | 1.80 ms | 7.01 ms | 9.69 ms | 38.80 ms | 0 |
| Hubtel | 18,628 | 1.89 ms | 7.19 ms | 9.82 ms | 34.56 ms | 0 |
| Nalo | 18,809 | 1.84 ms | 7.20 ms | 10.40 ms | 105.68 ms | 0 |
| Arkesel | 14,406 | 2.45 ms | 9.38 ms | 13.18 ms | 37.10 ms | 0 |

Menus only, with no backend calls (`-keys 3`): about 31,000 requests/s, with p99 at 6.2 ms on Africa's Talking.

**GitHub runner:** CI runs the same test on every push, with sessions in a real Redis, and posts the table in the run summary. See the "load-test" job on the [Actions tab](https://github.com/Jacksonoseikojo1122/ussd-runtime/actions).

**What the load test caught.** The first run had p99 at about 830 ms and a worst case of about 6 seconds. That is long enough to drop real sessions. The cause was Go's default HTTP client, which keeps only 2 idle connections per host. With 50 callers, almost every backend call opened a new TCP connection. The runtime now keeps up to 256 connections open per backend host. That one change took p99 from about 830 ms to about 10 ms.

Run it yourself:

```bash
go run ./cmd/loadtest -url http://localhost:8080 -gateway hubtel -c 50 -d 30s -keys 1,1001,1,250,1
```

## Sessions

- **Without `REDIS_URL`:** sessions live in memory. That's fine for one instance; a restart loses sessions in progress.
- **With `REDIS_URL`:** run as many instances as you like behind a load balancer. Each session is one key (`ussd:session:<gateway>:<id>`) that expires after `SESSION_TTL` (180 s by default).
- Upstash's free Redis works; use the `rediss://` URL.

## Docker

```bash
docker build -t ussd-runtime .
docker run -p 8080:8080 -v $PWD/flows:/flows:ro \
  -e FLOW_FILE=/flows/my-flow.json -e REDIS_URL=rediss://... -e GATEWAY_TOKEN=... -e ACTION_SECRET=... \
  ussd-runtime
```

The image is distroless, runs as non-root and has no shell. Tagged releases are published to `ghcr.io/jacksonoseikojo1122/ussd-runtime`.

## Render (free tier)

`render.yaml` deploys the image as a free web service:
- `GATEWAY_TOKEN` and `ACTION_SECRET` are generated for you.
- Set `REDIS_URL` to a free Upstash Redis.
- Put your flow in the image, or mount it as a Secret File and point `FLOW_FILE` at it.

Free services sleep when idle, and the first request after that is slow. Gateways won't wait, so use a paid instance (or keep it warm) for a live shortcode.

## Kubernetes

`deploy/k8s/ussd-runtime.yaml` runs two replicas that share sessions in Redis. It includes:
- readiness and liveness probes on `/healthz`;
- Prometheus scrape annotations;
- a read-only root filesystem and small resource limits.

The flow is mounted from a Secret.

## Metrics

`GET /metrics` (Prometheus):

| Metric | Meaning |
| --- | --- |
| `ussd_requests_total{gateway}` | Requests handled |
| `ussd_request_errors_total{gateway}` | Bad requests and storage failures |
| `ussd_request_duration_seconds{gateway}` | Time to answer, including action calls (histogram) |
| `ussd_sessions_started_total`, `ussd_sessions_ended_total` | Sessions |
| `ussd_actions_total{result}` | Backend calls, `ok` or `error` |
| `ussd_action_duration_seconds` | Backend call time (histogram) |

Alerts worth setting:

| Alert | PromQL |
| --- | --- |
| Replies getting slow | `histogram_quantile(0.99, sum by (le) (rate(ussd_request_duration_seconds_bucket[5m]))) > 1` |
| Backend failing | `rate(ussd_actions_total{result="error"}[5m]) > 0.05` |
| Sessions dying | `rate(ussd_request_errors_total[5m]) > 0` |

Logs are JSON on stdout, with phone numbers masked (`23324****567`).

## Security checklist

- Serve over HTTPS and set `GATEWAY_TOKEN`.
- Set a long random `ACTION_SECRET`, and verify the signature in your backend.
- Keep `SIMULATOR` off in production; it lets anyone drive your flow.
- Use `can_go_to` so your backend can only send callers to screens you expect.
- Never collect mobile money PINs over USSD.
