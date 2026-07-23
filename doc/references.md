# FinalWeave 技术参考与阅读顺序

> 本页列出研究与实现的证据来源。外部论文是设计输入，不是 FinalWeave 规范；冲突时以已接受 ADR、`protocol/`、测试向量和项目模型为准。

## 1. 建议阅读顺序

第一次进入协议开发：

1. [FinalWeave 系统架构](01-system-architecture.md)；
2. [需求与不变量](02-requirements-and-invariants.md)；
3. [Mysticeti](https://doi.org/10.14722/ndss.2025.240929) 的模型、DAG 和 slot 决策；
4. [IEEE S&P 2026 Mysticeti 机械化证明](https://doi.org/10.1109/SP63933.2026.00009)及[公开 artifact](https://zenodo.org/records/17345693)；
5. [数据可用性与 BlockDAG](protocol/02-data-availability-and-blockdag.md)；
6. [FinalDAG-C 共识](protocol/03-finaldag-consensus.md)；
7. [最终性、执行与纪元](protocol/04-finality-execution-and-epochs.md)；
8. [Block-STM](https://arxiv.org/abs/2203.06871)，再对照 FinalWeave 的有界执行约束；
9. 工程存储、同步、安全和测试文档。

阅读时始终问：论文的网络/故障模型是否相同？证明覆盖了哪些对象？实现是否改变 round advance、数据可用性、执行或 epoch 假设？

## 2. 直接 DAG 共识基线

### 2.1 Mysticeti

[Mysticeti: Reaching the Latency Limits with Uncertified DAGs](https://doi.org/10.14722/ndss.2025.240929)提出 uncertified DAG、Mysticeti-C 和低延迟 commit 规则。论文报告的吞吐/延迟属于其实现、硬件和负载；FinalWeave 不继承这些数字。

FinalWeave 借鉴的是：每作者/round 顶点、DAG 边隐式支持、slot commit/skip/undecided、direct/indirect decision 和按 slot 稳定输出。FinalDAG-C 不是“将论文名称换皮”，因为 BatchAC、执行证书、epoch、排序和恢复组合不同。

### 2.2 机械化安全/活性分析

[Mechanized Safety and Liveness Proofs for the Mysticeti Consensus Protocol under the LiDO-DAG Framework](https://doi.org/10.1109/SP63933.2026.00009)，IEEE Symposium on Security and Privacy 2026，指出在允许诚实进程无约束跳轮的规则下，可构造无限不提交轨迹，并给出恢复活性的跳轮限制。

[Zenodo artifact，DOI 10.5281/zenodo.17345693](https://zenodo.org/records/17345693)包含 Rocq/Coq 模型和实现测试用例。论文的恢复活性规则带有 GCT、`r' >= 3` 等模型前提；FinalWeave v1 采用从协议启动即生效的更强实例化，并由本项目规范固定 genesis/round 映射：跨过 `r'` 且 `DecisionRound[r'-2] == UNDECIDED` 时，必须补发 `r'` 顶点。这个实例化形成 FinalWeave 自身的新增证明义务，不能直接视为已被论文证明覆盖。

此证明覆盖论文模型，不自动证明 FinalWeave 的 BatchAC + FinalDAG-C + ExecutionAttestation + epoch 组合；项目需要建立 refinement map 和新增证明义务。

## 3. DAG 数据传播与早期路线

### Narwhal / Tusk

[Narwhal and Tusk: A DAG-based Mempool and Efficient BFT Consensus](https://arxiv.org/abs/2105.11827)系统化分离数据传播与排序，是理解 certified DAG 和 DAG mempool 的起点。

### DAG-Rider

[All You Need is DAG](https://arxiv.org/abs/2102.08325)描述异步 DAG BFT 和从公共因果结构导出总序的思路。

### Bullshark

[Bullshark: DAG BFT Protocols Made Practical](https://arxiv.org/abs/2201.05677)和[部分同步版本](https://arxiv.org/abs/2209.05633)展示如何直接解释 DAG 形成顺序并减少额外共识通信。

### Shoal / Shoal++

[Shoal](https://arxiv.org/abs/2306.03058)研究 DAG 共识流水化和 reputation；[Shoal++](https://arxiv.org/abs/2405.20488)研究进一步降低提交延迟。它们是 leader/anchor 流水研究参考，不直接进入 v1 规则。

## 4. 新一代 DAG 研究线

本节中尚未正式出版的论文固定到本次审阅所用的 arXiv 修订版；版本核对日期为 2026-07-23，后续若有正式会场版本应改引正式版本。

### Mahi-Mahi

[Mahi-Mahi: Low-Latency Asynchronous BFT DAG-Based Consensus（arXiv v2 预印本）](https://arxiv.org/abs/2410.08670v2)研究异步 uncertified structured DAG、每轮多个 block 和 4/5 message-delay 参数。FinalWeave v1 仍采用部分同步模型，不混合其 commit rule。

### Angelfish

[Angelfish: Leader, DAG, or Anywhere in Between（arXiv v3 预印本）](https://arxiv.org/abs/2509.15847v3)研究动态选择部分参与方发送轻量 vote，在 leader/DAG 谱系间调节。FinalWeave 同 epoch 禁止动态改变共识语义，因此仅作为后续完整协议版本研究。

### Lemonshark

[Lemonshark: Asynchronous DAG-BFT With Early Finality（NSDI '26）](https://www.usenix.org/conference/nsdi26/presentation/hu-michael)研究在正式 DAG commit 前为部分交易建立早期最终性。其交易级规则会影响 nonce、执行和证明；v1 不采用早最终路径。

### 研究准入规则

任何研究线进入 FinalWeave 必须：明确网络/故障模型；给出与现有 slot/epoch/BatchAC 的组合安全；补齐机器模型或证明；更新协议 vectors；仅在新 epoch/新协议版本激活；重新执行端到端 FinalityProof benchmark。

## 5. 线性 BFT 背景

### HotStuff

[HotStuff: BFT Consensus in the Lens of Blockchain](https://arxiv.org/abs/1803.05069)是理解 leader、quorum intersection、responsive BFT 和线性 view-change 的重要背景。FinalWeave v1 不使用 HotStuff 作为基线排序层，文档只在比较与教学中保留它。

### Tendermint / CometBFT

[CometBFT v0.39 当前官方概览](https://docs.cosmos.network/cometbft/latest/docs/introduction/intro)、[共识规范](https://docs.cosmos.network/cometbft/latest/spec/consensus/Byzantine-Consensus-Algorithm)、[内存池消息规范](https://docs.cosmos.network/cometbft/latest/spec/p2p/legacy-docs/messages/mempool)和[v0.39.3 发布页](https://github.com/cometbft/cometbft/releases/tag/v0.39.3)用于比较成熟 round-based BFT、应用接口和工程边界。

## 6. 企业链与许可型 EVM

### Hyperledger Fabric

- [Peers / ordering / ledger roles](https://hyperledger-fabric.readthedocs.io/en/latest/peers/peers.html)
- [Read-write set semantics](https://hyperledger-fabric.readthedocs.io/en/latest/readwrite.html)
- [Private data](https://hyperledger-fabric.readthedocs.io/en/latest/private-data/private-data.html)

用于比较 execute-order-validate、背书策略、Channel 与企业身份生态。

### Hyperledger Besu QBFT

[QBFT 许可网络配置](https://docs.besu-eth.org/private-networks/how-to/configure/consensus/qbft)与[Besu PoA 共识说明](https://docs.besu-eth.org/private-networks/concepts/poa)分别用于比较 EVM 兼容、proposer/vote 流程、即时最终性和成熟工具链。

## 7. 确定性并行执行

[Block-STM: Scaling Blockchain Execution by Turning Ordering Curse to a Performance Blessing](https://arxiv.org/abs/2203.06871)研究预设顺序下的软件事务内存和确定性并行执行。

FinalWeave 只吸收“结果与预定顺序一致”的核心思想，不照搬无界推测策略。v1 规范是：

- canonical `tx_index` 串行 Apply 是权威语义；
- exact-access 依赖图 + 有界 optimistic MVCC；
- 按 `tx_index` 前缀认证；
- 不可安全声明访问集的交易进入串行兼容 lane；
- 每笔最多一次推测、最多一次权威重执行；
- 串行引擎保留为 oracle 和恢复路径。

[Efficient Parallel Execution of Blockchain Transactions Leveraging Conflict Specifications](https://drops.dagstuhl.de/entities/document/10.4230/LIPIcs.AFT.2025.29)可作为显式冲突信息优化的后续研究参考。

## 8. 数据可用性、编码与承诺

### 纠删码

- [Reed–Solomon Codes and Their Applications（IEEE，DOI 10.1109/9780470546345）](https://doi.org/10.1109/9780470546345)
- 具体库的编码矩阵、padding、fragment index 和错误处理必须由 FinalWeave vectors 固定，不能只引用算法名称。

### Merkle 与状态证明

- [Certificate Transparency Version 2.0 — RFC 9162](https://www.rfc-editor.org/rfc/rfc9162)提供 Merkle audit path 的工程参考；
- Sparse Merkle Tree 的 key/value/default-node/domain 定义必须由 FinalWeave 协议固定。

## 9. 编码、签名与传输标准

- [RFC 8949 — CBOR](https://www.rfc-editor.org/rfc/rfc8949)
- [RFC 8032 — Ed25519](https://www.rfc-editor.org/rfc/rfc8032)
- [FIPS 180-4 — SHA-256](https://csrc.nist.gov/pubs/fips/180-4/upd1/final)
- [RFC 7301 — TLS ALPN](https://www.rfc-editor.org/rfc/rfc7301)
- [RFC 8446 — TLS 1.3](https://www.rfc-editor.org/rfc/rfc8446)
- [RFC 9000 — QUIC Transport](https://www.rfc-editor.org/rfc/rfc9000)
- [RFC 9001 — Using TLS to Secure QUIC](https://www.rfc-editor.org/rfc/rfc9001)
- [RFC 9002 — QUIC Loss Detection and Congestion Control](https://www.rfc-editor.org/rfc/rfc9002)
- [gRPC](https://grpc.io/docs/)
- [Protocol Buffers](https://protobuf.dev/programming-guides/proto3/)

“使用 CBOR/Ed25519/SHA-256”仍不足以实现兼容协议；必须同时固定域隔离、规范编码、长度、顺序、重复字段、错误输入和测试向量。

## 10. P2P、存储与可观测性

- [libp2p specifications](https://github.com/libp2p/specs)
- [Pebble](https://github.com/cockroachdb/pebble)
- [OpenTelemetry specifications](https://opentelemetry.io/docs/specs/)
- [Prometheus metric naming](https://prometheus.io/docs/practices/naming/)

实现选择不进入共识哈希，但 durability、限流、流优先级和资源上限会影响活性，必须纳入测试。

## 11. 形式化、测试与供应链

- [TLA+](https://lamport.azurewebsites.net/tla/tla.html)
- [Apalache](https://apalache-mc.org/)
- [Rocq Prover](https://rocq-prover.org/)
- [Jepsen](https://jepsen.io/)
- [Go fuzzing](https://go.dev/doc/security/fuzz/)
- [SLSA v1.2](https://slsa.dev/spec/v1.2/)
- [CycloneDX](https://cyclonedx.org/specification/overview/)

建议模型顺序：quorum/epoch；BatchAC durability；DAG slot/稳定前缀；restricted round-jump liveness；ordered/executed/finalized 分层；执行 serial refinement；WAL crash safety；snapshot/epoch 切换。

## 12. 性能引用规则

任何数字必须紧邻来源，并说明：论文/官方 benchmark 还是 FinalWeave 自测；commit 定义；客户端到提交还是协议内部延迟；validator 数和 Byzantine 情况；LAN/WAN；Batch/交易大小；硬件/带宽；是否含持久化、执行和 FinalityProof。

未满足这些字段的数字只能用作研究线索，不得进入容量承诺或 SLO。

## 13. 规范边界

外部资料不能替代：

- [ADR-002：FinalDAG-C](decisions/ADR-002-finaldag-c-direct-dag.md)；
- [ADR-003：确定性并行执行](decisions/ADR-003-deterministic-speculative-parallel-execution.md)；
- `protocol/` 的精确 schema、threshold、slot、排序、round-jump 和 proof；
- `specs/vectors` 的规范字节；
- FinalWeave 自身组合模型和审计结论。
