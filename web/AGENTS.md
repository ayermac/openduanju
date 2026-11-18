# C端简版网站协作约束

先读../AGENTS.md和../.doc/README.md，本文件补充前端边界。

- 本项目由create-vue官方脚手架生成，保留Vue SFC、TypeScript strict、Router、Pinia、Vitest和Playwright流程。
- C端视觉以 2026-11-18 确定的深色画布、宽屏左侧栏、窄屏底部导航和薄荷绿主操作为准。不复制参考站的名称、文案、账号或对话，也不把后台令牌改成同一套颜色。播放、路由和游客范围保持不变，不照搬B端组件。
- 页面放src/views，复用交互放components，网络访问放api，跨页面状态才进入stores；不把所有业务塞进App.vue或一个脚本。
- 路由、加载/空/错误/权限状态明确；切路由取消过时请求，离开播放器清理媒体、定时器和事件。
- 使用相对/api和开发代理；不硬编码localhost来源、不在VITE_*变量放密钥。
- 不把假数据伪装为实时指标；mock仅用于显式测试/开发夹具，不作为生产默认回退。
- 数据接口由server契约决定；64位ID保持string，不用Number转换；后端仍负责权限和业务状态。
- lint和format:check只读；修复格式显式运行format或lint:fix。不要通过关闭检查、any或吞异常使CI通过。
- 表单保留失败输入，区分提交/普通按钮，长任务显示分集阶段/失败/重试，不阻塞整页。
- 改UI至少运行类型检查、相关组件测试；关键交互需真实浏览器，页面HTTP200不能替代操作断言。
- 已有业务页面与首版UI，当前范围见根README及本项目README；目录、页面骨架或禁用按钮不算功能完成。

命令在仓库根运行：npm run dev -w web / npm run build -w web / npm run test:unit -w web -- --run / npm run test:e2e -w web。
