FROM node:24.19.0-alpine3.24 AS build
WORKDIR /src
COPY package*.json ./
RUN npm ci --ignore-scripts
COPY . ./
RUN npm run build
FROM nginx:1.30.4-alpine
ARG SOURCE_SHA=unknown
LABEL org.opencontainers.image.revision=${SOURCE_SHA}
LABEL org.opencontainers.image.source=https://github.com/guolichen007/crane-operations-platform
COPY --from=baobei_config nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html
