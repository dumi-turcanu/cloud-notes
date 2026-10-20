# syntax=docker/dockerfile:1

# ---- build -----------------------------------------------------------------
# The SPA is plain static output, so the build always runs on the build host's
# architecture regardless of the target platform.
FROM --platform=$BUILDPLATFORM node:24-alpine AS build

WORKDIR /app
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci

COPY . .
RUN npm run build

# ---- runtime ---------------------------------------------------------------
FROM nginxinc/nginx-unprivileged:1.30-alpine

ARG VERSION=dev
ARG COMMIT=unknown
LABEL org.opencontainers.image.title="cloud-notes-frontend" \
      org.opencontainers.image.description="Cloud Notes web UI (nginx serving the SPA and proxying /api)" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.source="https://github.com/dumi-turcanu/cloud-notes"

# PORT is substituted into the nginx template; NGINX_ENTRYPOINT_LOCAL_RESOLVERS
# makes the base image export NGINX_LOCAL_RESOLVERS (from /etc/resolv.conf).
# API_UPSTREAM is required at run time and deliberately has no default.
ENV PORT=8080 \
    NGINX_ENTRYPOINT_LOCAL_RESOLVERS=1

COPY --from=build /app/dist /usr/share/nginx/html
COPY nginx/templates/ /etc/nginx/templates/
COPY nginx/snippets/ /etc/nginx/snippets/
COPY --chmod=0755 nginx/docker-entrypoint.d/ /docker-entrypoint.d/

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["sh", "-c", "wget -qO /dev/null \"http://127.0.0.1:${PORT}/healthz\""]
