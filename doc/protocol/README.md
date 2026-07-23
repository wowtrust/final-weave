# FinalWeave 核心协议规范

本目录冻结 FinalWeave v1 的互操作、安全、最终性和共识哈希规则。v1 的共识协议命名为 **FinalDAG-C v1**。

FinalDAG-C 是许可型、等权、部分同步的 DAG 原生 BFT 协议。它不在 DAG 之上叠加链式共识：独立 BatchAC 证明大批次可恢复；每个元数据Vertex都由作者签名，但不再形成独立VertexAck/VertexCertificate（uncertified）；强边表达隐式投票，三轮重叠决策规则产生唯一 slot 前缀，确定性执行结果再由 Validator 的 Consensus Key 形成 FinalityCertificate。

## 阅读顺序

1. [数据模型与密码学](01-data-model-and-cryptography.md)
2. [BatchAC 与已签名、无独立顶点证书的元数据 DAG](02-data-availability-and-blockdag.md)
3. [FinalDAG-C 共识、排序与活性](03-finaldag-consensus.md)
4. [最终性、执行证明与 Epoch](04-finality-execution-and-epochs.md)
5. [执行注册表、Gas 与资源计量](05-execution-registry-gas-and-resource-metering.md)
6. [跨账本异步消息](06-cross-ledger-async-messaging.md)

六篇共同形成一个闭环：

```text
规范对象、编码、哈希与密钥
  -> Batch 纠删码、重构校验与 BatchAC
  -> 仅引用可用 Batch 的 uncertified metadata DAG
  -> support / certificate / skip pattern
  -> direct 与 indirect slot 决策
  -> 确定性因果线性化与连续高度
  -> 规范串行语义及受约束的并行优化
  -> 冻结 payload/operation 注册表、gas/fee 与资源计量
  -> FinalityCertificate 与可验证终态
  -> 以源最终性、目标 trust policy 与永久 consumed key 安全跨账本传递
```

## v1 冻结参数

| 项目 | FinalDAG-C v1 |
|---|---|
| Validator 数 | 严格 `n = 3f + 1`，`f >= 1`，且 `n <= 253` |
| Quorum | `q = 2f + 1` |
| Fragment 重建门槛 | `k = f + 1` |
| 哈希 | SHA-256，完整 32 字节，严格域隔离 |
| 共识编码 | RFC 8949 deterministic CBOR 严格子集 |
| Validator 密钥 | DAG Key 与 Consensus Key 分离 |
| 数据可用性 | 独立 BatchAC；ACK 前重构、重编码和持久化 |
| DAG | 无 VertexAck、无 VertexCertificate 的元数据 DAG |
| 每轮 proposer slot | 默认 `2`；范围 `1..q`；`q` 仅实验 profile |
| 排序 | FinalDAG-C 三轮重叠 direct/indirect 决策 |
| 最终性签名 | 恰好 `q` 个 Ed25519 签名与 bitmap |
| 执行 | 规范串行语义；并行结果必须可证等价并可串行回退 |
| 预过滤工作 | raw item先向containing Vertex occurrence sponsor收scan费，cheap prefix后再向同一sponsor收昂贵suffix；全部n个sponsor独占reserve + shared pool，Batch author只绑定来源 |
| Gas / fee / 资源 | operation exact registry；v1 无原生 fee；component/per-tx/per-block/Body 硬 cap |
| 跨账本消息 | source FinalityProof + 双 Event path；目标 Feature policy；source-event exact-once consume |
| 高度 | 创世状态为 0；首个 FinalizedBlock 为 1 |

## 关键不变量

- BatchAC 和 FinalityCertificate 的语义 ID 不含 signer subset。
- VertexID 不含附带证书 envelope，也不存在 VertexCertificate。
- DAG 强边只表达元数据因果关系和共识 support，不替代 BatchAC。
- 精确引用的Vertex分支必须完整提升；未引用sibling/evidence hint只进入有绝对上限且不参与共识的旁路cache。
- `max_strong_parents >= min(n,q+1)`，以同时容纳及时 primary 与兼容 certificate supporters。
- 任何节点不得越过全局最早 `UNDECIDED` proposer slot 输出后续结果。
- round jump 必须执行受限补轮规则，不能跳过仍影响决策的中间 Vertex。
- 只有 `nonce == next_nonce` 的 occurrence 才进入 transaction tree 并产生 Receipt。
- FinalityProof 的公共格式不要求携带 DAGCommitWitness；DAG witness 只用于审计和同步。
- 基础 `FinalityProof` 只从 Genesis 验证；运营方 checkpoint 使用独立 `CheckpointTrustAnchor/CheckpointFinalityProof`，且必须匹配本地预置 anchor ID。
- 跨账本 proof 的 expected source root 只能来自目标认证 FeatureSet；source crypto前先过target auth/nonce/Gas前缀，proof-work按containing signed DAGVertex occurrence sponsor保留份额；同一 source event的永久 consumed key不随policy、relayer或证书 signer subset改变，policy退役后的稳定过期须用双target context证明。
- Consensus Key 的签名意图必须在调用 signer 前写入 durable WAL。

## 规范优先级

1. 已接受 ADR；
2. 本目录中的 MUST、MUST NOT 和精确伪代码；
3. 冻结的 CDDL/schema 与测试向量；
4. [需求与不变量](../02-requirements-and-invariants.md)；
5. 工程文档和教程。

若 prose、schema、伪代码和测试向量不一致，实现必须停止相关签名和投票并提交规范问题，不能自行选择一种解释。

## 变更规则

以下变更必须升级协议版本并补充 ADR、向量、Byzantine 测试和模型检查：

- 共识对象字段、规范编码或 hash/signature domain；
- `n/q/k`、BatchAC 门槛或纠删码布局；
- strong/weak parent、support DFS 或 slot 排序；
- direct/indirect decide、restricted round jump 或 pacemaker；
- 执行根、FinalityCertificate、终态证明或 epoch close；
- 将 Ed25519 替换为聚合签名、阈值签名或其他密码学套件。
