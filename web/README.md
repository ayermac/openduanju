# OpenDuanju Web

Vue 3 + TypeScript + Vite 消费端网站，独立 Nginx 部署。包含最近发布、搜索分类、本设备继续观看、作品详情、选集、原生视频控件与凭证刷新。

[项目首页](../README.md) · [开发说明](../docs/development.md) · [已知限制](../docs/known-limitations.md) · [协作约束](AGENTS.md)

从仓库根执行：

```bash
npm ci
npm run dev -w web            # 127.0.0.1:5174
npm run build -w web
npm run lint -w web
npm run type-check -w web
npm run test:unit -w web -- --run
npm run test:e2e -w web
```

开发代理默认转发到 `http://127.0.0.1:8000`；可在 `.env.local` 配置 `VITE_API_PROXY_TARGET`，不得放凭据。页面使用公开 API，不能持有来源身份或 COS 永久密钥。

仅播放已发布修订的冻结素材，刷新不重新调用来源 SDK。进度保存在本设备 localStorage，不是账号或跨设备同步。首页与详情承载搜索/筛选/播放，不要求独立搜索和历史路由。

页面支持 375/768/1440 视口检查，尚未完成 iOS/Android 真机验证。Playwright 测试使用可控接口与生成视频，不代替真实 SDK/COS 验收。
