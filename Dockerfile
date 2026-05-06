# syntax=docker/dockerfile:1.7

FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM rust:1-bookworm AS backend
WORKDIR /src/backend
COPY backend/Cargo.toml backend/Cargo.lock* ./
COPY backend/migrations ./migrations
COPY backend/src ./src
RUN cargo build --release --bins

FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --home /nonexistent --shell /usr/sbin/nologin dashboard \
    && mkdir -p /app/backend /app/frontend/dist /data \
    && chown -R dashboard:dashboard /data
WORKDIR /app/backend
COPY --from=backend /src/backend/target/release/dashboardd /usr/local/bin/dashboardd
COPY --from=backend /src/backend/target/release/dashctl /usr/local/bin/dashctl
COPY --from=backend /src/backend/target/release/dashboard-agent /usr/local/bin/dashboard-agent
COPY --from=backend /src/backend/migrations ./migrations
COPY --from=frontend /src/frontend/dist ../frontend/dist
USER dashboard
ENV DASHBOARD_BIND=0.0.0.0:3000 \
    DASHBOARD_DATABASE_URL=sqlite:///data/dashboard.sqlite?mode=rwc \
    DASHBOARD_RP_ID=dash.olivermarcusson.se \
    DASHBOARD_ORIGIN=https://dash.olivermarcusson.se
EXPOSE 3000
CMD ["/usr/local/bin/dashboardd"]
