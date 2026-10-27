# 本地排行维护记录

记录日期：2026-10-21。

## 用户确认的保留范围

本地分支需要保留旧版的**本地 API Key 排行**。用户明确表示“社区我不要，我只要本地的排行”，并要求“最好跟上一版一样”。

这是需要长期保留的本地定制，不应在合并官方版本或解决冲突时被当作废弃代码删除。恢复采用上游删除前的本地实现，保留原界面组件和评分算法，不采用仅按 Token 排序的简化榜单替代。

### 界面与操作

- 顶部导航保留“排行”，访问路径为 `/ranking`；部署在子路径时沿用 `appPath` 的路径前缀处理。
- 桌面端保留旧版冠军居中、二三名分列两侧的布局，以及下方完整榜单；保留手机与深色主题适配。
- 指标为：综合排名、总 Token、请求数、缓存读取率、平均首字延迟、平均总延迟、峰值 TPM、峰值 RPM。
- 周期为：今天、昨天、本月、上月。周期划分沿用旧版本地排行的 `Asia/Shanghai` 自然日，与“用量”页可配置的工作时段统计日相互独立。
- 保留综合分算法说明、周期和指标的浏览器偏好、顶部刷新、点击头像编辑本地 Key 别名及头像。
- 榜单按旧版最多展示前 100 名，使用脱敏展示名，不向浏览器返回完整 API Key。
- 当前恢复范围是管理员本地排行；API Key 只读用户未新增排行入口。

### 本地数据与更新

- 保留独立本地聚合服务。启动时立即聚合一次，之后每 5 分钟聚合；前端每 60 秒读取榜单，隐藏标签页暂停轮询。
- 保留日、月快照和完整日结算方式。当前实现首次启用从今天开始，不回填启用前的排行周期。
- 头像保存在 `cpa_api_keys.local_ranking_avatar_id`，排行快照保存在 `local_ranking_period_stats`。
- 在排行弹窗修改或清空别名时，写入 `key_alias_source = manual`，与设置页的别名来源规则一致。
- 不恢复社区状态、参与/退出、排名中心连接、身份密钥、上传接口或上报任务。

## 线上空榜问题与修复（2026-10-21）

用户部署后反馈：概览当天有 8,533 次请求、1.31B Token，但“综合／今日”显示空榜。未直接读取用户的线上数据库；本地已通过真实事件入库复现相同现象：同一批请求在总 Token 榜存在，在综合榜全部被过滤。

用户继续反馈等待超过五分钟仍未聚合后，核对生产启动链路发现更直接的启动漏接：`buildReadyApp` 虽然创建了排行服务和后台任务，但 `startup_pipeline.go` 的 `installReadyApp` 没有将 `ready.LocalRanking` 移交给实际运行的 App，导致后台启动分支始终跳过它。接口仍持有服务对象，可以正常返回 HTTP 200 空榜；这不能证明后台任务已运行，也不能据此归因为缺少 TTFT。这是已在真实 `Initialize` 路径中复现的启动缺陷。

另有独立的数据兼容缺陷：旧版综合榜要求每个 Key 同时有输入 Token、TTFT、总耗时、峰值等完整指标。CPA 未提供 TTFT 或旧事件字段缺失时，有用量的 Key 会被整体排除。原 SQL 还将总耗时样本统计绑定在 TTFT 有值的条件上。首次后台聚合延迟 5 分钟则会额外造成部署初期空榜。

后续合并必须保留以下修复：

- 在 `installReadyApp` 明确移交 `LocalRanking`，使生产 `RunContext` 真正启动后台聚合；不可仅检查构造函数返回值或直接运行服务测试。
- 有请求、结果计数和 Token 的 Key 可以参与综合榜，不因缺少可选指标而整体消失。
- 保留原评分权重及完整样本的评分结果；缺失缓存或耗时指标只使用中性贡献，不进入对应指标的归一化样本集合，不能按零延迟加分。
- 表格缺失指标省略对应数值并显示 `—`，不能伪造 `0ms`。输入 Token 有值且缓存读取为零时，零缓存率仍是有效值。
- 成功请求的总耗时按自身有效性独立统计，不依赖是否上报 TTFT；失败耗时不计入平均总耗时。
- 后台服务启动时立即聚合一次，再进入五分钟周期；不清库、不重置迁移、不要求重新采集已有当天事件。
- 对单项指标无有效样本的情况说明原因，并提示可查看总 Token 或请求数；不能总是提示“下一轮聚合后会自动显示”。

回归依据：`internal/app/test/local_ranking_startup_test.go`、`internal/ranking/test/local_missing_samples_test.go`、`internal/ranking/local_overall_test.go`、`internal/ranking/test/local_runner_test.go` 和 `web/src/features/ranking/test/LocalRankingPage.test.tsx`。真实启动测试模拟已有事件、恢复迁移尚未执行的 v2 数据库，经过 `RunContext` 自动迁移、服务交接与后台启动后，要求 HTTP 综合榜在数秒内返回已有数据。线上缺失的具体字段仍以实际接口／数据库为准；不要把截图直接当作已核验的线上 SQL 诊断。

`20261021_restore_local_ranking` 由正常启动流程自动执行，修复后台交接和缺失指标处理不需要新增数据库迁移。不能通过删迁移记录、手工插入排行快照、清库或单纯重启旧镜像来修复这个启动漏接。

## 2026-10-26 上游合并兼容

- 保留本地已发布的迁移顺序，在 `20261021_restore_local_ranking` 之后追加上游 OIDC 会话身份和 `effective_service_tier` 字段迁移；不得遗漏任意一侧的注册，也不重排费用升级所固定的迁移分界。
- 上游将凭据导航从 `auth-files` 改为 `credentials`，相关路由、API 与测试采用新名称，同时继续保留 `/ranking`、`/api-key-usage` 和管理员权限边界。
- 上游页签保存键升级为 `cli-proxy-usage-tab-v2`。读取时必须兼容旧 `cli-proxy-usage-tab-v1`，避免升级后丢失已保存的 `ranking` 或 `api-key-usage` 页签；已有 v2 偏好和有效直达路径仍优先。排行自身的周期／指标偏好保存键保持原样。

本次合并使用 Go 1.26.10 完成全部后端测试（发布脚本测试使用 Python 3.12），并使用 Node.js 24.22.1 通过前端 202 个测试文件、1605 项测试、lint、类型检查及生产构建。页签升级回归测试覆盖实际页面挂载、v2 优先、旧值回退、直达路径和重新挂载；本次未重新执行浏览器视觉验收。

## 为什么容易在合并时丢失

官方 v2 主动移除了本地排行和社区排行，涉及页面、接口、后台任务、数据库及浏览器偏好，不能仅靠补回导航恢复。

可在本地 Git 历史中核对以下记录：

| 时间／提交 | 变更 |
| --- | --- |
| 2026-10-07，`29d98cf8`，PR #608 | 上游合并移除本地及社区排行的变更。 |
| `3e79ce52` | 删除前端排行页面、资源、翻译与导航；其父提交保留旧版前端实现。 |
| `1fa6a8f6` | 删除排行后端及相关持久化；其父提交保留旧版本地后端实现。 |
| 2026-10-19，`fc8cbde9`，PR #650 | v2 合入官方 `main`，包含排行移除。 |
| 2026-10-21，`8f290c14` | 本地 `codex/custom-features` 合并官方 `main`，继承移除逻辑。 |
| 2026-10-21，本地恢复工作 | 按用户要求恢复旧版本地排行，并新增独立恢复迁移。 |

不要将“官方已经删除”理解为本地也应继续删除。后续上游更改可能没有 Git 冲突，却仍让服务装配、导航或数据结构断开。

## 关键接入点

| 范围 | 文件／目录 | 合并时必须核对 |
| --- | --- | --- |
| 导航及路径 | `web/src/lib/usageNavigation.ts` | `ranking` 页签及 `/ranking` 映射存在。 |
| 页面挂载与刷新 | `web/src/pages/UsagePage.tsx` | 翻译标签、`LocalRankingPage` 挂载、顶部刷新分支、资料更新后刷新 Key 名称。排行使用自己的周期控件，不套用普通用量筛选。 |
| 浏览器启动 | `web/src/main.tsx` | 不清理排行偏好，不重置排行页签。 |
| 排行页面 | `web/src/features/ranking/LocalRankingPage.tsx` | 旧版指标、周期、评分说明、状态显示和资料编辑仍可用。 |
| 旧版布局与组件 | `web/src/features/ranking/RankingPage.module.scss`、`components/`、`format.ts` | 保留原前三名与榜单样式、指标单位和头像选择。 |
| 请求与前端状态 | `web/src/features/ranking/api.ts`、`hooks/useLocalRankingData.ts`、`preferences.ts` | 仅访问本地接口；切换、刷新、缓存、资料编辑、认证失效和偏好保存正常。 |
| 语言 | `web/src/i18n/index.ts` | 导航和排行使用的英文、简体中文、繁体中文文案存在。 |
| 后端启动装配 | `internal/app/app.go` | 构造 `LocalRankingService` 和 `LocalRankingRunner`，将服务传入 Router，并启动／停止后台任务。 |
| v2 启动交接 | `internal/app/startup_pipeline.go` | `installReadyApp` 将 `ready.LocalRanking` 移交给运行中的 App；缺少这一项会让接口正常而后台永不聚合。 |
| 受保护路由 | `internal/api/router.go`、`internal/ranking/httpapi/` | 在管理员保护下注册本地榜单读取及资料修改接口，支持部署子路径。 |
| 本地算法与快照 | `internal/ranking/local_*.go`、`types.go` | 保留原评分、排序、样本筛选、峰值口径和完整日结算逻辑。 |
| 数据模型 | `internal/entities/local_ranking_period_stat.go`、`cpa_api_key.go`、`all.go` | 快照实体、可选头像字段和核心实体注册不能丢失。 |
| 展示资料持久化 | `internal/repository/cpa_api_key.go` | 别名、头像在同一事务写入并回读，手动别名来源正确。 |
| 恢复迁移 | `internal/repository/migration/20261021_restore_local_ranking.go`、`migration.go` | 恢复迁移注册在上游移除迁移和现有自定义迁移之后；重启不清空新快照。 |

### 本地 API

```text
GET   /api/v1/ranking/local/leaderboards?period=today&metric=overall
PATCH /api/v1/ranking/local/profiles/:id
```

服务端复用现有管理员认证、请求意图检查和部署前缀。不要为了恢复页面而放开未认证访问，也不要把本地接口改成请求社区排名中心。

## 数据迁移注意事项

1. 保留已发布的 `20261009_remove_ranking`，不修改其内容或删除已执行记录。
2. 保留新增的 `20261021_restore_local_ranking`。它在历史移除迁移之后创建快照表，并在缺失时补回头像字段。
3. 后续表结构调整追加新迁移，确保已升级到 v2 的数据库也能继续升级。
4. 合并不能再次删除恢复迁移注册，或在其后加入清理本地排行的逻辑。
5. 已被 v2 删除的历史排行快照、头像覆盖值及状态不能凭代码恢复；若用户要求找回，需单独核对升级前备份。不要承诺上线即恢复旧榜单，也不要以回退整个数据库替代功能合并。

## 合并完成后的验证

使用项目要求的 Go 和 Node.js 版本运行：

```sh
go test ./internal/ranking/... ./internal/api/test ./internal/app/test ./internal/entities/test ./internal/repository/migration/test ./internal/repository/test
npm --prefix web run test -- src/features/ranking src/lib/test/usageNavigation.test.ts src/pages/test/UsagePage.logic.test.ts src/test/AppUsageNavigation.test.ts
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run build
git diff --check
```

如果合并还涉及数据库、事件入库、服务启动或共享组件，应再运行 README 中的完整前后端测试。

重点回归测试包括：

- `internal/ranking/test/`：原聚合、完整日结算、综合评分、排序、头像／别名编辑和后台任务周期。
- `internal/app/test/local_ranking_startup_test.go`：生产启动交接、自动恢复迁移、已有当天事件自动聚合，并通过真实 HTTP 接口读到榜单。
- `internal/api/test/local_ranking_routes_test.go`：部署前缀、认证边界和社区接口仍未注册。
- `internal/repository/migration/test/restore_local_ranking_test.go`：在已经移除排行的 v2 数据库上恢复结构，不改已有 Key 别名，重启不丢快照。
- `web/src/features/ranking/` 中的测试：旧版前三名及完整表格、周期控件、资料编辑回调、偏好、刷新和请求缓存。
- 导航及角色路径测试：管理员能进入 `/ranking`；普通 API Key 只读用户不会被导向管理员排行页。

浏览器验收应覆盖：从导航进入、直接打开 `/ranking`、周期和八项指标切换、刷新后保留偏好、顶部刷新、头像和别名编辑、请求失败反馈、375px 手机布局及深色主题。确认没有访问社区参与／上报接口。

2026-10-21 恢复时已完成前端 1503 项测试、各组后端测试、lint、类型检查、生产构建，以及使用隔离示例数据的桌面／手机浏览器验收。本记录描述代码恢复情况，不代表已经部署；提交和部署状态以 Git 与实际运行版本为准。

## 2026-10-26 集中推送模式兼容

- `INGEST_MODE=push` 的 `buildPushReadyApp` 继续构造本地排行服务和 runner，`installReadyApp` 继续移交 `LocalRanking`。
- 相同完整 API Key 跨来源合并，增量登记保留手工别名及头像，不执行单来源全量删除同步。
- 来源字段和去重凭据通过新增 `20261026_push_ingest` 迁移追加，旧排行迁移和顺序保持不变。
- 推送模式隐藏远端凭证操作，保留 `/ranking`、既有榜单指标及聚合；新增 `internal/app/test/push_startup_test.go` 验证启动交接。
