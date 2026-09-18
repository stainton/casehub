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

清单已将 `CASEHUB_PLANNER_URL` 设置为 `http://planner:4501`、`CASEHUB_GENERATOR_URL` 设置为 `http://generator:4502`，可直接连接同 namespace 中的 auto-test planner 与 generator 服务。跨 namespace 时改成对应 Service 地址。

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
- 自动化管理页：脚本树与用例树同名同构（目录取自用例当前所在目录），脚本与用例一一对应，用例改动后脚本自动标记为"已过时"
- 脚本生成：在用例管理中右键目录（目录下全部用例）、右键单条用例或勾选后批量触发，调用 auto-test 的 generator 生成 Playwright 脚本，
  结果自动同步到自动化管理；每个生成任务是"脚本生成"抽屉中的一个可展开选项卡，可并行、可取消，关闭抽屉或刷新页面后按任务 ID 恢复
- 可拖动/折叠用例树、文件夹与用例右键菜单、测试记录抽屉、主题切换
- 所有用例详情页（用例树、测试任务、用例评审）都可切换「阅读友好版」与「Planner 原始内容」；阅读友好版由 auto-test 的
  `/v1/planner/simplify` 改写生成，和用例本身一样持久化存储（非浏览器缓存），用例内容变更后会自动判定为过时并提示重新生成
- JSON 导出、Linux/Windows 构建脚本及容器部署

## 配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PORT` | `8080` | HTTP 监听端口 |
| `CASEHUB_STORE` | 自动选择 | 显式指定 `file`、`memory` 或 `postgres` 时优先；否则有 DB 连接串用 `postgres`，无连接串用 `file` |
| `CASEHUB_DATA` | `data/casehub.json` | 文件存储路径 |
| `DATABASE_URL` | — | PostgreSQL 连接串；可通过 `-database-url` 启动参数覆盖 |
| `CASEHUB_PLANNER_URL` | `http://localhost:4501` | auto-test planner 服务地址；Kubernetes 清单使用 `http://planner:4501` |
| `CASEHUB_GENERATOR_URL` | `http://localhost:4502` | auto-test generator 服务地址；Kubernetes 中为 `http://generator:4502` |

当前默认主线带有两条示例用例，因为设计文档尚未定义首次导入主线的来源与格式。

## 与 auto-test 对接

先在 auto-test 中准备 `build/planner/setting.json` 并运行 `node server/planner/main.mjs`，再启动 CaseHub，即可从需求管理的「AI 设计」抽屉调用 planner，以及在任意用例详情页生成「阅读友好版」（调用同一服务的 `/v1/planner/simplify`）。本机使用默认地址，无需 Token；分开部署时只设置 `CASEHUB_PLANNER_URL`。两个服务均支持浏览器跨域调用。

「AI 设计」抽屉里可以设置本次设计任务的超时时间（分钟，1–240，默认 15，沿用上次填写的值），随请求的 `timeoutMs` 一起提交；
探索耗时取决于被测系统和用例数量，服务端的固定默认值对大需求常常不够。实际生效的时限会显示在任务进行中的面板上，
最终由 planner 服务按 `PLANNER_MAX_TIMEOUT_MS` 封顶。

「补充说明」是给 planner 的硬性约束，不只是背景说明：可以限定覆盖范围，也可以限制探索行为（例如"提交后确认任务已开始即可，
不要等待任务运行完成"——有些产品的任务要跑很久，干等会把整段时限耗光）。被约束挡住的验证会作为"未验证范围"返回，
planner 不会绕开约束去试；同一段文字也会参与"建议覆盖用例数量"的评估。

任务失败后抽屉给两个选择：「重试」用上次提交的表单从头开始，「继续」接着上次的探索往下做（planner 服务在失败任务上标了
`continuable` 时才出现，超时失败最常见）。两条路都会带出上次提交的表单——URL、补充说明、测试账号、用例数量、需求缩写和超时
时间都保存在浏览器里，刷新、关标签页后仍在，因此「继续」前可以先把超时时间调大。只有测试账号密码不写进浏览器存储，刷新后
需要重填，表单里会提示。失败任务本身也留在浏览器里，刷新后重新打开抽屉仍能看到失败原因和这两个按钮。

脚本生成对应 auto-test 的另一个独立服务：准备 `build/generator/setting.json` 后运行 `node server/generator/main.mjs`（默认 4502），CaseHub 通过 `CASEHUB_GENERATOR_URL` 连接。planner 和 generator 是两个进程，可以只启动其中一个；未配置时对应入口会提示服务未配置，其余功能不受影响。

容器中的 localhost 指容器自身；如果 planner / generator 运行在另一容器或主机上，将 `CASEHUB_PLANNER_URL`、`CASEHUB_GENERATOR_URL` 配置为容器可访问的地址。

## 前端追加功能

- 顶栏的「API 文档」打开 `/web/api.html`，列出两个实际接口、全部操作和请求示例。
- 测试记录使用本地打包的 TOAST UI Editor，支持 Markdown / 富文本切换、格式工具栏和格式化回看；保存内容为 Markdown。
- 点击编辑历史可查看变更前后快照，支持链接刷新和返回用例详情。

## 回归验证

运行 `go test ./...` 检查业务逻辑、静态资源和 Markdown 记录持久化。

「AI 设计」抽屉的浏览器回归（评估、失败后保留表单、重试与继续）：`node tests/ai-design.cjs`，脚本自带一个假的 planner 服务（默认 127.0.0.1:4598，`CASEHUB_FAKE_PLANNER_PORT` 可改），不需要 auto-test 在场，也不调用模型；启动临时服务时把 `CASEHUB_PLANNER_URL` 指向它：`CASEHUB_STORE=memory PORT=18081 CASEHUB_PLANNER_URL=http://127.0.0.1:4598 go run .`。

自动化管理与脚本生成的浏览器回归：`node tests/automation.cjs`，脚本自带一个假的 generator 服务（默认 127.0.0.1:4599，`CASEHUB_FAKE_GENERATOR_PORT` 可改），不需要 auto-test 在场，也不调用模型；启动临时服务时把 `CASEHUB_GENERATOR_URL` 指向它：`CASEHUB_STORE=memory PORT=18081 CASEHUB_GENERATOR_URL=http://127.0.0.1:4599 go run .`。

浏览器回归脚本需要 Node.js、Playwright 和 Chromium。先在独立终端以 `CASEHUB_STORE=memory PORT=18081 go run .` 启动临时服务，再运行 `node tests/frontend.cjs`。可用 `PLAYWRIGHT_MODULE` 指定已有 Playwright 安装路径，用 `CASEHUB_TEST_URL` 指定临时服务地址。脚本会创建测试数据，请仅对临时内存服务执行。

## 复制到飞书思维导图

在用例目录上右键选择「复制飞书思维导图」，或勾选用例后点击同名批量操作。预览确认内容后，点击「复制思维导图」。进入飞书云文档中的白板编辑模式，点击画布空白处，按 `Ctrl+V`（Mac：`⌘V`）粘贴为可编辑的思维导图。请使用复制按钮，手动复制预览文字不会携带思维导图格式。

导出保留版本、目录、用例层级，以及优先级、前置条件、执行步骤和预期结果；批量导出只包含选中用例及其祖先目录，支持跨版本选择。步骤和预期结果分别保留完整多行文本，不根据行号推断对应关系。原有 JSON 导出仍可使用。

此方式针对飞书白板的思维导图粘贴入口；直接粘贴到文档正文或正在编辑的节点文字中，可能只得到文本。浏览器复制被拒绝时会显示失败提示，可使用 HTTPS 或 localhost 并允许剪贴板访问后重试。

格式依据用户提供的 HAR 中白板 `1.0.0.6838` 的 HTML 粘贴解析逻辑实现，使用 `mm-editor-clipboard` 包装 URI 编码的 JSON，不调用飞书接口，不使用文档 token 或登录凭据。已验证浏览器富文本剪贴板往返；飞书账号内的最终粘贴效果尚未实测，其私有格式后续可能变化。分析见 [飞书导出格式说明](docs/feishu-mindmap-export.md)。

导出回归测试使用同一临时内存服务：`node tests/feishu-export.cjs`，支持上述 `PLAYWRIGHT_MODULE` 和 `CASEHUB_TEST_URL` 环境变量。


## 合并后的历史与测试记录

合并版本、目录或选中用例时，主线会保存该用例的编辑历史快照和测试记录副本（包含草稿、提交状态、作者、时间、备注和来源版本）。删除分支用例或整个测试版本，不会删除已经归档到主线的内容。主线用例的「测试记录」默认显示只读归档；仍存在对应分支时，可以切换到分支查看和提交记录。

重复合并会去重，没有用例文本修改时也可通过合并版本或选中用例补存新增记录。归档以最后一次合并为准，之后新增的记录须再次合并才会保留。升级前已经合并的用例，请在删除分支前再合并一次；已经删除的数据无法通过此功能恢复。

回归测试：`go test ./...`；对临时内存服务运行 `node tests/mainline-archive.cjs` 验证主线记录展示、分支切换、版本删除和历史快照跳转，支持 `PLAYWRIGHT_MODULE`、`CASEHUB_TEST_URL`。
