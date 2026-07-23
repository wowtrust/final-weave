# Contributing Guide

FinalWeave protects consensus safety, data availability, deterministic execution, finality-proof semantics, recovery boundaries, and bounded resource behavior. Contributions must be small, reviewable, and tied to a clearly scoped issue.

This guide is bilingual. The Chinese section is authoritative for day-to-day project work; the English section mirrors the same rules.

## 中文版

### 基本原则

- 先有 Issue，再有实现。Bug、功能、重构、文档、CI、发布和运维工作都要先说明背景、范围、验收标准和验证方式。
- 一个 PR 只解决一个主题。不要混入无关格式化、本地数据、生成物、实验、草稿或顺手重构。
- README 和用户文档必须区分“已经实现”“目标设计”和“路线图”，不得把尚未存在的节点、CLI、SDK、API、性能或兼容性写成现状。
- 改动共识、数据可用性、规范编码、密码学、执行、最终性、状态承诺、跨账本、epoch、存储或恢复边界时，必须覆盖失败路径、Byzantine 输入和边界条件。
- `main` 合并前必须通过 required checks。CI 失败时先查看日志并定位原因，不得用删除测试或降低门槛掩盖问题。

### 分支命名

人工分支只允许使用以下前缀，并采用小写 kebab-case：

| 类型 | 用途 | 示例 |
| --- | --- | --- |
| `feat/` | 新功能或用户可见能力 | `feat/batch-availability-certificate` |
| `fix/` | Bug 或回归修复 | `fix/duplicate-vertex-detection` |
| `docs/` | README、规范、ADR、教程、模板 | `docs/clarify-finality-boundary` |
| `test/` | 测试向量、模型、Fuzz、Chaos、CI 覆盖 | `test/finality-certificate-vectors` |
| `refactor/` | 不改变行为的结构调整 | `refactor/dag-store-indexes` |
| `perf/` | 有测量口径的性能优化 | `perf/batch-fragment-recovery` |
| `security/` | 安全加固、密钥、鉴权、输入边界 | `security/bound-peer-handshake` |
| `chore/` | 依赖、工具、发布、仓库维护 | `chore/update-doc-tooling` |
| `ci/` | CI 配置和流水线 | `ci/add-protocol-vector-gate` |
| `build/` | 构建系统、打包、镜像 | `build/node-release-archives` |
| `release/` | 版本发布和发布说明 | `release/v0.1.0` |
| `revert/` | 回滚已合并变更 | `revert/finality-rule-change` |

`dependabot/` 是 GitHub Dependabot App 专用命名空间，不用于人工分支。

### Issue 标准

Issue 标题使用：

```text
[Bug] <简短问题>
[Feature] <用户可见能力>
[Task] <工程维护事项>
```

Issue 必须包含：

- 背景和可观察问题。
- 影响平面或模块。
- 目标与非目标。
- 安全、活性、数据可用性、最终性、兼容性、迁移、恢复、性能或运维风险。
- 可检查的验收标准。
- 验证计划。

安全漏洞不得公开提交，按 [SECURITY.md](SECURITY.md) 使用私密报告渠道。公开 Issue 中不得包含真实密钥、token、生产配置、个人数据或可直接利用的攻击细节。

### PR 标准

PR 标题和提交使用 Conventional Commits：

```text
feat(consensus): add ...
fix(storage): reject ...
docs(protocol): clarify ...
test(finality): cover ...
refactor(execution): simplify ...
perf(availability): reduce ...
security(network): bound ...
chore(github): maintain ...
```

PR 描述必须：

- 使用 `Fixes #123`、`Closes #123` 或 `Refs #123` 关联 Issue。
- 总结改动和用户、开发者或运营影响。
- 说明安全/活性、共识/排序、数据可用性、执行/状态、最终性/证明、存储/恢复、网络/API、跨账本、epoch/兼容性影响。
- 列出已运行验证，以及未运行验证的原因和残余风险。
- 给出风险与回滚方式。

没有关联 Issue 的紧急修复必须解释原因，并在合并后补齐记录。

### 提交格式

```text
<type>(<scope>): <imperative summary>
```

允许的 `type`：`feat`、`fix`、`docs`、`test`、`refactor`、`perf`、`security`、`chore`、`ci`、`build`、`revert`。

示例：

```text
fix(consensus): reject equivocated proposer slots
docs(finality): define external proof boundary
security(network): cap unauthenticated frames
perf(execution): reuse validated access summaries
```

可选本地设置：

```bash
git config commit.template .github/commit_message_template.txt
```

### 架构不变量

- `n = 3f + 1`、`q = 2f + 1`、Batch 恢复门槛与 epoch 冻结规则不得由本地配置偷偷放宽。
- `BatchAC` 只证明数据达到可恢复门槛，不证明交易有效、DAG 已排序或执行已最终。
- `commit / skip / undecided`、全局稳定前缀和 canonical 顺序必须对所有诚实节点确定一致。
- 投机并行执行必须严格等价于 canonical 串行 `Apply`；未知访问、冲突或超限只能安全回退、背压或失败，不能改变结果。
- `DAGCommitWitness` 不是外部执行最终证明；对外 `FINALIZED` 必须由精确匹配的 `FinalityCertificate` 和所需 inclusion proof 支撑。
- 规范编码、域隔离哈希、签名消息、对象 ID、validator-set chain 和状态承诺的任何变化都必须显式版本化。
- Safety WAL、原子状态提交、快照、裁剪、同步和恢复必须保留安全状态与幂等边界。
- Byzantine 输入的 CPU、内存、磁盘、网络和验签工作必须受预算约束；生产路径不能引入无界扫描、排序、加载或重算。
- 影响 wire schema、排序、最终性、执行语义、validator set 或安全关键参数的变化必须提交 ADR，并仅在 epoch 边界激活。

### 验证门

当前仓库处于规范阶段，所有改动至少运行：

```bash
git diff --check
python3 scripts/check_docs.py
```

文档或 ADR 变更还必须检查：

- 所有相对链接和文档入口有效。
- 术语与已接受 ADR、`doc/protocol/` 规范和统一不变量一致。
- 目标设计没有被误写为已实现或已实测能力。
- 引用性能数据时标明来源、硬件、配置、口径和是否为 FinalWeave 实测。

实现代码加入后，相关 PR 必须同时增加并运行单元、race、property、Fuzz、跨实现向量、模型、Byzantine、网络分区、崩溃恢复、快照、Chaos 和性能门禁中的适用部分。无法运行的检查必须在 PR 中说明原因和残余风险。

### 文档边界

- `README.md` 是访客入口，只描述项目真实状态，并链接到详细规范。
- `doc/` 保存系统架构、协议、工程、教程、参考和 ADR。
- 已接受 ADR 与 `doc/protocol/` 规范优先于教程和示例。
- 本地草稿、密钥、数据库、日志、抓包、构建产物和基准原始数据不得随手提交。
- 规范变更必须同步更新受影响的教程、配置、测试向量和兼容性说明。

## English Version

### Core rules

- Start with an issue for bugs, features, refactors, docs, CI, releases, and operations work.
- Keep each PR focused on one topic and exclude unrelated formatting, generated output, experiments, local data, and opportunistic refactors.
- User-facing material must distinguish implemented behavior, target design, and roadmap work. Do not present planned nodes, CLIs, SDKs, APIs, benchmarks, or compatibility as delivered.
- Changes to consensus, data availability, canonical encoding, cryptography, execution, finality, state commitments, cross-ledger messaging, epochs, storage, or recovery require boundary and failure-path validation.
- `main` must pass all required checks before merge.

### Branch names

Human-authored branches use lowercase kebab-case and one of:

| Prefix | Purpose |
| --- | --- |
| `feat/` | User-visible capability |
| `fix/` | Bug or regression fix |
| `docs/` | README, specification, ADR, tutorial, or template |
| `test/` | Test vectors, models, fuzzing, chaos, or CI coverage |
| `refactor/` | Behavior-preserving structure change |
| `perf/` | Measured performance work |
| `security/` | Security, keys, authentication, or input boundaries |
| `chore/` | Dependencies, tools, releases, or repository maintenance |
| `ci/` | CI configuration and pipelines |
| `build/` | Build, packaging, and images |
| `release/` | Releases and release notes |
| `revert/` | Revert of a merged change |

The `dependabot/` namespace is reserved for the GitHub Dependabot App.

### Issues, PRs, and commits

- Issue titles use `[Bug]`, `[Feature]`, or `[Task]` and document context, scope, non-goals, risks, acceptance criteria, and validation.
- PR titles and commits use `<type>(<scope>): <imperative summary>`.
- PR bodies link the issue, summarize impact, cover all relevant safety and compatibility boundaries, list validation, and describe risk and rollback.
- Security vulnerabilities use the private process in [SECURITY.md](SECURITY.md), not public issues.

### Safety review

Review every relevant change against quorum and epoch rules, BatchAC meaning, deterministic ordering, serial-equivalent execution, exact finality-proof binding, canonical encoding, durable recovery, bounded Byzantine work, compatibility, and rollback.

No optimization may trade away safety, determinism, verifiability, or recoverability. If the implementation cannot safely continue within a resource limit, it must apply bounded fallback, backpressure, or explicit failure without changing protocol meaning.

### Validation

The current specification repository requires at least:

```bash
git diff --check
python3 scripts/check_docs.py
```

As implementation code lands, each PR must add and run the applicable unit, race, property, fuzz, cross-implementation vector, model, Byzantine, partition, crash-recovery, snapshot, chaos, and performance gates. Document every skipped relevant check and its residual risk.
