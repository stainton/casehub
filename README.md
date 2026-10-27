# CaseHub

CaseHub 是按版本管理测试用例、测试任务和执行记录的本地系统。主线只读，测试版本从主线完整派生，文本变更经过冲突检查后合并。

项目结构与设计说明见 [docs/architecture.md](docs/architecture.md)。

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

> 与 auto-test 节点一起部署（存储选择、节点地址、重启与升级的影响）见 [DEPLOYMENT.md](DEPLOYMENT.md)。

部署文件为 [`deploy/kubernetes/casehub.yaml`](deploy/kubernetes/casehub.yaml)。将镜像地址替换为实际镜像后执行：

```sh
kubectl apply -f deploy/kubernetes/casehub.yaml
kubectl rollout status deployment/casehub
```

直接在 Deployment 的 `DATABASE_URL` 中填写 PostgreSQL 连接串即可，不需要创建 Secret。例如：`postgres://casehub:casehub@postgres.database.svc.cluster.local:5432/casehub?sslmode=disable`。数据库需要预先存在，程序会自动创建状态表。

`DATABASE_URL` 留空时使用 `/app/data/casehub.json`，默认挂载 `emptyDir`，Pod 删除或重建后这份临时数据会丢失。也可通过启动参数 `-database-url` 指定数据库连接串；配置了数据库但连接失败时会报错，便于检查配置。

`DATABASE_URL` 留空时任务记录（`/app/data/tasks/`）同样在 emptyDir 里，重建 Pod 即丢失；生产环境请使用 PostgreSQL。

清单已将 `CASEHUB_PLANNER_URL` 设置为 `dns+http://planner-nodes:4501`，`CASEHUB_GENERATOR_URL` 与 `CASEHUB_HEALER_URL` 设置为 `dns+http://generator-nodes:4502`（healer 运行在 generator 中），`CASEHUB_EXECUTOR_URL` 设置为 `dns+http://executor-nodes:4504`：这些是 auto-test 清单里的 headless Service，解析出同 namespace 中每个就绪的节点 Pod。跨 namespace 时改成 `dns+http://planner-nodes.<namespace>.svc.cluster.local:4501` 等。

### 集群外访问

页面与 API 共用同一个 HTTP 服务，外部用户访问：

```text
http://<可访问的节点 IP>:30080
```

需保证节点 IP 可从用户网络访问，并允许 TCP 30080 入站；若端口已被占用，可修改 Service 的 `nodePort`。NodePort 无需 Ingress Controller。集群支持外部负载均衡器时，也可将 `type` 改为 `LoadBalancer` 并移除 `nodePort`，通过 Service 分配的外部地址访问 80 端口。参见 [Kubernetes Service 文档](https://kubernetes.io/docs/concepts/services-networking/service/#type-nodeport)。

当前不实现选主，保持 `replicas: 1`，使用 `Recreate` 更新策略避免滚动更新期间两个实例同时写入；更新期间会短暂不可用。

## 已实现

- 只读主线、创建测试版本（主线里勾选了用例时只含所选用例及其所在目录，没勾选则完整复制主线）、无限层级目录和唯一用例 ID
- 用例编辑历史、执行记录、结果状态和批量创建测试任务
- 主线变更检测、拉取更新（点「拉取主线」不会静默拉取：弹窗按主线用例树列出「有更新」「新增」「冲突」的用例，由人勾选，勾选文件夹即批量选中其下用例，目录可折叠/展开；已有用例的更新默认勾选，新增的默认不勾，冲突的不可选；新增用例按主线目录结构放入，已有用例保留它在本版本里所在的目录；回归 `node tests/sync-pull.cjs`）、文本冲突保护和三种合并到主线的方式：整版本合并（保留目录结构）、
  文件夹合并（保留子树结构，合入选定的主线目标文件夹，同名冲突整体拒绝）、勾选/搜索结果合并
  （不保留目录，平铺合入单个目标文件夹）
- 标题/ID 模糊搜索、字段包含搜索、按测试结果过滤、平铺/目录两种结果视图
- 分支内空文件夹删除、勾选用例批量移动、整个测试版本删除（主线除外）、单条/批量删除分支用例（主线除外）
- 完成与归档版本：测试版本点「完成」后移入侧栏「用例树 / 测试任务 / 归档版本」第三个页签「归档版本」（用例树和测试任务里不再出现；归档页签里每个版本显示它的只读用例树和它的测试任务）。完成后只读——不能勾选、编辑、拉取主线、合并或记录结果，需要时「重新打开」；仍可删除。完成前未合并到主线的修改会在确认框里提示。回归：`node tests/archive-version.cjs`
- 测试任务界面按版本分组（一个版本一个文件夹），任务作为其下唯一一级子文件夹，任务内用例平铺展示，不还原原目录结构
- 自动化管理页：脚本树与用例树同名同构（目录取自用例当前所在目录），脚本与用例一一对应，用例改动后脚本自动标记为"已过时"
- 脚本生成：在用例管理中右键目录（目录下全部用例）、右键单条用例或勾选后批量触发，调用 auto-test 的 generator 生成 Playwright 脚本，
  结果自动同步到自动化管理；每个生成任务是"脚本生成"抽屉中的一个可展开选项卡，可并行、可取消，关闭抽屉或刷新页面后按任务 ID 恢复
- 可拖动/折叠用例树、文件夹与用例右键菜单、测试记录抽屉、主题切换
- 所有用例详情页（用例树、测试任务、用例评审）都可切换「阅读友好版」与「Planner 原始内容」；阅读友好版由 auto-test 的
  general-agent 改写生成，和用例本身一样持久化存储（非浏览器缓存），用例内容变更后会自动判定为过时并提示重新生成
- 知识库页：结构和需求文档一致（文件夹树 + Markdown 文档，可新建/重命名/删除，同样保存在 CaseHub 数据库的 state 中），存放人工整理的约束和补充信息；产品缺陷仍记录在问题单管理。顶层分为「全局库」（适用于所有产品的知识，即原来的根目录）和产品库：主线「全部用例」下的每个一级目录（不递归）各对应一个产品库，名称跟随该目录，不能单独重命名或删除。产品库随主线每次变动同步创建/改名；产品从主线移除后，空的产品库随之删除，有人工文档的保留为普通文件夹（名称后加「（产品已移除）」）。早期版本在产品库里自动生成的只读「用例库」文档已取消，加载时自动删除。早期版本的扁平条目会在加载时自动迁移为文档（按分类放进「约束」「补充信息」文件夹）。**每份文档有「类型」，新增时选择，之后可在详情页切换：**「约束」是 agent 工作时必须遵守的规则（例如用例树构建约束），由前端汇总成 `context.constraints`（Markdown，每份文档一节，标注 `[全局]` 或 `[产品：X]`），随 AI 设计、AI 托管（设计与脚本生成）、脚本生成、脚本修复、用例重整的请求发给 auto-test，节点的提示词规定它具有约束力；全局库的约束总是下发，产品库的约束只在涉及该产品时下发（需求取所在目录倒数第二级、用例取其在「全部用例」下的一级目录、用例重整取所填产品名）。「参考」只存储，不下发。通用 AI（阅读友好版、AI 修正、报告总结）和用例数评估暂不带约束。
- 目录右键「总结测试报告」：把目录及子目录下全部用例的最新结果、测试记录摘要，连同这些用例关联的需求文档（按用例编号 `TC-<需求缩写>-…` 匹配）和需求下未解决的问题单一并交给通用 AI。弹窗里按步骤显示进度（整理用例 → 载入需求 → AI 分析，附已等待秒数和 180 秒上限；通用 AI 一次性返回，没有更细的进度）、可取消、失败可重试，完成后直接预览并可下载/复制 Markdown。报告不罗列用例步骤：总体结论、结果统计、按目录分节的两列表「功能点 / 结果」（一个目录一节，节名即目录所代表的需求功能模块；默认一个用例一行，AI 用一句话说明它测什么，仅当多个用例测的点高度相似才合并；结果由前端按所含用例取最差值汇总，AI 漏掉的用例自动补行）、高风险项（只列高优先级用例失败/阻塞、阻断主流程的缺口、严重的未解决问题，每项一两句话）。它与逐用例的「输出测试报告」互补。回归：`node tests/folder-summary.cjs`
- JSON 导出、Linux/Windows 构建脚本及容器部署

## 配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PORT` | `8080` | HTTP 监听端口 |
| `CASEHUB_STORE` | 自动选择 | 显式指定 `file`、`memory` 或 `postgres` 时优先；否则有 DB 连接串用 `postgres`，无连接串用 `file` |
| `CASEHUB_DATA` | `data/casehub.json` | 文件存储路径 |
| `DATABASE_URL` | — | PostgreSQL 连接串；可通过 `-database-url` 启动参数覆盖 |
| `CASEHUB_PLANNER_URL` | `http://localhost:4501` | auto-test planner 节点地址；Kubernetes 清单使用 `dns+http://planner-nodes:4501` |
| `CASEHUB_GENERATOR_URL` | `http://localhost:4502` | auto-test generator 节点地址；Kubernetes 中为 `dns+http://generator-nodes:4502` |
| `CASEHUB_HEALER_URL` | 同 `CASEHUB_GENERATOR_URL` | auto-test healer（脚本修复）地址；默认由 generator 服务提供 |
| `CASEHUB_AUTOMATION_URL` | — | 可选的单进程 auto-test automation 服务地址；未单独设置 `CASEHUB_PLANNER_URL` / `CASEHUB_GENERATOR_URL` 时，planner、generator、healer 都通过此地址访问 |
| `CASEHUB_GENERAL_AGENT_URL` | `http://localhost:4503` | auto-test general-agent 服务地址；Kubernetes 中为 `http://general-agent:4503` |
| `CASEHUB_ORGANIZER_URL` | 同 `CASEHUB_PLANNER_URL` | 用例重整（临时功能）的 auto-test organizer 地址；它和 planner 在同一进程/Pod 里，一般不用单独设置 |
| `CASEHUB_EXECUTOR_URL` | `http://localhost:4504` | auto-test executor 服务地址 |
| `CASEHUB_TASK_QUEUE_TIMEOUT` | `30m` | 任务排队这么久仍没有执行节点接收，则以 `NO_NODE_AVAILABLE` 失败 |
| `CASEHUB_TASK_RETENTION` | `168h` | 已结束任务（含进度、结果、会话包、诊断包）保留时长 |
| `CASEHUB_NODE_LOST_AFTER` | `2m` | 执行节点连续联系不上这么久，即视为其上的任务已丢失 |

以上各 `*_URL` 都可以写成三种形式之一：单个地址；逗号分隔的多个节点（`http://a:4501,http://b:4501`，进程部署多开节点时用）；或 `dns+http://planner-nodes:4501`（Kubernetes headless Service，每次派发前解析出当前所有就绪 Pod）。

### 任务分发

planner、generator、healer、executor 的任务（`/api/<服务>/jobs`）由 CaseHub 持有并分发，接口形状与 auto-test 节点自己的 `/v1/<服务>/jobs` 一致。节点是无状态执行节点：CaseHub 先查各节点的 `/capacity`，把任务交给空闲的那个（`?queue=false`，忙则 429 换下一个；同一被测系统和测试账号优先交给已登录过的节点），推送所需资产，跟随节点的进度并存下来，任务结束后取回结果、planner 中断时的会话包和失败现场的诊断包，再在节点上释放该任务。所有节点都忙或都不在时任务在 CaseHub 排队，由节点空出或上线后接着派发。

- 任务记录、进度事件、结果、会话包和诊断包保存在 CaseHub 的存储里（文件存储为数据文件旁的 `tasks/` 目录，PostgreSQL 为 `casehub_tasks` 等表），CaseHub 或节点重启、换浏览器后都能找回；前端通过 `GET /api/tasks?kind=…&active=1` 找回未处理完的任务，不再在 localStorage 里保存任务 ID。
- 提交的请求内容（含测试账号、密码、storageState）平时只保存在 CaseHub 进程内存中；任务排队等节点期间会暂存到任务存储，交给节点后立即清除。CaseHub 在任务还排队时重启，该任务回到队列继续派发；已经在节点上运行的任务重启后继续跟随。只有请求没暂存下来的任务才以 `CASEHUB_RESTARTED` 失败，需要重新提交。
- 节点停机（缩容、重新部署）交还的任务、或节点丢失时尚未开始的任务自动重新排队；generator、healer、executor 运行中被中断的任务最多重试到第 3 次。planner 运行中被中断的任务不自动重跑，而是保留会话包，由人选择"继续"——可以在任意 planner 节点继续。旧版本的节点（不支持 `/capacity`、会话包下载）仍可使用，其会话只能回到原节点继续。
- `GET /metrics`（Prometheus 文本）和 `GET /api/queue`（JSON，`{"planner":{"queued":…,"running":…,"demand":…,"nodes":…}}`）给出每类任务的排队数、运行数和节点数，供自动扩缩容读取（auto-test 的 `deploy/kubernetes/components/autoscaling` 用 KEDA 读 `/api/queue`）；不扩缩时无需理会。
- CaseHub 是任务的唯一写入方，只运行一个副本；要多开的是 auto-test 节点。

部署、扩容与升级的完整说明见 [DEPLOYMENT.md](DEPLOYMENT.md)。

当前默认主线带有两条示例用例，因为设计文档尚未定义首次导入主线的来源与格式。

## 与 auto-test 对接

先在 auto-test 中准备 `build/planner/setting.json` 并运行 `node server/planner/main.mjs`，即可从需求管理的「AI 设计」抽屉调用 planner。阅读友好版则调用独立的 general-agent：准备 `build/general-agent/setting.json` 并运行 `node server/general-agent/main.mjs`。本机使用默认地址，无需 Token；分开部署时分别设置 `CASEHUB_PLANNER_URL`、`CASEHUB_GENERAL_AGENT_URL`。服务均支持浏览器跨域调用。

「AI 设计」抽屉先选择 agent，再显示对应参数。目前可选 aigc用例设计，选择后填写被测系统 URL、补充说明和测试账号等参数；重试与继续会恢复原 agent，旧版任务按 aigc用例设计 处理。

「AI 设计」旁边的「AI 托管」走同一条流程，但不再逐步人工确认，并且**由 CaseHub 服务端执行**（`internal/hosting`）：提交后关掉页面、换浏览器，甚至 CaseHub 重启，托管都会继续。填写被测系统、账号、资产、超时和**归档位置**（目标测试版本、产品、产品版本，默认取需求在需求树里的上两级目录；还没有合适的测试版本时可选「＋ 新建测试版本…」，开始托管时创建一个空版本作为目标）后，服务端调 planner 评估并直接采用评估出的用例数量和需求缩写（缩写已被其他需求占用时自动改用 `LOGIN2` 这样的下一个可用值），提交设计任务；任务完成后把用例放入「用例评审」里以需求命名的目录（同时保存探索记录、产品经验，并把发现的问题记入问题单管理），以「AI 托管」的名义全部评审通过，再导入目标测试版本的 `全部用例 / <产品> / <产品版本> / <需求>`，缺的上级目录自动创建、已有的按名称复用。勾选「托管范围」后还会继续：**生成自动化脚本**——把归档的用例（连同需求和探索记录）提交给 generator，可选择「已生成的脚本逐个存储」：勾选后每条用例生成完就立即保存脚本并继续下一条；不勾选则等全部用例生成完成后一次保存结果（受阻的也保存，注明缺什么），脚本生成时发现的预期与实际不符记入问题单；**自动执行一次**——为每个生成的脚本提交一个 executor 任务，执行完保存一条已提交的测试记录（通过/失败，附执行报告和截图）。执行任务本身失败（浏览器崩溃、节点丢失）的不算结果，托管会停在「自动化执行」，「重试这一步」只重跑这些。每一步是一次原子操作，进度记在状态里的 `hostings`（每份需求一条），页面只负责发起和显示进度（托管进行中时轮询 `GET /api/hostings`）。任务中心有单独的「AI 托管」页签：「进行中」列出还没完成的托管（含停下等人处理的），「已完成」列出已归档的，每行显示阶段和归档位置，点开进入该需求的托管视图；托管的设计任务不出现在「Planner · 设计」页签里。

托管也可以从「用例管理」发起：在测试版本里勾选用例后点批量栏的「AI 托管」，或右键目录选「AI 托管」（=目录下全部用例）。流程和上面一样，只是没有 AI 设计用例的阶段——服务端直接为这些用例生成自动化脚本（按用例编号 `TC-<需求缩写>-…` 匹配需求文档作为背景，探索记录和发现的偏差也记到对应需求下；没有执行步骤的用例跳过），勾选后再自动执行一次并保存测试记录。已有脚本的用例会重新生成（旧脚本留在脚本历史里）。一条用例同时只能在一个进行中的托管里；再次托管同一批（或更多）用例会替换已结束的旧记录。只需要配置 generator（不需要 planner），停下、重试、放弃和需求托管相同，在任务中心「AI 托管」页签里按托管的目录名或「已选 N 条用例」列出。请求为 `POST /api/hostings`，带 `Source: "cases"`、`VersionID`、`CaseIDs`、`Label`、`withRun`、`saveScriptsIndividually` 和不含用例的生成请求 `generate`；`GET /api/hostings` 的 `cases` 表示能否托管用例。

某一步失败时托管停下：导入、评审、归档失败（如目标位置已有同名用例）可「重试这一步」（`POST /api/hostings/{id}/retry`，不会重新设计、不会重复导入）；设计任务失败可重新填写后「继续上次的设计」或「重新设计」——测试账号只随请求留在 CaseHub 内存里、从不落盘，一直留到脚本生成任务提交（不生成脚本时到设计任务提交）为止：期间的停下和重试都不用重新填写，但 CaseHub 在这之前重启的话，评估、设计或脚本生成阶段需要重新填写（「重新填写并生成」只补生成用的参数，不会重新设计）。执行脚本不需要账号，重启后照常继续。「取消托管/放弃托管」（`DELETE /api/hostings/{id}`）会取消正在跑的设计任务，已放入评审区的用例留给人工处理。托管的设计任务 subject 是 `hosting:<需求 ID>`，不会出现在手动「AI 设计」的确认流程里。

点击右上角头像弹出菜单，里面有「设置」和「资产」。「设置」打开 agent 设置弹窗：左侧选择 agent（aigc用例设计 / 脚本生成 / 通用 AI，各自一份配置），右侧分为两部分，分别保存：

- **业务默认参数**：默认被测系统 URL、补充说明、用户名、密码和任务时长，新任务选择该 agent 后自动带入，已有任务保留原参数。默认密码可预填、修改和清空。保存业务默认参数不会改变配置版本号，agent 不会因此改写自己的 setting.json。
- **setting.json**：agent 的完整 Claude 配置正文（任意 `env`、模型、权限及扩展字段），按原文保存，CaseHub 不认识的字段照样保留。保存会校验 JSON 格式及 `env`/`model` 基本类型，并用版本号检查是否有人在此期间保存过，避免覆盖新内容。

两部分都存在 CaseHub 的 agent 配置表里（PostgreSQL 为 `casehub_agent_settings`，文件存储为状态文件旁的 `casehub-agents.json`，memory 模式仅在进程内），不同设备共享。

### 资产

头像菜单的「资产」与设置弹窗同样的布局：左侧类型（图片 / 视频 / 音频），右侧上传、预览、删除。上限分别为 15 / 200 / 50 MB。资产存在 CaseHub 数据库里（PostgreSQL 为 `casehub_assets`，文件存储为状态文件旁的 `casehub-assets.json` 加 `casehub-assets-data/` 目录，memory 模式仅在进程内），列表和解析只读元数据，不加载文件内容。

「AI 设计」抽屉里可勾选要带给 agent 的资产。agent 没有 CaseHub 的数据库访问权限，也不会回头找 CaseHub 拿文件：提交时前端只发资产 id（`context.assetIds`），CaseHub 代理先把文件推给 agent（`HEAD /v1/planner/assets/{sha256}` 询问 agent 缓存里有没有，没有再 `PUT` 字节，同一文件不会重复发送），再把 `assetIds` 换成 `context.assets`（`{id, name, type, mimeType, sha256, size}`，只有引用）转发。agent 把文件缓存到本地，任务开始时复制进工作目录，探索确实需要时直接用本地路径。已被删除的资产会被忽略，不会让任务失败；agent 收不下资产（不可达、版本过旧没有该接口）时任务返回 502 `PLANNER_ASSETS_UNAVAILABLE`，不会不带资产悄悄继续。

### 配置如何到达 agent

CaseHub 不读写 agent 的文件系统，也不需要共享目录：每次发起任务（设计、评估、阅读友好版、脚本生成）时，代理会把该 agent 存着的配置连同版本号放进请求体的 `agentSettings`（`{revision, content}`）。agent 在任务开始前把自己的 `setting.json` 对齐成这份 `content`（内容一致就不写盘），随后每次调用 Claude CLI 都读这份配置，无需重启。已运行的 Claude 进程不受影响。

因此 agent 那边的文件只是落地结果：镜像重建、回滚到旧镜像或有人手工改过，下一次任务都会把 CaseHub 里的配置重新写回去，不需要持久卷，也不需要先在 CaseHub 改一次配置来触发。尚未在 CaseHub 保存过配置时（版本号 0），请求不带 `agentSettings`，agent 继续使用镜像内自带的 `setting.json`。CaseHub 读不到自己的配置表时，该请求直接以 `PLANNER_SETTINGS_UNAVAILABLE` / `GENERATOR_SETTINGS_UNAVAILABLE` 失败，不会拿一份来源不明的配置去跑任务。

升级到本机制之前保存过的业务默认参数会自动迁移到新表；旧的状态文档里那份保留不动，作为回退。

业务配置接口为 `GET/PUT /api/agent-settings/{agent}`（`playwright`、`generator`）；setting.json 为 `GET/PUT /api/agent-settings/{agent}/runtime`（`content` 为完整 JSON 文本，保存时带上读取到的 `revision`，版本号不一致返回 409）。关闭弹窗会放弃未保存的修改，「恢复初始值」需保存后生效。用例数量与需求缩写仍根据具体需求评估。

「AI 设计」抽屉里可以设置本次设计任务的超时时间（分钟，1–240，新任务采用 agent 业务默认时长，初始为 15），随请求的 `timeoutMs` 一起提交；
探索耗时取决于被测系统和用例数量，服务端的固定默认值对大需求常常不够。实际生效的时限会显示在任务进行中的面板上，
最终由 planner 服务按 `PLANNER_MAX_TIMEOUT_MS` 封顶。

「补充说明」是给 planner 的硬性约束，不只是背景说明：可以限定覆盖范围，也可以限制探索行为（例如"提交后确认任务已开始即可，
不要等待任务运行完成"——有些产品的任务要跑很久，干等会把整段时限耗光）。被约束挡住的验证会作为"未验证范围"返回，
planner 不会绕开约束去试；同一段文字也会参与"建议覆盖用例数量"的评估。

任务失败后抽屉给两个选择：「重试」用上次提交的表单从头开始，「继续」接着上次的探索往下做（planner 服务在失败任务上标了
`continuable` 时才出现，超时失败最常见）。两条路都会带出上次提交的表单——URL、补充说明、测试账号、用例数量、需求缩写和超时
时间都保存在浏览器里，刷新、关标签页后仍在，因此「继续」前可以先把超时时间调大。只有测试账号密码不写进浏览器存储，刷新后
需要重填，表单里会提示。失败任务本身也留在浏览器里，刷新后重新打开抽屉仍能看到失败原因和这两个按钮。

每份需求文档还保存独立的探索记录：AI 设计完成时 CaseHub 保存 planner 返回的记录；脚本生成或修复完成时保存 generator/healer 按需求返回的记录。另外按被测系统 origin 保存一份产品级探索经验（各需求共用的入口、导航和控件定位），服务每次只返回新验证的段落，由 CaseHub 去重合并。下一次设计、生成或修复时，CaseHub 把需求记录（`explorationNotes`）和对应 origin 的产品级经验（`context.productExperience`）随请求带给 auto-test；auto-test 自身不保存经验，planner 与 generator 分开部署也共用同一份。

脚本生成对应 auto-test 的另一个独立服务：准备 `build/generator/setting.json` 后运行 `node server/generator/main.mjs`（默认 4502），CaseHub 通过 `CASEHUB_GENERATOR_URL` 连接。general-agent（默认 4503）提供不带 Playwright 的通用 Claude CLI 调用，CaseHub 当前用它生成阅读友好版，通过 `CASEHUB_GENERAL_AGENT_URL` 连接。三个服务相互独立，可以只启动所需服务；未配置时对应入口会提示服务未配置，其余功能不受影响。

容器中的 localhost 指容器自身；如果 agent 服务运行在另一容器或主机上，将对应的 `CASEHUB_*_URL` 配置为容器可访问的地址。

## 前端追加功能

- 顶栏的「API 文档」打开 `/web/api.html`，列出两个实际接口、全部操作和请求示例。
- 测试记录使用本地打包的 TOAST UI Editor，支持 Markdown / 富文本切换、格式工具栏和格式化回看；保存内容为 Markdown。
- 点击编辑历史可查看变更前后快照，支持链接刷新和返回用例详情。

## 回归验证

运行 `go test ./...` 检查业务逻辑、静态资源和 Markdown 记录持久化。

Kubernetes 部署中，planner、generator（同时提供 healer）、executor 各是一组无状态 Pod，CaseHub 通过 headless Service `planner-nodes:4501`、`generator-nodes:4502`、`executor-nodes:4504` 找到每个就绪 Pod 并把任务交给空闲的那个；副本数可以固定，也可以引入 auto-test 的 autoscaling 组件按排队数自动扩缩；先在 auto-test 中执行 `kubectl apply -k deploy/kubernetes`（同时部署 general-agent），再部署 CaseHub。

「AI 设计」抽屉的浏览器回归（评估、失败后保留表单、重试与继续）：`node tests/ai-design.cjs`，脚本自带一个假的 planner 服务（默认 127.0.0.1:4598，`CASEHUB_FAKE_PLANNER_PORT` 可改），不需要 auto-test 在场，也不调用模型；启动临时服务时把 `CASEHUB_PLANNER_URL` 指向它：`CASEHUB_STORE=memory PORT=18081 CASEHUB_PLANNER_URL=http://127.0.0.1:4598 go run .`。

「AI 托管」的浏览器回归（提交后关掉浏览器，服务端仍完成 评估 → 设计 → 自动评审 → 归档到 产品/版本/需求；停下后在页面上重试）：`node tests/ai-host.cjs`，同样自带假的 planner 服务，启动方式同上；最后一段从用例管理（右键目录、勾选用例）发起托管。服务端流程（含 CaseHub 重启后续跑、继续失败的设计、脚本生成与执行、只重跑执行出错的脚本、放弃、托管用例）另有 `go test ./internal/hosting/`。浏览器回归的假服务同时充当 generator 和 executor，启动 CaseHub 时把 `CASEHUB_GENERATOR_URL`、`CASEHUB_EXECUTOR_URL` 也指向它。

「AI 修正需求」浏览器回归（详情页按钮 / 右键菜单、变更确认、需求与关联用例一起保存、用例打标与确认）：`node tests/req-ai-revise.cjs`，通用 AI 服务由脚本拦截成固定结果，不调用模型；只对 `CASEHUB_STORE=memory PORT=18081 go run .` 启动的临时服务运行。

「用例重整」可在主线和未归档的测试版本上使用，按所选版本独立提交 organizer 任务、预览并应用目录和编号调整；已归档版本仍只读。主线重整仍要求先处理所有未归档的测试版本。测试版本重整后合并时，继承自主线的用例会按新编号替换原用例，不会留下旧编号的重复项。

「用例重整」（临时功能）的浏览器回归：`node tests/reorganize.cjs`，脚本自带一个假的 organizer 服务（与假 planner 同一端口，默认 127.0.0.1:4598），启动临时服务时把 `CASEHUB_PLANNER_URL` 指向它即可（organizer 默认同址）。

自动化管理与脚本生成的浏览器回归：`node tests/automation.cjs`，脚本自带一个假的 generator 服务（默认 127.0.0.1:4599，`CASEHUB_FAKE_GENERATOR_PORT` 可改），不需要 auto-test 在场，也不调用模型；启动临时服务时把 `CASEHUB_GENERATOR_URL` 指向它：`CASEHUB_STORE=memory PORT=18081 CASEHUB_GENERATOR_URL=http://127.0.0.1:4599 go run .`。

agent 设置弹窗回归：`node tests/agent-settings.cjs`。临时服务需设置 `CASEHUB_STORE=memory`，配置只写入该进程内的存储，不会动到实际 agent 的配置。

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
