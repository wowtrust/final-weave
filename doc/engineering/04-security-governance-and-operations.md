# FinalWeave 安全、治理与运维规范

> 状态：设计基线（Draft）
>
> 适用范围：FinalWeave v1

## 1. 安全目标

FinalWeave 必须在至多 `f` 个 Byzantine Validator、任意消息延迟/重排/重复、恶意客户端、恶意同步来源和单节点存储故障下保持：

- 两个诚实节点不接受冲突的 stable slot prefix；
- 同一高度不能形成两份冲突的合法 `FinalityCertificate`；
- 未取得 `BatchAC` 的数据不进入合法 Vertex 引用；
- 状态、Receipt 与 Event 只由规范排序和确定性执行产生；
- 节点崩溃重启后不对同一唯一 slot 签不同 digest；
- 治理不能绕过密码学最终性，密码学最终性也不能绕过治理授权。

不可承诺的是在网络长期分区、少于 q 个诚实可通信 Validator、所有可用磁盘损坏或治理密钥整体失陷时继续推进。此时必须停止进展并保存证据。

## 2. 信任与威胁模型

攻击者可能：

- 提交畸形、超大、动态高冲突或高成本交易；
- 发送错误 fragment、伪造 ACK/AC、隐藏 Batch body；
- 在同 `(epoch,round,author)` 发布两个 Vertex；
- 构造非法 round jump、父 author 重复或缺失因果依赖；
- 尝试让 support 从一个 slot 候选切换到冲突候选；
- 分别向节点展示会导致 direct/skip 分歧的 DAG 片段；
- 对同一高度签署不同执行根；
- 投毒 snapshot、Header、状态证明或 validator-set transition；
- 利用磁盘回滚、KMS 重试、虚拟机快照恢复制造双签；
- 攻击管理 API、CI/CD、依赖供应链和审计系统。

协议不信任系统时间、peer 多数、数据库索引、日志、缓存和运维人员的口头确认。

## 3. 强制安全不变量

| 编号 | 不变量 |
|---|---|
| SEC-01 | DA ACK 只能在完整 codeword 验证、`CODEWORD_VERIFIED` 和固定 fragment durable 后签署 |
| SEC-02 | 同一 Vertex slot 只能签一个语义 digest |
| SEC-03 | 同一 Validator/height 只能签一个 ExecutionAttestation digest |
| SEC-04 | direct 与 skip 对同一 slot 不得同时成立 |
| SEC-05 | stable slot prefix 只能前缀扩展，不得改写 |
| SEC-06 | unrestricted round jump 不得进入有效 DAG；只接受 S&P26 restricted jump |
| SEC-07 | `P` 必须满足 `1 <= P <= q`，v1 默认 `P=2` |
| SEC-08 | FinalityCertificate 必须包含恰好 q 个不同 Validator 对同一 FinalityStatement 的 Ed25519 Consensus Key 签名 |
| SEC-09 | 未持久化 PreparedExecution 和 attestation intent 时不得签执行根 |
| SEC-10 | 治理生效必须同时满足链下组织授权与带 FinalityProof 的链上结果 |
| SEC-11 | 同一 Vertex slot 中所有被共识对象按 ID 引用的冲突 Vertex 必须递归提升并保留其 payload occurrence；未引用 gossip 只能占有界 quarantine，evidence 不得取代已提升 payload |
| SEC-12 | `EPOCH_CLOSED`、`EXECUTION_ATTESTATION_INTENT` 与 `EpochSealVoteLock` 必须共享同一 Consensus signer 串行锁域 |
| SEC-13 | 所有共识签名必须按 strict RFC 8032 pure Ed25519 profile 验证，batch 与逐笔验证的接受集必须相同 |

## 4. 密钥分层

| 密钥 | 用途 | 在线程度 |
|---|---|---|
| Peer key | P2P 双向 TLS 1.3 leaf + PeerHello 与 PeerID | 在线；不是 API TLS key；非 Validator 可按策略轮换，Validator 仅在 epoch 边界随 descriptor 轮换 |
| DAG key | BatchHeader、DA_ACK、DAGVertex | 在线、高频、受 slot store 保护 |
| Consensus key | ExecutionAttestation、EpochSealVote | 在线但隔离，最高协议安全级别 |
| Account key | 对 `tx_intent_hash` 的账户签名 | 客户端或账户托管边界 |
| Governance key | 组织批准、Validator 变更、升级授权 | 离线或强审批 HSM |
| Snapshot/Release key | 可选 artifact 来源认证 | 不赋予账本最终性 |

FinalityCertificate 使用 epoch `ValidatorSet` 中登记的 Consensus Ed25519 公钥。签名算法由 FinalWeave v1 协议固定；DAG/Consensus public key 进入对应 epoch 的 `ValidatorSet`，激活时点由 `ValidatorSet.epoch` 决定。Safety WAL 的 semantic key ID 必须按 `VALIDATOR_SIGNING_KEY_ID(role,network,ledger,validator_id,public_key)` 冻结公式重算；物理 key URI/provider generation 才是纯本地 KMS metadata。两者都不得伪装成共识 `ValidatorDescriptor` 字段或改变验签语义。不得在相同 epoch 静默替换公钥。

Peer、DAG、Consensus、Account 和 Governance key 必须是不同物理或逻辑 handle。DAG Key 不得签 ExecutionAttestation/EpochSealVote，Consensus Key 不得签 BatchHeader/DA_ACK/DAGVertex。通用 `Sign(bytes)` API 禁止用于 Validator 安全路径。

`ValidateValidatorSet` 只能发现同一集合中的公钥复用，不能证明另一 network、ledger或独立KMS租户没有指向同一物理secret。生产签名服务因此必须维护组织级、跨所有托管network/ledger/role的KMS inventory：以provider返回的不可变key-version identity、导出或attested SPKI fingerprint和用途登记组成唯一索引，禁止同一物理key version同时分配给Peer/DAG/Consensus/托管Account/Governance任意不同scope。readiness必须在持有inventory fencing lease时把本Ledger待启用semantic key ID与物理identity原子登记；冲突、provider无法证明版本身份、inventory陈旧或lease丢失都fail closed。协议domain能够阻止跨scope重放，但不能把密钥泄漏的共同故障域变成独立密钥，所以这项检查不能省略。

### 4.1 Strict Ed25519 验证 profile

所有协议签名只使用 RFC 8032 **pure Ed25519**，签名消息是完整 32-byte `DomainHash`；禁止 Ed25519ph、Ed25519ctx 和自定义 context。验证器必须在进入 quorum 计数之前执行全部检查：

1. public key `A` 和签名 `R` 均为 canonical compressed Edwards25519 encoding，y-coordinate 严格小于 `p=2^255-19`；
2. `A` 与 `R` 可解压、非 identity，且位于素数阶 `L` 子群；小阶点和含非零 torsion component 的点必须拒绝；
3. `S` 为 canonical little-endian scalar 且 `0 <= S < L`；
4. 验证未乘 cofactor 的 RFC 8032 方程 `[S]B = R + [SHA-512(R || A || message) mod L]A`；
5. 不得使用 ZIP-215 式宽松解码或仅依赖库的默认“verify=true”语义；
6. 批量验签只是优化，其接受/拒绝集必须与逐签名 strict 验证完全相同。

节点启动时用 RFC 正例以及 `S>=L`、non-canonical y、identity、小阶/torsion `A`/`R`、错误 domain 和错误 key-use 向量自检。无法明确强制该 profile 的密码库不得用于 Validator 或账户共识验签。

## 5. Signer 与 Safety Store

建议接口：

```text
SignBatchHeaderWithDAGKey(slot, digest)
SignDAAckWithDAGKey(slot, digest, durable_fragment_record)
SignVertexWithDAGKey(slot, digest, vertex_intent_record)
SignExecutionAttestationWithConsensusKey(slot, digest, durable_prepared_record)
SignEpochSealVoteWithConsensusKey(old_epoch, digest, epoch_closed_record, seal_vote_lock)
SignGovernanceApproval(proposal_id, digest, approval_context)
```

Signer 必须自己校验 domain、network、ledger、epoch、消息类型和 slot，不能只信调用方传入 digest。所有返回遵循：同 slot 同 digest 幂等；同 slot 异 digest 硬拒绝并产生 P0 告警。

对应 durable 记录：

- `CODEWORD_VERIFIED(batch_id)`；
- `DA_ACK_LOCK`；
- `VERTEX_SIGN_INTENT`；
- `EXECUTION_ATTESTATION_INTENT`；
- `EPOCH_CLOSED(old_epoch,final_height,final_block_id,closing_intent_hash)`；
- `EpochSealVoteLock`。

`EXECUTION_ATTESTATION_INTENT`、`EPOCH_CLOSED` 与 `EpochSealVoteLock` 不是三个独立的并发 map；它们必须由 Consensus signer 按 `(network_id,ledger_id,old_epoch,key_generation)` 在同一可恢复串行锁域内检查并写入。关闭记录存在后，新的旧 epoch attestation 只能满足 `height <= final_height`；若关闭前已有 `height > final_height` 的 intent 或签名，signer 必须拒绝写 `EPOCH_CLOSED`、拒绝签 seal 并进入 `SAFETY_HALT`。恢复时必须在开放 Consensus signing 前重放这一整个锁域，不得只恢复各自的最大 slot。

具体顺序见[存储规范](./02-storage-snapshot-and-pruning.md#4-safety-wal)。KMS/HSM 的审计日志是辅助证据，不替代本地 Safety WAL；本地 WAL 也不能伪造 HSM 已签结果。

虚拟机 snapshot restore、磁盘克隆和容器回滚可能恢复旧 slot store。Validator 数据盘不得制作可回滚运行快照；灾备恢复必须使用最新加密 WAL 备份并经过双人校验。无法证明 DAG 与 Consensus slot 状态完整时，旧 Validator protocol keys 必须治理撤销后才能重新加入。

## 6. FinalDAG-C 安全控制

### 6.1 Vertex 验证

签名前必须验证：

- 作者、epoch、round 和唯一 author slot；
- Batch 引用均有合法 `BatchAC`；
- 强父边来自足够多不同 author；
- 父 Vertex 已验证并形成所需因果闭包；
- round 关系满足协议的 S&P26 restricted round jump；
- support、direct、skip 和稳定前缀计算使用规范排序；
- Vertex bytes、parent list 与 attestation list 都满足上限。

验证成功且已被接受 Vertex、证书/witness 或 anchor 按精确 ID 引用的 Byzantine sibling 不得因唯一 author slot 而被覆盖。存储层的 dependency index 必须让一个 `(epoch,round,author)` 映射到所有已提升 `VertexID`，对每个被引用 sibling 保留完整签名 bytes 与 payload；闭包完整前不得 support/commit。纯旁路 gossip 只进入协议硬上限为每 slot 4 个、每 Ledger 65,536 个/67,108,864 canonical bytes 的 quarantine，满额可驱逐但不得永久判 invalid；晚引用必须按 ID 重拉并提升。每 slot 的本地 evidence cache 只保留已观察集合中 VertexID 最小的冲突 pair，受同一预算约束且不参与共识。只对完全相同 `VertexID` 幂等；对 dependency-store 中已提升分支不按 author、`BatchID` 或 `tx_id` 折叠 occurrence。

### 6.2 Sticky support

一旦诚实 Validator 的合法 Vertex 在某 slot 对候选形成 support，其后续可比较 Vertex 不得切换到同 slot 的冲突候选。重启后该约束从已签 Vertex 和 DAG 因果历史恢复，不能依赖易失缓存。

收到冲突父视图时宁可停在 undecided，也不能本地猜测。support 只来自协议定义的可达关系，不来自 peer 计数、到达时间或作者声誉。

### 6.3 Direct/skip 与 prefix

direct commit 和 skip 是互斥决定。实现必须让同一状态机函数同时返回 decision 与最小 witness，并在测试中对所有消息调度验证互斥性。stable prefix 只能包含已决定的连续 slot；后面 slot 已决定不能越过前面的 undecided hole。

### 6.4 Restricted round jump 与 P

round jump 只允许协议规定的证据和跨度，禁止仅因观察到高 round 对象就直接跃迁。`P` 控制协议定义的并行/前瞻范围：默认 2，链上取值范围 `1..q`。增大 P 可能改变 DAG 宽度、内存、带宽和决策延迟，必须经模型检查与容量基准后在 epoch 边界生效。

## 7. 数据可用性安全

v1 唯一可签 ACK 的编码 profile 是 `RS_GF256_V1`：`n=3f+1`、`k=f+1`、`m=2f`，GF(2^8) primitive polynomial 为 `0x11d`，字节使用 polynomial basis，`x_i=i+1`，`V[i,j]=x_i^j`，`G=V*inverse(V[0..k-1,0..k-1])`。G 前 k 行必须是 identity，generator id 必须精确为 ASCII `FW_RS_GF256_V1_VANDERMONDE_11D`。GF(2^8) profile 限制 `n<=253`、`f<=84`；超过必须新协议版本，不能换本地库参数。

canonical BatchBody 只允许在尾部补 `0x00`，`shard_length=ceil(body_length/k)`，fragment `0..k-1` 是连续 systematic data shards。ACK signer 必须从任意 k 个不同 index fragment 恢复 Batch，核对每个 index 与精确 shard length、`coding_context_hash`、规范 body/hash/交易根，确认裁掉的 padding 全为零，然后重编全部 n 个 fragments 并重建 fragment root。只验证单个 Merkle path 不足以防恶意作者分发不一致 codeword。

通过后的持久化顺序固定为：保存 signer 对应 index 的规范 fragment，写 `CODEWORD_VERIFIED(batch_id)`，写 `DA_ACK_LOCK`，fsync，再用 DAG Key 签 ACK。任一记录缺失、顺序颠倒或恢复后无法验证都必须撤销 `da_ready`。

AC 验证检查 q 个不同 signer、规范 bitmap、相同 Batch commitment、epoch validator set 和签名。AC 不证明交易有效、DAG support、顺序或最终执行。

## 8. 执行与最终性安全

节点只对完整 `FinalityStatement` 签署 attestation。签名前必须一致地重算：stable prefix 输入、父认证 meta/auth/nonce 完整账户三元组下的 canonical occurrence filter、完整 Body/results、Header 的 parent/slot/proposer/ordered-vertex/transaction/receipt/event/state roots、`parent_block_mmr_root`、validator/config hash，以及 Header hash 得到的 `FinalizedBlockID`。随后把当前块的 `BLOCK_MMR_LEAF` 追加父 MMR peaks，所得当前 `block_mmr_root` 与 state root 一起进入 statement；当前 MMR root 不得回填 Header。

并行执行必须与串行 oracle 等价；出现 root mismatch、MVCC 不变量错误、不可分类 runtime panic 或本地数据损坏时，节点撤销 `finality_ready`，不得把错误编码成业务 Receipt。

签名先写 `EXECUTION_ATTESTATION_INTENT`。证书收集器对 signer 去重，拒绝混合 `FinalityStatement`、epoch 或算法。证书 envelope 的 signer 子集和排列不得成为区块 id、随机种子或治理排序输入。

同高度两份冲突 q 证书意味着安全假设、密钥或实现已经失效，必须冻结 Ledger；禁止按高度、时间、peer 多数或较大 signer 数自动选一份。

## 9. 治理模型

治理动作分两层：

1. 组织策略授权：满足组织阈值、角色、提案窗口和审批审计；
2. 账本最终性：治理交易及其生效状态有可验证 `FinalityProof`。

任一层缺失都不能生效。链下管理员不能直接改 validator set、q、P、round-jump 规则、哈希/签名算法、执行版本、gas schedule、状态模型、ProtocolConfig 的 DAG/Batch GC 边界或协议永久保留材料。snapshot 周期和可选历史服务 SLA 属于非共识 `LocalHistoryPolicy`；管理员可以加强或延长它，但不能借它提前突破上述协议条件。

v1 使用 `LEDGER_RECONFIGURE_V1` 的完整 `ReconfigureLedgerPayloadV1`，不是未定义的配置 delta：action core 内嵌下一 epoch ValidatorSet/ProtocolConfig/FeatureSet/GasSchedule、target epoch 和可选迁移 hash，并由 immutable Genesis ledger GovernancePolicy 的 approvals 对 `GOVERNANCE_ACTION` ID 达到 threshold。当前 state machine 只允许空迁移。交易还必须由现有账户签名并消费其 nonce；完整 bundle 在执行前验证，但approvals、strict keys与registry验证只有在cheap window/policy-hash/exact-nonce/block-reserve通过、并成功扣取该occurrence的containing signed DAGVertex sponsor通用prefilter work后才能启动，Batch author只绑定来源，坏bundle消耗sponsor预算且不退款。同一 epoch 的 pending key 只允许按 `tx_index` 第一个 action 成功，后续得到 FAILED Receipt，不能由 DAG 作者或本地到达时间选择。Snapshot 是最终状态的异步恢复产物，不是治理 action 或 EpochSeal readiness 位；导出失败由运维重试/跨节点复制处理，不得卡住已认证 epoch 切换。

## 10. Epoch 与升级

以下变更只在 epoch 边界激活：

- validator set、DAG/Consensus/Peer key 与 PeerID；
- q、erasure coding 与 ACK 参数；
- FinalDAG-C P、restricted-jump 版本和 slot 规则；
- 编码、哈希、签名与 FinalityProof 版本；
- state machine、Feature registry 和 GasSchedule；未来 WASM runtime 只能随新协议版本纳入；
- ProtocolConfig 已冻结的 DAG/Batch GC 边界；snapshot 周期与可选历史查询 SLA 不在 epoch 共识配置内。

### 10.1 Closing frontier 与最后高度

`LEDGER_RECONFIGURE_V1` 的 SUCCESS Receipt、pending state 和所在 FinalizedBlock 都被同一 `FinalityCertificate` 认证后，可以触发治理提前关闭；FAILED、仅排序或仅本地执行的 action 都不能触发。协议还会在本 epoch 第 `EPOCH_FINALIZED_BLOCKS_MAX_V1` 个最终块，或 cumulative emitted count 首次跨过 `EPOCH_EMITTED_VERTEX_ROLLOVER_TRIGGER_V1` 的 committed candidate 上自动 rollover；若父状态无适用 pending action，next set 只递增 epoch且成员/keys/weights不变，config/Feature/Gas逐字节复用。自动触发按 committed candidate 而非有限 DAG round 尾巴，不能用本地 timeout 改写。

closing candidate `C` 是稳定 slot 前缀中第一个同时满足以下条件的 committed proposer：

- `Past(C)` 包含至少 q 个不同作者、`epoch_closing=true` 的 Vertex；
- 从上次已输出 slot 到 C 的所有更早 slot 都已 `COMMIT` 或 `SKIP`。

治理提前 C 使用上述 q-closing frontier；自动 C 则是确定性资源触发式命中的第一个 stable COMMIT，不等待 q closing Vertex。识别 C 不等待其执行证书。stable-prefix 派生器必须在与旧 epoch 高度分配器、Consensus signer 相同的串行锁域，先 fsync `EPOCH_CLOSING_RESERVATION(old_epoch,C.slot,C.proposer_vertex_id,candidate_height,derivation_candidate_digest)`，立即禁止 C+1，再允许 C 进入 `ORDER_FINAL` 和确定性执行。执行后读取 C 的 post-state：若 C 自身或父状态已有合法 target-epoch pending action则选择 `PENDING_RECONFIG`，否则选择 `SAME_CONFIG_ROLLOVER`；随后依次 fsync `EpochClosingIntentV1` 与 `EPOCH_CLOSING_FENCE(old_epoch,reservation_record_hash,closing_intent_hash,C.slot,C.proposer_vertex_id,candidate_height,derivation_candidate_digest)`，最后才允许 attestation intent/签名。C 之后不再派生新账本高度，只允许重传既有 Vertex、C 的 attestation 和 seal vote。

C 取得有效 `FinalityCertificate` 后，certified publication 在同一锁域验证 fence 并原子写入：

```text
EPOCH_CLOSED(old_epoch, final_height, final_block_id, closing_intent_hash)
```

### 10.2 共享 Consensus signer 锁域

写 reservation 时必须先查询该 key generation 的旧 epoch 高度分配和 `EXECUTION_ATTESTATION_INTENT`；正确流水线在此时不可能存在 `height > candidate_height`。若存在则立即 `SAFETY_HALT`，不能继续等待 C 获证。reservation durable 后禁止更高高度但只允许执行 C；final fence durable 后，`SignExecutionAttestationWithConsensusKey` 才允许完全匹配 C 的 `candidate_height`，拒绝更高高度。publication 只能把同一 reservation/intent/fence 原子提升为含相同 `closing_intent_hash` 的 `EPOCH_CLOSED`。关闭记录 durable 后继续执行相同上界。

崩溃恢复以 durable records 而非网络观察为准：有 reservation、无 intent/fence 时只能幂等重建并重执行同一 C，再从相同 post-state导出 intent；有 fence、无 `EPOCH_CLOSED` 时只能认证同一 C，不能撤销、重选或派生 C+1。如果 closing block 的 certificate 已持久化但 certified publication事务未提交，恢复器重跑同一原子 publication；如果 `EPOCH_CLOSED` 已提交，则恢复相同 intent/hash/高度上界。无法证明记录链、锁域完整性或 WAL 新鲜度时，`finality_ready=false` 且禁止该 Consensus key签名。

### 10.3 EpochSeal 与跨 epoch 验证

旧 Validator 只能通过 `SignEpochSealVoteWithConsensusKey` 签 seal。该 API 在共享锁域内按以下顺序执行：

1. 验证 closing frontier、最终状态，并取得完整 next `ValidatorSet`、`ProtocolConfig`、`FeatureSet` 和 `GasSchedule`；
2. 调用 `ValidateValidatorSet(next_validator_set,old_epoch+1,network_id)`、`ValidateProtocolConfigStructure(next_protocol_config,next_validator_set)` 与 `ValidateExecutionConfigBundle(next_protocol_config,next_feature_set,next_gas_schedule,next_validator_set)`；后者必须验证 typed Feature tuple、active Gas operation exact set/正系数和全部资源 cap 交叉约束。若激活 `CROSS_LEDGER_V1`，还要以 policy proof/signature caps与 GasSchedule计算 max-single proof-work，验证 `n * max_single <= block gas cap`，其中n是next ValidatorSet大小而不是proposer slot数P；再以当前 ledger 的已认证 network/ledger和同一 `next_protocol_config`调用 `ValidateCrossLedgerParametersForLedger`，检查 proof + relayer Envelope overhead可表示等 contextual规则，不能只做 network-scoped typed validation。以各自冻结的 domain/scope 重算 ValidatorSet、ProtocolConfig、FeatureSet、GasSchedule 四个内容 ID，要求执行对象 ID 与 next config 的两个引用逐字节一致；
3. 先调用 `ValidateEpochSealStatementIntrinsic` 重算 set/config ID 与 `next_epoch_seed`；再以 closing publication、`EPOCH_CLOSED`、closing intent、current bundle、optional governance action/approvals 和完整 next Feature/Gas bundle 调用 `ValidateEpochSealAuthorization`。pending-reconfig 分支验证 approvals，same-config rollover 分支拒绝伪造 action并验证 set 只递增 epoch、其余对象逐字节复用；两者都核对 final height/block/state/MMR 与 statement 完全一致；
4. 再次确认不存在更高的旧 epoch attestation intent/signature；
5. 写入唯一 `EpochSealVoteLock` 并 fsync；
6. 用旧 ValidatorSet 中该 validator 的 Consensus Key 签 `EpochSealVote`。

收集旧 ValidatorSet **恰好 q 个**不同、strict Ed25519 有效签名才形成 `EpochSealCertificate`；不得混用 old/new set signer。实际 genesis 启动也必须取得完整 epoch-0 四元组，调用 `ValidateValidatorSet(set,0,network_id)`、两个 config 谓词，重算并核对全部内容 ID 后才可原子安装；EpochSeal 签名同样不得缺失任一 next 对象。因此内容 ID 错配、typed feature 参数未登记、gas 越界或任一 config 谓词失败时，节点都不得激活 epoch，旧 signer 也不得签 seal。

公共 `FinalityProof.ValidatorSetProof` 则使用去重后的基础 schema：每个 `EpochTransitionProof` 只携带 seal certificate、next ValidatorSet 和 next ProtocolConfig；`ValidatorSetProof` 末尾只携带完整 `target_feature_set/target_gas_schedule`。验证者用当前旧 ValidatorSet 验证每跳 certificate，并以实际可得的 wire 对象调用 `ValidateEpochSealStatementIntrinsic` 重算 next set/config hash与 `next_epoch_seed`，然后原子更新“当前 set/config”验证状态；不得虚构本地 closed record/action 去调用 authorization 谓词。到达 Header epoch 后才重算目标两对象的内容 ID、核对目标 config 并调用 `ValidateExecutionConfigBundle`。全历史 replay需另行按每个已认证 config hash 流式取得中间 FeatureSet/GasSchedule；缺少中间对象会阻止 replay，但不推翻已验证的目标最终性。本地 `api.maxInclusionProofBytes` 只限制单个 Merkle/SMT/MMR/status inclusion response，不属于协议配置，也不约束可增量解码和缓存前缀的 epoch chain。

跨账本容量评审不能只看平均 proof。每个 policy的proof-byte/signature cap会决定`max_single`，而author安全保留固定占用`n * max_single`的配置空间；期望的突发吞吐还应另外留出shared proof-work余量。若一个多年Genesis链把max-single推得过高，应通过经治理审计的较新checkpoint policy缩短transition链，而不是关闭author保留、降低验签数或让本地节点偷偷接受更大预算。变更提案必须展示ValidatorSet大小n、proposer slot数P、各policy max-single、reserved total、shared remainder与基准机上的cache-cold CPU时间，以防再次混淆两者。

`CheckpointTrustAnchor/CheckpointFinalityProof` 是另一套冻结 Schema。安装 anchor 是扩大运营信任面的高权限动作：审批材料必须展示 canonical anchor bytes、`CHECKPOINT_TRUST_ANCHOR` ID、目标 block/state/MMR roots、完整 set/config/Feature/Gas bundle 和 MMR peaks；至少双人复核并写入只读 trust store。同步、查询、snapshot 和多数 peer 都无权修改该 ID。checkpoint verifier 必须同时匹配本地 expected ID 与 proof ID，从 anchor epoch 的旧集合开始连续验证，且不能在失败时回退到 Genesis verifier 或把基础 proof 重解释为 checkpoint proof。

在本节点执行真实 epoch activation 时，上述完整 next 对象验证成功后只能将 `(ValidatorSet,ProtocolConfig,FeatureSet,GasSchedule)` 作为一个不可分割的 epoch-activation generation 原子切换；崩溃恢复只能看到完整旧四元组或完整新四元组，任何新 set+旧 config/gas/feature 或旧 set+新执行配置都是 `SAFETY_HALT`。旧 epoch 的最终 checkpoint 因而由 `EpochSealCertificate` 和后续 `FinalityProof` 链锚定完整新配置。跨 epoch 对象严格拒绝；下一 epoch key 在激活前只做 readiness challenge，不能提前签协议对象。

### 10.4 升级流程

升级流程：开发与形式化验证，双实现/test vector，测试网 shadow/replay，组织审批，链上最终提案，epoch 激活，分阶段 readiness。二进制不支持将激活版本时必须提前退出 Validator set，不能在激活点临时降级规则。

## 11. 参数变更安全门

任何性能参数必须给出安全不变量、资源上界和回退条件。尤其：

- `P` 只能在 `1..q`；默认 2；
- q 必须由 validator set 和 `f` 推导，不能由本地配置降低；
- Batch/fragment/Vertex 上限须同时满足网络、内存和恢复预算；
- attestation 搭载上限不能饿死父引用和 DAG 活性；
- execution window、worker 和 cache 是本地优化，不能改变结果；
- 保留窗口不能短于未决 DAG、DA 恢复、快照回退和证明服务需求。

未经模型检查、故障测试和目标硬件实测，不批准“理论上更快”的生产参数。

## 12. RBAC 与管理面

建议权限：

- `node.observe`：指标和脱敏状态；
- `ledger.query`：业务查询；
- `validator.operate`：启动/停止，不可导出 key；
- `key.rotate.request` / `key.rotate.approve`：双人分离；
- `governance.propose` / `governance.approve`：职责分离；
- `snapshot.export` / `snapshot.import`；
- `incident.freeze`：只停止，不能制造最终状态。

管理面与 P2P/API 使用独立监听、证书和防火墙。生产禁止默认口令、共享管理员账号、在 URL query 放 token，以及从管理 API 直接修改 Safety WAL 或 finalized cursor。

## 13. 审计与隐私

必须审计：登录、权限变更、配置变更、key lifecycle、signer slot 冲突、治理审批、snapshot 导入、软件发布、Ledger freeze/unfreeze 和数据导出。

日志不得包含私钥、seed、完整 access token、未脱敏交易 payload 或可重放完整签名请求。共识对象按内容 hash 引用；调查时从受控对象库取原文。审计日志远程追加、WORM/签名封存并定期校验。

## 14. 供应链与发布

- 依赖锁定、SBOM、漏洞扫描和许可证审查；
- 可复现构建或至少双环境 hash 对比；
- 发布 artifact、容器和配置模板签名；
- 运行时验证 binary digest 与配置 hash；未来启用 WASM 时再强制验证其 engine/code hash；
- CI 密钥与生产治理/Validator key 完全隔离；
- 紧急补丁仍需最小双人审批、回放测试和审计记录。

## 15. Readiness

Validator 对每个 Ledger 分别暴露：

```text
network_ready
da_ready
dag_ready
execution_ready
finality_ready
storage_ready
```

只有相关前置条件都满足才能签名。启动流程必须先完成 strict Ed25519 自检，再重放 Safety WAL，并交叉检查 `CODEWORD_VERIFIED -> DA_ACK_LOCK`、`EXECUTION_ATTESTATION_INTENT`、`EPOCH_CLOSED` 和 `EpochSealVoteLock` 的先后与一致性。典型撤销条件：Safety WAL/fsync 失败、DAG 缺口超过窗口、stable prefix 分歧、执行根不一致、PreparedExecution 无法恢复、epoch-close 锁域不一致、KMS key generation 错误、磁盘高水位和系统时钟严重异常。时间异常不改变安全判断，但可能表示证书、审计和运维风险。

## 16. 运行指标与告警

至少监控：

- 当前 epoch/round、DAG round lag、undecided slot age；
- sticky-support conflict、direct/skip conflict、restricted-jump reject；
- BatchAC 形成时间、fragment 重建和 ACK fsync；
- stable-prefix 与 finalized-height 差距；
- execution prepare、attestation、certificate 和 state-commit 延迟；
- 通用prefilter每occurrence sponsor的reserved/shared消耗、`PREFILTER_SCAN_CAP`/`PREFILTER_VERIFY_CAP`、昂贵suffix耗时与checkpoint块首重扫；Batch author只作为source binding维度，不得作为扣款标签；
- unreferenced sibling quarantine三重水位、dependency promotion/fetch、闭包pending age和evidence hint丢弃；
- signer slot conflict、KMS error、WAL checksum；
- epoch closing frontier、`EPOCH_CLOSED` 高度、seal-vote lock、更高旧 epoch attestation reject 和 `SAFETY_HALT`；
- peer/organization 可达性、队列和带宽；
- root mismatch、snapshot verify、磁盘水位。

P0：冲突证书、prefix disagreement、direct/skip 同时成立、本地双签记录、同输入不同执行根。P1：长时间无最终性、q 可达性丢失、Safety WAL 或 KMS 不可用、磁盘临界。P2：单 peer 故障、索引滞后、快照生成失败。

## 17. 事故运行手册

### 17.1 最终性停滞

依次检查：q 跨组织连通、BatchAC、DAG 父缺口、restricted-jump reject、sticky support 分布、最老 undecided slot、执行积压、attestation 聚合和 state commit。禁止降低 q、放宽 round jump 或人工跳过 slot。

### 17.2 执行根分歧

立即停止 attestation，固定相同 BlockDerivationCandidate、父 state/MMR state、配置 hash 和二进制，逐 tx 比较串行 oracle、read/write set、Receipt 和中间 root；隔离有差异版本并保存最小 fixture。不得让少数节点接受多数 root。

### 17.3 同 slot 冲突签名

隔离 Validator 与 key，封存 Safety WAL/KMS 审计/原始对象，确认是存储回滚、并发 signer、克隆实例还是 key 泄露；治理撤销 key 前不得恢复签名。

### 17.4 冲突 FinalityCertificate

冻结 Ledger 的新 Batch、Vertex、attestation 和状态提交；最终查询固定在最后无争议高度。跨组织保全两份证书、validator set、WAL、binary/config hash 和网络证据。恢复只能通过独立治理和明确的新信任锚，不能由软件自动选链。

### 17.5 数据损坏

撤销 readiness，从已验证 `FinalityProof` 快照恢复，重放最终块并重算 roots。损坏节点不参与 ACK、Vertex 或 attestation，直至全链路验证通过。

## 18. 灾备

备份包括创世/信任锚、治理和 validator 配置链、Safety WAL 加密副本、最新快照、其 `FinalityProof`、快照后最终块与证书。恢复演练必须覆盖新主机、新网络地址、KMS 灾备和索引重建。

备份不等于可回滚 Validator。恢复旧 Safety WAL 时必须证明它包含该 key 的最新 slot；否则撤销旧 key 并以新 epoch 身份加入。

## 19. 验收清单

- [ ] DA、Vertex、attestation、EpochSealVote 使用分用途 signer API 和 durable slot。
- [ ] `RS_GF256_V1` 的有限域、generator、systematic/padding 字节布局被冻结，且 `CODEWORD_VERIFIED` 先于 `DA_ACK_LOCK` durable。
- [ ] Ed25519 严格执行 pure RFC 8032、canonical point/scalar、素数阶子群与 uncofactored equation，不接受 ZIP-215 宽松结果。
- [ ] FinalityCertificate 的 q、去重、epoch 和 FinalityStatement digest 全部验证。
- [ ] sticky support、direct/skip 互斥与 prefix-only extension 有运行时断言。
- [ ] 同 slot 所有被共识依赖引用的冲突 Vertex 都能从 cache 或网络按 ID 提升并保留 payload；无限未引用 sibling flood 受硬上限约束，evidence/quarantine 不会进入或替代 canonical occurrence。
- [ ] unrestricted round jump 全部拒绝，P 默认 2 且只允许 `1..q`。
- [ ] certified closing publication 原子写 `EPOCH_CLOSED`，并与 attestation intent/印章投票锁共享 Consensus signer 串行域。
- [ ] `EpochSealCertificate` 仅接受恰好 q 个旧 ValidatorSet signer，重启后仍拒绝关闭高度之上的旧 epoch attestation。
- [ ] Genesis/EpochSeal 的实际激活验证完整 `(ValidatorSet,ProtocolConfig,FeatureSet,GasSchedule)`，并以单一 generation 原子切换；恢复只允许完整旧四元组或完整新四元组。
- [ ] 公共 FinalityProof 的 transition 只携带 next set/config，目标 epoch 才携带并验证 FeatureSet/GasSchedule；缺失中间执行配置只阻断历史重放，不推翻目标最终性。
- [ ] 治理授权与 `FinalityProof` 分别验证。
- [ ] Safety WAL 丢失或回滚时 Validator 不自动恢复签名。
- [ ] 冲突证书、执行根分歧和最终性停滞均有演练记录。
- [ ] 管理面、审计、供应链和密钥职责分离通过评审。
