# FinalWeave 需求与不变量

> 状态：规范性  
> 适用版本：FinalWeave v1 / FinalDAG-C v1  
> 关键词：MUST/MUST NOT/SHOULD/MAY 按 RFC 2119/8174 解释

## 1. 系统与故障模型

每个 Ledger 在一个 epoch 内有固定的 `n=3f+1` 个验证者，最多 `f` 个 Byzantine，法定人数 `q=2f+1`，Batch 恢复门槛 `k=f+1`。v1 只接受 4/7/10/13… 且 `n<=253` 的节点集合，不使用任意 `n>=3f+1` 的模糊配置；ValidatorID、PeerID 与 DAG/Consensus/Peer key 的派生和跨角色唯一性由统一 `ValidateValidatorSet` 强制执行。

网络模型为部分同步：GST 前消息可任意延迟、重排或丢失；GST 后诚实在线节点间消息在未知有限界内到达。密码学假设为哈希抗碰撞、签名不可伪造、规范编码唯一。主机、磁盘、KMS、时钟、网络和管理员都可能局部故障；安全性不得依赖墙钟一致。

Byzantine 节点可双发顶点、选择性发送、伪造父边、签冲突执行摘要、发送畸形对象或拒绝服务。诚实节点可能崩溃并从 durable state 恢复，但不得在恢复后违反签名唯一性。

## 2. 项目级优先级

| ID | 规范 |
|---|---|
| PRJ-001 | 功能正确性、安全、可持续性能和可恢复运维 MUST 优先于实现简洁。 |
| PRJ-002 | 增加复杂度 MUST 有可复现收益指标、显式不变量、资源边界和故障恢复路径。 |
| PRJ-003 | 优化 MUST NOT 改变规范字节、canonical 顺序、串行 Apply 结果或证明语义。 |
| PRJ-004 | 无法证明或测量收益的复杂机制 MUST NOT 进入默认生产路径。 |
| PRJ-005 | 性能报告 MUST 同时包含吞吐、p50/p95/p99 延迟、错误率、CPU、内存、磁盘、网络和恢复成本。 |
| PRJ-006 | 论文或其他系统的结果 MUST 标注来源、硬件、节点数、负载和网络口径，MUST NOT 表述为 FinalWeave 实测。 |

## 3. 功能需求

### 3.1 账本、身份与隔离

| ID | 规范 |
|---|---|
| FR-LEDGER-001 | 每个 Ledger MUST 有独立 Genesis、epoch、validator set、DAG、状态、mempool 和证明命名空间。 |
| FR-LEDGER-002 | 协议对象 MUST 按其语义层级绑定适用的 network/ledger/epoch，并直接、经 versioned core、经版本化包含对象或经已认证 ProtocolConfig/state-machine version 绑定 schema 与专用 domain；network-scoped 身份不得伪造 ledger/epoch 绑定，TransactionIntent 绑定 ledger 但不绑定 epoch。 |
| FR-LEDGER-003 | PeerID、ValidatorID、OrganizationID 和 AccountAddress MUST 是不同类型且不得隐式转换；PeerID MUST 从 TLS/PeerHello 使用的 strict Ed25519 Peer public key 与 NetworkID 唯一派生。 |
| FR-LEDGER-004 | 共享进程、连接或数据库 MUST NOT 允许一个 Ledger 的过载或状态污染另一个 Ledger。 |
| FR-LEDGER-005 | validator set 只能由已最终治理状态定义，DHT/peer score/本地配置不得改变 quorum。 |

### 3.2 交易与 mempool

| ID | 规范 |
|---|---|
| FR-TX-001 | Admission MUST 校验大小、规范编码、账本、版本、签名、有效窗口和基础配额。 |
| FR-TX-002 | `ACCEPTED` 只表示本节点接纳；API MUST NOT 将其映射为最终成功。 |
| FR-TX-003 | 账户 `next_nonce` MUST 在认证状态中单调递增且不得因裁剪重置。 |
| FR-TX-004 | 一个 `(sender, nonce)` 最多有一个 canonical winner 进入交易树并产生收据。 |
| FR-TX-005 | 成功与确定业务失败都 MUST 消费 nonce；future nonce MUST 延后而非伪造失败收据。 |
| FR-TX-006 | 未最终交易在 Batch、顶点或 slot 未提交后 MUST 可重新打包。 |
| FR-TX-007 | `FINALIZED_*`、`REPLACED`、`EXPIRED` MUST 有可验证的 TransactionStatusEvidence。 |
| FR-TX-008 | 账户 MUST 由同一 AccountAddress key 下的 immutable meta、auth、nonce 三项认证状态完整表示；三项 MUST 全有或全无，普通模块 MUST NOT 写入保留 namespace。 |
| FR-TX-009 | 父状态不存在账户只能由 `ACCOUNT_CREATE_V1` 以地址 core、initial policy 和 nonce 0 自证；用户 access scope MUST 为空，三个 system write MUST 原子提交，同块不得创建两次或立即使用新账户。 |
| FR-TX-010 | 每个 raw occurrence MUST 在完整 Envelope 解码前按已验证source binding/frame length收取确定性scan work；唯一付费主体是承载该`AvailabilityReference`的签名DAGVertex作者（occurrence sponsor），Batch author只认证来源。cap失败只能流式比对source并分类`PREFILTER_SCAN_CAP`。通过bounded cheap context、窗口、active policy hash、exact nonce与block reserve后，Ed25519、strict-key、治理 approvals和完整reconfiguration bundle MUST 使用同一sponsor的独立确定性昂贵suffix work。 |
| FR-TX-011 | 通用 prefilter work MUST 为 ValidatorSet 全部 n 个 authenticated occurrence sponsor分别保留至少一个最大合法occurrence item的scan+suffix份额，并只把余量作为 shared pool；跨作者Batch引用MUST合法但不能把费用转嫁给Batch author，invalid 不退款、cache 不减逻辑 cost、cap loser不写 attempt map，MUST NOT 以 proposer slot数 P 分配。 |
| FR-TX-012 | scan/common/source-proof 的 charge与`STARTED`记录 MUST crash-consistent；记录 MUST 绑定origin occurrence cursor与charge receipt，恢复 MUST NOT重扣，本地故障 MUST NOT写成协议INVALID，cursor MUST NOT越过任一STARTED。 |

### 3.3 Batch 数据可用性

| ID | 规范 |
|---|---|
| FR-DA-001 | BatchHeader MUST 承诺作者、epoch、body length/hash、transaction root、编码参数和作者批次序号。 |
| FR-DA-002 | 编码 MUST 生成与 validator index 固定映射的 `n` 个 canonical fragments，恢复门槛 MUST 为 `k=f+1`。 |
| FR-DA-003 | ACK signer MUST 先收集 `k` 个不同合法分片并重构完整 body。 |
| FR-DA-004 | ACK signer MUST 验证 body/transaction root，按规范参数重编码并验证完整 codeword。 |
| FR-DA-005 | ACK signer MUST 在签名发送前持久化自己的 canonical fragment 和 `CODEWORD_VERIFIED` 状态。 |
| FR-DA-006 | BatchAC MUST 包含当前 epoch `q` 个不同验证者对同一 BatchHeader 的有效 ACK。 |
| FR-DA-007 | BatchAC MUST NOT 被解释为交易有效性、顺序或执行成功证明。 |
| FR-DA-008 | 不可恢复或编码不一致的 Batch MUST 被隔离并产生证据，MUST NOT 阻塞其他 Batch。 |

### 3.4 DAG 形成

| ID | 规范 |
|---|---|
| FR-DAG-001 | 每个诚实验证者在同一 `(ledger, epoch, round)` MUST 最多签一个 DAGVertex。 |
| FR-DAG-002 | 非 epoch 起点顶点 MUST 引用上一轮至少 `q` 个不同作者的合法 strong parents。 |
| FR-DAG-003 | strong/weak parent 必须按 schema 分离；weak parent MUST NOT 计入推进 round 的 quorum。 |
| FR-DAG-004 | 顶点只能引用有效 BatchAC，MUST NOT 内嵌未认证大 Batch body。 |
| FR-DAG-005 | DAGVertex 是签名小元数据消息，v1 MUST NOT 存在 VertexAck 或 VertexCertificate。 |
| FR-DAG-006 | 接收节点 MUST 验证作者、签名、round、父边、BatchAC 和祖先边界，并按该 epoch 已认证 ProtocolConfig 校验全部数量/字节约束；DAGVertex 不另带可被伪造的 config hash。 |
| FR-DAG-007 | 同作者同轮冲突顶点 MUST 形成 equivocation evidence；实现不得任意选择其中一个作为全局事实。 |
| FR-DAG-008 | 缺失 strong parent MUST 优先同步；祖先请求 MUST 有深度、数量和字节上限。 |
| FR-DAG-009 | 每个 epoch MUST 以 q 个协议派生 synthetic round-0 anchors 起步，首个实际签名 Vertex MUST 为 round 1；不得存在可签名 genesis Vertex。 |
| FR-DAG-010 | own-parent gap 达到 GC 窗口时，诚实作者只能通过经同 epoch Header/FinalityCertificate 及 authenticated emitted membership 约束的 `DAGRejoinCheckpointRef` 重入，并 MUST 先持久化永久放弃旧分支的 rejoin intent。 |
| FR-DAG-011 | 被已接纳 Vertex、证书/witness 或 anchor 按精确 VertexID 引用、可能进入 `Past(P)` 的每个 sibling MUST 按需拉取并原子提升到 dependency store；递归闭包完成前 MUST NOT support、decide、commit 或 attestation。 |
| FR-DAG-012 | 未被引用的同槽 sibling gossip MUST 只进入按 slot、Ledger 对象数和 canonical bytes 三重绝对上限约束的 quarantine；驱逐 MUST NOT 使 wire 对象无效，晚引用 MUST 可按 ID 重拉并提升，旁路 evidence/cache MUST NOT 参与共识。 |
| FR-DAG-013 | `evidence_refs` MUST 只作为有界审计 hint；Vertex support/validity 路径 MUST NOT 同步拉取完整 evidence，异步 worker MUST 受同一对象/字节 cap，缺失或错误 evidence MUST NOT 反向使 containing Vertex 无效。 |

### 3.5 FinalDAG-C 决策与顺序

| ID | 规范 |
|---|---|
| FR-CONS-001 | slot 映射、leader 选择和扫描顺序 MUST 由 epoch 配置确定且所有节点一致。 |
| FR-CONS-002 | 每个 slot 的本地状态只能是 `commit`、`skip` 或 `undecided`。 |
| FR-CONS-003 | direct commit/skip 与 indirect decision MUST 完全按协议图可达性和阈值规则计算。 |
| FR-CONS-004 | 节点 MUST 只输出从上一 cursor 开始连续已决定的全局 slot 稳定前缀。 |
| FR-CONS-005 | 后序 slot 已提交 MUST NOT 允许越过任何前序 `undecided` slot。 |
| FR-CONS-006 | commit slot 的新祖先闭包 MUST 以同一规范拓扑规则排序且每个对象只消费一次。 |
| FR-CONS-007 | DAG 边是隐式支持；v1 MUST NOT 引入 Proposal、Vote、QC、TC 或第二共识链。 |
| FR-CONS-008 | DAGCommitWitness MUST 让验证者从其 slots/vertices/dependencies 重算 epoch、连续 slot 决策、stable cursor 与 ordered root，并与调用方显式给出的审计目标逐项对照；不得把外部目标误当作 witness 内未定义字段。 |
| FR-CONS-009 | DAGCommitWitness 只能证明顺序，MUST NOT 单独作为外部执行最终性证明。 |
| FR-CONS-010 | `proposer_slots_per_round` v1 默认 2、范围 `1..q`、仅 epoch 边界可变；`q` 仅是实验 profile。 |
| FR-CONS-011 | 每 round 只有 primary slot（slot 0）timer 获得活性保证；secondary slot 不得引入改变语义的独立 timer。 |
| FR-CONS-012 | 每个 COMMIT 派生块 MUST 将完整 Vertex delta 原子插入 epoch-scoped authenticated emitted sparse set，并在 Header 认证累计 count/root；SKIP MUST NOT 改变它。 |

### 3.6 Restricted round-jump

| ID | 规范 |
|---|---|
| FR-JUMP-001 | 节点从本地 round 跳向更高 round 时 MUST 枚举每个被跨过的 `r'`。 |
| FR-JUMP-002 | 若 `DecisionRound[r'-2] == UNDECIDED`，节点 MUST 先构造并发出自己在 `r'` 的合法顶点。 |
| FR-JUMP-003 | 补发顶点 MUST 满足正常父边、BatchAC、签名唯一性和 WAL 规则。 |
| FR-JUMP-004 | 重启、状态同步、快速追赶和正常 reactor MUST 共用同一 round-jump 状态机。 |
| FR-JUMP-005 | 本地配置或性能模式 MUST NOT 关闭、抽样或跳过此检查。 |

该限制来自已发表的 Mysticeti 机械化安全/活性分析；FinalWeave 必须保留相应反例与修复回归，同时验证其组合协议中的适用条件。

### 3.7 执行与外部最终性

| ID | 规范 |
|---|---|
| FR-EXEC-001 | canonical 语义 MUST 是按 `tx_index` 递增逐笔串行 `Apply`。 |
| FR-EXEC-002 | 生产执行 MAY 并行，但结果 MUST 与规范串行 oracle 的状态、收据、事件、gas 和错误逐字节相同。 |
| FR-EXEC-003 | v1 默认 MUST 使用 exact-access 依赖图、有界 optimistic MVCC、按 `tx_index` 前缀认证和串行权威回退。 |
| FR-EXEC-004 | 不能安全声明精确访问集的交易 MUST 自动进入串行兼容 lane，不得拒绝合法功能。 |
| FR-EXEC-005 | 每笔交易最多一次推测执行；冲突时最多一次权威重执行，MUST NOT 出现无界 abort storm。 |
| FR-EXEC-006 | 串行引擎 MUST 保留为测试 oracle、恢复模式和过载回退。 |
| FR-EXEC-007 | FinalizedBlockHeader/FinalityStatement MUST 绑定 ordered/state/receipt/event roots、height、epoch、validator/config hash 和状态机版本。 |
| FR-EXEC-008 | 验证者只能在执行结果 durable 且完整验证后签 ExecutionAttestation。 |
| FR-EXEC-009 | FinalityCertificate MUST 由当前 epoch `q` 个不同验证者对同一 FinalityStatement 的背书组成。 |
| FR-EXEC-010 | 外部 `FINALIZED` MUST 由 FinalityProof 建立；DAG commit 或单节点执行结果不得替代。 |
| FR-EXEC-011 | FeatureSet 与 GasSchedule MUST 通过 v1 typed/exact operation registry；缺项、额外项、未知项或零成本绕过 MUST 使 bundle 无效。 |
| FR-EXEC-012 | GasEvent 顺序、input/output byte metric、OOG、revert 和权威重执行计量 MUST 跨实现一致；推测工作不得重复计入 `gas_used`。 |
| FR-EXEC-013 | v1 `fee_limit` MUST 为 0，且 MUST NOT 产生任何费用状态写或 `FEE_LIMIT_EXCEEDED` Receipt。 |
| FR-EXEC-014 | state/event/return/call/Body 的 component、per-tx、per-block 与绝对 cap MUST 在读取或分配前 checked；普通 winner 超限产生确定性 `STATE_LIMIT_EXCEEDED`。 |

### 3.8 查询、同步与跨账本

| ID | 规范 |
|---|---|
| FR-PROOF-001 | 交易最终查询 MUST 返回交易/收据包含证明和 FinalityProof。 |
| FR-PROOF-002 | 状态查询 MUST 可返回 SMT inclusion/non-inclusion proof 与对应 FinalityProof。 |
| FR-PROOF-003 | 证明验证 MUST 使用对应 epoch validator set，并从信任锚验证配置变更链。 |
| FR-PROOF-004 | 同步节点 MUST 先验证 checkpoint/manifest/chunk proof 再切换状态。 |
| FR-PROOF-005 | 跨账本消息 MUST 验证源事件 proof、source FinalityProof、目标 replay protection 和有效窗口。 |
| FR-PROOF-006 | Gateway/Full/Archive 均不得仅凭角色身份替代密码学证明。 |
| FR-PROOF-007 | 跨账本 source trust root/policy MUST 来自目标账本当前已认证 FeatureSet；proof、relayer、API 响应、peer 多数或本地 cache MUST NOT 安装、替换或选择它。 |
| FR-PROOF-008 | source proof MUST 将成功的原生 SEND transaction、Receipt、per-tx Event path 和 block Event path 绑定到同一 source Header/FinalityCertificate，并重建唯一 SourceEventID。 |
| FR-PROOF-009 | 目标 replay key MUST 只由 target context、source ledger 与 SourceEventID 派生；其 present state MUST 永久保留并进入 SMT/Snapshot，不能随 policy、epoch、proof signer subset 或历史裁剪重置。 |
| FR-PROOF-010 | 同一 source event 的并发 CONSUME occurrence MUST 按 canonical occurrence 顺序选择首个可完整预留者；replay loser MUST NOT 进交易树、耗 nonce 或产生失败 Receipt。 |
| FR-PROOF-011 | source message window MUST 由源发送方签名并以目标账本高度解释；墙钟、source/target height 差值和 relayer 到达时间 MUST NOT 改变有效性。 |
| FR-PROOF-012 | source proof bytes、epoch transitions、source signatures、message payload、Gas、Event、state write 与 Body MUST 同时受前分配绝对上限和 active policy/config 上限约束。 |
| FR-PROOF-013 | CONSUME MUST 在source proof密码学验证前完成bounded outer parse、target账户鉴权、tx窗口/exact nonce、policy/relayer/RequiredGas与success-reserve前缀；声明message窗口/tentative replay仅可reject-only且成功proof后必须重算；proof-work MUST按ValidatorSet全部n个authenticated containing Vertex sponsor保留份额并受共享总上限约束，Batch author/relayer/peer/proposer不得替代sponsor，MUST NOT按proposer slot数P分配。 |
| FR-PROOF-014 | `EXPIRED_UNCONSUMED` MUST 同时验证 message window内认证 exact历史 policy的 target Header/FeatureSet context，以及严格晚于 until的同 ledger tip context与 consumption-key non-inclusion；current policy miss MUST NOT替代历史证据。 |
| FR-PROOF-015 | 当前 epoch 从 Snapshot 恢复并重新成为 Validator MUST 同时验证同一 target Header 的 `DAGDerivationCheckpoint`、MMR peaks、committed slot 和 emitted count/root，并原子安装 full snapshot marker；state-only install MUST 保持 query-only。 |
| FR-PROOF-016 | `DAGDerivationCheckpoint` MUST 携带可重建 exact membership 的严格排序唯一 VertexID payload；仅有 root、未认证 peer cache 或被裁剪 DAG MUST NOT 恢复 signer readiness。 |

### 3.9 Epoch 与治理

| ID | 规范 |
|---|---|
| FR-EPOCH-001 | validator set、`n/f/q/k` 和所有安全关键协议参数 MUST 在 epoch 内固定。 |
| FR-EPOCH-002 | 同一 epoch MUST NOT 动态切换 FinalDAG-C 与任何其他共识算法或决策规则。 |
| FR-EPOCH-003 | 配置/实现升级 MUST 由旧 epoch 最终治理状态授权并在新 epoch 激活。 |
| FR-EPOCH-004 | 新 epoch 起点 MUST 唯一绑定旧 epoch 终止 FinalityProof、validator set 和 state root。 |
| FR-EPOCH-005 | 管理 API MUST NOT 绕过链上治理直接改写共识或状态机配置。 |
| FR-EPOCH-006 | 无适用 pending reconfiguration 时，epoch MUST 在第 65,536 个 finalized block 或 authenticated emitted count 首次跨过 4,194,304 的第一个 stable COMMIT candidate 执行 same-config rollover；不得以有限 DAG round 作硬停止条件。 |
| FR-EPOCH-007 | 自动阈值 candidate 可以有一个有限 emitted delta overshoot；一旦 reservation durable，旧 epoch MUST NOT 分配 C+1 高度。 |
| FR-EPOCH-008 | close MUST 遵循 reservation → 确定性执行 C → 读取 C post-state 选择 pending/same-config → closing intent → fence → attestation；任一步恢复只能继续同一 C 与同一 digest。 |
| FR-EPOCH-009 | closing certified publication MUST 与含同 `closing_intent_hash` 的四字段 `EPOCH_CLOSED` 原子可见；next epoch synthetic anchors/seed MUST 由已验证 seal 唯一派生。 |

## 4. 核心安全不变量

### SAFE-001：单轮单顶点

诚实验证者在同一 `(network, ledger, epoch, round)` 最多签一个顶点。签名 intent 必须先 durable，进程并发和重启共用同一原子 compare-and-set。

### SAFE-002：BatchAC 可恢复性

合法 BatchAC 含 `q` 个 ACK，其中至少 `f+1` 来自诚实节点。每个诚实 ACK signer 已验证完整 codeword 并持久化固定分片，因此在最多 `f` Byzantine 下存在足够诚实分片恢复 Batch。

### SAFE-003：合法父边与隐式支持

诚实节点只 strong-reference 已完整验证的父顶点。一个 strong edge 的签名作者对该引用负责；接收方不能把未验证、缺失或跨 epoch 父边计入阈值。

### SAFE-004：slot 决策一致

任何两个诚实节点对同一 slot 都不能一方合法输出 commit、另一方合法输出 skip；不同 DAG 可见性下的 indirect rule 必须保持这一性质。实现需以论文模型、项目模型和差分测试分别验证。

### SAFE-005：全局稳定前缀

任意两个诚实节点输出的 slot 序列必须互为前缀。节点不得基于“后面已经 commit”推断前面缺失 slot 可跳过。

### SAFE-006：唯一 canonical 顺序

给定同一稳定 slot 前缀和 DAG，所有诚实节点产生相同 ordered vertices、Batch 和 transaction index。排序 tie-break 不依赖到达时间、本地 map 迭代、线程时序或证书 signer 子集。

### SAFE-007：执行串行等价

无论 exact-access 图、MVCC 推测或 worker 数如何，最终结果与 `tx_index` 串行 Apply 一致。不能证明 exact access 的交易进入串行 lane；冲突推测只允许一次权威重执行。

### SAFE-008：执行不双签

诚实验证者在同一 `(ledger, epoch, execution_height)` 不得签署两个冲突 FinalityStatement。attestation intent 必须在签名前持久化；恢复后必须验证 state/ordered cursor 再恢复签名能力。

### SAFE-009：外部最终性

只有合法 FinalityCertificate 才能认证 FinalityStatement 及对应 FinalizedBlockHeader。交易/收据/状态证明还必须绑定该 header 的 Merkle/SMT root。DAGCommitWitness、progress stage、节点身份或缓存都不能替代它。

### SAFE-010：epoch 隔离

旧 epoch 的顶点、ACK 或 attestation 不计入新 epoch quorum。任何跨 epoch 重放都因域、epoch、validator set/config hash 不匹配而失败。

### SAFE-011：跨账本唯一消费

目标 active FeatureSet 唯一选择 source trust policy；只有完整验证的 native SEND source event 才能派生 SourceEventID。目标 consumed key 以该 occurrence 身份永久存在，同一块以 exact working set、跨块以 SMT state 共同保证最多一个成功 CONSUME，任何 proof 重包装、relayer 竞争、policy/epoch 轮换或裁剪都不能产生第二次消费。

## 5. 活性不变量

### LIVE-001：Batch 继续可用

GST 后，若至少 `q` 个正确/响应验证者有资源处理合法 Batch，则诚实作者能在有限时间收集 BatchAC。单个坏 Batch 不得阻塞其他作者。

### LIVE-002：DAG 推进

GST 后，至少 `q` 个诚实/可用作者持续创建合法顶点，缺失父边同步有界且高优先级时，DAG round 能持续推进。

### LIVE-003：受限跳轮

所有 round 推进路径必须执行 restricted round-jump。在允许诚实进程无约束跳轮的规则下可构造的无限不提交轨迹，必须成为永久回归测试。

### LIVE-004：slot 最终决定

在部分同步活性前提和协议 leader schedule 下，每个 stable-prefix 前沿 slot 最终从 undecided 变为 commit 或 skip；坏 leader 不能永久阻塞后续输出。

### LIVE-005：执行与背书跟进

排序生产必须受执行/状态提交背压。若 quorum 节点能执行稳定前缀，则 ExecutionAttestation 最终形成 FinalityCertificate；持续产生 DAG 但执行永远落后不是可接受活性。

### LIVE-006：交易重包含

未终态交易即使作者崩溃、Batch 未获证书、顶点未提交或 slot 被 skip，也能在有效窗口内由其他 Batch 重包含。

### LIVE-007：跨账本 proof 公平推进

active bundle 必须为每个 Validator作为containing Vertex author时保留至少一次最大合法 source proof的验证额度。诚实Vertex author只引用本地完整预验并公平调度的 CONSUME；f个 Byzantine Vertex sponsor持续引用外层有效、source proof无效的 occurrence时，只能耗用各自保留份额与共享余量。即使它们反复引用honest validator签发的旧Batch，也不能消耗该Batch author作为未来Vertex sponsor时的独占份额，因而不能永久阻塞诚实sponsor通道中的合法跨账本消息。

### LIVE-008：通用预过滤公平推进

ProtocolConfig 必须为每个 Validator作为containing Vertex author时保留至少一次最大合法 v1 occurrence item的scan、昂贵静态、鉴权和治理验证额度。诚实Vertex author只引用本地完整预验并公平调度的交易；f个 Byzantine sponsor即使持续提交坏签名、坏 key、坏 reconfiguration bundle，或反复引用其他作者的旧大Batch并耗尽 shared pool，也不能消耗诚实Vertex sponsor的独占份额。

## 6. 崩溃与恢复不变量

| ID | 规范 |
|---|---|
| REC-001 | DAG 顶点签名 intent MUST 在发送前 fsync；同轮冲突 intent MUST fail closed。 |
| REC-002 | DA ACK MUST 在 canonical fragment/codeword 状态 durable 后发送。 |
| REC-003 | ExecutionAttestation MUST 在执行状态和 attestation intent durable 后发送。 |
| REC-004 | stable cursor、slot decisions、ordered root、state root 和 receipt root MUST 原子推进或可确定恢复。 |
| REC-005 | snapshot 导入 MUST 写新命名空间、验证证明，再原子切换；失败不得破坏旧状态。 |
| REC-006 | WAL 损坏、epoch 不明或签名状态冲突 MUST 阻止 Validator readiness。 |
| REC-007 | 恢复后跳轮 MUST 重新执行 restricted round-jump，不能从远端 round 直接赋值。 |
| REC-008 | 跨账本 consumed state、成功 Receipt、Event、nonce 与 public cursor MUST 原子发布；恢复后只能看到全无或一个完整消费，永久 key MUST NOT 因 proof/Body 裁剪丢失。 |
| REC-009 | certified publish、full snapshot install 与 query-only snapshot install MUST 使用具名 marker、previous-active hash chain 和共享 CAS；marker durable 后崩溃 MUST roll-forward，分叉 chain MUST fail closed。 |
| REC-010 | 当前 epoch active authenticated emitted exact set MUST NOT 进入通用 GC；最新可恢复 state Snapshot MUST 与同 target DAGDerivationCheckpoint 配对保留。 |
| REC-011 | 有 closing reservation 无 intent/fence 时 MUST 重建并执行同一 C；有 intent 无 fence时只能补同一 fence；有 fence 无 closed时只能认证/发布同一 C。 |
| REC-012 | state-only `QuerySnapshotInstallMarkerV1` MUST NOT 锚定 H+1 publication 或开放 ACK/Vertex/attestation signer；同 target query→full 升级需原子验证完整 derivation generation。 |
| REC-013 | occurrence-filter 中途 checkpoint MUST 同时绑定可选in-flight scan、completed-scan逐sponsor/shared累计量、common/source-proof exact attempt maps及receipts、全部n个sponsor的两套reserve/shared spend、working consumption set、active bundle内容ID，以及由containing signed DAGVertex取得的sponsor作者和由BatchHeader取得的Batch作者映射；所有receipt MUST记录`sponsor_author_index`。charge+STARTED及Finish的receipt并入/清in-flight/推进cursor MUST原子，全部spend MUST由累计量与receipts重算；任一项不可验证时 MUST回滚完整occurrence边界或从块首重扫。 |

## 7. 资源与拒绝服务不变量

- 所有解码、父边、祖先、Batch、proof、snapshot、查询、stream 和队列 MUST 有显式大小/数量/时间上限；
- 跨账本 source proof MUST 额外限制 canonical bytes、target-envelope overhead、transition 与逻辑签名数；cache hit不得改变逻辑 Gas trace或信任结论；cheap target前缀、per-occurrence-sponsor reserve、shared proof-work cap与同 tx-id attempt dedup必须在恢复和并行执行中保持确定；
- raw occurrence的decode/hash/state放大与account、policy rotation、governance/reconfiguration的非Receipt密码学/registry work MUST分别受scan/suffix绝对上限、per-occurrence-sponsor reserve和shared cap约束；sponsor只能从containing signed DAGVertex恢复，Batch author不得替代。scan cap后不得解码Envelope或读状态，suffix cap后不得启动昂贵worker。完整验证任意有限causal delta仍可需要与输入字节成线性的stream I/O，MUST用固定chunk、spill、sponsor-fair fetch与背压保持空间有界而不是伪造截断；
- Gas 预算与硬资源 cap MUST 同时生效；GasSchedule 不能以零 cost 代替内存/磁盘/Body 上限；
- winner 选择 MUST 为 Envelope、最大失败结果和 mandatory protocol writes 预留空间，不能选中一笔连失败 Receipt 都无法发布的交易；
- 控制消息优先级 MUST 高于 bulk fragment、历史查询和 snapshot；
- admission MUST 对 organization/account/IP/ledger 施加配额；
- 同槽未引用 sibling quarantine MUST 同时强制 v1 的 `4/65_536/67_108_864-byte` 硬上限；dependency fetch MUST 与其分账并按 authenticated author 预留工作份额，使一把 Byzantine DAG key 的无限 sibling/child ancestry 不能制造无限 durable 保存义务或饿死诚实 author；
- DAG 未决定深度、尚未进入认证 emitted set 的 DAG bytes、尚未满足真实 release/GC 条件的 retained referenceable Batch durable bytes、control DAG bytes 和 execution lag 触发带 hysteresis 的背压；Batch 首次被消费 MUST NOT 从保留 counter 扣减；
- payload high MUST 停止新 Batch、未锁 ACK 与 AvailabilityReference 但保留控制排空；control high 或 reserve 不足 MUST 进入 `CONTROL_STORAGE_PAUSE`，停止新 DAG/DA 签名且不得删除 Safety WAL；counter checksum/manifest 重算不一致 MUST 撤销 readiness；
- 磁盘达到软水位停止低优先级复制，达到硬水位停止 admission，但保留共识/证明/恢复通道；
- 不得用 peer score 减少 quorum 或单方面排除 validator；
- 密钥、payload、token、未脱敏交易和 KMS 错误细节不得进入日志。

## 8. 可观测性要求

至少暴露：

- `dag_round`、`authored_round`、`round_jump_fill_total`；
- 每 slot decision 与 `undecided_depth`、`stable_prefix_cursor`；
- authenticated emitted count/root、四个 backlog counters、backpressure mode 与 control reserve；
- `batch_ac_latency`、reconstruct/re-encode bytes 与失败原因；
- ordered/executed/attested/finalized height 及各阶段 lag；
- exact-access 覆盖率、parallel lane 利用率、MVCC conflict、authoritative re-execution、serial-lane 比例；
- 通用prefilter每occurrence-sponsor reserve/shared spend、scan/cheap reject、suffix invalid、`PREFILTER_SCAN_CAP`、`PREFILTER_VERIFY_CAP`、in-flight STARTED恢复和checkpoint重扫；Batch author仅作为独立source binding诊断维度；
- sibling quarantine objects/bytes、dependency fetch/promotion、未闭合root age与audit-hint drop；
- FinalityCertificate 聚合延迟和冲突 attestation；
- WAL fsync、state commit、snapshot、compaction、disk watermarks；
- 网络每类队列、丢弃、重传和 peer 限速。
- unreferenced-sibling quarantine entries/bytes/eviction、按 author dependency promotion pending/staging bytes，以及 late-reference refetch 成功/失败。

告警不得把单一节点的本地 height 当作全网事实；必须携带 ledger/epoch/config hash。

## 9. 测试与形式化门槛

### 9.1 必需模型

1. FinalDAG-C slot direct/indirect commit/skip 与稳定前缀模型；
2. restricted round-jump 的机械化/可执行规格映射和 liveness 反例；
3. DAG equivocation、丢失父边、重排和 epoch 边界模型；
4. BatchAC 持久化/恢复模型；
5. ordered -> executed -> attested -> finalized 两阶段状态模型；
6. 并行执行与串行 oracle 的 refinement/differential model；
7. WAL crash points 和不双签模型。
8. automatic rollover reservation/intent/fence、snapshot install marker 与 mid-epoch derivation recovery 模型。

### 9.2 必需测试

- 规范编码 golden vectors 与跨实现互操作；
- 属性测试和 Fuzz：所有对象、proof、parent graph、snapshot；
- Byzantine：双顶点、错误 BatchAC、选择性父边、冲突 attestation、祖先放大；
- sibling admission：未引用洪泛在三重 `MAX/MAX+1` 下资源有界，最小 evidence pair 与到达顺序无关；对象先被驱逐、后被 honest 引用时可按 ID 恢复并提升，闭包完成前零 support/commit，完成后与先引用路径的 ordered root 一致；
- 网络：GST 前任意调度、分区、乱序、重复、恢复、慢 peer；
- crash：每个持久化边界前后 kill -9；
- differential：并行执行与串行 oracle 每个根/收据/事件一致；
- account：Genesis 三元组全量验证、`ACCOUNT_CREATE_V1` 自证/空 scope/system gas、同块 created set 和 meta/auth/nonce 原子崩溃矩阵；
- 长稳：饱和负载、epoch 切换、裁剪、compaction、snapshot 同时发生；
- recovery：同 target Snapshot+DAGDerivationCheckpoint、query-only→full 升级、marker 后 pointer 前崩溃、emitted set 错根/少一叶及 closing reservation 各阶段；
- backpressure：每个 low/high 边界、已消费但仍可引用 Batch 累计、counter checksum 损坏和 control pause 期间最终性/close 继续；
- API negative：`ORDER_FINAL`、`EXECUTED_LOCAL` 或 `FINALITY_CERTIFIED` 不能伪装为稳定 `FINALIZED_*` 终态。

### 9.3 发布门槛

任何 FinalDAG-C 规则变化、round-jump 变化、排序变化或 attestation schema 变化都要求：ADR、协议版本、模型更新、跨版本拒绝/兼容测试、长稳和 Byzantine 复测，并只在新 epoch 激活。

## 10. 不变量追踪矩阵

| 不变量 | 主要实现边界 | 主要验证 |
|---|---|---|
| 单轮单顶点 | DAG Safety WAL + signer | 并发/崩溃/双实例测试 |
| BatchAC 可恢复 | availability + storage | 编码差分、节点丢失、恶意分片 |
| slot 一致与稳定前缀 | FinalDAG-C decider | 模型检查、随机 DAG 差分 |
| restricted round-jump | round manager + recovery | S&P 反例回归、Chaos |
| sibling 有界接纳 | DAG quarantine + dependency store/fetch scheduler | 无限双签洪泛、晚引用恢复、到达顺序差分 |
| 串行等价 | executor + serial oracle | 每交易 differential/property |
| 执行不双签 | execution WAL + signer | crash matrix、冲突摘要 |
| 外部最终性 | proof verifier/API | negative proof corpus |
| epoch 隔离 | epoch manager/domain hash | replay、混合配置、升级测试 |
| 跨账本唯一消费 | source-proof verifier + occurrence filter + SMT | root substitution、并发 relayer、epoch/policy、crash/prune 矩阵 |

## 11. 相关文档

- [系统架构](01-system-architecture.md)
- [FinalDAG-C 共识](protocol/03-finaldag-consensus.md)
- [最终性、执行与纪元](protocol/04-finality-execution-and-epochs.md)
- [跨账本异步消息](protocol/06-cross-ledger-async-messaging.md)
- [测试、发布与性能](engineering/05-testing-release-and-performance.md)
