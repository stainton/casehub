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

部署文件为 [`deploy/kubernetes/casehub.yaml`](deploy/kubernetes/casehub.yaml)。将镜像地址替换为实际镜像后执行：

```sh
kubectl apply -f deploy/kubernetes/casehub.yaml
kubectl rollout status deployment/casehub
```

直接在 Deployment 的 `DATABASE_URL` 中填写 PostgreSQL 连接串即可，不需要创建 Secret。例如：`postgres://casehub:casehub@postgres.database.svc.cluster.local:5432/casehub?sslmode=disable`。数据库需要预先存在，程序会自动创建状态表。

`DATABASE_URL` 留空时使用 `/app/data/casehub.json`，默认挂载 `emptyDir`，Pod 删除或重建后这份临时数据会丢失。也可通过启动参数 `-database-url` 指定数据库连接串；配置了数据库但连接失败时会报错，便于检查配置。

清单已将 `CASEHUB_PLANNER_URL` 设置为 `http://planner:4501`，可直接连接同 namespace 中的 auto-test planner 服务。跨 namespace 时改成对应 Service 地址。

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
| `CASEHUB_PLANNER_URL` | `http://localhost:4501` | auto-test planner 服务地址；Kubernetes 清单使用 `http://planner:4501` |

当前默认主线带有两条示例用例，因为设计文档尚未定义首次导入主线的来源与格式。

## 与 auto-test 对接

先在 auto-test 中准备 `build/planner/setting.json` 并运行 `node server/planner/main.mjs`，再启动 CaseHub，即可从需求管理的「AI 设计」抽屉调用 planner。本机使用默认地址，无需 Token；分开部署时只设置 `CASEHUB_PLANNER_URL`。两个服务均支持浏览器跨域调用。

容器中的 localhost 指容器自身；如果 planner 运行在另一容器或主机上，将 `CASEHUB_PLANNER_URL` 配置为容器可访问的地址。

## 前端追加功能

- 顶栏的「API 文档」打开 `/web/api.html`，列出两个实际接口、全部操作和请求示例。
- 测试记录使用本地打包的 TOAST UI Editor，支持 Markdown / 富文本切换、格式工具栏和格式化回看；保存内容为 Markdown。
- 点击编辑历史可查看变更前后快照，支持链接刷新和返回用例详情。

## 回归验证

运行 `go test ./...` 检查业务逻辑、静态资源和 Markdown 记录持久化。

浏览器回归脚本需要 Node.js、Playwright 和 Chromium。先在独立终端以 `CASEHUB_STORE=memory PORT=18081 go run .` 启动临时服务，再运行 `node tests/frontend.cjs`。可用 `PLAYWRIGHT_MODULE` 指定已有 Playwright 安装路径，用 `CASEHUB_TEST_URL` 指定临时服务地址。脚本会创建测试数据，请仅对临时内存服务执行。
