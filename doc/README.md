# FinalWeave 文档中心

> **FinalWeave — Parallel by design, final by proof.**  
> 生而并行，以证为终。

FinalWeave 是一个从零设计的许可型、多账本、高性能区块链系统。每个验证者并行生产交易批次和轻量 DAG 顶点；FinalDAG-C v1 直接从 DAG 的因果关系导出一致顺序，不再叠加第二套提议/投票链；排序后由确定性投机并行执行器计算状态，后续 DAG 顶点为执行结果背书，形成对外可验证的最终性证明。

本文档描述目标系统，不表示相应代码已经实现。任何协议结论都必须由规范、测试向量、模型检查和实现测试共同支持。

## 1. 一句话架构

```text
并行接收交易
  -> Batch + 纠删码分片
  -> q 个持久化 ACK 形成 BatchAC
  -> 每轮每作者一个签名元数据顶点
  -> 强父边同时表达因果关系和隐式支持
  -> FinalDAG-C 对 proposer slot 做 commit / skip / undecided
  -> 全局 slot 稳定前缀导出唯一交易顺序
  -> 与该顺序串行语义等价的确定性投机并行执行
  -> 后续顶点携带 ExecutionAttestation
  -> q 个相同执行摘要形成 FinalityCertificate
  -> FinalityProof + Merkle/SMT proof 支撑外部最终查询
```

`n = 3f + 1`，`q = 2f + 1`，Batch 恢复门槛 `k = f + 1`，且固定 GF(2^8) 编码令 v1 要求 `4 <= n <= 253`。v1 使用部分同步网络模型，epoch 内验证者集合、共识算法和所有安全关键参数固定。

## 2. 四个协作平面

| 平面 | 主要对象 | 责任 |
|---|---|---|
| 数据可用性 | Transaction、Batch、Fragment、DA ACK、BatchAC | 让大数据可并行传播、校验、持久化与恢复 |
| DAG 排序 | DAGVertex、strong/weak parent、proposer slot、decision | 以小元数据消息形成稳定因果前缀和唯一总序 |
| 执行最终性 | OrderedPrefix、Receipt、ExecutionAttestation、FinalityCertificate | 并行计算确定结果，并将执行摘要绑定到已提交顺序 |
| 状态与证明 | State Root、Receipt Root、FinalityProof、Merkle/SMT proof | 支持状态同步、轻验证、审计和跨账本消息 |

BatchAC 与 DAG 共识是独立层：BatchAC 证明批次可恢复；DAG 顶点只引用 BatchAC，顶点本身不使用 `VertexAck` 或 `VertexCertificate`。DAG 边是共识输入，但不是 Batch 数据可用性证书。

## 3. 项目级复杂度原则

FinalWeave 的优化优先级是：**正确功能与安全边界 > 可持续性能 > 可恢复运维 > 实现简洁度**。

复杂度可以接受，但每一项复杂机制必须同时满足：

1. 有明确、可复现的功能、性能、安全或运维收益；
2. 写出机器可检查或测试可观测的不变量；
3. 定义过载、超时、部分失败和崩溃后的恢复路径；
4. 能灰度观测、回滚实现版本，且不在同一 epoch 动态更换共识语义；
5. 不能用“理论峰值”掩盖尾延迟、数据恢复、重复执行或状态提交成本。

复杂度本身不是目标。没有测量口径、失效边界和恢复方案的优化不得进入生产基线。

## 4. 文档地图

### 4.1 总体设计

| 文档 | 内容 |
|---|---|
| [系统架构](01-system-architecture.md) | 系统边界、组件、主流程、最终性分层与信任边界 |
| [需求与不变量](02-requirements-and-invariants.md) | 故障模型、安全/活性/执行/恢复不变量和验收门槛 |
| [节点角色与部署](03-node-roles-and-deployment.md) | Validator、Full、Archive、Gateway、Observer 与拓扑 |
| [实施路线](04-implementation-roadmap.md) | 从 schema、BatchAC、DAG 到并行执行和生产化的依赖顺序 |
| [配置规范](05-configuration-reference.md) | 本地、Genesis、链上配置及静态/动态边界 |
| [成熟方案比较与取舍](06-comparison-and-tradeoffs.md) | 与线性 BFT、成熟联盟链、直接 DAG 和并行执行路线比较 |
| [技术参考](references.md) | 论文、RFC、证据口径与建议阅读顺序 |

### 4.2 核心协议

入口：[核心协议规范](protocol/README.md)

| 文档 | 内容 |
|---|---|
| [数据模型与密码学](protocol/01-data-model-and-cryptography.md) | schema、规范编码、域隔离哈希、签名和证明 |
| [数据可用性与 BlockDAG](protocol/02-data-availability-and-blockdag.md) | BatchAC、纠删码、轻量 DAG 顶点、父边和证据 |
| [FinalDAG-C 共识](protocol/03-finaldag-consensus.md) | slot 决策、direct/indirect rule、稳定前缀和 restricted round-jump |
| [最终性、执行与纪元](protocol/04-finality-execution-and-epochs.md) | 确定性并行执行、ExecutionAttestation、FinalityCertificate、epoch |
| [执行注册表、Gas 与资源计量](protocol/05-execution-registry-gas-and-resource-metering.md) | payload/operation 注册表、gas 公式、fee 与资源边界 |
| [跨账本异步消息](protocol/06-cross-ledger-async-messaging.md) | source event/finality proof、目标 trust policy、并发唯一消费与 relayer |

### 4.3 工程与生产化

入口：[工程规范](engineering/README.md)

| 文档 | 内容 |
|---|---|
| [执行与状态](engineering/01-execution-and-state.md) | 顺序等价的确定性投机并行执行、收据和状态根 |
| [存储、快照与裁剪](engineering/02-storage-snapshot-and-pruning.md) | 原子提交、WAL、快照、恢复和保留策略 |
| [网络、同步、查询与 API](engineering/03-network-sync-query-and-api.md) | P2P、同步、证明查询和外部接口 |
| [安全、治理与运维](engineering/04-security-governance-and-operations.md) | 密钥、升级、监控、故障和应急 |
| [测试、发布与性能](engineering/05-testing-release-and-performance.md) | 属性、Fuzz、形式化、Byzantine、Chaos 和性能门禁 |

### 4.4 连续新开发者教程

入口：[FinalWeave 新开发者教程](tutorial/README.md)

教程以同一笔交易贯穿密码学、数据可用性、DAG 排序、执行和证明，不把知识拆成孤立术语。建议依次阅读 `tutorial/00` 至 `tutorial/05`，再进入对应协议或工程文档。

### 4.5 架构决策

| ADR | 决策 |
|---|---|
| [ADR-001](decisions/ADR-001-permissioned-multi-ledger.md) | 许可型多账本、每账本独立验证者集合 |
| [ADR-002](decisions/ADR-002-finaldag-c-direct-dag.md) | BatchAC + FinalDAG-C 直接 DAG 共识 |
| [ADR-003](decisions/ADR-003-deterministic-speculative-parallel-execution.md) | 与规范串行顺序等价的确定性投机并行执行 |
| [ADR-004](decisions/ADR-004-hash-and-encoding.md) | SHA-256、确定性 CBOR、32 字节域隔离哈希 |
| [ADR-005](decisions/ADR-005-proof-carrying-query.md) | 查询响应携带可独立验证的 FinalityProof |

## 5. 推荐阅读路线

第一次接触区块链的开发者：

```text
tutorial/00 -> tutorial/01 -> tutorial/02
 -> 01-system-architecture -> tutorial/03 -> tutorial/04
 -> tutorial/05 -> 与任务相关的 protocol/ 或 engineering/ 文档
```

协议开发者：

```text
02-requirements-and-invariants
 -> protocol/01 -> protocol/02 -> protocol/03 -> protocol/04
 -> engineering/02 -> engineering/05 -> decisions/
```

SRE/安全工程师：

```text
02-requirements-and-invariants -> 03-node-roles-and-deployment
 -> engineering/02 -> engineering/03 -> engineering/04 -> engineering/05
```

## 6. 统一术语

| 术语 | 精确定义 |
|---|---|
| Ledger | 独立共识、状态、验证者集合和资源命名空间 |
| Round | FinalDAG-C 的逻辑轮次；不是最终区块高度 |
| Slot | `(epoch, proposer_round, proposer_rank)` 标识的确定性位置；epoch schedule 再把 rank 映射到 `proposer_index` |
| DAGVertex | 每位作者每轮最多一个签名小元数据对象，引用 BatchAC 和父顶点 |
| Strong parent | 指向上一轮不同作者顶点的边；至少 `q` 条，用作因果关系与隐式支持 |
| Weak parent | 为补齐未排序祖先而引用的更早顶点；不计入推进轮次的 `q` |
| BatchAC | `q` 个 DA ACK 聚合形成的独立批次可用性证书 |
| Occurrence sponsor | 承载某个 `AvailabilityReference` 的已签名 DAGVertex 作者；它控制该引用产生的 raw occurrence，因而承担 scan/common/source 验证预算。Batch 作者只认证数据来源，可以与 sponsor 不同 |
| commit / skip / undecided | slot 的三个本地决策状态；只有稳定前缀内决定才可输出 |
| DAGCommitWitness | 内部证明某 DAG 有序前缀已按规则提交的见证，不是外部执行最终证明 |
| ExecutionAttestation | 验证者在后续 DAG 顶点中对已执行前缀摘要的签名背书 |
| FinalityCertificate | 同一执行摘要的 `q` 个 ExecutionAttestation |
| FinalityProof | 外部可验证的 FinalizedBlockHeader、执行证书、validator-set chain 与所需 inclusion proof |
| Height | 从稳定 DAG 前缀导出的线性执行检查点高度 |
| Epoch | 验证者集合与安全关键协议参数固定的区间 |

不得混用：BatchAC 不证明交易有效；DAG commit 不等于外部 `FINALIZED`；Round 不等于 Height；本地 `undecided` 不是 `skip`；投机并行的调度顺序不改变规范串行语义。

## 7. v1 统一基线

| 项目 | 决策 |
|---|---|
| 网络与故障模型 | 许可型、部分同步、`n=3f+1`、最多 `f` Byzantine |
| 法定人数 | `q=2f+1`；v1 只允许 4/7/10/13… 且不超过 253 个验证者 |
| Batch 数据可用性 | 独立 BatchAC；ACK 前以 `k=f+1` 分片重构、重编码、验证并持久化 |
| DAG | 每个 Vertex 已由作者签名，但无独立 VertexAck/VertexCertificate 的元数据 DAG（uncertified） |
| 排序 | FinalDAG-C：Mysticeti 风格 slot direct/indirect commit/skip 与全局稳定前缀 |
| proposer slots | 每 round 默认 2、范围 `1..q`；仅 primary timer；只可在 epoch 边界调整 |
| 轮次推进 | 采用 IEEE S&P 2026 机械化证明所需 restricted round-jump |
| 执行 | exact-access 依赖图 + 有界 optimistic MVCC + 串行兼容 lane；结果严格等价于 canonical 串行 Apply |
| 入块前验证 | pre-decode scan、common static/auth suffix、cross-ledger source proof三段独立计量；按全部n个已签名containing Vertex author（occurrence sponsor）保留份额，STARTED checkpoint恢复不重扣；不得归因给Batch作者 |
| 计量与资源 | typed Feature/payload/Gas operation exact registry；v1 `fee_limit=0`；state/event/return/call/Body 双层硬 cap |
| 对外最终性 | `q` 个相同 ExecutionAttestation -> FinalityCertificate -> FinalityProof |
| 密码学 | SHA-256、Ed25519 多签列表、确定性 CBOR 严格子集 |
| 状态承诺 | Sparse Merkle Tree |
| P2P / API | 认证 QUIC + TLS 1.3，TCP + TLS 1.3 兼容；规范 PeerHello；Protobuf/gRPC；受控 HTTP/JSON 网关 |
| 存储 | 原子 WriteBatch + DAG/签名安全 WAL + 可验证快照 |
| 升级 | 治理交易在 epoch 边界激活；同 epoch 不动态切换共识 |

FinalDAG-C 是 FinalWeave 对已发表直接 DAG 思路的工程化基线名称，不声称复用论文结论即可自动证明整个组合系统。将独立 BatchAC、执行背书、epoch、裁剪和恢复组合后，仍需建立 FinalWeave 自身的形式化证明义务。

## 8. 文档优先级与变更规则

冲突时依次采用：已接受 ADR；`protocol/` 规范与测试向量；本文件的不变量链接；`engineering/` 契约；总体架构；教程和示例。

任何影响以下内容的变化都必须提交 ADR，并只能在 epoch 边界生效：slot 映射、父边要求、决策规则、排序、BatchAC 门槛、执行摘要、证书语义、规范编码、验证者集合或状态机版本。实现不得通过本地配置偷偷放宽链上规则。

## 9. 完成的含义

一个能力只有同时具备以下内容才算完成：

- 规范、schema 和跨实现测试向量；
- 安全/活性/恢复不变量及负向测试；
- 指标、资源上限、过载行为和运行手册；
- Byzantine、网络分区、崩溃恢复和快照同步验证；
- 端到端吞吐、p50/p95/p99 延迟、CPU、内存、磁盘和网络口径；
- 不把论文数据或其他系统数据写成 FinalWeave 实测结果。
