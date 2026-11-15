# Docker 部署与配置

[返回项目首页](../README.md)

## 适用环境

本目录提供本机开发与隔离验收配置。默认五服务为 admin、web、server、mysql、redis，使用新项目自己的数据库和卷，不依赖旧项目资源。默认地址仅绑定 `127.0.0.1`。

生产需另行配置 TLS、Secure Cookie、秘密管理、日志与备份恢复；仓库配置不是生产容量或高可用承诺。

## 首次启动

在仓库根复制 `.env.example` 为 `.env`，设置自己的 MySQL root 与应用密码。Compose 将变量转发到服务端配置，`.env` 不提交。

```bash
cp .env.example .env
# 编辑 .env 后执行：
docker compose --env-file .env -f deploy/compose.yaml build server admin web
docker compose --env-file .env -f deploy/compose.yaml up -d --wait mysql redis

docker compose --env-file .env -f deploy/compose.yaml run --rm --no-deps \
  --entrypoint /app/migrate server -conf /app/configs -up

python3 -c 'import getpass; print(getpass.getpass("管理员初始密码："))' | \
  docker compose --env-file .env -f deploy/compose.yaml run --rm -T --no-deps \
  --entrypoint /app/staff-init server -conf /app/configs -username admin

docker compose --env-file .env -f deploy/compose.yaml up -d server admin web
```

**迁移与初始化必须使用 `--entrypoint`。** server 镜像默认入口是 `/app/server`，仅把 `/app/migrate` 写在服务名之后不会切换可执行程序。服务启动不自动应用业务迁移。

管理员初始化成功后退出，不启动 HTTP 或 worker。已有启用管理员时拒绝重复初始化，不会重置密码。管理员密码由你设置，没有默认账号口令。

首次库为空。使用后台创建运营与审核员，再创建作品、上传、授权、处理、审核并发布；C 端只展示当前可公开访问的发布内容。

## 端口与卷

| 服务        | 主机地址          | 容器端口 |
| ----------- | ----------------- | -------- |
| admin       | `127.0.0.1:18081` | 80       |
| web         | `127.0.0.1:18082` | 80       |
| server HTTP | `127.0.0.1:18000` | 8000     |
| server gRPC | `127.0.0.1:19000` | 9000     |
| MySQL       | `127.0.0.1:13380` | 3306     |
| Redis       | `127.0.0.1:16380` | 6379     |

默认 Compose 项目名 `openduanju-rebuild`。`mysql_data` 保存数据库，`media_data` 挂载 `/var/lib/openduanju`，保存本地素材、处理检查点与 SDK 档案。服务端使用 UID 10001，卷目录需要可写。

常规重建不删除卷。`stop` 和 `down` 默认保留命名卷；不要把 `down -v` 用作日常停止命令。

## 配置说明

| 配置                                     | 用途                                                                         |
| ---------------------------------------- | ---------------------------------------------------------------------------- |
| `MYSQL_ROOT_PASSWORD` / `MYSQL_PASSWORD` | 新开发库 root / 应用用户口令                                                 |
| `MYSQL_DATABASE` / `MYSQL_USER`          | 默认 `openduanju_dev` / `openduanju`                                         |
| `KRATOS_MEDIA_STORAGE_DRIVER`            | 首次体验使用 `local`；`cos` 需要真实配置与验证                               |
| `KRATOS_MEDIA_RESOLVER_DRIVER`           | `command` 为可替换桥接；`hongguo_sdk` 为 SDK 适配器；人工上传不依赖 resolver |
| `KRATOS_HONGGUO_PROFILE_PATH`            | 容器内持久化 SDK 档案路径，默认在素材卷中                                    |
| `KRATOS_MEDIA_PLAYBACK_TICKET_TTL`       | 播放凭证期限，默认 `10m`                                                     |
| `KRATOS_COS_*`                           | 可选 COS 配置入口，真实桶验证仍待完成，不作为首次运行前置                    |

服务端文件占位符从 Kratos 配置树解析。直接运行 Go 时需要导出 `KRATOS_` 前缀变量；普通同名进程变量不会自动生效。Compose 只会转发 YAML 已声明的变量，新增变量需同步配置与接线，不要仅修改 `.env` 就认为已生效。

`configs/config.yaml` 为开发配置，`debug=true`、`secure_cookie=false`；生产必须使用经过评审的配置关闭 SQL debug 和自动迁移，并在 HTTPS 下启用 Secure Cookie。共享 Bearer 管理令牌已退役，后台使用 staff 会话。

## 检查与排查

```bash
docker compose --env-file .env -f deploy/compose.yaml ps
docker compose --env-file .env -f deploy/compose.yaml logs --tail=100 server
docker compose --env-file .env -f deploy/compose.yaml exec server \
  /app/migrate -conf /app/configs -status
make check-proxy-logs
```

页面报错附带 trace 编号，可关联请求日志、运行/批次和后台审计；日志不得保留签名媒体路径中的凭证。不要把包含凭据或设备信息的完整日志贴入公开 Issue。

## 更新

先备份数据库及素材卷，构建新镜像。在停写条件下显式执行评审后的增量迁移，再更新对应服务。MySQL DDL 不提供整文件事务回滚，迁移失败需要根据失败历史和实际结构恢复处理。

```bash
docker compose --env-file .env -f deploy/compose.yaml build server admin web
docker compose --env-file .env -f deploy/compose.yaml run --rm --no-deps \
  --entrypoint /app/migrate server -conf /app/configs -up
docker compose --env-file .env -f deploy/compose.yaml up -d server admin web
```

以上命令不代替备份、停写或生产升级方案；不要仅回退镜像就假定数据库也已回退。

## 隔离验收

`compose.accept.yaml` 用于独立验收资源。已有同名栈时不能直接覆盖，应先检查其项目、端口、数据库和卷。集成测试会清理表，只能指向一次性测试库。
