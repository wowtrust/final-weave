# FinalWeave 测试、发布与性能规范

> 状态：设计基线（Draft）
>
> 适用范围：FinalWeave v1

## 1. 原则

FinalWeave 的测试目标不是“跑出块”，而是同时证明：任何允许的消息调度和崩溃点不产生冲突最终结果；网络恢复且 q 个诚实 Validator 可通信时系统最终继续；相同规范输入在所有平台产生相同字节结果；性能结论可以复现且不隐藏瓶颈。

形式化模型、单元/属性测试、fuzz、差分测试、多进程故障注入、目标硬件基准和长期 soak 缺一不可。任何性能优化必须先有串行语义 oracle 和安全回归测试。

## 2. 测试层次

| 层次 | 主要目的 |
|---|---|
| Schema/test vector | 固定编码、hash、签名、Merkle/SMT 和 proof 字节 |
| 单元与属性 | 覆盖纯状态机、边界和代数不变量 |
| Model checking | 穷举小规模 DAG、消息顺序和故障，检查 safety/liveness |
| Fuzz | 畸形字节、资源边界、状态机动作序列 |
| 差分 | 并行/串行、不同实现、不同平台逐字节比较 |
| 多进程集成 | 真实网络、磁盘、KMS、重启和升级 |
| Byzantine/chaos | 主动构造冲突对象、分区、丢包、腐败和资源耗尽 |
| Benchmark/soak | 容量、尾延迟、退化曲线、泄漏和长期稳定性 |

测试随机种子、二进制摘要、配置 hash、拓扑和故障时间线必须随结果保存。

## 3. 编码与密码学

每个规范对象提供 golden bytes、domain-separated digest、签名和负向向量。至少覆盖：`PeerIdentityCore`、`PeerHelloCoreV1`、`PeerHelloV1`、`ValidatorDescriptor`、`ValidatorSet`、`AccountAddressCore`、`AccountMetadataState`、`AccountAuthState`、`AccountNonceState`、`CreateAccountPayloadV1`、TransactionIntent、含完整 SignerPolicy 的 TransactionEnvelope、BatchHeader、fragment commitment、DA_ACK、BatchAC、含/不含 `DAGRejoinCheckpointRef` 的 DAGVertex、`FeatureEntry`、`FeatureSet`、`GasCostEntry`、`GasSchedule`、`CausalInputChunkCore`、`CausalInputChunk`、`CausalInputManifestCore`、`CausalInputManifest`、含 epoch emitted count/root 的 FinalizedBlockHeader、FinalityStatement、ExecutionAttestation、FinalityCertificate、`EpochSealStatement/EpochSealVote/EpochSealCertificate`、`EpochTransitionProof`、`ValidatorSetProof`、`FinalityProof`、`CheckpointTrustAnchor`、`CheckpointFinalityProof`、`TransactionStatusEvidence`、`CheckpointTransactionStatusEvidence`、全部 CrossLedger policy/message/source-position/proof-envelope/consumed-state schema、`MerkleProof`、`SparseMerkleProof`、`BlockMMRProof`、`DAGCommitWitness`、`SnapshotChunkCore`、`SnapshotChunk`、含 emitted target 的 `SnapshotManifestCore/SnapshotManifest`、`DAGDerivationCheckpointChunkCore/Chunk/ManifestCore/Manifest`、Receipt 和 Event。

负向向量包括非最短整数、重复/乱序字段、unknown field、SignerPolicy hash/threshold/weight 错误、`gas_limit/fee_limit/priority_class/payload_type/authorized_access_scope` 任一签名字段被改写、重复 signer、非规范 bitmap、错误 network/ledger/epoch、截断 hash、错误 domain、错误 key generation、错位 Merkle index 和空树边界。v1 必须把任意 `fee_limit!=0` 或 `priority_class!=0` 判为 `STATIC_INVALID`，不能把 priority 当本地 hint 接受。还必须验证 DAG Key 不能签 attestation、Consensus Key 不能签 Batch/ACK/Vertex、Peer Key 不能签任何链上协议对象，并且只有 Consensus Key 能签 `EpochSealVote`。

Governance corpus 覆盖 policy signer count `1/1_024/1_025`、policy canonical bytes 恰为 `262_144`/多 1、Genesis approval count `0/1/1_024/1_025`、approval 多于 policy signer，以及 GenesisCertificate canonical bytes 恰为 `1_048_576`/多 1；所有超限必须在按数组声明分配、重算全部 key 或验签前拒绝。`LEDGER_RECONFIGURE_V1` approvals 同样受 `min(policy signer count,1_024)` 和完整 transaction/payload byte cap 约束。

Peer identity corpus 必须固定 `PEER_ID` 的 network-scoped 32-byte 派生、raw Ed25519 key 与 TLS leaf SPKI 提取，并证明 network/key 任一变化都会改变 PeerID，base58/multibase/libp2p multihash、证书 DER hash、截断值和自报文本都拒绝。`ValidateValidatorSet` 覆盖 PeerID 重算、PeerID 重复、DAG/Consensus/Peer key 在全集合跨角色复用和 strict-key 负例。PeerHello golden 必须只有规范的 7-field core/3-field envelope，不携应用 nonce；覆盖 TLS 1.3 exporter label/空 context/32-byte 输出、network、PeerID、精确版本 `[1]`、精确压缩 `[NONE]`、frame min/max/effective-min 算法、跨 frame 的 U64BE length-delimited 增量解码和签名。TLS 1.2、缺任一方向 certificate、缺失/错误 ALPN `finalweave-p2p/1`、旧连接 hello 重放、错 exporter、额外/unknown field、额外/未知版本或压缩、hello 的 PeerID/签名与 leaf raw key 不匹配、ValidatorID 自报冒充均不得建立应用连接或取得 Validator 控制流权限；恢复会话必须产生新 exporter 并重跑 hello，无法绑定原 leaf SPKI 的实现必须关闭 resumption。QUIC/TLS 与 TCP/TLS 对同一向量必须给出相同应用身份和授权。

KMS inventory corpus必须跨两个network、两个ledger和Peer/DAG/Consensus/托管Account/Governance角色生成alias、不同URI但同provider key version、同SPKI不同handle、不同SPKI、key-version轮换和并发readiness登记。任何解析到同一物理secret的跨scope复用都必须在开放signer前失败；inventory服务不可达、版本回退、provider无法给出不可变version identity、SPKI与已认证descriptor不匹配或fencing lease丢失同样撤销readiness。仅因domain不同能防重放不得视为通过；相反，不同物理key但恰有相似alias不得误拒绝。

逐消息授权 corpus 必须证明连接上下文只保存 `(network_id,peer_id,peer_public_key)`：在同一 TLS 连接和同一 stream 连续发送跨 Ledger、跨 epoch、跨 protocol id 的消息，每条都用 `(ledger_id,message_epoch,validator_set_hash,peer_id,protocol_id)` 完整 cache key 查当前已认证 set。覆盖“一个 Ledger 是 Validator、另一个不是”、epoch 激活后 Peer 被移除/Peer key 改变、旧 cache entry、消息若携 set hash 则与已认证 set 不匹配或企图用自报 hash 选 set、在 `dag/vertex` stream 投递 `ack` 形状、旧 epoch repair 允许/禁止和对象签名错误；任何失败都必须发生在 P0/P1 入队、Validator 配额和大对象 fetch 前。cache hit 不能绕过 active generation 检查，epoch 四元组切换必须原子失效旧 capability，而通过 pre-auth 的对象仍须完成独立签名验证。

`schema_version` 是共识语义，不是解析器装饰。对 `AccountAddressCore/MetadataState/AuthState/NonceState`、`CreateAccountPayloadV1`、`FeatureSet`、`GasSchedule`、全部 CrossLedger schema、`CausalInputChunkCore/Chunk/ManifestCore/Manifest`、`SnapshotChunkCore/Chunk/ManifestCore/Manifest`、`FinalityStatement`、`ExecutionAttestation`、`FinalityCertificate`、`EpochSealStatement/EpochSealVote/EpochSealCertificate`、`EpochTransitionProof`、`ValidatorSetProof`、`FinalityProof`、`CheckpointTrustAnchor`、`CheckpointFinalityProof`、`MerkleProof`、`SparseMerkleProof`、`BlockMMRProof` 和 `DAGCommitWitness` 分别生成正确、缺失、错误和未知版本向量。解码器必须在验签/计 quorum 或写 staging 前拒绝后三者，且不能因版本错误而跳过 network/ledger/domain 检查或降级为其他 schema。

本地storage schema另维护27-field`CausalValidationRecordV1`、`DerivationStateGenerationV1`、`SafetyRecordCoreV1/SafetyRecordV1`、`GenerationChecksumEntryV1`、`BatchRetentionManifestV1/BatchGCRecordV1` canonical bytes与全部checksum/hash golden。改变任一不可变逻辑value、manifest/count/root、derivation generation或FC byte必须改变相应checksum；只改变SST/page布局、compaction或物理压缩不得改变core generation checksum。Safety WAL覆盖kind 1..16、每种typed slot/payload、sequence 1/ZERO链首、连续previous hash、bit flip、重复/跳sequence、尾frame与同slot异digest；特别覆盖`BATCH_AUTHOR_INTENT`与AUTHOR_BODY manifest同组durable、DA_ACK_LOCK与ACK_FRAGMENT manifest同组durable，任一manifest前签名都失败。record checksum/hash公式必须无自引用，signer/HSM audit不能证明安全时禁止自动截断。

三类 certificate envelope hash 必须固定完整 canonical certificate preimage：`AC_ENVELOPE(AvailabilityCertificate)`、`FINALITY_CERT_ENVELOPE(FinalityCertificate)`、`EPOCH_SEAL_ENVELOPE(EpochSealCertificate)`。为同一 statement 构造两个不同但合法的 q-signer subset，断言语义 ID 相同而 envelope hash 不同；把 envelope hash 用作 BatchID、FinalizedBlockID、MMR leaf、epoch seed、DAG parent 或查询身份的实现必须失败。

Feature/Gas corpus 必须冻结四个对象的字段顺序、canonical bytes，以及用 `(network_id,ZERO_ID)` 和 `FEATURE_SET/GAS_SCHEDULE` domain 得到的内容 ID；改变 network、任一字段或一个 parameter byte 必须改变相应 ID，使用真实 LedgerID、错误 domain 或只接受调用者自报 hash 必须失败。Feature entries 只接受按 `feature_id` 严格升序且 ID 唯一：乱序、重复，以及同一 ID 并存不同 `feature_version` 都拒绝，不能实现“最高版本获胜”。`parameters_cbor` 必须恰为一个 RFC 8949 deterministic CBOR item，并通过当前 `(protocol_version,state_machine_version,feature_id,feature_version,parameter_schema_version)` 登记的 typed schema；覆盖非规范编码、尾随第二个 item/垃圾字节、未知 tuple、JSON、空 byte string、错误类型和该 schema 的规范空对象。边界向量必须覆盖 0/1/256/257 个 entry、单项参数恰为 64 KiB 与多 1 byte、完整 FeatureSet 恰为 1 MiB 与多 1 byte，并对所有长度求和执行 checked arithmetic。

Gas entries 只接受按 `operation_id` 严格升序且 ID 唯一，并必须与当前 `(protocol_version,state_machine_version,FeatureSet)` 的 active operation registry 完全相等。空表、缺任一固有/payload operation、额外/未知/未激活 KV/跨账本 operation、乱序、重复和必需系数为零都拒绝；同时覆盖 65,536/65,537 项及完整 GasSchedule 恰为 4 MiB/多 1 byte。逐项固定 `TX_BASE`、账户签名、READ/WRITE、EVENT、RETURN、host crypto、七个 payload operation与两个跨账本 proof operation 的 input/output byte metric和调用顺序。FeatureSet 空集时 exact set 为 8 个固有 operation + ACCOUNT_CREATE/ACCOUNT_POLICY_ROTATE/LEDGER_RECONFIGURE；`NATIVE_KV_V1{allow_delete=false|true}` 条件增加 KV；`CROSS_LEDGER_V1` 必须同时增加 SEND/CONSUME/PROOF_VERIFY/SOURCE_SIGNATURE_VERIFY。覆盖空集、各自单独激活与二者同时激活的全部 exact sets。

配置规范中的完整 `n=4` Genesis 示例必须作为正向 fixture 逐字段解析，并通过 `ValidateValidatorSet`、`ValidateProtocolConfigStructure` 与匹配 bundle 的 `ValidateExecutionConfigBundle`；尤其验证 `max_state_read_bytes_per_tx=67_108_864` 足以覆盖 `MaxRequiredNativeReadBytesV1`，任何示例值因交叉约束而无效都视为文档/发布门禁失败。

每个 ProtocolConfig hard cap 都生成 `0/min/min-1/max/max+1`（适用处）向量：两个 pacemaker timeout、schedule lookahead、`dag_gc_rounds`（1 无法形成合法 own/weak gap，2 为最小）、Batch retention、execution lag、block tx/Gas、两项 prefilter verification-work、authorized access entries/bytes、observed keys、execution parallelism、MVCC versions/bytes、dependency edges 和 tx-index prefix。另覆盖 certificate timeout 小于 primary、retention 不大于 execution lag、prefix 超过 block tx、`n*prefilter_reserve>prefilter_total`、reserve 比 `MaxSinglePrefilterVerificationWorkV1` 少 1，以及所有 checked add/mul overflow。任何 MAX+1 都必须在 epoch activation/大分配/worker启动前由结构谓词拒绝，不能依赖节点本地资源报错；示例必须落在全部边界内。

对 remaining-budget `base + rate*units` 算法覆盖 cost 恰等于 remaining、差 1 OOG、`rate/units=0/1/UINT64_MAX` 和运行时乘加若直接计算会溢出的输入；规范结果必须在副作用前 OOG 且 `gas_used=gas_limit`，不能 wrap、饱和或因平台整数宽度改变。固定七个 payload 的 success/登记 failure 完整 GasEvent trace，并对同一 operation 同时越权、resource exceeded 和 Gas 不足的组合断言 `ACCESS_SCOPE_VIOLATION > STATE_LIMIT_EXCEEDED > OUT_OF_GAS`；前两者拒绝的 operation 不得进 Gas trace。普通 winner 的 `COMMIT_NONCE_UNMETERED` 不产生 `STATE_WRITE` event，但成功、业务失败、revert、scope/resource violation 和 OOG 都必须提交它并输出 nonce StateChange；账户创建与两个 CrossLedger payload必须验证 required Gas/success reserve与执行逐字节相等，不能降级为 FAILED。非 OOG 失败保留失败点之前 Gas，OOG 取满；推测结果丢弃与 suffix serial fallback 只保留一次权威计量。`fee_limit=0` 是唯一正例，任意非零值 `STATIC_INVALID`，v1 Receipt 的 error code 7 无效且不得出现费用状态写。最后用正负 bundle 向量证明两个内容 ID 必须逐字节等于 `ProtocolConfig` 引用，且 `ValidateExecutionConfigBundle` 对缺对象、交换对象、hash 错配、typed parameter 失败、registry mismatch、零成本绕过和错误 ValidatorSet 均拒绝。

Causal/Snapshot/DAGDerivationCheckpoint golden corpus 必须同时固定 core/envelope canonical bytes、chunk core hash、带 index 的 leaf、奇数复制的每层 node、`chunks_root`、manifest core bytes 和 manifest ID，并证明 `manifest_id`/`siblings_bottom_up` 只出现在 chunk envelope，不反向进入 core/root/manifest ID 造成哈希环。Header 必须含 epoch emitted count/root，checkpoint manifest 含 target block ID但 Header 不含 checkpoint ID，静态依赖图不得成环。

FinalizedBlock Body/Header corpus 必须覆盖 schema version、`epoch/height/committed_slot/proposer_vertex_id` 四个重复字段逐项相等、Body/manifest `ordered_vertex_count` 相等，以及 ordered-vertex/transaction/receipt/event/state-change/state roots 全量重算。分别只改变 Body 的每个重复字段或 count 而保持其余数组/root 正确，也必须拒绝；不得让同一认证 Header 接受两个不同 canonical Body。另构造 canonical causal stream 大于 `max_finalized_block_body_bytes`、但筛选后只有空或小型 transaction/result 数组的 COMMIT slot，证明 ID sidecar 可流式验证而 Body 仍可表示，任何实现不得把 sidecar 字节计入 Body cap、截断 delta 或因该差异停链。

`AccountAddressCore` 向量必须证明：地址只由 `(network_id,ZERO_ID)`、`POLICY_V1`、`creation_salt` 和 initial policy hash 决定；改 salt 会改地址，policy 轮换不改地址，同地址可加入多 Ledger，但真实 LedgerID 变化必须使交易签名/状态证明不可重放。meta 中完整 initial SignerPolicy 必须重算出 core hash；普通交易只按 state-root 认证的 meta/auth/nonce 三元组授权，不要求 Envelope 携 core，也不能在缺 core 时发明另一种地址派生。

Ed25519 必须用独立实现或权威向量交叉验证。正例使用 RFC 8032 pure Ed25519 并对完整 32-byte `DomainHash` 签名；负例至少覆盖 Ed25519ph/ctx、`S>=L`、non-canonical y、identity、小阶点和带 torsion component 的 public key `A`/签名 `R`、不可解压点、错误 domain 与 key-use，并检查未乘 cofactor 的 RFC 8032 方程。ZIP-215 宽松接受向量在 FinalWeave 中必须拒绝。对同一含有多个边界签名的 corpus，batch verify 与逐笔 strict verify 必须产生逐项完全相同的接受/拒绝 bitmap；批量失败后的定位路径不得改变该集合。

## 4. 数据可用性测试

### 4.1 编码与恢复

- 冻结 `RS_GF256_V1` 参数：`n=3f+1`、`k=f+1`、`m=2f`、GF(2^8) primitive polynomial `0x11d`、polynomial-basis byte、`x_i=i+1`、`V[i,j]=x_i^j`、`G=V*inverse(first k rows)` 与 generator id ASCII `FW_RS_GF256_V1_VANDERMONDE_11D`；
- 对所有允许的 `n/k` 组合做任意 k fragment 恢复，特别覆盖最大 `n=253,f=84`；
- `n=254`、`f=85` 及任何不满足 `n=3f+1,k=f+1,m=2f` 的参数必须在编码/大内存分配前拒绝；
- 断言 G 前 k 行为 identity，fragments `0..k-1` 逐字节等于 canonical body 尾部补零后的连续 systematic shards；
- 覆盖 `body_length mod k` 为 `0/1/k-1`，检查 `shard_length=ceil(body_length/k)` 且裁掉的 padding 每字节都为 `0x00`；
- 少于 k 个严格失败；
- 交换 index、越界 index、错误 length、截断、重复、单字节污染、非零裁剪 padding 和错误 `coding_context_hash` 均拒绝；
- 从任意 k 个 index 恢复后重新编码全部 n 个 fragments，逐字节比较并重建相同 fragment root；
- 至少两个独立语言实现对每个 fixture 产生完全相同的 n 个 shard bytes，不只比较 root；
- `BATCH_BODY_SINGLE_TX_WRAPPER_BYTES_V1` 固定为 5；构造完整 canonical Envelope 恰为 `max_transaction_bytes` 的单交易 Batch，断言其 body 长度恰等于 `MaxSingleTxBatchCanonicalBytes`，配置少 1 byte 必须被 `ValidateProtocolConfigStructure` 拒绝；
- Header 声明 `transaction_count/body_length` 恰等于上限时可继续，任一多 1、zero count/body、checked shard-length 溢出都必须在 fragment fetch、RS matrix/shard buffer 分配前拒绝；fragment 声明长度不等于导出 shard length、Merkle path count 不等于由 `n` 导出的层数时在对应分配前拒绝；恢复后的实际完整 canonical body count/bytes 与 Header 不同也拒绝；
- BatchBody 总 bytes/count 均合法但其中一个 TransactionEnvelope 恰为 `max_transaction_bytes+1` 时，ACK 与 repair 必须在 transaction root、codeword marker和签名前拒绝；恰等于上限可继续。结构/字节合法但账户签名无效的交易仍可形成 DA、随后由 occurrence filter跳过，不能把 DA 静态边界误实现成账户状态执行；
- 最大 Batch 下 canonical 输入、内存与临时文件上限不越界；P2P v1 路径不得实例化解压器。

### 4.2 ACK 锁

- 未完整验证 codeword 不签；
- `CODEWORD_VERIFIED(batch_id)` 未 durable 不签，并且必须在 `DA_ACK_LOCK` 之前写入；
- 固定 fragment 未 durable 不签；
- 同 slot 同 digest 幂等；
- 同 slot 异 digest 硬拒绝；
- intent fsync、签名和发送之间每个崩溃点重启后不双签。

### 4.3 BatchAC

- `q-1` 个不同 signer 拒绝，q 个接受；
- 重复 signer 不计数；
- 混合 Batch commitment、epoch 或 validator set 拒绝；
- AC 形成后从任意允许的 signer/fragment 组合恢复 Batch；
- AC 不触发顺序或交易终态。

## 5. FinalDAG-C 纯状态机测试

### 5.1 Vertex

- 诚实 author 的 signer 在同 `(epoch,round,author)` 只能产生一个签名对象；验证器必须完整保存所有被接受 Vertex、证书/witness 或 anchor 按 ID 引用的 Byzantine sibling，但对未引用 gossip 只使用受三重绝对 cap 约束且不再传播的 quarantine；
- 父 author 去重、父数边界和 canonical 排序；
- 对每个合法 ValidatorSet 生成 `MinRequiredVertexCanonicalBytes(set)` golden；完整 signed q+1-parent 控制 Vertex 恰等于该边界时接受，`max_vertex_bytes` 少 1 的 ProtocolConfig 无效。接收方用 checked bounded reader 最多接受 `max_vertex_bytes` bytes、仅探测下一个 byte 判越界；AvailabilityReference 多 1 或完整 envelope 多 1 时必须在 parent/AC fetch 前拒绝，且不得依赖可回绕的 `max_vertex_bytes + 1` 算术；
- `execution_attestations` 与 `evidence_refs` 分别在固定 absolute MAX 接受、MAX+1 在 dependency fetch/验签前拒绝；构造完整 Vertex bytes 未超但数组 count超限，证明字节 cap 不能替代验签放大 cap。同 `(statement_id,signer)` 重复、同 signer/height冲突、statement network/ledger/epoch 与 outer Vertex不等分别拒绝计票；
- 对 transaction/signer policy/account signers/signatures/Batch bytes/Batch tx count/Vertex bytes/weak parents/Batch references/future round+nonce gap/validity window 的每个 v1 绝对上限生成 `MAX/MAX+1` config 与 wire 向量；config 超限在 epoch seal 前拒绝，wire 超限在数组/shard/parent/transaction 分配与 RS 重构前拒绝。默认 Genesis 示例必须低于全部绝对上限；Validator readiness 还要证明本地 spill/worker/disk 预算能最终处理链上允许范围；
- 缺父进入 orphan，不产生 support；
- resolved own/strong/weak parent、BatchHeader/AC、attestation 的 network/ledger/epoch 必须逐项等于 outer Vertex；无 BatchAC、错误 AC、跨 epoch合法证书混装均拒绝。evidence ref 在 Vertex关键路径只验固定Hash32/排序，1,024个随机或缺失ref不得触发同步fetch或影响support；异步取得的evidence上下文错只丢hint，不反向使Vertex无效；
- 普通 own/weak gap `dag_gc_rounds-1` 接受、等于窗口拒绝。rejoin fixture 用同 epoch certified Header 的 256 层 emitted membership与 non-membership各一例；错 target finality/root/path、recent cursor gap越界、round 1携 rejoin、own parent与 rejoin同时有/同时无、未 fsync `VERTEX_REJOIN_INTENT` 都拒绝。n=4 中 D长期离线、C随后故障、A/B/D恰为q时，D经认证 rejoin 后必须恢复 control DAG；旧 branch不得再签；
- sibling flood 构造同一 DAG key 在同 slot 生成远超 `4` 个、总对象 `65_536` 个和 `67_108_864` bytes 的有效 Vertex；断言 quarantine 在 `MAX` 有界、`MAX+1` 不增加内存/磁盘/传播，未引用对象不进入 support/decision/Past，peer断连与驱逐不写永久 invalid；
- evidence cache 对同一已观察 sibling 集的全部到达排列始终选 VertexID 最小 pair；cache pair 可驱逐且不改变 slot decision。`DAGEquivocationEvidence` 在 active `5+2*max_vertex_bytes` 与全局 `33_554_437` bytes 边界做 exact/+1、嵌套 Vertex 先越界测试；
- 以 `EVIDENCE_REFS_PER_VERTEX_MAX_V1=1_024` 个 ref 指向各约 32 MiB evidence，断言 P0 Vertex handler 下载/分配 full evidence bytes 为 0；异步 worker 总 bytes/objects 始终受共享 quarantine cap，错误/缺失 ref 不改变 VertexID、support 或 ordered root；
- 先让 sibling 被 quarantine 后驱逐，再由已接纳 honest Vertex/已验证 witness 精确引用：节点必须从引用来源或另一 peer 按 ID 重取、fsync 提升其完整递归 closure，且 promotion 不受 quarantine cap 阻挡；取回前 support/commit/attestation 均为零，取回后结果与“先引用后到达”相同；
- 在 staging object fsync、edge-closed append、closure-verified、dependency pins/root-installed transaction及quarantine清理前后逐点kill；恢复只能续拉/幂等安装/安全回收未安装staging，绝不能出现closed edge缺对象、INSTALLED对象被cache eviction删除或半闭包进入decider；
- Byzantine child/sibling 递归 ancestry flood 只能耗尽其 author dependency-work 份额；诚实 author 保留 lane 仍闭合并推进。提升对象不可降回 quarantine，GC 前存在 undecided/candidate/recovery 引用时不得删除；
- `Past(P)` 中所有已提升的不同 `VertexID`——包括 committed proposer 和同 slot referenced siblings——均按 `(round,author_index,vertex_id)` 进入 canonical traversal；
- 每个精确 `VertexID` 在全局 ordered output 中恰好发出一次，即使它可从多个 committed proposer 的 past 达到；
- 相同 `BatchID` 和 `tx_id` 的所有 raw occurrence 都进入 occurrence filter，禁止在 Vertex/Batch 展开层预去重；
- 对同一最终可用依赖集合，先 gossip/后引用、先引用/后响应、quarantine 驱逐后重拉及全部 sibling 到达排列产生相同因果闭包、ordered roots 与 winner/filter 结果；本地 evidence cache 内容不属于共识结果。

### 5.2 Canonical causal input stream

- `CausalInputItem` variant 只接受 `VERTEX=0/OCCURRENCE=1`；每个 Vertex 先输出连续 ordinal 的 Vertex item，再按 AvailabilityReference/Batch transaction 顺序输出全部 occurrence，并逐字节核对 DAG/Batch 来源索引与 envelope；
- 对 `U64BE(len(canonical(item))) || canonical(item)` 做单 item、多 item、长度前缀跨 chunk、item 跨 2 个和多个 chunk 向量；残缺前缀、长度过小/过大、尾部多字节、非规范 CBOR 和按未验长度预分配均失败；
- `chunk_payload_bytes` 只接受 `1_048_576`，除末块外每块恰为 1 MiB，index 和 `first_byte_offset` 从 0 连续；COMMIT 空 stream、错 `item_count`、checked-sum 溢出、错 `stream_byte_length/chunk_count` 均拒绝；
- 对 `CausalInputChunk` 逐块重算 core hash/indexed leaf，路径长度必须由 `chunk_count` 唯一决定且最长 64，左右由逐层 index bit 推导，奇数复制层 sibling 必须等于当前 hash；路径少/多一项、错方向、错复制值、错 root 均不得写 staging；
- P2P v1 原始 canonical envelope 精确为 `1_050_823` bytes 可进入规范解码，`1_050_824` bytes 必须由 bounded reader 在 payload/sibling/CBOR 分配前拒绝，且测试桩断言没有 compressor/decompressor 被创建或调用；HTTP Gateway/未来压缩 profile 另以流式输出计数做相同 `MAX+1` 边界，高压缩比不得绕过 `CAUSAL_INPUT_CHUNK_MAX_CANONICAL_BYTES_V1`；
- 缺 chunk、重排、重复、截断、尾部追加、跨 manifest 替换、错 manifest ID 和同 derivation candidate 出现不同 manifest 均有负向向量；
- 强制内存/磁盘高水位时只能减少 in-flight、暂停、spill 或换 peer，恢复后 ordered roots/winners 必须与无背压串行 oracle 逐字节相同。

### 5.3 Sticky support

构造同 slot 冲突候选 A/B，覆盖消息延迟、不同可见子图、作者 Byzantine 和重启。断言诚实 Validator 一旦通过已签 Vertex 支持 A，后续合法 Vertex 不支持 B；未达到条件时保持 undecided，而不是猜测。

### 5.4 Direct/skip 互斥

对每个 slot 同时运行 direct 和 skip 判定：

```text
not (Direct(slot, candidate) && Skip(slot))
```

测试最小 quorum 边界、重复 author、延迟父边、冲突 Vertex 和不同 P。任何同时为真立即失败并输出最小 DAG witness。

### 5.5 Stable prefix agreement

任意两个诚实节点输出的 stable slot 序列必须互为前缀；新输出只能扩展旧输出。后续 slot 已决定但前面有 hole 时不能越过。相同 stable prefix 派生的 canonical vertices、batches、occurrences 和 `BlockDerivationCandidate` 必须逐字节相同。

### 5.6 S&P26 restricted round jump

模型中同时保留两个版本：

1. 关闭限制的故意错误版本必须在预置 adversarial trace 上重现 prefix disagreement 或 sticky-support 破坏反例；
2. 正式 restricted 规则在相同及穷举 trace 上不得出现反例。

覆盖跳转证据缺失、跨度边界、伪造高 round、不同节点先见高 round 和 epoch 边界。该反例测试是永久回归门禁，不能因实现重构删除。

### 5.7 P 参数

至少测试 `P=1`、默认 `P=2` 和 `P=q`；配置拒绝 `P=0`、`P>q` 和 epoch 中途改变。比较安全状态空间、DAG 宽度、未决 slot、带宽和内存，但不能因性能差异放松规范判断。

## 6. 形式化模型

模型状态至少包含：validator set、round/slot、已收的全部 Vertex siblings、已签 Vertex、parent graph、Batch availability、support、direct/skip decision、stable prefix、BlockDerivationCandidate、ExecutionAttestation、`EXECUTION_ATTESTATION_INTENT`、`EPOCH_CLOSED`、`EpochSealVoteLock`、EpochSealCertificate 和最终证书集合。Consensus signer 的三类记录必须建模为同一串行转移系统，不能拆成无关状态。

Safety properties：

```text
OneVertexPerHonestAuthorRound
StickySupport
DirectSkipMutualExclusion
StablePrefixAgreement
StablePrefixMonotonic
RestrictedRoundJumpOnly
BlockDerivationCandidateAgreement
OneExecutionAttestationPerHonestHeight
NoConflictingFinalityCertificates
FinalizedParentContinuity
NoAttestationAboveEpochClose
OneEpochSealVotePerHonestOldEpoch
EpochSealUsesExactlyOldValidatorSetQuorum
EpochCloseRecoveryPreservesSignerBounds
```

Liveness properties只在明确假设下检查：网络进入及时阶段、至少 q 个正确 Validator 可通信、Batch 数据可恢复、调度公平、执行和磁盘最终完成。模型不得把本地 timeout 或某个固定作者诚实作为安全前提。

小模型至少覆盖 4/7 个 Validator、1～4 个 Byzantine、多个 round/slot、乱序/重复/丢失消息和崩溃恢复。状态缩减规则与 symmetry reduction 需审查，避免删掉关键攻击轨迹。

## 7. 排序与交易筛选

对相同 stable prefix 验证：

- Vertex、Batch 和 occurrence 的规范顺序；
- 高度窗口前后边界；
- `CanonicalAndStaticValid` 必须以完整 deterministic-CBOR bytes 计量：构造 `canonical(TransactionEnvelope)` 恰为 `max_transaction_bytes` 的接受向量和多 1 byte 的 `STATIC_INVALID` 向量；对完整 `canonical(SignerPolicy)` 同样覆盖恰为 `max_signer_policy_bytes` 与多 1 byte，且不得只计 payload/signers；
- 冻结`PrefilterScanWorkCostV1`、`PrefilterExpensiveWorkCostV1`与两者checked sum的跨语言golden：canonical item byte chunk边界、固定suffix base=1、每次strict Ed25519 public-key occurrence、target/account/governance signature occurrence和每类complex registry entry分别做0/1/MAX/MAX+1与checked overflow；三个field counter全0时suffix cost仍恰为1，任何真正创建attempt/调用worker的路径都不能零成本。重复的相同key/entry仍按出现次数计，cache hit/miss、批量/逐笔验签不得改变逻辑cost。固定field-path registry，证明`CROSS_LEDGER_CONSUME.proof_envelope`全部后代不进入三类common suffix counter，而完整item bytes仍恰付一次scan fee；
- 对每个 v1 payload的最大合法 sizing template重算 `MaxSinglePrefilterVerificationWorkV1(config)`，尤其覆盖 1,024 approvals、253成员 next ValidatorSet及FeatureSet/GasSchedule绝对entry上限。模板无需分配完整对象；用当前`n=4`、next set=253的重配置证明结果不依赖current set，reserve少1必须拒绝。任一 payload/Feature升级若没有版本化field-path与模板，bundle activation必须失败；
- 令 `P<n`，从`sponsor_author_index=0`、`P` 与 `n-1` 注入同成本候选，验证全部 n 个 authenticated containing Vertex occurrence sponsor各有独立 reserve，shared pool恰等于 `total-n*reserve`。f个Byzantine Vertex sponsor提交最大成本、外层合法但坏签名/坏approval/坏bundle的occurrence并耗尽自己的reserve与shared后，honest Vertex sponsor本地完整预验、公平引用的最大合法交易仍能使用其保留份额成为winner；归因只能来自已验证containing signed DAGVertex author，不能来自BatchHeader author、relayer、gossip peer或slot proposer；
- 增加cross-author重引用回归：f个Byzantine Vertex sponsor在多个Vertex中反复引用同一honest validator早先签发的最大Batch，且派生层不按BatchID或tx-id预去重。每个raw occurrence的scan/common/source receipt必须归给各自containing Vertex sponsor，honest Batch author的reserve保持不变；shared耗尽后，该honest validator作为新Vertex sponsor引用本地完整预验候选，仍可用自己的reserve成为winner；
- prefilter调度分别覆盖scan/suffix cost恰等于sponsor余量、跨sponsor+shared边界、总量差1、invalid不退款、成功不退款、cache命中不减免、`PREFILTER_SCAN_CAP`/`PREFILTER_VERIFY_CAP`不写suffix attempt map，以及同一完整Envelope先因cap失败、后由另一sponsor重试成功。同一`tx_id`一旦真正尝试，后续 occurrence复用同一 VALID/INVALID结果且不再次验签；因为`tx_id`承诺完整Envelope，改变SignerPolicy/signatures会得到不同key而不能污染合法结果；
- 使用decoder/tx-hash/SMT/crypto/allocator spy证明scan cap失败后只以固定chunk流式比对source，不解码Envelope、不计算tx ID、不读SMT、不调用任何签名/registry worker。构造巨型stale、duplicate和CROSS_LEDGER proof occurrence，在恶意sponsor耗尽reserve/shared后仍只产生线性stream I/O与有界内存；任意有限causal delta最终排空，但测试不得断言与输入字节无关的固定wall-clock上界；
- 使用同一组spy证明stale/future/expired/accepted-duplicate、policy-hash不匹配、exact nonce不匹配、transaction/Gas/Body/write reserve明显不足的occurrence都在Ed25519、strict-key、governance approval和完整next-bundle验证前短路；仍可能成为winner的candidate只有在成功suffix扣款后才能调用这些昂贵后缀。分类和winner roots在cache开关、worker数和到达顺序变化下逐字节一致；
- 对scan/common与source-proof worker逐点注入timeout、cancel、进程kill、磁盘满和checkpoint撕裂：charge与`STARTED`必须同生共灭，durable STARTED恢复时cursor等于origin并且worker重跑但预算不重扣；terminal后才可推进cursor，本地故障不得写INVALID。Finish时验证in-flight scan receipt恰好一次并入completed sponsor/shared累计量；从completed累计量+可选in-flight+全部common attempt receipts重算common spend，从全部source attempt receipts重算source spend。source binding中containing Vertex sponsor或Batch author、receipt中的`sponsor_author_index`、source VALID artifact/digest bit flip，STARTED cursor错配、孤立/重复receipt、只保留charge或只保留attempt均必须拒绝恢复；
- 高度窗口覆盖 `valid_from_height==valid_until_height` 的单高度、`span_minus_one=max_validity_window_heights-1` 的最大含首尾窗口接受、`span_minus_one=max_validity_window_heights` 拒绝、`valid_from_height>valid_until_height` 拒绝，以及接近 `UINT64_MAX` 时 checked subtraction 不下溢；不得用不安全的 `until-from+1` 实现边界；
- `authorized_access_scope` 分别构造条目数恰等于/多 1 于 `max_authorized_access_entries_per_tx`，以及完整规范数组 CBOR 恰等于/多 1 于 `max_authorized_access_bytes_per_tx`；字节向量必须包含随数组长度变化的 CBOR array header，不能只累加 entry bytes；
- 普通交易用户 scope 对 `finalweave/v1/account/meta|auth|nonce` 任一保留 namespace 的 EXACT/PREFIX WRITE 都是 `STATIC_INVALID`；协议 system access 不得编码进用户数组，创建交易的用户 scope 必须严格为空；
- 上述 envelope/policy/window/access-scope 任一静态失败都不查询账户/SMT、不进入 transaction/receipt/event tree、不写 nonce、不产生 Receipt；对 Batch 中相邻合法 occurrence 的筛选结果不得受影响；
- 账户创建/Genesis 安装时必须从 `AccountAddressCore` 重算 32-byte `AccountAddress`；普通交易不携 core，只按父 state root 认证的完整三元组授权。meta/auth/nonce 只能使用 UTF-8 `finalweave/v1/account/meta`、`finalweave/v1/account/auth`、`finalweave/v1/account/nonce` namespace，三者 raw key 都恰为 sender 地址；
- 三项全无表示不存在，三项全有才表示账户；任意 1/2 项组合、错误 schema、meta 地址/core 错配、initial policy hash/preimage 错配都必须 `EXECUTION_HALT`，不能降级为新账户或普通认证失败；
- 块开始 `AccountAuthState` 的 active signer-policy hash 与普通 Envelope policy/signatures 权威匹配；普通 self-consistent 自造 policy 仍无权声称目标 sender；
- `ACCOUNT_CREATE_V1` 正例必须同时满足 `payload_type=1`、canonical `CreateAccountPayloadV1`、两个 schema version 为 1、`nonce=0`、空 `authorized_access_scope`、core 重算 sender、core/Intent/Envelope 三处 initial policy hash 相同、threshold signatures 有效，以及父 meta/auth/nonce 全无；resolver 必须只注入三个保留 key 的 `EXACT WRITE` system access，并按 Gas operation `0x00010001` 的完整 trace 得到相同 required/executed Gas；
- 创建负例覆盖错误 type/schema、非规范或尾随 payload、非空/重复 system scope、地址/salt/policy hash/signature/nonce 任一错配、缺失/错误 gas operation、任一状态已存在、残缺三元组、同块第二个创建、创建后同块普通交易。它们均不进树、不产 Receipt、不写 accepted set；有效创建必须产生成功 Receipt 并原子写 immutable meta、无 pending auth 与 `next_nonce=1`；
- 对 meta/auth/nonce 三项 state journal、SMT node、commit marker 前后逐点崩溃；恢复只能看见全无或完整新账户，不能出现部分 key。已通过 winner 预检后的本地失败必须停止 attestation，不能伪造没有 nonce 状态的 `FAILED` Receipt；
- Genesis fixture 对每个账户要求完整三元组，重算地址与 initial SignerPolicy，auth base 等于 initial hash、pending 全无且 `next_nonce=0`；孤立/多余系统记录、缺 policy preimage、非法 policy、非零 nonce、重复地址或任一 hash 错配均使 Genesis 无效；
- 固定攻击用例：攻击者用自造 SignerPolicy 对目标 sender/nonce 产生内部自洽 Envelope，必须作为 `AUTH_INVALID_OCCURRENCE` 跳过且不产 Receipt、不耗 nonce、不写 accepted tx-id set；
- 固定轮换用例：高度 h 内成功轮换策略后，同块后续 occurrence 使用新策略仍为 `AUTH_INVALID_OCCURRENCE`，到 h+1 才可参与 winner 选择；
- `AccountAuthState` 的两个 pending 字段必须同时存在或同时缺失；轮换失败、回滚或 `h+1` checked-add 溢出均不得改变认证状态；
- `accepted_tx_ids_in_block` 只在 occurrence 成为 winner 后写入，raw occurrence 不预去重；
- 固定反例：父 `next_nonce=10`，nonce 11 的 tx A 先被跳过，nonce 10 的 tx B 成为 winner，tx A 的后续 raw occurrence 随后可以成为 nonce 11 winner；
- `max_future_nonce_gap` 只测 admission 资源策略：超过 `min(UINT64_MAX-1,next_nonce+gap)` 的请求可返回可重试 `NONCE_TOO_FAR` 且不持久化；把逐字节相同 envelope 注入 Byzantine Batch 时，共识 occurrence filter 必须仍按 `nonce>next_nonce` 归类为 `FUTURE_NONCE`，不得读取本地 future-lane 接纳记录，也不得产树叶/Receipt；覆盖 `next_nonce+gap` 饱和到 `UINT64_MAX-1` 的边界；
- sender `next_nonce` 的一次顺序扫描；
- 同 nonce 不同 tx id 的首个规范 winner；
- static-invalid、auth-invalid、future/deferred、expired 和 stale occurrence 不进 transaction tree、不产 Receipt；
- 协议配置必须同时保证 `max_transactions_per_finalized_block > 0` 和 `max_execution_gas_per_finalized_block > 0`；任一为零或解码/参数溢出都拒绝；
- `gas_limit == 0` 或 `gas_limit > max_execution_gas_per_finalized_block` 均属于 static-invalid，不能留到 `BLOCK_CAP` 分支；
- `fee_limit != 0` 在 v1 一律 static-invalid，不产 Receipt、不耗 nonce；
- transaction-count/gas cap 按 raw occurrence 单遍应用，所有 reserved-count/reserved-gas 的加减和边界比较都使用 checked arithmetic；精确等于 cap 可接受，再多 1 必须进入 `BLOCK_CAP`；
- count cap 达到后仍继续扫描以保持规范诊断，但不得再产生 winner；gas cap 遇到超限大 gas 的同 nonce occurrence 时只跳过该 occurrence，后续更小 gas 的 raw occurrence 仍可成为 winner；
- `BLOCK_CAP` 不产 Receipt、不耗 nonce、不写 `accepted_tx_ids_in_block`；Gas 预留使用签名的 `gas_limit` 而不是事后 `gas_used`。普通交易同时预留 Envelope exact bytes、含一项 nonce StateChange 的 `FailureResultReserve` 与 mandatory nonce journal bytes；账户创建预留含 meta/auth/nonce 三项 StateChange 的完整成功 result 及三项 journal bytes；
- COMMIT slot 不得因两类 block cap 而被截断、拆分或延迟到另一派生批次；
- 普通 winner 成功或业务失败均消费既有账户 nonce；创建 winner 只允许成功并原子建立 `next_nonce=1`，本地失败不得形成 Receipt；
- uint64 nonce 溢出拒绝；
- 一个 tx id 最多一个 Receipt。

快照/裁剪后重放必须只依赖认证的 immutable meta、`AccountAuthState`、`next_nonce` 与冻结的 h+1 策略激活规则，不得依赖历史 seen-set、intent outcome map 或 `nonce_winner` 表。

## 8. 确定性并行执行

串行执行器是强制 oracle。每个 block fixture 都运行：

```text
HybridParallelExecute(input) == SerialExecute(input)
```

逐字节比较 state、Receipt、Event、ChangeSet、gas 和所有 roots。

工作负载覆盖：

- 零冲突、单热 key、链式、星形和 Zipf 冲突；
- exact AuthorizedAccessScope、PREFIX preflight、ExactObservedAccess 预测失配、access-scope violation；
- 不存在读取、删除/重建、写写覆盖；
- success、业务失败、revert、out-of-gas、runtime trap；
- 同 sender 连续 nonce 与多 sender；
- 1/2/4/32 worker，正序、逆序和随机完成；
- 强制 yield、任务取消、内存高水位和 serial fallback；
- BeginBlock/EndBlock barrier；v1 对任何 WASM/未登记动态 payload 一律 static-invalid。

`max_exact_observed_access_keys_per_tx` 使用完整原始 `ConsensusStateKey` 对 `read_keys ∪ write_keys ∪ system_keys` 去重：构造恰好 limit 的成功向量、加入第 `limit+1` 个不同 key 的失败向量、同一 key 同时读写仍只计一次，以及 key hash 相同但 namespace/key 不同仍计两个的强制碰撞向量。无论 resolver 在执行前发现，还是 instrumented execution 在运行中发现，均必须在读取第 `limit+1` 个 key 的值或应用其写入前返回 `STATE_LIMIT_EXCEEDED`，回滚业务 journal、保留 winner 的协议写、生成 FAILED Receipt 并消费 nonce；测试必须证明它不被误分为 `STATIC_INVALID`、`ACCESS_SCOPE_VIOLATION`、suffix fallback 或本地资源失败，cache 与物理 SMT node 也不计数。

资源 corpus 首先对 14 个配置字段做 schema/YAML 字段对齐，再覆盖零值、对应 v1 绝对上限恰等于/多 1，固定系统 namespace 最大 raw 长度、32-byte system key、`MaxFixedProtocolStateValueBytesV1`、native read/write sizing templates、per-tx<=per-block/Event<=Body、`max_transactions*nonce_write_bytes` 和每一处 checked overflow。对每个 `max_state_namespace/key/value_bytes`、per-tx read/write bytes、per-block write bytes、Event count/bytes、return bytes、call count/depth 和完整 FinalizedBlockBody canonical bytes 覆盖 `limit-1/limit/limit+1`。每个越界用 allocator/journal/event spy 证明在读取 value 或分配/追加/写入前失败；普通 winner 得到 `STATE_LIMIT_EXCEEDED`、业务输出回滚且 nonce 消费。block reserve 测试跨 CBOR array 长度 `23/24`、`255/256`、`65,535/65,536`，并要求 FAILED 恰含 nonce StateChange、创建 SUCCESS 含三项 StateChange；证明连最大保底结果都放不下时 occurrence 才是无 Receipt 的 `BLOCK_CAP`，而成功输出挤占未来 reserve 时在相同 tx_index 转为 FAILED。

KV corpus 要求 namespace 非系统前缀、key 非空，空 value PUT 是正例；分别冻结 PUT `canonical({state_key,value_presence:1,value})` 与 DELETE `canonical({state_key,value_presence:0})` 的 exact write bytes。DELETE present/absent 都成功，最终使用 absent leaf，Body/Snapshot 不得出现 tombstone value。治理下调新写入 namespace/key/value cap 后，旧 component 在各自 v1 绝对上限内仍能 read/delete，同尺寸新 PUT 失败。

Snapshot 测试先强制 `SNAPSHOT_STATE_RECORD_MAX_CANONICAL_BYTES_V1=17_891_328`，再分别强制 namespace/key/value 的三个 v1 绝对 component cap：每个字段均覆盖 `limit/limit+1`，并构造“总 record 未超限但单个 namespace/key/value 超限”的拒绝向量。目标 epoch 已下调的 active cap 不能拒绝合法旧记录；record 或 component 绝对上限多 1 byte 必须在 frame/字段分配前拒绝。Genesis 记录则必须同时满足 epoch-0 active caps、component 绝对 caps 与 record 绝对 cap。

属性：每个最终读看到最大 `< tx_index` writer；推测状态不可查询；每块最多一次 suffix fallback；首个认证失败 index 之后的全部推测结果被丢弃并各权威执行一次；本地推测不计 gas；最终 Receipt 不含 worker/incarnation/耗时。

对 `tx_index_prefix_size` 取 `1`、整块交易数、不能整除交易数和协议允许的最大值，记录每个 `ValidateSpeculativeResult(i)` 调用，断言 window 内仍逐 index 连续认证、最后短 window 正常 checkpoint，且只在完成 window 后释放旧 MVCC 版本。注入中间 index 失败时不得认证同 window 后续 index或任何未来 window；直接以接近 `UINT64_MAX` 的 helper 状态测试 `checked_add(window_start,size)` 溢出，唯一结果是 `EXECUTION_HALT`，不能 wrap、跳过或产生证书。改变该参数只能改变 checkpoint/资源释放位置，不能改变首个失败 index、Receipt、roots 或是否触发权威 fallback。

访问 resolver、MVCC、journal 和 SMT batch update 分别 fuzz。高冲突攻击必须保持内存有界并最终退化到正确串行速度，而不是无界 abort storm。

## 9. ExecutionAttestation 与证书

- Header 的 parent/slot/proposer/ordered-vertex/transaction/receipt/event/state root、`parent_block_mmr_root`、validator/config hash 任一改变都会改变 FinalizedBlockID，进而改变 attestation statement；
- 执行前 derivation candidate 不得含或预测 FinalizedBlockID；只有 computed Header 产生后才能计算 ID；
- Header 不含当前 `block_mmr_root`：先算 ID，再追加 `{height,ID}` 得当前 root；当前 root 进入 FinalityStatement，下一 Header 才引用它；
- attestation intent 未 durable 时 signer 拒绝；
- 同高度同 digest 幂等、异 digest 硬拒绝；
- `q-1` 或 `q+1` signer 的 envelope 都不是 v1 证书，只有恰好 q 个不同 signer 成证书；
- 重复、未知、旧 epoch、错误 Ed25519 签名拒绝；
- signer 顺序/合法子集变化不改变 FinalizedBlock id；
- f 个 Byzantine 可双签时仍不能形成两份冲突 q 证书；
- 注入两份表面合法的冲突证书必须触发 Ledger freeze，不得自动选取。

`FinalityProof` 测试必须使用去重后的完整基础 schema：带 `schema_version` 的 `FinalityProof {finalized_block_header,finality_certificate,validator_set_proof,merkle_proofs}`；其 `ValidatorSetProof` 含 genesis certificate/governance policy/epoch-0 validator set/config、`transitions`、完整 `target_feature_set` 和 `target_gas_schedule`；每个版本化 `EpochTransitionProof` **只**含 seal certificate、next validator set 和 next protocol config。验证 corpus 覆盖：

- 从带外期望 network/ledger/`genesis_reference` 调用完整参数的 `ValidateGenesisCertificate`：验证 governance policy/approvals 后，调用 epoch-0 set/config 谓词并重算其 hash，无条件重算 `epoch0_seed`、空 MMR root、policy hash 与 reference；覆盖每个派生字段单独篡改、少于 threshold、未知/重复/乱序 signer、错 reference/domain/key generation、无效签名、zero weight、threshold/weight 溢出，且 reference 正确但其他检查畸形仍必须拒绝。实际安装另调用 `ValidateGenesisInstallation`，从完整 Manifest 重建 state root、验证 LedgerID 和完整 Feature/Gas bundle；proof-only 路径则明确以 expected reference 信任未携 manifest 的 state root，不能声称已重放 Genesis；
- 每一跳都校验 seal old epoch、`ValidateValidatorSet(next_validator_set,e+1,expected_network_id)`，只用“当前旧 ValidatorSet”的 Consensus Key 验证恰好 q 个 seal signer，核对 next set/config hash、调用 `ValidateProtocolConfigStructure(next_protocol_config,next_validator_set)`，最后才原子切换验证器的当前 set/config；
- 到达 Header epoch 后重算 `target_feature_set/target_gas_schedule` 的 network-scoped 内容 ID，要求与目标 config 引用一致并通过 `ValidateExecutionConfigBundle(target_protocol_config,target_feature_set,target_gas_schedule,target_validator_set)`；目标对象缺失、错 hash、未登记 typed schema 或超界 gas 均拒绝；
- old epoch 从 0 连续递增到 Header epoch，无 transition 只对 epoch 0 有效；缺跳、乱序、重复、old/new signer 混用、用新集合回签旧 seal、仅携带目标集合但缺中间公钥均拒绝；
- 在 transition 中塞入已删除的 per-hop feature/gas 字段，或使用已删除的 genesis feature/gas 字段，必须作为非规范/未知字段拒绝，不得兼容旧变体；
- 全历史 replay 按每个已认证 config hash 独立获取中间 FeatureSet/GasSchedule；中间对象缺失时 replay 标记不可用，但同一目标 `FinalityProof` 仍必须验证成功；
- 快速同步只为每跳提供 seal、next ValidatorSet/ProtocolConfig 并仅为目标提供完整 FeatureSet/GasSchedule 时必须成功；把中间 bundle 缺失误报为目标 proof 无效必须失败测试。先由 FC 认证 MMR root、后由 snapshot peaks 以目标 height 重建同 root，缺 peaks 时不得产生可追加 MMR state；
- 构造超过本地 `api.maxInclusionProofBytes` 但密码学有效的长 epoch chain，要求增量 canonical-CBOR 解码、分段获取、已验前缀缓存和断点续传均成功；该本地值只对单个 Merkle/SMT/MMR/status inclusion proof 超限向量生效，且不得出现在 `ProtocolConfig` schema 中；
- 基础 schema 不得接受 checkpoint 作为伪 genesis。`CheckpointTrustAnchor` 固定完整 set/config/Feature/Gas bundle 与 MMR peaks；覆盖 canonical anchor ID、错 network/ledger/epoch/height、错 set/config hash、错 bundle、非规范 peaks/root 和 `height=0`。`CheckpointFinalityProof` 必须同时匹配 proof `anchor_id` 与本地预置 expected ID；覆盖只匹配自报 ID、未预置/替换 anchor、首跳 old epoch 错误、缺跳/乱序、第一 seal 早于 anchor、等高 block/state/MMR 不同、目标早于 anchor、目标 bundle/证书错误，以及在两个 proof Schema 间形状猜测或失败降级。正确 checkpoint proof 只能验证 anchor 之后历史，不能声称验证 anchor 之前历史；
- `merkle_proofs` 严格按 `(tree_kind,index,item_hash)` 排序且唯一，每个 root 绑定同一 Header/Receipt；乱序、重复、转换 tree kind、不绑定的自报 root 均拒绝；
- 创世、多 epoch、Header/transaction/Receipt/Event inclusion、独立 State proof、裁剪后离线验证，以及重复携带 Header 与 proof Header 非逐字节相同的拒绝。

## 10. 状态、Receipt 与查询证据

- SMT 固定深度 256：`STATE_KEY` 绑定原始 `{namespace,key}`，`STATE_VALUE` 用 `{presence:0}` 与 `{presence:1,value}` 分离 absent/present，present leaf 带 `0x01`，`empty[256]` 只带 `0x00`；
- inclusion/non-inclusion 的 `SparseMerkleProof` 均必须有恰好 256 个 `siblings_top_down`，按 `key_hash` 从 MSB 到 LSB 定位，再从 depth 255 到 0 带 `U16BE(depth)` 重建并包装 `STATE_ROOT(tree_depth=256,tree_top)`；255/257 siblings、倒序 siblings、错 depth 或错原始 key 均拒绝；
- 空 byte-string value 必须证明为 present，不是 deletion；删除后叶必须回到 `empty[256]`，`StateChange` 的 absent side 必须使用 `absent_value_hash`，并覆盖空树 `empty[0]` 与删除最后一个 key；
- 强制 key-hash collision fixture 必须进入 `SAFETY_HALT`，不得随机选一个原始 key；
- `BlockMMRProof` golden 必须冻结 schema/canonical bytes、`BLOCK_MMR_LEAF/NODE/ROOT` 每层 hash、mountain 覆盖区间和最终 peaks；验证要求 `1 <= leaf_height <= mmr_leaf_count`，`target_peak_level` 与该叶所在 mountain 一致，siblings 的 level 连续且 side/左右顺序精确，`other_peaks` 插回后形成由 leaf count 唯一决定的规范从左到右 peaks。覆盖单叶、跨多次 merge、多个 peaks、最大合法 level，以及错 leaf height/block ID/target level、少/多/乱序 sibling、错 side、缺失/重复/非规范 other peak、leaf-count 不匹配和 root mismatch；空 MMR 不能生成 inclusion proof；
- Receipt/transaction/Event leaf 位置绑定和奇数叶自复制；
- `PER_TX_EVENT` 路径用局部 `j` 和 `EVENT/EVENT_LEAF/EVENT_NODE/EVENT_ROOT` 验证 `Receipt.event_root`，Receipt 再验证到 Header `receipt_root`；
- `BLOCK_EVENT` 路径用连续全局 `g` 和 `{transaction_index:i,event_index:j,event}`，按 `(i ASC,j ASC)` 展平并用 `BLOCK_EVENT_ITEM/BLOCK_EVENT_LEAF/BLOCK_EVENT_NODE/BLOCK_EVENT_ROOT` 验证 Header `event_root`；
- 同时返回两条 Event proof 时 `{i,j,event}` 必须一致；局部/全局空列表各自使用其 ROOT domain 的 count-zero root，禁止用 per-tx roots 列表作 Header root 或按 topic/emitter 重排；
- Snapshot fixture 覆盖空状态、单记录、多 chunk、`U64BE(length)` 前缀跨 chunk、record frame 跨 2/多个 chunk，以及按 `key_hash` 严格升序；空 byte-string 必须作 present record，tombstone/MVCC/index 不得输出；
- `SnapshotChunk` 的 path 长度必须由 `chunk_count` 唯一决定且不超过 64，左右由 index bit 推导，奇数复制层 sibling 必须等于当前 hash；少/多 sibling、错方向、错复制值、错 manifest ID/root 在写 staging 前拒绝；
- Snapshot 同样只接受 `chunk_payload_bytes=1_048_576`；P2P v1 原始 canonical envelope 恰为 `1_050_823` bytes 可解码，`1_050_824` bytes 由 bounded reader 在分配前拒绝且不经过解压器；HTTP Gateway/未来压缩 profile 另验证流式解压输出的相同边界，高压缩比、伪造 Content-Length 和分段传输均不能绕过上限；
- Snapshot manifest 的 network/ledger/epoch/height/block/finality/state/MMR/epoch emitted count/root/validator/config 目标必须与按本地 trust-root 类型选择的基础 `FinalityProof` 或 `CheckpointFinalityProof` 逐项匹配；`GetSnapshotManifestResponse` 与 `GetCheckpointSnapshotManifestResponse` 是不同 Schema，混入另一种 proof 字段、方法与本地 trust store 不符、checkpoint 自报 ID 替换和失败后 fallback 均拒绝。MMR peaks 以 `leaf_count=target_height` 重算 root，缺 peaks、非规范 level/覆盖、错 target proof 均拒绝；
- Snapshot 缺/多/重复/改值/乱序 record、缺/重排/重复/截断 chunk、尾部 framing 残留和重建 SMT root 错误都不得激活 staging generation；
- EpochEmitted sparse set 覆盖空树、单叶、共享 255-bit 前缀、多叶、错 epoch/count/root、delta 内重复、父 set 已存在 ID、equivocation siblings 不同 ID、跨 epoch reset 与 SKIP 不改变 root；lookup 缺节点必须返回 `CORRUPT_OR_MISSING`，不能伪造 non-membership；
- DAG checkpoint 串固定为严格升序、无重复的 raw Hash32 concat，覆盖少/多/乱序/重复 ID、`count*32` 溢出、错 offset/chunk path、恰好 `1_050_823` 与多 1 byte。基础/Checkpoint 两种 manifest API 独立，target committed slot/count/root 必须匹配认证 Header；Snapshot 正确但 checkpoint 少/多一个 ID时只能 query-only，禁止 Validator readiness；
- 全历史节点与 mid-epoch `Snapshot + DAG checkpoint` 节点从同一 target 后继续，逐高度比较 `VertexDelta`、Header emitted root/count、Body roots和 state root；trailing SKIP 不推进认证 resume cursor/GC，current active exact set不得被通用 HistoryGC；
- Receipt gas、Event 顺序和全部 roots 不受执行完成顺序影响；
- `FINALIZED_SUCCESS/FAILED` 必须有原交易、Receipt inclusion 与该具名 evidence 对应的基础或 checkpoint finality proof；
- 基础 `TransactionStatusEvidence` 与 `CheckpointTransactionStatusEvidence` 分别只接受 `FinalityProof` 和 `CheckpointFinalityProof`，以独立 domain/ID、API 和 verifier 运行同一 presence matrix；错误 proof 类型、错误预置 anchor、字段包装、跨类型 cache 命中和一个 verifier 失败后 fallback 全部拒绝；
- 四种 terminal status 都先对 queried Envelope 执行目标 proof context 下的完整 schema/network/ledger/SignerPolicy/signature/window/gas/fee/access/payload static validation。错误 network/ledger、伪造 sender、bad policy hash/threshold/signature、`valid_from>valid_until`、窗口 overflow、非法 gas/fee/access 或未激活 payload即使 replacement/nonce proof正确也拒绝；replacement Envelope 同样匹配当前 proof context；
- `REPLACED/EXPIRED`必须携带窗口内`candidate_height`的具名authorization context：TRUST_ROOT_STATE或精确高度`candidate-1`的同trust-root父proof、sender固定meta/auth/nonce三份SMT proof、candidate ValidatorSet/Config/Feature/Gas及可选activation transition。覆盖Genesis首块、checkpoint下一块、同epoch、跨epoch首块、active/pending policy边界、完整/残缺三元组、`next_nonce==nonce/<nonce/>nonce`，并证明`nonce > max_future_nonce_gap`仍不使终态证据无效；同块前序nonce可推进它。跨network/ledger、基础/checkpoint混型、candidate窗口外/高于终态、父proof错一高度、candidate同高post-state、同块创建后普通tx、同块nonce大推进后倒推授权、sealed parent仍声明旧epoch、错误activation/bundle/policy或伪造victim sender全部拒绝；历史proof被裁剪且Archive不可得返回`PROOF_UNAVAILABLE/HISTORY_PRUNED`；
- `REPLACED` 必须有相同 sender/nonce、不同 tx id 的 nonce-consuming winner；
- `EXPIRED` 必须有 `tip.height > valid_until_height` 的证明，且 nonce SMT proof namespace 逐字节等于 UTF-8 `finalweave/v1/account/nonce`、raw key 等于 queried sender；present 时只接受 `AccountNonceState{schema_version:1,next_nonce<=nonce}`，non-inclusion 只覆盖已由历史授权 proof 完整自证的过期 `ACCOUNT_CREATE_V1`。普通不存在账户交易、错误 schema、其他 namespace/key 或 absent 被伪解码为零值都拒绝；
- 当 `next_nonce > nonce` 时 EXPIRED 必须拒绝；
- winner 已最终时 REPLACED 优先于 loser 的过期诊断；
- UNKNOWN/PENDING 不带伪造终态证据。

九个本地阶段 `MEMPOOL/BATCHED/DA_CERTIFIED/DAG_REFERENCED/SLOT_SUPPORTED/ORDER_FINAL/EXECUTED_LOCAL/FINALITY_CERTIFIED/COMMITTING` 做 enum、JSON/Protobuf 和文档一致性测试，并验证它们可回退而不改变稳定状态。

### 10.1 跨账本异步消息

跨账本 corpus 必须从两个独立 FinalWeave fixture ledger 构造，不用 mock `finalized=true` 绕过源证明：

- 冻结 `CrossLedgerParameters/Policy/Message/SourcePosition/ProofEnvelope/ConsumedState` 全部 canonical bytes、八个新 domain、payload `4/5`、Feature `(2,1,1)` 与四个条件 Gas operation golden；
- 冻结 `CrossLedgerExpiredUnconsumedEvidenceV1` bytes（完整 10 MiB exact/+1、内嵌对象先越界）与 `OneSignerCrossLedgerConsumeEnvelopeOverheadV1(proof_len)` 在 `0/23/24/255/256/65,535/65,536/policy-max` 的 exact length；policy envelope-overhead cap在 template `exact/-1`、`proof_cap + overhead_cap == max_transaction_bytes/+1/overflow`，以及实际 Envelope overhead `exact/+1` 全部有独立向量；
- source trust root 只取 target active FeatureSet。proof 自报另一个 root、GENESIS/CHECKPOINT presence matrix错误、两个 verifier互相fallback、API返回“最新 anchor”全部拒绝；verified-proof cache必须绑定 authenticated FeatureSet hash + policy ID + 本次完整bytes重算的 proof-envelope hash，以未验证 source-event/finality ID命中、缺任一 key、cache artifact context错配都拒绝；
- source proof 同时验证 source transaction/Receipt、`PER_TX_EVENT index=0` 与 `BLOCK_EVENT` 两条 path。FAILED Receipt、伪 native emitter/topic、错 tx/event index、错 source Feature/Gas bundle、消息 destination/policy/channel/window/payload 单 bit 变化全部拒绝；
- source proof bytes、transition、逻辑 source signatures、policy/channel/relayer、message payload、Merkle siblings 分别做 `MAX/MAX+1`，并用 allocator/crypto spy 证明越界在大分配与验签前失败；
- 构造完整8 MiB proof的未授权target账户、tx height窗口外、stale/future nonce、声明message窗口外、tentative parent/working replay与`gas_limit=1` occurrence；source Finality/Merkle/Ed25519 crypto spy必须全为0，Envelope只能流式扫描一次。与invalid proof组合时固定按廉价reject-only前缀分类；tentative absent不能产生可消费结论，proof成功后window/key/working set必须重算，不能因worker并发改变；
- `max_single_cross_ledger_verification_gas`、`n*max_single` 和 sponsor-reserved/shared scheduler做 `limit-1/limit/limit+1`、overflow、cache hit/miss、invalid不退款与同tx-id块内只尝试一次。cap loser不写`cross_ledger_proof_attempts`，以后由另一sponsor可重试；charge成功先写含origin/receipt的STARTED，恢复不重扣且不越cursor，确定invalid写INVALID，VALID保存canonical artifact+digest。更晚occurrence到达source scheduler时才是`DUPLICATE_CROSS_LEDGER_ATTEMPT`；若首个已成为winner，则更早分类为`DUPLICATE_OCCURRENCE`。令`P<n`并从`sponsor_author_index=P`与`n-1`提交证明，确认全部n个Vertex sponsor都有份额且数组不越界。f个Byzantine Vertex sponsor连续多块提交最大成本无效proof并耗尽shared时，honest Vertex sponsor公平引用的合法CONSUME仍使用其保留份额最终入选；归因必须来自authenticated containing DAGVertex author而不是Batch author、relayer、peer或slot proposer，并复用上述反复引用honest Batch的攻击向量；
- 2/64/1,024 个不同 relayer、不同 target sender/nonce、不同合法 signer subset/proof envelope 并发提交同一 source event，canonical 第一个可完整预留者是唯一 winner；其余 replay 无 transaction/Receipt/Event leaf、无 nonce变化、无 working-key写；
- 第一个 occurrence 因 `BLOCK_CAP` 跳过时不得占 key，后续较小合法 occurrence可赢；同 message ID的不同 source event必须分别消费，同 source event的不同 proof必须合并到一个 key；
- SEND/CONSUME完整 GasEvent顺序、proof cache hit/miss和 batch/individual Ed25519路径逻辑等价；required gas、success Body/Event/state/write reserve差 1都在 winner前拒绝，不能伪造 OOG/STATE_LIMIT FAILED Receipt；
- 成功 CONSUME恰有 consumed + nonce 两项 StateChange、一个 `consumed/v1` Event、固定 return hash和 SUCCESS Receipt；consumed value与 target tx/height/index逐字段一致；
- target message window 的 from、until、until+1和接近 `UINT64_MAX` checked边界；墙钟、source height与两个账本高度差不能影响结果；
- target policy epoch grace 同时保留 old/new、删除 old、checkpoint root轮换与 reactivation；旧消息不能隐式套用新 policy，已消费 key不能因 policy删除而消失；
- `CONSUMED` 必须有 target finalized Header + state inclusion；`EXPIRED_UNCONSUMED` 必须有双 target verified contexts：窗口内历史 Header/FeatureSet认证 exact policy，以及同一 ledger上 `tip.height > until` 的 Header + consumption-key non-inclusion。policy删除后的历史正例、context不在窗口、FeatureSet错配、tip等于 until、索引 miss、仅 current policy inactive或 peer未见均覆盖；后几类不能伪造终态。

## 11. 存储与崩溃注入

在以下前后逐点 kill -9/断电模拟：fixed fragment、`CODEWORD_VERIFIED`、`DA_ACK_LOCK`、Vertex/rejoin intent、`EpochClosingIntentV1`、`EPOCH_CLOSING_FENCE`、PreparedExecution、authenticated derivation generation、`CausalValidationRecordV1`、账户创建 meta/auth/nonce 三项 journal、跨账本 consumed/nonce/Event journal、attestation intent、签名 receipt、证书写入、SMT 节点、certified/full-snapshot/query-snapshot marker、cursor 和 GC plan。

epoch close 单独执行完整 crash/race matrix：

- 在 stable prefix 首次识别 C、`EPOCH_CLOSING_RESERVATION` fsync 前/后、C deterministic execution/post-state、closing intent/final fence fsync前后、attestation/获证前后、certified publication事务中和 `EPOCH_CLOSED` 可见前后注入崩溃；有 reservation 无 intent 时只能重执行同一 C并从 post-state重建相同 activation，有 fence无 closed时只能认证/发布同一 C，不得撤销或分配 C+1；
- 自动边界 C 自身携 `LEDGER_RECONFIGURE_V1` 的 SUCCESS/FAILED/因 BLOCK_CAP 未入选三例：SUCCESS 必须在 post-state选择 PENDING_RECONFIG，另外两例选择 SAME_CONFIG；父状态为空不能提前锁 same-config。reservation/intent/fence任一 hash、record顺序或 crash recovery结果变化均停机；
- 自动 rollover 覆盖 epoch block ordinal `65_535/65_536/65_537`，emitted count `trigger-1`、恰好跨线和单块有限 overshoot；等号候选必须先写 closing intent/fence，MAX+1 必须因先前 fence缺失而停机。父状态有适用 pending action时激活它，否则严格 same-config/next-epoch set；两者优先级、hash和 seal authorization跨实现一致。模型调度令 GST 任意晚、长期无 COMMIT：不能因有限 DAG round硬停；下一 stable candidate仍按计数触发并唯一关闭；
- 并发调度 C 与 C+1 的高度分配/`EXECUTION_ATTESTATION_INTENT`，共享锁域必须保证 fence durable 后只允许 C；若 fence 写入前已出现更高 intent，立即 `SAFETY_HALT`，不能等 C 获证后才发现；
- 在 `EpochSealVoteLock` 写前、写后 fsync 前、fsync 后签名前、签名后发送前注入崩溃；重启只能幂等签同一 statement，不得变更 final block/config；
- `EPOCH_CLOSED` durable 后任何 `height>final_height` 的旧 epoch attestation 都被拒绝，该上界在重启、WAL replay 和 KMS retry 后仍保持；
- `EpochSealCertificate` 只有恰好 q 个旧 ValidatorSet signer 才接受；`q-1`、`q+1`、重复 signer、old/new 混合和错 key generation 均拒绝。
- 对同一 corpus，证书 verifier、基础/Checkpoint proof 每跳只凭 wire 对象调用 `ValidateEpochSealStatementIntrinsic`；旧 signer/持有完整历史的 activation 另调用包含 closing publication、4-field closed record、closing intent、current bundle、optional action 和 next bundle 的 `ValidateEpochSealAuthorization`。测试不得给公共 proof verifier 注入伪造本地状态；只改变 `next_epoch_seed`、closing intent hash或 same-config copy 任一字段、使用错误 domain/字段顺序、把 signer subset 混入 seed 或让 q 个旧 signer 对错误 seed 签名均必须由 intrinsic/authorization 相应拒绝。

Genesis/epoch activation 另做四元组原子性矩阵：在下载、四个内容 ID 重算、`ValidateValidatorSet`、`ValidateProtocolConfigStructure`、`ValidateExecutionConfigBundle`、新 generation fsync、activation marker 提交前/中/后逐点崩溃。恢复后 active pointer 只能引用完整旧 `(ValidatorSet,ProtocolConfig,FeatureSet,GasSchedule)` 或完整新四元组；Genesis 只能是“尚未激活”或完整 epoch-0 四元组。新 set 配旧 config/feature/gas、旧 set 配新执行配置、marker 指向缺对象 generation 或任一内容 ID 不匹配都必须 `SAFETY_HALT`，且下一 epoch key 仍不得签协议对象。

CausalInput sync 对 manifest durable、chunk proof 验证前/后、payload fsync 前/后、8-byte frame prefix 跨 chunk、item 跨 chunk、occurrence-filter checkpoint、游标 checkpoint、`CausalValidationRecordV1` fsync、generation checksum、publish marker 与 active-pointer 切换前/中/后逐点 kill。filter checkpoint必须同时恢复可选`OccurrenceScanAttemptV1`、逐sponsor completed-scan reserved与shared累计量、`attempted_prefilter_tx_results`、全部n个occurrence-sponsor common reserve/shared/total spend；激活跨账本时再恢复working-consumption、`cross_ledger_proof_attempts`及独立proof-work spend。每个source binding必须同时恢复containing Vertex sponsor与Batch author，所有attempt/receipt都以`sponsor_author_index`扣款。scan charge与in-flight record、Finish并入completed累计量/清in-flight/推进cursor、common/source charge与STARTED分别做before/between/after crash矩阵；STARTED的origin必须是当前cursor，恢复不得重扣，所有receipt必须逐sponsor/shared重算到counter，VALID source artifact digest必须复验。专门让一个最大item跨3个chunk，在每个chunk checkpoint kill，证明下载/verified-byte cursor可以领先但filter cursor仍指frame prefix，恢复从frame起点重喂且不重扣；提前删除覆盖in-flight frame的staging chunk必须触发重取全验或回滚，不能从frame中间续解。任一map/counter/receipt/artifact、active bundle内容ID、containing Vertex sponsor映射或BatchHeader author映射缺失/损坏时只能回滚到完整occurrence边界；若无此边界则从块开头重扫。从中间游标把预算/in-flight/attempt map清零或让cursor越过STARTED的mutant必须导致测试失败。marker 前恢复必须以同一 manifest ID 重验最后完整 chunk proof，使用保存的 next byte/item/Vertex/occurrence 游标、framed-decoder 边界、leaf 校验信息和 filter 状态幂等续传；重复 chunk 不得重复发出 item，改变 manifest/已验证 byte 必须拒绝。record 的 manifest/chunks root、声明/consumed counts、五个 Header roots、FinalizedBlockID、MMR root、generation ID/checksum 或 complete flag 任一错配都禁止发布；generation 内 canonical manifest core 缺失、改值或不再重算到 record manifest ID 同样必须停机。marker 提交后删除全部原始 causal chunks、临时 leaf files和已消费 DAG/Batch source，重启必须仅凭 record+generation+FC+marker chain 恢复；marker 前删除同样数据则只能重取/全验且保持不可见。不完整 causal stream、未 fsync 数据或未提交 marker 绝不能形成部分可见派生状态。

单独固定31-field `CertifiedPublishMarkerV1`、其内2-field `HistorySegmentRefV1`、29-field `SnapshotInstallMarkerV1`和26-field `QuerySnapshotInstallMarkerV1`的canonical bytes与三个marker hash golden。certified marker 覆盖 tagged previous kind/hash、emitted count/root和 derivation generation；full/query install 覆盖 install sequence、previous active kind/hash/height和互斥字段。断电恰在 staging fsync 后、marker fsync 前时可丢弃 staging；恰在任一 marker fsync 后、active-pointer CAS 前时必须视为已提交，沿唯一 hash chain完整校验后幂等 roll-forward。高度 H full snapshot → H+1 certified marker 必须引用 full marker；query marker不得作 publication anchor。两个 target 并发引用同一 previous、sequence分叉/跳跃、同高非 QUERY→FULL、不同 marker hash、generation ID复用、跳高或 parent 不连续均 `SAFETY_HALT`；不能任选最高 target，逐字节相同重放才幂等。

Snapshot sync 对 manifest 与本地 trust-root 类型选定的基础/Checkpoint finality proof durable、Snapshot/DAG-checkpoint chunk inclusion 验证前/后、payload fsync、跨 chunk record、SMT/sparse-set staging checkpoint、重建 roots 完成和两类 install marker 前/中/后逐点 kill。恢复须重验 proof、两个 manifest与最后完整 chunk，从保存游标幂等继续；full active 只能是完整旧状态或同 target 的 state+MMR+derivation+certified cursor，query active 只能有 state/MMR且明确 `SYNCING_DERIVATION`，绝不能出现部分 key、混合 generation、cursor先行或 query marker启用签名。恶意 peer 在断点后替换 manifest、chunk、proof 类型、anchor或一个 emitted ID必须在 staging/marker 前失败。

恢复后断言：不双签、不倒退、无半状态、同输入不产生第二组 root、cursor 与数据一致、重复提交幂等，且 `EPOCH_CLOSED`/attestation/seal-vote 共享锁域的相对顺序不变。对 WAL 截断、bit flip、旧磁盘快照、磁盘满、fsync error 和 compaction crash 做专门测试；无法证明锁域完整时不得自动恢复 Consensus signing。

快照和 CausalInput 测试都从多个恶意来源混合 chunks，只能接受与已选 manifest/root 一致且逐块 proof 有效的集合；导入 staging 失败不改变 active state/prepared generation。

## 12. 网络、同步与 API

每个 protocol id 做 codec fuzz、frame 上限、慢读写、乱序、重复、断流、orphan 洪泛和公平调度测试；P2P v1 的 `compression_algorithms` 只有精确 `[NONE]` 能完成握手，任意额外、重复、重排或未知 ID 均失败，所有消息直接进入 bounded canonical decoder，测试桩必须证明不创建、不调用解压器。HTTP Gateway Content-Encoding/未来版本另做压缩炸弹测试：解压后恰好 `MAX_CANONICAL_BYTES` 接受，首个越界 byte 在 decoder/staging/声明长度分配前中止，未配置有限上限时拒绝启用压缩；transport frame 任意切分不得改变结果。验证大 fragment 传输时 P0 Vertex/attestation/certificate 尾延迟仍受控。

`/finalweave/1/causal-input/sync` 与 Snapshot chunk API 均覆盖固定 `1_048_576`-byte payload、非末块短一 byte/多一 byte、跨 chunk length-prefix/frame、路径长度和奇数复制、乱序并行拉取后的有序消费、磁盘 spill/暂停/换 peer 与断点 Range 重连。两种 P2P v1 原始 canonical chunk envelope 恰为 `1_050_823` bytes 可进入 CBOR 解码；第 `1_050_824` byte 必须由 bounded reader 在 payload/sibling/CBOR 大对象分配和 staging 写入前拒绝。HTTP Gateway/未来压缩 profile 对解压输出重复该 `MAX+1` 测试，伪造 Content-Length、高压缩比和任意 transport frame 切分均不得绕过。

同步覆盖：空节点、落后多个 epoch、只有 Header、Body 缺失、快照损坏、恶意高高度、配置链分叉、DAG 安全窗口缺口。peer 多数错误不能覆盖合法 `FinalityProof`。

API 做幂等、分页绑定、索引滞后、授权、限流和 SDK 离线验证。普通 transaction/receipt/state/event 查询响应不得含未类型化 finality-proof 字段；SDK 必须先通过本地 trust mode 唯一选择的基础 `GetFinalityProof` 或 checkpoint `GetCheckpointFinalityProof` 建立已验证 Header context，再要求查询 Header 逐字节相同并验证 inclusion proof。若所选 proof 也携带同一 MerkleProof，两份必须逐字段相同；proof 缺失、tree-kind/index/item-hash/path 错误、Header 错配、响应偷带另一 proof 类型或失败后 fallback 均拒绝。还要验证 Gateway 当前状态授权预检不能替代块开始的权威 meta/auth/nonce 三元组：预检通过后发生轮换可被筛除，预检失败也不得改变已排序数据的确定性执行。

`/finalweave/1/cross-ledger-proof/sync` 与 relayer API 另做：调用方 target policy/root精确绑定、source端拒绝替换 root kind、proof envelope 8 MiB/MAX+1、慢流/断流/重复 signer subset、Archive locator不受信、独立 verifier pool背压，以及同一消费 RPC超时后重提相同 tx id。proof洪泛不得挤占 P0/P1；queue drop只能是本地可重试诊断，不能改变链上 static validity。

本地 P2P 配置矩阵要求 `p2p.alpn="finalweave-p2p/1"`、`p2p.mutualCertificateRequired=true`、`65_536 <= p2p.maxFrameBytes <= 16_777_216`，并只注册 `NONE`。覆盖 frame 两端点、各越界 1、错误/空 ALPN、关闭双向 certificate、非 Ed25519 leaf、`identity.p2pTlsCertFile` leaf SPKI 与 `identity.peerKeyUri` 不同、API TLS key 复用 Peer key、启用无法绑定原 leaf 的 resumption，以及本地 Validator 的派生 PeerID/key 与 active/pending descriptor 不同；所有错误必须在 listener/readiness/signer 开放前失败。热重载中途失败保持完整旧配置，成功切换则原子失效受影响 Ledger 的授权 cache，不能出现新证书/旧 key 或部分 Ledger 已提升权限。

本地 API 配置测试要求 `api.maxRequestBytes >= checked_add(max_transaction_bytes,65_536)`，不足时 SubmitTransaction readiness 失败。分别以 binary、JSON/base64 和 multipart 提交 canonical envelope 恰等于 `max_transaction_bytes` 的协议有效交易，transport 必须通过表示层膨胀预算或受限流式解码接受，不能因 raw wire bytes 大于 canonical limit 误拒；canonical 多 1 byte 则统一由 `CanonicalAndStaticValid` 拒绝。压缩炸弹、超长 base64、multipart header/字段洪泛仍须由独立 raw/decompressed/temp-storage 上限有界拒绝。

## 13. Byzantine 与 chaos 场景

至少包含：

- Batch 作者分发不一致 codeword；
- ACK signer 未持久化、重复 signer、混合 commitment；
- 同 author/round 双 Vertex；
- 父 author 重复、缺父、跨 epoch、非法 round jump；
- support 切换尝试、direct/skip 边界分割；
- 按组织分区、非对称丢包、30% 丢包、延迟尖峰和消息重排；
- 执行器慢、单节点 root 错误、同高度 attestation 冲突；
- KMS 超时、返回错误 key、WAL 回滚；
- 磁盘满、数据库 corruption、snapshot 投毒；
- 查询洪泛、跨 Ledger noisy neighbor。
- source proof/root substitution、伪 native Event、并发 relayer抢跑、target policy切换和 consumed索引/SMT不一致。

每个场景同时断言 safety 和在恢复假设下的 liveness，不能只看最终又开始增长。

## 14. 跨平台与差分

同一 fixture 至少在 amd64/arm64、Linux/macOS 开发环境、不同 CPU 核数和两个独立代码路径运行。比较规范 bytes、所有 hash、stable prefix、BlockDerivationCandidate、FinalizedBlockID/MMR、Receipt/Event、state roots 和 proofs。

禁止浮点、宿主字节序、map 迭代、墙钟、随机数、未固定 WASM engine 和数据库迭代顺序影响结果。发现差异先停止 attestation，再把最小 fixture 永久加入 conformance corpus。

## 15. 性能测量模型

截至本文定稿，FinalWeave 是从零实现的设计，**没有生产实测 TPS、延迟或资源承诺**。任何容量数字必须标注硬件、网络、Validator 数、Batch/交易大小、冲突率、P、软件提交和统计窗口；不能引用其他系统数字作为本系统保证。

端到端延迟必须拆分：

```text
T_final = T_admission
        + T_DA
        + T_order
        + T_execution
        + T_seal
        + T_state_commit
        + T_client_delivery
```

跨账本端到端另报告 `T_source_finality + T_source_proof_build + T_relay_queue + T_target_admission + T_target_finality + T_target_proof`，并分别扫描 source epoch transition 数、proof bytes/signature 数、message payload、并发 relayer数与 proof cache命中率。不得把“source Event已最终”当作 target delivery latency终点，也不得用 cache warm峰值隐藏 cold verifier成本。

定义：

- `T_DA`：Batch 创建到 `BatchAC` 可验证；
- `T_order`：AC 可用到其 slot 进入 stable prefix；
- `T_execution`：BlockDerivationCandidate 可用到 computed Header/FinalizedBlockID/MMR state 的 durable Prepared record；
- `T_seal`：首个 attestation 到 q 证书可验证；
- `T_state_commit`：证书可用到 commit cursor 原子前进；
- 客户端最终性：提交到 `FinalityProof` 可验证，而非看到任一本地阶段。

每段报告 p50/p95/p99/max、吞吐、CPU、内存、磁盘、入/出带宽和失败/回退率。只报平均值或只报协议内部延迟不合格。

## 16. 基准矩阵

### 16.1 工作负载

- 小/中/最大交易；
- 读多、写多、事件多、签名多；
- v1 ACCOUNT_CREATE、ACCOUNT_POLICY_ROTATE、LEDGER_RECONFIGURE，`NATIVE_KV_V1` 激活时的 KV_PUT/条件启用 KV_DELETE，以及 `CROSS_LEDGER_V1` 的 SEND/CONSUME（扫描 proof bytes、transition/signature数、payload、并发 relayer与cache冷/热）；未来 WASM 只能在独立新版本 benchmark track 中出现；
- 通用prefilter对照组：全valid、stale/future cheap reject、坏account signature、坏strict key、最大governance approvals、最大可表示reconfiguration bundle；分别以1到f个Byzantine Vertex sponsor耗尽reserved/shared，并验证honest-sponsor最大合法候选仍推进；另测恶意sponsor反复引用honest作者最大Batch且不做BatchID去重，确认费用不转嫁；
- 冲突率 0% 到热 key 极端；
- authorized exact、PREFIX preflight 与 serial barrier；
- 多 Ledger 公平性；
- steady、burst、backfill 和 snapshot catch-up。

### 16.2 协议参数

- 4/7/10/13 Validator；
- 本地、同城、跨地域 RTT/带宽/丢包；
- `P=1/2/q`；
- Batch size、fragment 参数、Vertex cadence；
- attestation 搭载数量与证书收集拓扑；
- 1 到目标核心数的执行 worker。

### 16.3 退化曲线

必须测少数慢 Validator、一个组织断网、fragment 丢失、DAG 父缺失、执行冲突升高、prefilter坏签名/bundle洪泛、fsync 尾延迟、证书聚合者故障、磁盘 compaction 和快照同步。prefilter报告必须同时给出每occurrence sponsor的reserved/shared逻辑units、真实CPU、crypto调用数、cache冷热、`PREFILTER_SCAN_CAP`/`PREFILTER_VERIFY_CAP`与合法重提延迟；性能报告必须展示何处从并行退化为串行以及是否仍有界。

## 17. 关键性能指标

- DA：Batch bytes/s、AC/s、codeword verify CPU、fragment 恢复率、ACK fsync；
- Order：Vertex/s、DAG bytes、round lag、undecided slot age、stable slots/s、direct/skip 比例；
- Execution：tx/s、gas/s、parallel speedup、artifact reuse、authoritative re-execution、MVCC bytes、serial fallback；
- Seal：attestation/s、certificate latency、签名验证 CPU、搭载字节；
- Commit：state delta、SMT hash/s、DB write amplification、fsync 与 commit cursor lag；
- End-to-end：finalized tx/s、submit-to-proof 延迟、terminal evidence 查询延迟。

不要用“Batch 接收 TPS”代替 finalized TPS，也不要把 ORDER_FINAL 当交易成功。

## 18. 容量与背压验收

容量上限取网络、DA 验证、DAG 元数据、执行、seal、状态提交和最慢必要 Validator 中的最小值。生产参数必须留出故障恢复余量，不能把稳态 CPU、内存、磁盘和网络跑满。

背压链路必须可测：以 checked `highest_ordered_candidate_height-public_finalized_height` 得到 execution lag；在 `max_execution_lag_heights-1` 仍允许 payload、恰等于上限立即停止新 Batch/AvailabilityReference、降回 `<` 后恢复。背压期间 control-only Vertex、dependency repair、execution、attestation、FC 与 epoch close 必须继续。`gc_boundary_round` 在 `highest_complete_round-dag_gc_rounds` 的等号边界、Batch `terminal_height+batch_retention_heights` 的 inclusive release 边界及加减 overflow 都做正反向向量；overflow 不得 wrap 后提前 GC。DAG 缺父时优先 sync，查询过载先降历史查询；背压不得改变 canonical order 或丢弃已稳定输入。

对 `BacklogBackpressurePolicyV1` 的每个 low/high 做 `low-1/low/high-1/high/MAX` 与 checked overflow测试，并冻结 `NORMAL -> PAYLOAD_BACKPRESSURE -> CONTROL_STORAGE_PAUSE -> 恢复` 状态机：任一 payload counter到 high即停止新 Batch、尚无锁的 ACK与 AvailabilityReference，全部 counter降到 low且 execution lag低于上限才退出；control到 high或 reserve不足停止新 DAGVertex/ACK/Batch，但仍能转发、repair、执行稳定输入、通过独立 finality通道完成已有 attestation/FC及已落 reservation/fence 的 close/seal，且绝不删除 Safety WAL。至少 q 个诚实节点背压时，f 个 Byzantine signer不能单独形成新 BatchAC。

Batch计数必须覆盖攻击回归：连续生产多个Batch，每个都恰好被认证causal publication消费一次但epoch尚未关闭；counter仍单调增加并在high触发背压。AUTHOR_BODY与同Batch ACK_FRAGMENT分别按`stream length + canonical manifest length`计数，重复的自包含Header也分别计；任何按BatchID去重实现须由golden counter拒绝。只有`EPOCH_CLOSED.final_height + retention`inclusive到期、全部引用/Archive/pin条件满足且独立`BatchGCRecordV1` durable后才减；从未publication的Batch、晚到Vertex再次引用、release前崩溃、record后bytes删除前崩溃和overflow均验证。record前bytes缺失必须`SAFETY_HALT`，counter checksum或registry/GC重算不符保持只读，不能清零。

## 19. 发布门禁

### 19.1 必须通过

- 所有 schema/test vectors 和跨平台差分；
- strict Ed25519 的 point/scalar/subgroup/ZIP-215 负例与 batch-vs-individual 等价；
- `RS_GF256_V1` 跨语言全部 shard bytes、任意 k 恢复与最大 n 向量；
- FinalDAG-C safety model，无 restricted-jump 反例；
- 错误的 unrestricted 模型能复现固定反例；
- sticky support、direct/skip 互斥、prefix agreement；
- 并行执行等于串行 oracle；
- attestation 防双签和证书冲突测试；
- `EPOCH_CLOSED`/更高旧 epoch attestation/EpochSealVote 共享锁的 crash-race matrix；
- Safety WAL/原子提交 crash matrix；
- `ACCOUNT_CREATE_V1` 自证/空 scope/system access/gas/同块 created-set 向量，以及 meta/auth/nonce 全有全无的 Genesis、snapshot、journal crash matrix；
- epoch `(ValidatorSet,ProtocolConfig,FeatureSet,GasSchedule)` activation generation 的 crash 原子性矩阵；
- Batch/Vertex 完整 canonical envelope 上限函数、昂贵 fetch/RS 前声明上限检查与恢复后实际值复验；
- CausalInput/Snapshot manifest-core/chunk-core golden、逐 chunk proof、跨 chunk frame、`1_050_823` 边界、`CausalValidationRecordV1`、marker 后 source GC 恢复与 activation crash matrix；
- execution-lag、DAG-GC、Batch-retention 的 inclusive 边界、checked overflow 与 control-only 活性矩阵；
- snapshot/裁剪后 proof 验证；
- `CanonicalAndStaticValid`、future-nonce admission 隔离、exact observed-key cap 与 `tx_index_prefix_size` 全部边界分类测试；
- 跨账本 source-root policy、双 Event path、proof union/上限、Gas trace、并发唯一 winner、永久 consumed SMT、epoch/checkpoint与 crash/prune矩阵；
- Byzantine、分区和 q 恢复测试；
- 目标硬件容量、24h 压测和更长期 soak 无泄漏/游标停滞。

### 19.2 阻断发布

任何冲突 stable prefix、冲突合法证书、同输入不同 root、签名前 intent 缺失、证明绕过、未界定内存/磁盘增长、无法解释的跨平台差异或未经实测的生产参数都阻断发布。

## 20. 分阶段上线

1. 单节点执行/状态闭环和串行 oracle；
2. Batch/fragment/AC 故障网；
3. FinalDAG-C 模型与 4 Validator 测试网；
4. 并行执行 shadow，对每块同步跑 oracle；
5. attestation/certificate 与 proof API；
6. 快照、裁剪、跨 epoch；
7. 多地域 canary，限制 Ledger 和负载；
8. 满足容量门禁后逐步提高参数。

回滚只能停止新协议版本或在尚未激活前撤销治理提案；已最终状态不能软件回滚。发现安全问题时冻结 Ledger 并建立新的治理信任锚流程。

## 21. 完成定义

- [ ] 每一项安全不变量都有模型属性和运行时断言。
- [ ] 每个协议对象有正/负 test vector 与 fuzz target。
- [ ] S&P26 restricted jump 反例成为永久测试。
- [ ] parallel==serial oracle 覆盖全部交易结果和 roots。
- [ ] terminal evidence 与九阶段诊断通过 API/SDK 一致性测试。
- [ ] 性能报告逐段列出 DA/order/execution/seal/state commit，且明确硬件与提交。
- [ ] 文档中所有性能数字均来自可复现实测；无数据处明确写“尚未实测”。
