FROM node:24.19.0-alpine3.24 AS build
WORKDIR /src
COPY package*.json ./
RUN npm ci --ignore-scripts
COPY . ./
RUN npm run build
# Runtime base pinned by multi-arch index digest (official nginx:1.30.7-alpine3.24, 2026-11-18).
# The base still ships zlib 1.3.2-r1 and libssl3/libcrypto3 3.5.9-r0; upgrade within the same
# Alpine 3.24 series and fail the build if the fixed packages are not installed.
FROM nginx:1.30.7-alpine3.24@sha256:bef154735288bc38d99fac41ae63730684bcc661503673a04a0f9bc526ba8598
RUN apk upgrade --no-cache \
 && apk info -e 'libxslt>=1.1.43-r4' 'zlib>=1.4-r0' >/dev/null
ARG SOURCE_SHA=unknown
LABEL org.opencontainers.image.revision=${SOURCE_SHA}
LABEL org.opencontainers.image.source=https://github.com/guolichen007/crane-operations-platform
COPY --from=baobei_config nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html
