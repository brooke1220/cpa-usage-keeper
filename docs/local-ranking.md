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

- 保留独立本地聚合服务。后台每 5 分钟聚合一次，首次启动等待一个周期；前端每 60 秒读取榜单，隐藏标签页暂停轮询。
- 保留日、月快照和完整日结算方式。当前实现首次启用从今天开始，不回填启用前的排行周期。
- 头像保存在 `cpa_api_keys.local_ranking_avatar_id`，排行快照保存在 `local_ranking_period_stats`。
- 在排行弹窗修改或清空别名时，写入 `key_alias_source = manual`，与设置页的别名来源规则一致。
- 不恢复社区状态、参与/退出、排名中心连接、身份密钥、上传接口或上报任务。

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
- `internal/api/test/local_ranking_routes_test.go`：部署前缀、认证边界和社区接口仍未注册。
- `internal/repository/migration/test/restore_local_ranking_test.go`：在已经移除排行的 v2 数据库上恢复结构，不改已有 Key 别名，重启不丢快照。
- `web/src/features/ranking/` 中的测试：旧版前三名及完整表格、周期控件、资料编辑回调、偏好、刷新和请求缓存。
- 导航及角色路径测试：管理员能进入 `/ranking`；普通 API Key 只读用户不会被导向管理员排行页。

浏览器验收应覆盖：从导航进入、直接打开 `/ranking`、周期和八项指标切换、刷新后保留偏好、顶部刷新、头像和别名编辑、请求失败反馈、375px 手机布局及深色主题。确认没有访问社区参与／上报接口。

2026-10-21 恢复时已完成前端 1503 项测试、各组后端测试、lint、类型检查、生产构建，以及使用隔离示例数据的桌面／手机浏览器验收。本记录描述代码恢复情况，不代表已经部署；提交和部署状态以 Git 与实际运行版本为准。
