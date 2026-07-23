# FinalWeave 与成熟方案：优势、代价和适用边界

> 状态：技术决策评估  
> 结论范围：架构预期，不是尚未实现系统的性能承诺

## 1. 结论摘要

FinalWeave v1 的核心组合是：

1. 所有验证者并行生产 Batch；
2. 独立 BatchAC 通过重构、重编码和持久化确认大数据可用；
3. FinalDAG-C 以每轮签名小顶点和隐式支持边直接导出共识顺序；
4. Mysticeti 风格 slot direct/indirect commit/skip 加全局稳定前缀；
5. IEEE S&P 2026 分析要求的 restricted round-jump；
6. 预设顺序可串行化的 exact-access + 有界 MVCC 并行执行；
7. 后续 DAG 顶点搭载 ExecutionAttestation，`q` 个相同摘要形成外部最终性；
8. 按全部containing Vertex occurrence sponsor隔离的通用prefilter/source-proof工作预算和有界DAG dependency promotion；
9. proof-carrying query、快照、同步和跨账本消息。

相对成熟线性 BFT，预期优势是：数据面不受单 leader 带宽约束、元数据共识不重复建立显式投票链、持续高负载下网络利用更均匀、执行可用多核且不改变串行语义。代价是 DAG 决策、缺失祖先、round-jump、两阶段最终性、并行执行验证、GC 和恢复显著复杂；生态成熟度、审计历史和运维人才都弱于成熟产品。

## 2. 比较方法

不能只比较“TPS”。本项目按以下维度评价：

- 故障/网络模型及最终性；
- 数据传播瓶颈、每节点带宽和消息放大；
- 正常路径与故障路径延迟；
- 大 Batch 的可恢复性；
- DAG/状态/证书存储增长；
- 执行冲突和多核利用率；
- 证明、轻验证、同步和裁剪；
- 崩溃恢复、防双签、epoch 升级；
- 实现/审计/运维复杂度与生态。

论文结果只说明其论文实现和实验环境。FinalWeave 必须在自己的代码、硬件、节点数、WAN 拓扑和负载上重新测量。

## 3. 路线总览

| 路线 | 数据传播 | 排序/最终性 | 执行 | 主要优势 | 主要限制 |
|---|---|---|---|---|---|
| FinalWeave | 全体 Batch + BatchAC | FinalDAG-C 直接解释 DAG；执行背书证书 | 预设顺序可串行化并行 | 高负载并行、证明与恢复一体设计 | 新系统、协议与运维复杂 |
| CometBFT 类 | proposer 广播块 | round proposer + prevote/precommit | 应用确定性 | 成熟、心智模型清晰、生态好 | 大块/高负载受 proposer 和轮次控制 |
| Hyperledger Fabric | client/peer 背书 | orderer 为 channel 排序 | execute-order-validate | 企业身份/隐私/策略生态成熟 | 编程模型和背书/MVCC 语义不同 |
| Besu QBFT | proposer 广播 EVM 块 | 许可验证者轮流提议与投票 | EVM 顺序语义 | EVM 工具、合约和运营生态成熟 | EVM 兼容成本；数据面非 DAG |
| HotStuff 类 | leader 提议 | 显式 vote/QC 链 | 通常顺序执行 | 简洁线性证明、leader change 明确 | leader 路径和附加投票/证书开销 |
| Narwhal + Tusk/Bullshark | DAG 先认证数据 | 在 certified DAG 上解释顺序 | 后置执行 | 强模块化 DA、吞吐好 | 显式 DAG certification 增消息/签名 |
| Mysticeti/Mahi-Mahi 类 | Vertex已签名、无独立顶点证书的结构化DAG（uncertified） | DAG 边隐式投票 | 后置执行 | 少显式证书、低延迟潜力 | 证明/实现复杂，round 行为敏感 |
| [Sui/Lutris owned-object 快速路径](https://docs.sui.io/paper/sui-lutris.pdf) | 对象或交易依赖驱动 | owned-object 无冲突交易走特殊路径 | 原生依赖并行 | 低冲突业务极低延迟 | 应用模型受限，通用共享状态复杂 |

## 4. 为什么不再叠加线性共识链

BatchAC 已经让大数据在进入 DAG 前可恢复；每轮 `q` strong parents 已经产生 quorum 交叉的签名因果关系。再让 leader 为 DAG 闭包构造提议、收显式 vote 和证书，会增加：

- 第二套 round/view/height 安全状态；
- leader 选择和超时路径；
- 额外签名验证、WAL、证书传播和同步；
- 数据已经并行传播后再次等待串行控制路径；
- DAG backlog 与线性共识 backlog 的双重背压。

FinalDAG-C 让顶点本身承载隐式支持，直接做 slot 决策。其优势在持续高吞吐而非所有负载：空闲或中等负载下，简单 leader BFT 可能更省消息、更易运维。

## 5. 与 CometBFT 类方案

[CometBFT v0.39 当前官方概览](https://docs.cosmos.network/cometbft/latest/docs/introduction/intro)和[共识规范](https://docs.cosmos.network/cometbft/latest/spec/consensus/Byzantine-Consensus-Algorithm)描述 proposer、prevote、precommit、round 和 ABCI，强调共识与确定性应用分离；比较版本固定为[v0.39.3](https://github.com/cometbft/cometbft/releases/tag/v0.39.3)。

成熟优势：多年生态和互操作经验；线性 block/height 容易解释；工具、RPC、索引和轻客户端更成熟；团队更容易招聘和运维。

FinalWeave 预期优势：所有验证者并行承载 Batch，避免每高度由一个 proposer 广播完整 body；BatchAC 显式覆盖可恢复性；共识边由持续 DAG 流量携带；执行 proof/快照/裁剪从设计开始统一。

FinalWeave 代价：每个节点要处理更多并行作者流量；DAG stable prefix 和缺失祖先远比线性高度复杂；对小联盟、低 TPS、成熟优先的项目，CometBFT 类通常是更稳妥选择。

## 6. 与 Hyperledger Fabric

[Fabric 架构](https://hyperledger-fabric.readthedocs.io/en/latest/peers/peers.html)将提案执行/背书、ordering 和 validation 分开；[读写集与 MVCC](https://hyperledger-fabric.readthedocs.io/en/latest/readwrite.html)决定交易在排序后是否有效。

Fabric 优势：组织/证书/Channel/私有数据/背书策略/运维工具成熟；适合业务先由指定机构背书再排序的联盟场景。

FinalWeave 差异：每个 Ledger 有统一 BFT validator set；交易先被 BatchAC/DAG 排序，再以确定性状态机执行；proof-carrying query 和轻验证是一等接口；并行执行不要求 Fabric 式应用背书流程。

若业务核心是复杂背书政策、Channel 隔离和既有 Fabric 生态，迁移到 FinalWeave 没有天然优势。若核心是高持续写入、BFT 排序、可恢复 Batch 和统一最终证明，FinalWeave 的模型更直接。

## 7. 与 Besu QBFT / 许可型 EVM

[Besu QBFT 配置文档](https://docs.besu-eth.org/private-networks/how-to/configure/consensus/qbft)描述许可验证者的提议和投票配置；[Besu PoA 共识说明](https://docs.besu-eth.org/private-networks/concepts/poa)明确 QBFT 的即时最终性。

Besu 的决定性优势是 EVM：Solidity、钱包、RPC、监控、审计和应用人才成熟。FinalWeave 不应以纯共识吞吐数字低估迁移这些生态的成本。

FinalWeave 预期优势是可为业务状态机、BatchAC、DAG 与 proof 接口共同优化；不承担 EVM 字节码/存储/RPC 完全兼容开销；执行器能利用 exact-access 声明和串行兼容 lane。

需要无改造运行 Solidity、使用现成 EVM 工具或连接 DeFi 生态时应优先 Besu。FinalWeave 更适合协议和应用栈都可共同设计的专用联盟基础设施。

## 8. 与 HotStuff

[HotStuff](https://arxiv.org/abs/1803.05069)给出了部分同步 leader BFT、quorum certificate、响应性和线性通信的经典设计。它的线性安全状态和 view change 更容易解释、建模和审计。

FinalDAG-C 相比之下：

- 不增加 Proposal/Vote/QC/TC 消息族；
- 每轮每作者顶点同时传播 Batch 引用、父边支持和执行背书；
- 没有单 leader 承载全部数据，但全体验证者都承担持续发送和验证；
- 对慢 leader 的影响被多个 proposer slot 和 indirect decision 吸收，但前序 undecided slot 仍会造成 head-of-line；
- DAG 证明、round-jump 和 GC 更难，错误空间更大。

对低负载、验证者较少、工程团队小或审计预算有限的系统，HotStuff/成熟实现可能总体更优。FinalWeave 的选择是为性能目标接受可控制的复杂度，而非声称协议在所有维度优胜。

## 9. 与 Narwhal、Tusk、Bullshark

[Narwhal/Tusk](https://arxiv.org/abs/2105.11827)将可靠数据传播与 consensus 分离；[Bullshark](https://arxiv.org/abs/2201.05677)从 DAG 解释顺序并减少额外共识开销。这一谱系验证了 DAG 数据面在高吞吐 BFT 中的价值。

FinalWeave 保留独立 BatchAC，但 DAGVertex 自身不再单独认证。原因是：大 Batch 的可恢复性值得显式证书和持久化门槛；小元数据顶点再做 ACK/certificate 会增加延迟、签名和对象数量。强父边直接表达隐式支持。

代价是组合证明更复杂：必须严谨区分 BatchAC 可用性、DAG slot 顺序和执行最终性。协议不能从“Batch 有 BatchAC”推导“顶点已共识”，也不能从“DAG 已提交”推导“状态根已认证”。

## 10. 与 Mysticeti

[Mysticeti-C](https://doi.org/10.14722/ndss.2025.240929)使用 uncertified DAG，并报告三消息轮次提交下界、超过 200k TPS 和约 0.5 秒 WAN 提交等实验结果；这些数字属于论文实现和环境，不是 FinalWeave 承诺。

更重要的是，[IEEE S&P 2026 的机械化分析](https://doi.org/10.1109/SP63933.2026.00009)指出，在允许诚实进程无约束跳轮的规则下可构造无限不提交轨迹，并给出恢复活性的跳轮限制及 Rocq 证明；[公开 artifact](https://zenodo.org/records/17345693)包含模型和实现测试用例。

FinalDAG-C 因此把 restricted round-jump 列为安全关键基线。论文规则带有 GCT、`r' >= 3` 等模型前提；FinalWeave v1 采用从协议启动即生效的更强实例化，并由本项目规范固定 genesis/round 映射：跨过 `r'` 时，如果 `DecisionRound[r'-2] == UNDECIDED`，先补发 `r'` 顶点。它适用于正常推进、重启和状态同步，不能用配置关闭；这个更强实例化属于 FinalWeave 的新增证明义务，不能直接以论文证明替代。

这也揭示直接 DAG 的真实代价：常见路径性能很有吸引力，但一个看似局部的“快速追赶优化”就可能破坏活性。FinalWeave 必须维护模型—规范—实现映射，而不是只复制 commit rule。

## 11. Mahi-Mahi、Angelfish 与 Lemonshark为何是研究线而非 v1 拼装件

[Mahi-Mahi（arXiv v2 预印本）](https://arxiv.org/abs/2410.08670v2)研究异步 uncertified structured DAG、多 block/round 和 4/5 message-delay 参数；[Angelfish（arXiv v3 预印本）](https://arxiv.org/abs/2509.15847v3)研究在 leader 与 DAG 路线间动态调整发送轻 vote 的参与方；[Lemonshark（NSDI '26 正式论文）](https://www.usenix.org/conference/nsdi26/presentation/hu-michael)研究在官方 DAG commit 前对部分交易早最终。预印本版本核对日期为 2026-07-23。

这些方案提供重要优化方向，但 v1 不把多个论文的局部规则混合成未经证明的“更快协议”：

- FinalWeave 故障模型冻结为部分同步；
- 同 epoch 不动态切共识或 leader/DAG 语义；
- 外部最终性统一要求 ExecutionAttestation quorum；
- 早期交易 finality 需要重新定义 nonce、执行和 proof，暂不加入；
- 新机制必须有完整组合模型、实现映射和 epoch 升级。

`proposer_slots_per_round=2` 是 v1 稳健折中；`q` slots 只做低故障实验 profile。slot 更多可能增加 Byzantine/异步时未决次级 slot，放大全局稳定前缀的 head-of-line。每轮仅 primary slot timer 提供活性保证。

## 12. 执行层：并行而不改变语义

[Block-STM](https://arxiv.org/abs/2203.06871)表明预设顺序可支持乐观并行并保持顺序等价；论文报告的 32 线程和特定 benchmark 加速不能直接外推到 FinalWeave。

FinalWeave v1 不是无界重试的纯 Block-STM：

- canonical 语义始终是 `tx_index` 串行 Apply；
- 可安全验证 exact access 的交易进入依赖图和有界 optimistic MVCC；
- 不能声明 exact access 的交易自动进入串行兼容 lane，不损失功能；
- 每笔最多一次推测执行，冲突最多一次权威重执行；
- 按 `tx_index` 前缀认证结果；
- 串行引擎长期保留为 oracle、恢复和压力回退。

优势是低冲突工作负载利用多核，高冲突时成本有硬上限。代价是访问集验证、版本内存和调度开销；exact-access 覆盖率低时性能可能接近串行。

## 13. FinalWeave 的实际优势

### 13.1 大数据与共识元数据分离

BatchAC 处理大 body 的可恢复性，DAG 顶点只传摘要、父边和背书。共识快速路径不需要 leader 重播所有 body。

### 13.2 网络并行与故障退化

多个作者并行出 Batch/顶点，可利用总出站带宽；单作者慢或 Byzantine 不必让整轮无数据。但 quorum、前序 undecided slot、执行和磁盘仍可能阻塞最终性。

### 13.3 执行多核与兼容性同时保留

exact-access 交易并行，未知访问集交易串行；不为了性能拒绝通用状态机功能；冲突重试有界。

### 13.4 顺序与执行证明分离

DAGCommitWitness 允许内部流水执行；FinalityCertificate 明确认证 state/receipt/event roots。API 不把“排序了”误报成“执行最终”。

### 13.5 证明、恢复和升级是协议组成

FinalityProof、validator set chain、snapshot、WAL、epoch 和裁剪不是后补插件。对于合规审计和不信任查询服务，这比仅返回节点 JSON 更可靠。

### 13.6 Byzantine 资源公平性进入共识筛选设计

坏账户签名、strict key、治理bundle和source proof会在产生Receipt前消耗大量CPU。这些成本属于执行前验证；成熟系统是否由Gas或其他资源计量覆盖取决于具体实现，不能从传统执行Gas一概推导。FinalWeave为全部n个authenticated containing Vertex occurrence sponsor各保留最大合法验证额度，余量才共享；Batch author只认证数据来源，即使恶意Vertex反复引用honest作者的旧大Batch，也只能消耗引用者自己的份额。未引用DAG sibling只进入固定硬上限quarantine，被精确引用的闭包则走独立sponsor-fair promotion。优势是在恶意sponsor耗尽自己与shared资源时仍保留诚实sponsor推进通道，而且分类、checkpoint和cache语义跨节点确定。代价是两套work meter、attempt map、恢复checkpoint、容量challenge和更多测试/可观测性；若实现把预算归因错到Batch author、proposer、relayer或peer，这项优势反而会变成活性故障。

## 14. 主要劣势和风险

1. **尚未实现/验证**：所有性能和生产稳定性都待代码、审计和长稳证明。
2. **协议复杂**：slot 可见性、indirect decision、稳定前缀、restricted jump 和 GC 容易产生角落错误。
3. **资源放大**：所有作者并行出数据；ACK signer 重构/重编码；DAG/祖先同步和执行背书增加成本。
4. **两阶段延迟**：DAG order commit 后还需执行与 `q` attestation，外部最终性晚于内部顺序。
5. **执行收益依赖负载**：高冲突或 exact-access 覆盖低时退化接近串行。
6. **生态弱**：没有 EVM/Fabric/CometBFT 的现成工具、人才、应用和多年运维经验。
7. **形式化义务大**：引用论文不能覆盖 BatchAC + FinalDAG-C + execution finality + epoch 的组合安全。
8. **公平性/MEV**：确定性 tie-break 可能产生顺序偏置；任何随机化必须来自可验证、不可由证书 signer 子集操纵的语义输入，并需单独 ADR。
9. **防DoS状态复杂**：per-occurrence-sponsor work、shared pool、dependency promotion和crash checkpoint都需要精确实现；本地“简单限流”不能替代协议分类。

## 15. 不应选择 FinalWeave 的场景

- 低 TPS、小区块、尽快上线、团队希望使用成熟共识库；
- 必须无改造兼容 Solidity/EVM 或 Fabric Channel/背书；
- 无法提供稳定带宽、NVMe、KMS 和跨故障域验证者；
- 无预算做模型、外部审计、Byzantine/Chaos 和长期运维；
- 业务不需要 proof-carrying query 或可恢复大 Batch；
- 需求主要是数据库复制，而不是多组织 Byzantine 信任边界。

## 16. 公平基准计划

比较对象应使用同硬件、同 validator 数、同 WAN 拓扑、同交易状态机和相近 durability：至少一个成熟线性 BFT、一个许可型 EVM/Fabric 代表、FinalWeave serial mode 和 hybrid mode。

报告：submitted/accepted/ordered/finalized TPS；端到端 submit-to-FinalityProof p50/p95/p99；BatchAC/DAG/execute/attestation 分段延迟；每节点及最慢节点 CPU/RAM/disk/network；故障恢复；高冲突；1 个 crash/Byzantine；过载后恢复时间。

禁止只报告 Batch 生成或 DAG commit TPS，因为应用等待的是外部 FinalityProof。

## 17. 决策结论

FinalWeave 选择 FinalDAG-C，是因为目标用户明确把持续性能置于实现简洁之前，并愿意承担经过测量和验证的复杂度。该选择成立的条件是：

- BatchAC 的可用性语义不被削弱；
- restricted round-jump 和稳定前缀不被“优化”绕开；
- 并行执行严格 refinement 到串行 Apply；
- 外部 FINALIZED 始终需要 ExecutionAttestation quorum；
- 同 epoch 不切换共识；
- 自有组合协议完成形式化、审计和真实基准。

若这些条件无法满足，退回成熟线性 BFT 比维护一个不可证明的高性能协议更合理。

## 18. 相关文档

- [系统架构](01-system-architecture.md)
- [ADR-002：FinalDAG-C](decisions/ADR-002-finaldag-c-direct-dag.md)
- [ADR-003：确定性并行执行](decisions/ADR-003-deterministic-speculative-parallel-execution.md)
- [技术参考](references.md)
