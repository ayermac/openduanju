# 本机开发与验证

[返回项目首页](../README.md) · [Docker 部署](../deploy/README.md)

## 环境

工具版本以 [.tool-versions.json](../.tool-versions.json)、[server/go.mod](../server/go.mod) 及 npm 锁文件为准。当前使用 Go 1.26.2、Node 22.23.5、npm 12.0.2、Python 3；媒体检查需要 FFmpeg/ffprobe。

```bash
make install
npm exec -- playwright install chromium
```

`make install` 安装 npm workspace 依赖、Go 生成工具并下载模块。服务端生成工具放在忽略的 `.tools/bin`，不用全局工具版本覆盖项目约定。

## 启动开发环境

先按 [部署说明](../deploy/README.md) 配置 `.env` 并启动独立 MySQL/Redis。根目录 `.env` 用于 Compose 替换，不会自动进入本机 Go 进程。

在仓库根显式设置本机服务配置；替换示例密码，使其与自己的数据库用户一致：

```bash
export KRATOS_DATABASE_SOURCE='openduanju:YOUR_LOCAL_PASSWORD@tcp(127.0.0.1:13380)/openduanju_dev?parseTime=true&loc=UTC&charset=utf8mb4'
export KRATOS_DATABASE_EXPECTED_NAME=openduanju_dev
export KRATOS_MEDIA_STORAGE_DRIVER=local
export KRATOS_MEDIA_RESOLVER_DRIVER=command
export KRATOS_MEDIA_STORAGE_LOCAL_ROOT="$PWD/var/media-objects"
export KRATOS_MEDIA_TEMP_DIR="$PWD/var/media-work"

make migrate-up
```

首次使用通过一次性 CLI 创建管理员，密码不写在参数里：

```bash
python3 -c 'import getpass; print(getpass.getpass("管理员初始密码："))' | \
  sh -c 'cd server && go run ./cmd/openduanju-staff-init -conf ./configs -username admin'
```

三个终端分别运行：

```bash
make dev-server   # HTTP 8000 / gRPC 9000
make dev-admin    # http://127.0.0.1:5173
make dev-web      # http://127.0.0.1:5174
```

Vite 默认把 `/api/` 转发到本机 8000；代理目标可在各子项目 `.env.local` 中设置 `VITE_API_PROXY_TARGET`。该变量是公开配置，不能放密钥。不要把本机 Go 进程和容器服务误认为同一运行版本。

## 契约与生成

修改 Protobuf、配置 proto 或 Ent schema 后，从根目录执行：

```bash
make generate
make check-generated
```

生成的 `*.pb.go`、HTTP/gRPC 接线、Ent、Wire 和 OpenAPI 必须与源文件一起提交，不手工修改生成文件。数据库结构改变需增加 SQL 迁移，不能以运行时自动建表代替。

## 检查分层

| 命令                    | 范围                                                           |
| ----------------------- | -------------------------------------------------------------- |
| `make check-fast`       | 文档、两端 lint/格式/类型/单测/构建、Go 格式/vet/竞态测试/构建 |
| `make check-generated`  | 生成产物与源文件一致性                                         |
| `make media-smoke`      | 可控视频处理与浏览器媒体冒烟                                   |
| `make check`            | 上述检查与两端 Playwright 用例                                 |
| `make check-proxy-logs` | 已运行五服务上的签名媒体路径日志检查；只发送虚构无效凭证       |

如需系统 Chrome，可通过 `PLAYWRIGHT_CHANNEL=chrome` 运行前端测试；默认 CI 使用 Playwright Chromium。实际编解码支持取决于浏览器构建，不能仅以页面加载成功判定播放通过。

### 真实 MySQL 集成测试

必须先准备一次性库和对应用户权限；测试会清理表，不自动为你创建库，不得复用正在使用的数据库。

```bash
export MIGRATE_TEST_DSN='test_user:test_password@tcp(127.0.0.1:13380)/openduanju_content_test?parseTime=true&loc=UTC'
make test-content
```

可用目标：

```text
test-migrate        迁移、校验和、失败历史与锁
test-source         本地目录、采集运行及启动保护
test-content        草稿编辑、人工创建、新修订及素材继承重提
test-media          素材批次、租约、阶段与存储
test-upload         上传会话与确认对象版本
test-authorization  授权依据与门禁
test-staff          后台账号与会话
test-review         审核、发布、登录限流及权限
test-web            公开投影与播放会话
test-audit          审计事务、错误与 Trace
```

相同测试库的目标应串行执行。不要将同一个 `TEST_MYSQL_DSN` 注入并行运行的全部包；部分数据库用例在未配置时会跳过，`make check` 通过不代表它们已经执行。

可控 resolver、本地存储、真实数据库、真实来源/COS 与浏览器播放是不同证据，结果分开记录。截图只能说明对应版本和页面状态，不能代替事务或故障恢复验证。

## 专项工具

`server/cmd/` 包含迁移、管理员初始化、单集媒体处理、Web 来源检查、SDK 探测、COS 检查及权限探针。先阅读各命令参数和前置条件再执行；权限探针含写操作，不得对真实业务内容随意运行。

内部需求、任务与原始证据保存在不分发的 `.doc/`。公开克隆可以按本文运行；需要未公开产品规则时先通过 Issue 或维护者确认，不根据缺失资料臆造规则。
