# Grafana image for Railway with the datasource, dashboard provider, and the
# committed dashboard all baked in (no bind mounts on Railway). Anonymous-admin
# so the public URL renders with no login. Build context is the repo root; on
# Railway set this service's Dockerfile Path to
# deployments/railway/grafana.Dockerfile and expose it on port 3000.
FROM grafana/grafana:11.1.0

ENV GF_AUTH_ANONYMOUS_ENABLED=true \
    GF_AUTH_ANONYMOUS_ORG_ROLE=Admin \
    GF_AUTH_DISABLE_LOGIN_FORM=true

# Railway datasource (prometheus.railway.internal) + the shared dashboard
# provider, and the committed dashboard JSON.
COPY deployments/railway/datasources/prometheus.yml /etc/grafana/provisioning/datasources/prometheus.yml
COPY deployments/grafana/provisioning/dashboards/dashboards.yml /etc/grafana/provisioning/dashboards/dashboards.yml
COPY deployments/grafana/dashboards/ /var/lib/grafana/dashboards/
