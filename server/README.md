# OpenDuanju Server

Go/Kratos v3 服务端，提供管理端 HTTP/gRPC、公开查询和播放接口，并运行持久化采集、调度与素材 worker。

[项目首页](../README.md) · [架构](../docs/architecture.md) · [开发与测试](../docs/development.md) · [Docker](../deploy/README.md)

## 模块

- 来源/通道配置、采集计划、运行、预算、候选快照。
- 作品、修订、选集、授权、素材批次、上传与封面。
- 账号会话、权限、审核、发布/下架、事务审计与 Trace。
- 公开分类/搜索/详情、冻结素材播放会话与凭证刷新。

保持官方 service→biz→data、Wire、Ent 和 Protobuf 流程。Redis 配置预留，当前业务客户端未接入。官方 Todo 示例仅用于参考模板，不属于业务权限承诺。

## 启动与生成

从根目录按[开发说明](../docs/development.md)设置 `KRATOS_DATABASE_SOURCE` 等配置，先显式迁移，再初始化管理员与启动服务。普通启动不要求旧的共享管理令牌，不自动迁移。

```bash
# 仓库根目录
make -C server init
make generate
make check-generated
make migrate-up
make dev-server
```

本机默认 HTTP 8000、gRPC 9000；Docker 默认映射 18000、19000。配置源在 `configs/`，不能使用旧项目的配置路径或数据库。

## 验证

```bash
# server 目录
go vet ./...
go test -race ./...
```

真实 MySQL 需单独执行根目录 `make test-*`，不能把未配置 DSN 的跳过当成通过。媒体处理需要 FFmpeg/ffprobe；运行镜像已包含工具。生成文件不得手改。

## 契约与工具

- [管理 API](api/admin/v1/admin.proto)
- [公开 API](api/web/v1/web.proto)
- [OpenAPI](openapi.yaml)
- [SQL 迁移](migrations)
- `cmd/`：server、migrate、staff-init、媒体/来源/SDK/COS 专项工具。

生产需另行配置 HTTPS、私有存储、凭据、关闭 debug/auto_migrate，并验证实际来源与 COS；现有 local 证据不能替代这些验收。

Kratos 模板的 MIT 声明保留在 [LICENSE](LICENSE)，项目许可见[根许可证](../LICENSE)。
