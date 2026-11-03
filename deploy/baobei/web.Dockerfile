FROM node:24.19.0-alpine3.24 AS build
WORKDIR /src
COPY package*.json ./
RUN npm ci --ignore-scripts
COPY . ./
RUN npm run build
FROM nginx:1.30.4-alpine
COPY --from=baobei_config nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html
