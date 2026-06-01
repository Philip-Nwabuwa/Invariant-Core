# Prometheus image for Railway with the scrape config baked in (Railway has no
# bind mounts, so config is COPYed into the image instead). Build context is the
# repo root; on Railway set this service's Dockerfile Path to
# deployments/railway/prometheus.Dockerfile.
FROM prom/prometheus:v2.53.0
COPY deployments/railway/prometheus.yml /etc/prometheus/prometheus.yml
