# CaseHub 部署指南

本文说明 CaseHub 作为 auto-test 任务派发方时的部署方式：存储怎么选、怎样连接执行节点、重启和升级有什么影响。auto-test 节点自身的部署、扩缩容和停机行为，见 auto-test 仓库的 `DEPLOYMENT.md`。

## 1. CaseHub 在其中的角色

AI 设计（planner）、脚本生成（generator）、脚本修复（healer）和脚本执行（executor）的任务都由 CaseHub 持有：

- **派发**：CaseHub 把任务排进队列，查询每个 auto-test 节点的空闲槽位，交给空闲的节点，同时推送资产和 agent 配置。
- **跟随与收尾**：CaseHub 跟随节点上的进度并存下来，任务结束后取回结果、planner 的会话包和失败现场的诊断包，再通知节点释放这个任务。
- **对前端**：前端只和 CaseHub 打交道，可以从 `/api/tasks` 找回自己的任务，换浏览器也不受影响。

由此带来三条部署约束：

1. **CaseHub 只能运行一个副本。** 它是任务记录的唯一写入方；要扩容的是 auto-test 节点。
2. **任务记录需要持久存储。** 生产环境使用 PostgreSQL。
3. **CaseHub 必须能访问每个节点，节点不需要访问 CaseHub。**

## 2. 选择存储

| `CASEHUB_STORE` | 适用场景 | 任务记录存放位置 |
| --- | --- | --- |
| `postgres`（设置了 `DATABASE_URL` 时自动选用） | 生产环境、Kubernetes | `casehub_tasks`、`casehub_task_events`、`casehub_task_blobs` 三张表，启动时自动创建 |
| `file`（默认） | 单机进程部署 | 数据文件旁的 `tasks/` 目录（默认 `data/tasks/`），每个任务一个 JSON 文件，会话包和诊断包与之同名 |
| `memory` | 测试 | 进程内存，重启即丢失 |

注意事项：

- **file 存储的数据目录必须是持久目录。** Kubernetes 清单里 `DATABASE_URL` 留空时，数据目录是 `emptyDir`，Pod 重建后用例和任务都会丢失。
- **请求内容不会写入任何存储。** 测试账号、密码和 storageState 只保存在 CaseHub 进程内存里，直到任务结束；数据库和文件中只有任务状态、进度和结果。
- **会话包和诊断包会占用空间。** 会话包通常几 KB 到几 MB，诊断包最大 50 MiB。已结束的任务在 `CASEHUB_TASK_RETENTION`（默认 7 天）后连同这些文件一起删除。

## 3. 连接 auto-test 节点

每类节点的地址都支持三种写法：

| 写法 | 示例 | 适用场景 |
| --- | --- | --- |
| 单个地址 | `http://localhost:4501` | 只有一个节点 |
| 逗号分隔的列表 | `http://10.0.0.5:4501,http://10.0.0.6:4501` | 进程部署多开节点 |
| `dns+` 前缀 | `dns+http://planner-nodes:4501` | Kubernetes headless Service，每次派发前解析出所有就绪 Pod |

| 变量 | 默认值 | Kubernetes 清单中的值 |
| --- | --- | --- |
| `CASEHUB_PLANNER_URL` | `http://localhost:4501` | `dns+http://planner-nodes:4501` |
| `CASEHUB_GENERATOR_URL` | `http://localhost:4502` | `dns+http://generator-nodes:4502` |
| `CASEHUB_HEALER_URL` | 同 generator | `dns+http://generator-nodes:4502`（healer 运行在 generator 进程中） |
| `CASEHUB_EXECUTOR_URL` | `http://localhost:4504` | `dns+http://executor-nodes:4504` |
| `CASEHUB_GENERAL_AGENT_URL` | `http://localhost:4503` | `http://general-agent:4503`（只有同步调用，不经过任务队列） |
| `CASEHUB_ORGANIZER_URL` | 同 `CASEHUB_PLANNER_URL` | 一般不用设：用例重整（临时功能）的 organizer 跑在 planner Pod 里，任务走 `/api/organizer/jobs`，使用 planner 的 agent 配置 |
| `CASEHUB_AUTOMATION_URL` | — | 只在使用单进程 automation 时设置；未单独设置 planner、generator 地址时，三类任务都发往这里 |

CaseHub 与 auto-test 不在同一 namespace 时，`dns+` 地址要写完整域名，例如 `dns+http://planner-nodes.<namespace>.svc.cluster.local:4501`。

## 4. 部署方式

**进程部署**：

```sh
sh build.sh
DATABASE_URL=postgres://casehub:***@db:5432/casehub \
CASEHUB_PLANNER_URL=http://10.0.0.5:4501,http://10.0.0.6:4501 \
CASEHUB_GENERATOR_URL=http://10.0.0.5:4502 \
./output/casehub
```

不设置 `DATABASE_URL` 时使用 file 存储，需确保 `CASEHUB_DATA` 所在目录是持久的。

**Docker Compose**：`docker compose up --build` 会同时启动 PostgreSQL。节点地址默认指向宿主机的 4501 和 4502 端口，可通过 `CASEHUB_PLANNER_URL` 等环境变量覆盖，写法同样可以是地址列表。

**Kubernetes**：

1. 在 [`deploy/kubernetes/casehub.yaml`](deploy/kubernetes/casehub.yaml) 中填写镜像地址和 `DATABASE_URL`。
2. 执行：

   ```sh
   kubectl apply -f deploy/kubernetes/casehub.yaml
   kubectl rollout status deployment/casehub
   ```

3. auto-test 节点用 auto-test 仓库的 `kubectl apply -k deploy/kubernetes` 部署。

清单保持 `replicas: 1` 和 `Recreate` 更新策略，不要改动这两项。

**自动扩缩**：如果 auto-test 启用了 KEDA 扩缩组件，KEDA 会读取 CaseHub Service 上的 `GET /api/queue`，CaseHub 这边不需要任何额外配置。

## 5. 队列参数

| 变量 | 默认值 | 含义 |
| --- | --- | --- |
| `CASEHUB_TASK_QUEUE_TIMEOUT` | `30m` | 任务排队超过这个时间仍没有节点接收，就以 `NO_NODE_AVAILABLE` 失败。节点按需从零扩容时，应留出拉起 Pod 的时间 |
| `CASEHUB_NODE_LOST_AFTER` | `2m` | 节点连续联系不上超过这个时间，视为其上的任务已丢失 |
| `CASEHUB_TASK_RETENTION` | `168h` | 已结束任务及其会话包、诊断包的保留时长 |

## 6. 重启与升级

**重启 CaseHub 的影响：**

- **已经交给节点的任务不受影响**：CaseHub 启动后会重新跟随这些任务。
- **还在排队的任务重启后继续排队**：排队期间请求内容（含测试账号密码）会暂存在任务存储里，交给节点后立即清除；重启后这些任务自动接着派发。只有暂存失败（存储写入出错）或请求已丢失的旧任务，才会以 `CASEHUB_RESTARTED` 失败，需要重新提交。
- **重启前先确认队列为空**：`curl http://<CaseHub>/api/queue`，确认每类任务的 `queued` 都为 0。

**从旧版本升级**（旧版本把请求直接转发给单个节点，前端在浏览器本地记录任务 ID）：

1. 在没有任务运行时升级。前端会清除本地保存的旧任务 ID，升级时正在运行的任务在页面上将无法再找到。
2. 先升级 CaseHub。使用 PostgreSQL 时，新表在启动时自动创建，无需手动迁移。
3. 再升级 auto-test 节点，并把节点地址改成第 3 节中的写法。新版 CaseHub 也能使用旧版节点，只是不能查询空闲槽位，旧节点上中断的 planner 任务也只能回到原节点继续。

## 7. 检查与排查

```sh
curl http://<CaseHub>/api/queue                # 每类任务：queued / running / demand / nodes
curl http://<CaseHub>/metrics                  # 同样的数据，Prometheus 文本格式
curl 'http://<CaseHub>/api/tasks?active=1'     # 未处理完的任务
```

| 现象 | 处理 |
| --- | --- |
| 某类任务的 `nodes` 为 0 | CaseHub 找不到这类节点。检查对应的 `CASEHUB_*_URL`；使用 `dns+` 时，检查 headless Service 是否存在、是否有就绪的 Pod |
| 任务长时间显示"所有执行节点都在忙" | 节点数不够。增加节点或启用自动扩缩 |
| 任务显示"暂时联系不上执行节点" | CaseHub 到节点的网络、端口或 DNS 不通 |
| 任务以 `NODE_LOST`、`NO_NODE_AVAILABLE` 或 `CASEHUB_RESTARTED` 结束 | 见 auto-test `DEPLOYMENT.md` 的"常见问题" |
| 失败的生成或修复任务要查看现场 | 下载 `GET /api/<generator\|healer>/jobs/<id>/diagnostics`，得到 tar.gz 诊断包 |
