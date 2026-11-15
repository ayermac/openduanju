# The admin console is a static bundle; nginx serves it and proxies the API
# to the server container.
FROM node:22-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
COPY admin/package.json admin/package.json
COPY web/package.json web/package.json
RUN npm ci
COPY . .
RUN npm run build -w admin

FROM nginx:1.27-alpine
COPY deploy/nginx-admin.conf /etc/nginx/conf.d/default.conf
# Both console configs share the API proxy snippet: the signed media
# locations differ only in which errors they may log. The upload settings are
# admin-only; the public site has never accepted an upload body.
COPY deploy/nginx-api-proxy.conf /etc/nginx/snippets/api-proxy.conf
COPY deploy/nginx-api-upload.conf /etc/nginx/snippets/api-upload.conf
COPY --from=build /app/admin/dist /usr/share/nginx/html
