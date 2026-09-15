# CaseHub

CaseHub 是按版本管理测试用例、测试任务和执行记录的本地系统。主线只读，测试版本从主线完整派生，文本变更经过冲突检查后合并。

## 本地运行

```sh
sh build.sh
./output/casehub
```

访问 `http://localhost:8080`。默认使用 `data/casehub.json` 持久化；测试时可设置 `CASEHUB_STORE=memory`。

Windows 使用：

```powershell
./build.ps1
./output/casehub.exe
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
- 主线变更检测、拉取更新、文本冲突保护和三种合并到主线的方式：整版本合并（保留目录结构）、
  文件夹合并（保留子树结构，合入选定的主线目标文件夹，同名冲突整体拒绝）、勾选/搜索结果合并
  （不保留目录，平铺合入单个目标文件夹）
- 标题/ID 模糊搜索、字段包含搜索、按测试结果过滤、平铺/目录两种结果视图
- 分支内空文件夹删除、勾选用例批量移动、整个测试版本删除（主线除外）、单条/批量删除分支用例（主线除外）
- 测试任务界面按版本分组（一个版本一个文件夹），任务作为其下唯一一级子文件夹，任务内用例平铺展示，不还原原目录结构
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

## 复制到飞书思维导图

在用例目录上右键选择「复制飞书思维导图」，或勾选用例后点击同名批量操作。预览确认内容后，点击「复制思维导图」。进入飞书云文档中的白板编辑模式，点击画布空白处，按 `Ctrl+V`（Mac：`⌘V`）粘贴为可编辑的思维导图。请使用复制按钮，手动复制预览文字不会携带思维导图格式。

导出保留版本、目录、用例层级，以及优先级、前置条件、执行步骤和预期结果；批量导出只包含选中用例及其祖先目录，支持跨版本选择。步骤和预期结果分别保留完整多行文本，不根据行号推断对应关系。原有 JSON 导出仍可使用。

此方式针对飞书白板的思维导图粘贴入口；直接粘贴到文档正文或正在编辑的节点文字中，可能只得到文本。浏览器复制被拒绝时会显示失败提示，可使用 HTTPS 或 localhost 并允许剪贴板访问后重试。

格式依据用户提供的 HAR 中白板 `1.0.0.6838` 的 HTML 粘贴解析逻辑实现，使用 `mm-editor-clipboard` 包装 URI 编码的 JSON，不调用飞书接口，不使用文档 token 或登录凭据。已验证浏览器富文本剪贴板往返；飞书账号内的最终粘贴效果尚未实测，其私有格式后续可能变化。分析见 [飞书导出格式说明](docs/feishu-mindmap-export.md)。

导出回归测试使用同一临时内存服务：`node tests/feishu-export.cjs`，支持上述 `PLAYWRIGHT_MODULE` 和 `CASEHUB_TEST_URL` 环境变量。
