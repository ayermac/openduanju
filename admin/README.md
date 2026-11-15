# OpenDuanju Admin

Vue 3 + TypeScript + Vite + Element Plus 运营后台，独立 Nginx 部署。包含统一导航、账号会话、来源/采集、内容修订、上传与素材、授权、审核发布及审计页面。

[项目首页](../README.md) · [开发说明](../docs/development.md) · [部署说明](../deploy/README.md) · [协作约束](AGENTS.md)

从仓库根执行：

```bash
npm ci
npm run dev -w admin          # 127.0.0.1:5173
npm run build -w admin
npm run lint -w admin
npm run type-check -w admin
npm run test:unit -w admin -- --run
npm run test:e2e -w admin
```

开发代理默认 `http://127.0.0.1:8000`，可在 `.env.local` 设置 `VITE_API_PROXY_TARGET`，必须保留浏览器 Host 以匹配 Origin 校验。前端变量公开，不能放来源或 COS 凭据。

没有内置账号或默认密码。首次管理员通过服务端 `staff-init` 创建；后台使用 HttpOnly 会话与 CSRF，按钮可见性不替代服务端权限。

构建产物在 `dist/`，不嵌入 Go。浏览器测试默认 Playwright Chromium，设置 `PLAYWRIGHT_CHANNEL=chrome` 可使用系统 Chrome。测试通常使用可控 API，不能据此证明真实来源或 COS 可用。
