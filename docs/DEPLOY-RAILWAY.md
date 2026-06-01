# Deploying Invariant Core to Railway

A step-by-step for running the system live on [Railway](https://railway.app) with
GitHub auto-deploy. You get a public REST transfer API, a public Grafana
dashboard, and a Jaeger UI — all wired over Railway's private network.

**Topology — one project, 7 services:**

| Service | Source | Public? | Notes |
|---|---|---|---|
| `Postgres` | Railway database | no | provides `DATABASE_URL` |
| `ledger` | repo `Dockerfile`, start `/app/ledger` | no | gRPC :50051, metrics :8081 |
| `mockrail` | repo `Dockerfile`, start `/app/mockrail` | no | gRPC :50053, metrics :8082 |
| `switchd` | repo `Dockerfile`, start `/app/switchd` | **yes (8080)** | public REST API |
| `jaeger` | image `jaegertracing/all-in-one:1.57` | yes (16686) | traces UI |
| `prometheus` | `deployments/railway/prometheus.Dockerfile` | optional (9090) | scrapes the services |
| `grafana` | `deployments/railway/grafana.Dockerfile` | **yes (3000)** | dashboards |

> Name the services **exactly** `ledger`, `switchd`, `mockrail`, `prometheus`,
> `jaeger` — the private DNS (`<name>.railway.internal`) in the env vars and
> scrape config depends on these names.

---

## 0. Prerequisites

- The deploy files are committed (root `Dockerfile`, `.dockerignore`,
  `deployments/railway/*`). Push them to GitHub first.
- A Railway account with the repo accessible.
- `migrate` CLI locally (`make tools`) for the one-time schema setup.

## 1. Create the project + Postgres

1. Railway → **New Project** → **Deploy from GitHub repo** → pick `Invariant-Core`.
   Railway will try to build one service from the root `Dockerfile` — you'll
   reconfigure it as `switchd` below, and add the rest.
2. **+ New** → **Database** → **Add PostgreSQL**. Leave it named `Postgres`.

## 2. The three app services (`ledger`, `switchd`, `mockrail`)

All three deploy from the **same repo + root `Dockerfile`**; they differ only by
**Custom Start Command** and **Variables**. For each: **+ New → GitHub Repo →**
the same repo (or rename the auto-created first service), then in the service's
**Settings → Deploy → Custom Start Command** set the binary, and add the
variables under **Variables**.

**`ledger`** — Start Command `/app/ledger`
```
DB_URL=${{Postgres.DATABASE_URL}}
OTEL_EXPORTER_OTLP_ENDPOINT=jaeger.railway.internal:4317
```

**`mockrail`** — Start Command `/app/mockrail`
```
OTEL_EXPORTER_OTLP_ENDPOINT=jaeger.railway.internal:4317
SWITCH_CALLBACK_TARGET=switchd.railway.internal:50052
# optional chaos (default 0 = everything settles):
# MOCKRAIL_P_DECLINE=0.3
# MOCKRAIL_P_TIMEOUT=0.2
# MOCKRAIL_LATENCY_MS=150
```

**`switchd`** — Start Command `/app/switchd` · this is the public one
```
DB_URL=${{Postgres.DATABASE_URL}}
LEDGER_GRPC_TARGET=ledger.railway.internal:50051
MOCKRAIL_GRPC_TARGET=mockrail.railway.internal:50053
OTEL_EXPORTER_OTLP_ENDPOINT=jaeger.railway.internal:4317
PORT=8080
SWITCHD_HTTP_ADDR=:8080
```
Then **Settings → Networking → Public Networking → Generate Domain**, target
port **8080**. Optionally set **Settings → Deploy → Healthcheck Path** = `/healthz`.

> If a service can't reach Postgres, append `?sslmode=disable` to its `DB_URL`
> (Railway's private Postgres is non-TLS).

## 3. Observability (`jaeger`, `prometheus`, `grafana`)

**`jaeger`** — **+ New → Empty Service** (or Docker Image) →
image `jaegertracing/all-in-one:1.57`. Add variable `COLLECTOR_OTLP_ENABLED=true`.
Generate a domain on port **16686** to see traces. (OTLP ingest is on :4317,
reached privately by the app services.)

**`prometheus`** — **+ New → GitHub Repo** (same repo) →
**Settings → Build → Dockerfile Path** = `deployments/railway/prometheus.Dockerfile`.
No start command. Optionally generate a domain on port **9090**.

**`grafana`** — **+ New → GitHub Repo** (same repo) →
**Settings → Build → Dockerfile Path** = `deployments/railway/grafana.Dockerfile`.
Generate a domain on port **3000**. The datasource, dashboard provider, and the
dashboard are baked into the image (anonymous-admin) — it renders with no login.

## 4. One-time migrate + seed (from your laptop)

Railway services don't run migrations. Do it once against the **public** DB URL
(Postgres service → **Variables** → `DATABASE_PUBLIC_URL`):

```bash
export RAILWAY_DB="<DATABASE_PUBLIC_URL>?sslmode=require"
make migrate-up DB_URL="$RAILWAY_DB"     # applies migrations/0001 + 0002
DB_URL="$RAILWAY_DB" make seed           # creates CUST-001 / CUST-002 (idempotent)
```

## 5. Verify

With `<switchd>` = the generated switchd domain:

```bash
curl -s https://<switchd>/healthz                       # -> {"status":"ok"}

curl -i -X POST https://<switchd>/v1/transfers \
  -H 'Idempotency-Key: 11111111-1111-1111-1111-111111111111' \
  -H 'Content-Type: application/json' \
  -d '{"reference":"NIP-RW-001","source":"CUST-001","destination":"CUST-002","amount_minor":500000,"currency":"NGN"}'
# -> 202; copy the id, then:
curl -s https://<switchd>/v1/transfers/<id>              # -> SETTLED
```

- **Postman:** import `api/postman/invariant-core.postman_collection.json`, set the
  `switchd_url` collection variable to `https://<switchd>`, and run the Transfers
  folder.
- **Reversals:** set `MOCKRAIL_P_DECLINE=1.0` on the `mockrail` service (it
  redeploys), fire transfers → they reach `REVERSED` with the source refunded.
- **Grafana:** open the grafana domain → "Transfers & Backpressure" populates as
  traffic flows.
- **Jaeger:** open the jaeger domain → a transfer is one trace
  switchd → ledger → mockrail.

## Notes

- **Cost/surface:** 7 services. Only `switchd`, `grafana`, (and `jaeger`) need
  public domains — keep `ledger`, `mockrail`, `prometheus` private.
- **Auto-deploy:** every push to the tracked branch rebuilds the affected
  services. Migrations are not re-run automatically (rerun step 4 if the schema
  changes).
- **Live feedback loop (stretch):** the reconcile → `CorrectiveReversal` loop
  needs switchd's gRPC :50052. Add a Railway **TCP Proxy** to switchd :50052 and
  run `reconcile run --switch-addr <proxy-host:port>` locally to demo it.
