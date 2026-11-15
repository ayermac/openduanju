# The public site is a static bundle; nginx serves it and proxies the public
# API to the server container.
FROM node:22-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
COPY admin/package.json admin/package.json
COPY web/package.json web/package.json
RUN npm ci
COPY . .
RUN npm run build -w web

FROM nginx:1.27-alpine
COPY deploy/nginx-web.conf /etc/nginx/conf.d/default.conf
# The public site shares the API proxy snippet with the console: all three
# signed endpoints are reachable through it, and the settings must not drift.
# It does not copy the upload snippet: this proxy accepts no upload body.
COPY deploy/nginx-api-proxy.conf /etc/nginx/snippets/api-proxy.conf
COPY --from=build /app/web/dist /usr/share/nginx/html
