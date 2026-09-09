# CaseHub

CaseHub 是按版本管理测试用例、测试任务和执行记录的本地系统。主线只读，测试版本从主线完整派生，文本变更经过冲突检查后合并。

## 本地运行

```sh
sh build.sh
./build/casehub
```

访问 `http://localhost:8080`。默认使用 `data/casehub.json` 持久化；测试时可设置 `CASEHUB_STORE=memory`。

Windows 使用：

```powershell
./build.ps1
./build/casehub.exe
```

## 容器运行（独立 PostgreSQL）

```sh
docker compose up --build
```

应用和 PostgreSQL 分别运行在两个容器中。应用使用 `Repository` 接口隔离领域逻辑与内存、文件及 PostgreSQL 存储实现。

## Kubernetes 部署

部署文件为 [`deploy/kubernetes/casehub.yaml`](deploy/kubernetes/casehub.yaml)，包含单副本 Deployment 和对外访问的 NodePort Service。先将其中的 `example.com/casehub:placeholder` 替换为实际镜像。以下命令默认使用当前 kubectl namespace；如果指定 `-n <namespace>`，Secret 和应用必须部署在同一个 namespace。

### 不指定 DB：本地临时存储

```sh
kubectl apply -f deploy/kubernetes/casehub.yaml
kubectl rollout status deployment/casehub
```

未创建 `casehub-db` Secret（或没有 `DATABASE_URL` 键）时，默认写入容器内 `/app/data/casehub.json`。该目录挂载 `emptyDir`，同一个 Pod 内容器重启时保留，Pod 删除、更新或重建后数据丢失，不使用 PVC。参见 [Kubernetes emptyDir 文档](https://kubernetes.io/docs/concepts/storage/volumes/#emptydir)。

### 指定已有的 PostgreSQL Service

编辑 [`deploy/kubernetes/examples/db-secret.yaml`](deploy/kubernetes/examples/db-secret.yaml)，替换用户名、密码、数据库名及 Service 地址，再执行：

```sh
kubectl apply -f deploy/kubernetes/examples/db-secret.yaml
kubectl apply -f deploy/kubernetes/casehub.yaml
# 如果应用已启动，需要重启以读取新增或修改的 Secret。
kubectl rollout restart deployment/casehub
kubectl rollout status deployment/casehub
```

示例地址 `postgres.database.svc.cluster.local:5432` 表示 `database` namespace 中名为 `postgres` 的 Service；同 namespace 可直接写 `postgres:5432`。若集群 DNS 域不是 `cluster.local`，需对应调整。数据库及用户需预先存在且允许建表，应用启动时自动创建状态表；这些文件不创建 DB 服务。示例 `sslmode=disable` 需按数据库的 TLS 配置调整，连接串中的特殊字符需 URL 编码。填写真实凭据后不要将 Secret 文件提交到仓库。

也可直接通过容器 `args` 指定 `-database-url=postgres://...`，或设置 `DATABASE_URL` 环境变量；推荐使用上面的 Secret。命令行连接串优先于环境变量，未显式设置 `CASEHUB_STORE` 时，有连接串自动选择 PostgreSQL，没有则使用文件存储。配置了 DB 但连接失败时启动报错，不会退回本地存储。切换存储不会自动迁移已有数据。

### 集群外访问

页面与 API 共用同一个 HTTP 服务，外部用户访问：

```text
http://<可访问的节点 IP>:30080
```

需保证节点 IP 可从用户网络访问，并允许 TCP 30080 入站；若端口已被占用，可修改 Service 的 `nodePort`。NodePort 无需 Ingress Controller。集群支持外部负载均衡器时，也可将 `type` 改为 `LoadBalancer` 并移除 `nodePort`，通过 Service 分配的外部地址访问 80 端口。参见 [Kubernetes Service 文档](https://kubernetes.io/docs/concepts/services-networking/service/#type-nodeport)。

当前不实现选主，保持 `replicas: 1`，使用 `Recreate` 更新策略避免滚动更新期间两个实例同时写入；更新期间会短暂不可用。

## 已实现

- 只读主线、完整创建测试版本、无限层级目录和唯一用例 ID
- 用例编辑历史、执行记录、结果状态和批量创建测试任务
- 主线变更检测、拉取更新、文本冲突保护和版本合并
- 标题/ID 模糊搜索、字段包含搜索、平铺/目录两种结果视图
- 可拖动/折叠用例树、文件夹与用例右键菜单、测试记录抽屉、主题切换
- JSON 导出、Linux/Windows 构建脚本及容器部署

## 配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PORT` | `8080` | HTTP 监听端口 |
| `CASEHUB_STORE` | 自动选择 | 显式指定 `file`、`memory` 或 `postgres` 时优先；否则有 DB 连接串用 `postgres`，无连接串用 `file` |
| `CASEHUB_DATA` | `data/casehub.json` | 文件存储路径 |
| `DATABASE_URL` | — | PostgreSQL 连接串；可通过 `-database-url` 启动参数覆盖 |

当前默认主线带有两条示例用例，因为设计文档尚未定义首次导入主线的来源与格式。

## 前端追加功能

- 顶栏的「API 文档」打开 `/web/api.html`，列出两个实际接口、全部操作和请求示例。
- 测试记录使用本地打包的 TOAST UI Editor，支持 Markdown / 富文本切换、格式工具栏和格式化回看；保存内容为 Markdown。
- 点击编辑历史可查看变更前后快照，支持链接刷新和返回用例详情。

## 回归验证

运行 `go test ./...` 检查业务逻辑、静态资源和 Markdown 记录持久化。

浏览器回归脚本需要 Node.js、Playwright 和 Chromium。先在独立终端以 `CASEHUB_STORE=memory PORT=18081 go run .` 启动临时服务，再运行 `node tests/frontend.cjs`。可用 `PLAYWRIGHT_MODULE` 指定已有 Playwright 安装路径，用 `CASEHUB_TEST_URL` 指定临时服务地址。脚本会创建测试数据，请仅对临时内存服务执行。
