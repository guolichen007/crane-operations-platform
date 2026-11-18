FROM node:24.19.0-alpine3.24 AS build
WORKDIR /src
COPY package*.json ./
RUN npm ci --ignore-scripts
COPY . ./
RUN npm run build
# Runtime base pinned by multi-arch index digest (official nginx:1.30.6-alpine3.24).
# The base still ships libxslt 1.1.43-r3 (3 HIGH) and zlib 1.3.2-r1; upgrade within the
# same Alpine 3.24 series and fail the build if the fixed packages are not installed.
FROM nginx:1.30.6-alpine3.24@sha256:901e593c95dc85f5a729f5765516321366ef4ac401821431d408897e95287d0a
RUN apk upgrade --no-cache \
 && apk info -e 'libxslt>=1.1.43-r4' 'zlib>=1.4-r0' >/dev/null
ARG SOURCE_SHA=unknown
LABEL org.opencontainers.image.revision=${SOURCE_SHA}
LABEL org.opencontainers.image.source=https://github.com/guolichen007/crane-operations-platform
COPY --from=baobei_config nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html
