# FinalWeave 存储、快照与裁剪规范

> 状态：设计基线（Draft）
>
> 适用范围：FinalWeave v1

## 1. 目标与故障模型

存储层必须在进程终止、断电、部分写、磁盘满、文件损坏和消息重放后同时保持：不签冲突对象、不倒退最终高度、不发布半个状态、不丢失重建最终证明所需的数据。

安全优先级从高到低为：签名锁与签名意图、`FinalityCertificate` 和提交游标、最终状态、可用性对象、可重建索引与缓存。无法确认安全 WAL 完整时，节点必须进入只读同步模式，不能继续以 Validator 身份签名。

## 2. 逻辑存储域

建议使用支持原子 write batch、snapshot 和 checksum 的嵌入式 KV。物理 column family 可以变化，逻辑域不得混淆：

| 逻辑域 | 主要内容 | 可否重建 |
|---|---|---:|
| `safety` | codeword 验证标记、DA ACK/Vertex/ExecutionAttestation/EpochSealVote 签名锁、`EPOCH_CLOSED`、签名回执 | 否 |
| `batch` | BatchHeader、fragment、`BatchAC`、恢复状态 | 部分 |
| `batch_retention` | 不可变 `BatchRetentionManifestV1`、author body/ACK fragment 保留义务与 active refs | 否 |
| `batch_gc` | append-only `BatchGCRecordV1` hash chain | 否 |
| `dag` | Vertex、父边索引、round/author 索引、stable slot 与决策证据 | 可从对象重算，但成本高 |
| `ordered` | stable slot prefix、线性化增量、BlockDerivationCandidate | 可重算 |
| `causal_input` | CausalInput manifest/chunks、verified progress、`CausalValidationRecordV1`、临时 leaf files 与 generation marker | marker 前可从 DAG/Batch 重算；marker 后 canonical manifest core 与 validation record 进入不可裁剪核心，raw chunks/source 作为 history segment 按规则 GC |
| `execution_scratch` | MVCC、journal、未认证执行 artifact | 是 |
| `execution_prepared` | PreparedExecution、computed Header/ID、MMR state、可恢复 state batch | 不应丢失 |
| `finalized` | 不可裁剪 Header、`FinalityCertificate`、发布核心与证明链元数据；Body 只以受管 history segment 保存 | 核心否；segment 可归档/裁剪 |
| `state` | 当前权威 SMT、每高度 state root、永久跨账本 consumed markers；旧版本节点与未注册 proof cache | 当前状态与 consumed markers 否；历史版本可由快照和最终块重放 |
| `receipt_event` | Receipt、Event、Merkle 证明材料的 history segment | 可重放，生产应保留策略明确 |
| `history_segment` | Body、Receipt/Event、ordered-Vertex proof、DAG witness、旧状态证明与其他可选历史 payload | 是；内容寻址且独立校验 |
| `history_gc` | append-only `HistoryGCRecordV1` hash chain 与受管 segment 目录 | 否 |
| `snapshot` | manifest、chunks、导入 staging | 可重新生成 |
| `index` | tx/event/account 查询索引 | 是 |
| `meta` | schema、迁移版本、各阶段 cursor | 否 |

所有 key 必须包含 `ledger_id`。高度、round、slot 和 index 使用固定宽度 unsigned big-endian；不得把宿主整数、JSON 或数据库迭代顺序带入协议结果。

UTF-8 `finalweave/v1/cross-ledger/consumed` namespace 属于当前权威共识状态，不是 `index`、proof cache 或可裁剪 history segment。其 raw key 必须是协议重算的 32-byte consumption key，value 必须完整解码并重新绑定 source event、message、policy 与唯一 target consumer location；present marker 不得删除、覆盖、TTL、按 epoch迁移出 SMT或因 source/target policy失活而回收。source proof、target Body/Receipt/Event可以按 history policy归档，但 marker继续进入每个后继 state root与 Snapshot，维持裁剪后的重放安全。

## 3. 单调游标

每个 Ledger 至少持久化：

```text
received_dag_round
stable_slot_cursor
ordered_block_cursor
causal_input_generation_cursor
prepared_execution_cursor
finality_certificate_cursor
state_commit_cursor
index_cursor
snapshot_cursor
```

约束：

```text
state_commit_cursor <= finality_certificate_cursor
finality_certificate_cursor <= prepared_execution_cursor
ordered_block_cursor <= stable_slot_cursor 派生出的可线性化范围
index_cursor <= state_commit_cursor
```

某些执行可以并行准备多个连续高度，但 `state_commit_cursor` 只能按父状态连续前进。游标更新与对应数据必须处于同一原子事务；启动时发现游标越过数据、父根不连贯或同高度两个 block id，立即安全停机。

## 4. Safety WAL

### 4.1 记录格式

Safety WAL 是本地 storage schema，不是 wire 共识对象，但 v1 的恢复与 signer 防双签语义要求所有实现使用同一逻辑编码。规范记录为：

```text
SafetyRecordCoreV1 {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  sequence: uint64
  epoch: Epoch
  kind: uint16
  unique_slot_cbor: byte_string
  semantic_digest: Hash32
  key_id: Hash32
  payload_cbor: byte_string
  previous_record_hash: Hash32
}

SafetyRecordV1 {
  core: SafetyRecordCoreV1
  record_checksum: Hash32
  record_hash: Hash32
}

record_checksum = SHA256(
  U32BE(len("FINALWEAVE_SAFETY_RECORD_CHECKSUM_V1")) ||
  ASCII("FINALWEAVE_SAFETY_RECORD_CHECKSUM_V1") ||
  U64BE(len(canonical(SafetyRecordCoreV1))) ||
  canonical(SafetyRecordCoreV1)
)

record_hash = SHA256(
  U32BE(len("FINALWEAVE_SAFETY_RECORD_V1")) ||
  ASCII("FINALWEAVE_SAFETY_RECORD_V1") ||
  U64BE(len(canonical(SafetyRecordCoreV1))) ||
  canonical(SafetyRecordCoreV1) || record_checksum
)
```

Core 的 11 个字段与 envelope 的 3 个字段分别按列出顺序使用 deterministic-CBOR integer key `1..N`；两个 byte-string 必须各自恰好包含一个当前 kind 登记的 canonical typed object，未知/缺失/重复字段、非最短整数或尾随字节都拒绝。`schema_version=1`。首条固定 `sequence=1/previous_record_hash=ZERO_ID`；后续 sequence 必须 checked 加一并引用前一完整 envelope 的 `record_hash`。checksum 与 hash 都重算，不能只信数据库 key。`key_id=ZERO_ID` 仅用于不调用 signer 的 kind；签名 intent/receipt 必须使用协议篇 `VALIDATOR_SIGNING_KEY_ID` 公式从已认证 ValidatorDescriptor、role、network/ledger 重算的 DAG/Consensus key ID，禁止使用 KMS URI、provider handle hash 或自增 generation 代替。

kind 数值与 `unique_slot_cbor` 的 typed 字段固定如下；slot 内字段仍按列出顺序编号：

| kind | 数值 | unique slot |
|---|---:|---|
| `CODEWORD_VERIFIED` | 1 | `{epoch,batch_author_index,batch_seq,batch_id}` |
| `DA_ACK_LOCK` | 2 | `{epoch,batch_author_index,batch_seq,ack_signer_index}` |
| `VERTEX_SIGN_INTENT` | 3 | `{epoch,round,author_index}` |
| `VERTEX_REJOIN_INTENT` | 4 | `{epoch,author_index,new_round}` |
| `EXECUTION_ATTESTATION_INTENT` | 5 | `{epoch,height,validator_index}` |
| `EPOCH_CLOSING_INTENT` | 6 | `{old_epoch}` |
| `EPOCH_CLOSING_RESERVATION` | 7 | `{old_epoch}` |
| `EPOCH_CLOSING_FENCE` | 8 | `{old_epoch}` |
| `EPOCH_SEAL_VOTE_LOCK` | 9 | `{old_epoch,validator_index}` |
| `EPOCH_CLOSED` | 10 | `{old_epoch}` |
| `SIGNATURE_RECEIPT` | 11 | `{intent_record_hash}` |
| `EPOCH_SEAL_CERTIFICATE` | 12 | `{old_epoch}` |
| `EPOCH_KEY_ACTIVATION` | 13 | `{epoch,key_role,key_id}` |
| `EPOCH_KEY_RETIREMENT` | 14 | `{epoch,key_role,key_id}` |
| `COMMIT_CURSOR_ADVANCE` | 15 | `{height}` |
| `BATCH_AUTHOR_INTENT` | 16 | `{epoch,batch_author_index,batch_seq}` |

`payload_cbor` 逐 kind 固定承载：完整 codeword/body/fragment commitments；ACK 的 BatchID/fragment commitment；Vertex core/ID；rejoin target finality/prior own ID/proof root；FinalityStatement；完整 `EpochClosingIntentV1`；reservation 的 `{closing_slot,proposer_vertex_id,candidate_height,derivation_candidate_digest}`；fence 的 `{reservation_record_hash,closing_intent_hash,closing_slot,proposer_vertex_id,candidate_height,derivation_candidate_digest}`；EpochSealStatement；`{old_epoch,final_height,final_block_id,closing_intent_hash}`；`{intent_record_hash,signature}`；完整 EpochSealCertificate；key generation metadata；`{height,finalized_block_id,certified_publish_marker_hash}`；或完整 `BatchHeaderCore`。typed payload 的未知字段拒绝。签名 intent/receipt 的 `semantic_digest` 等于实际 wire signing digest；其他 kind 固定为：

```text
SafetyPayloadDigest(kind,payload_cbor) = SHA256(
  U32BE(len("FINALWEAVE_SAFETY_PAYLOAD_V1")) ||
  ASCII("FINALWEAVE_SAFETY_PAYLOAD_V1") ||
  U16BE(kind) || U64BE(len(payload_cbor)) || payload_cbor
)
```

同 unique slot 已有相同 semantic digest/payload 时幂等返回；任一字节不同都硬拒绝，不能靠“较新 sequence”覆盖。`CODEWORD_VERIFIED` 必须先于 matching ACK lock durable；`fragment_index == ack_signer_index`。closing reservation/fence 必须锁同一 C，fence 引用 reservation record hash；closing intent/fence/closed 三者必须引用同一 `closing_intent_hash`；closed 的 final height/block 必须等于 fence 候选。receipt 必须引用现存 intent record hash且签名验真，不能单独插入。

物理 WAL frame 固定为 `U64BE(len(canonical(SafetyRecordV1))) || canonical(SafetyRecordV1)`；长度以 bounded reader 检查后才分配。恢复必须从 sequence 1 连续验证到 durable tail，拒绝 bit flip、重复/跳跃 sequence、断链、合法 CBOR 后尾随字节或同 slot 冲突。只有在 signer/HSM audit 证明最后一个完整 intent 之后没有签名调用时，才可丢弃未完成且从未 fsync 成功的尾 frame；否则截断、缺失或 checksum 错误一律撤销所有 signer readiness 并 `SAFETY_HALT`。压缩只能生成带旧 tail record hash、全部活跃 slot和 signer receipts 的新 checkpoint，经双副本校验与原 WAL 原子替换后进行；不得删除仍可能约束签名的记录。

### 4.2 统一签名前顺序

任何安全签名都遵循：

```text
验证规范对象与本地前置条件
  -> 查询 unique_slot
  -> 同 slot 同 digest：返回已保存签名
  -> 同 slot 异 digest：硬拒绝并告警
  -> append intent
  -> fsync / durable group commit
  -> 调用对应 key 签名
  -> append signature receipt
  -> 才允许发送
```

`DA_ACK_LOCK` 还要求完整 codeword 验证完成，且分配给本 Validator 的固定 fragment 与 `CODEWORD_VERIFIED` 已持久化。`VERTEX_SIGN_INTENT` 要求父引用、round jump、BatchAC 引用和作者 slot 全部有效。`EXECUTION_ATTESTATION_INTENT` 要求 stable-prefix 输入、父 Header/MMR state、父认证 `AccountMetadataState/AccountAuthState/AccountNonceState` 完整三元组下的确定性授权与 occurrence 筛选、并行/串行等价检查、computed Header、FinalizedBlockID 与当前 `block_mmr_root` 均完成，且 durable Prepared record 已 fsync。

`BATCH_AUTHOR_INTENT` 使用 DAG signing key ID，`semantic_digest=batch_id`，其 slot 是协议冻结的 `(epoch,author_index,batch_seq)`；同 slot 不同 BatchHeaderCore 永久冲突。author body 的 retention manifest 必须与该 intent 在签 BatchHeader 前 durable；ACK fragment 的 retention manifest 必须与 `DA_ACK_LOCK` 在签 ACK 前 durable。这样“作者唯一批次序号”和“删除前可审计保留义务”都跨崩溃成立。

stable-prefix 高度分配、`EPOCH_CLOSING_RESERVATION`、`EPOCH_CLOSING_INTENT`、`EPOCH_CLOSING_FENCE`、`EXECUTION_ATTESTATION_INTENT`、`EPOCH_SEAL_VOTE_LOCK` 与 `EPOCH_CLOSED` 必须进入同一个 Consensus signer 串行锁域。发现第一个 closing candidate C 时，在释放 C 的 `ORDER_FINAL`/执行前先 fsync reservation，立即禁止 C+1；确定性执行 C 后，从其 post-state 唯一选择 `PENDING_RECONFIG` 或 `SAME_CONFIG_ROLLOVER`，依次 fsync 完整 `EpochClosingIntentV1` 和引用 reservation/intent 的 fence，最后才允许 C 的 attestation intent/签名。恢复时有 reservation 而无 intent/fence只能重建并执行同一 C，不能另选 candidate；有 intent 而无 fence只能校验并补同一 fence。closing publication 只能把该 fence 原子提升为相同高度且同 `closing_intent_hash` 的 closed record；seal vote 只有在关闭记录与 statement 的 final height/block/intent完全一致时才可签。任一方向的冲突都进入 `SAFETY_HALT`，不能靠数据库最后写入者覆盖。

批量 fsync 可以合并多个 intent，但只有 group commit 明确成功后才能释放相应签名；不能先签后补 WAL。

## 5. Batch、fragment 与 BatchAC

- BatchHeader 与 fragment 以内容 hash 寻址；同 hash 不同字节是存储损坏。
- fragment 写入先落 staging，校验 Merkle path、index、长度和 codeword 后原子提升为 durable。
- `BatchAC` 单独保存规范 signer bitmap 与签名；不得只保存一个“已认证”布尔值。
- ACK signer 的 fragment 在 AC 仍可能参与尚未裁剪的 DAG/最终块恢复期间不得删除。
- 恢复得到完整 Batch 后必须重算 batch hash、重新编码并验证 fragment root，不能信任提供者的拼接顺序。

### 5.1 独立 Batch retention registry

Batch 保留义务在任何 certified publication 之前就可能产生，且从未被 causal stream 消费的 Batch 也必须保留到所属 epoch 关闭后的协议窗口。因此它不能借用只为 publication segment 定义的 `HistorySegmentManifestV1/HistoryGCRecordV1`。v1 冻结独立本地 schema：

```text
BatchRetentionManifestV1 {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  epoch: Epoch
  batch_id: Hash32
  holder_validator_index: ValidatorIndex
  obligation_kind: uint16
  fragment_index: optional ValidatorIndex
  canonical_stream_byte_length: uint64
  content_checksum: Hash32
  registration_safety_record_hash: Hash32
}

BatchGCRecordV1 {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  epoch: Epoch
  batch_id: Hash32
  batch_retention_manifest_id: Hash32
  terminal_height: Height
  retention_release_height: Height
  authorized_at_public_height: Height
  gc_sequence: uint64
  previous_gc_record_hash: Hash32
}
```

`obligation_kind` 只允许 `AUTHOR_BODY=1` 与 `ACK_FRAGMENT=2`。前者要求 `holder_validator_index=BatchHeaderCore.author_index`、`fragment_index` 缺失，规范流固定为：

```text
U64BE(len(canonical(BatchHeaderCore))) || canonical(BatchHeaderCore) ||
U64BE(len(canonical(BatchBody)))       || canonical(BatchBody)
```

后者要求 `holder_validator_index=fragment_index=ACK signer index`，规范流固定为：

```text
U64BE(len(canonical(BatchHeaderCore))) || canonical(BatchHeaderCore) ||
U64BE(len(canonical(BatchFragment)))   || canonical(BatchFragment)
```

两种流都必须恰好消费、无尾随字节，并重算 BatchID、body/transaction root或fragment item/path/root。author 在 BatchHeader 签名/广播前必须把 canonical body、kind 16 `BATCH_AUTHOR_INTENT` 和引用该 intent record hash 的 AUTHOR_BODY manifest 一起 fsync；ACK signer 在签 ACK 前必须把 canonical fixed fragment、`CODEWORD_VERIFIED`、`DA_ACK_LOCK` 和引用该 lock record hash 的 ACK_FRAGMENT manifest 一起 fsync。cache、peer重复副本和不承担上述两种义务的临时 repair bytes不进入 registry，可按本地水位清理。

```text
batch_retention_content_checksum = SHA256(
  U32BE(len("FINALWEAVE_BATCH_RETENTION_CONTENT_V1")) ||
  ASCII("FINALWEAVE_BATCH_RETENTION_CONTENT_V1") ||
  U64BE(canonical_stream_byte_length) || canonical_stream
)

batch_retention_manifest_id = SHA256(
  U32BE(len("FINALWEAVE_BATCH_RETENTION_MANIFEST_V1")) ||
  ASCII("FINALWEAVE_BATCH_RETENTION_MANIFEST_V1") ||
  U64BE(len(canonical(BatchRetentionManifestV1))) ||
  canonical(BatchRetentionManifestV1)
)

batch_gc_record_hash = SHA256(
  U32BE(len("FINALWEAVE_BATCH_GC_RECORD_V1")) ||
  ASCII("FINALWEAVE_BATCH_GC_RECORD_V1") ||
  U64BE(len(canonical(BatchGCRecordV1))) ||
  canonical(BatchGCRecordV1)
)
```

manifest 的 11 个字段和 GC record 的 11 个字段分别按所列顺序使用 deterministic-CBOR integer key `1..11`；`schema_version=1`，未知/缺失/重复字段、未知 kind、错误 optional presence与尾随字节拒绝。manifest一旦注册永久保留且不可改写；同一manifest ID的逐字节重放幂等，同一安全slot出现不同manifest/BatchID是`SAFETY_HALT`。

每个 Ledger 的 Batch GC chain 从 `gc_sequence=1/previous_gc_record_hash=ZERO_ID` 开始，之后 checked 加一并引用上一完整 record hash；每个 manifest最多一条 GC record。record必须匹配manifest的network/ledger/epoch/batch、已认证`EPOCH_CLOSED.final_height`，并令`retention_release_height=checked_add(terminal_height,batch_retention_heights)`、`authorized_at_public_height`等于append时public cursor且不小于release。加法溢出时永不允许生成record。还须满足全部未决引用、repair/export/Archive/pin和本地延长条件。

GC worker 先在互斥锁内验证流bytes与manifest checksum，再append+fsync record；这是该义务的逻辑释放线性化点，随后才能删除bytes和active ref。record前bytes缺失是不可掩盖的存储损坏；record后残留bytes只做幂等清理。`retained_referenceable_batch_bytes`精确等于所有没有GC record的本地manifest之 `checked_add(canonical_stream_byte_length,len(canonical(manifest)))` 总和；启动和每次变更均从registry/GC chain重算，首次消费、publication或当前未见引用都不减计数。

## 6. DAG 与 stable prefix

Vertex 分为两个不得混用的存储等级：

1. `dependency store` 按 `(ledger,epoch,round,author)` 建 slot 索引，值是按 VertexID 排序的集合，内容按 VertexID 保存。凡精确 ID 已被接受 Vertex、已验证证书/witness、committed anchor 引用，或已经进入其递归 `Past(P)` 的对象，完整验证后必须在关闭依赖边之前原子提升到这里；同槽所有被引用 sibling 都保留，不能覆盖。提升后不可降回旁路 cache，只能在不存在 undecided/candidate/recovery/retention 引用后按共识感知 GC 删除；
2. `unreferenced-sibling quarantine` 只接收签名/结构有效但尚未被任何共识对象按 ID 引用的旁路 gossip。它同时受 `per-slot=4`、`per-ledger objects=65_536`、`per-ledger canonical bytes=67_108_864` 三个 v1 绝对硬上限；本地可配置更低水位。满额时可以驱逐或暂拒缓存，绝不能把对象写入永久 invalid set。每 slot 的 evidence cache 只保留当前已观察有效集合里 VertexID 最小的两份完整对象，较小者到达时替换较大者；它计入同一预算且不参与共识。

只有诚实作者受“一 slot 一次签名”约束；因此“签名有效”本身不能创建无界 durable 保存义务。接收/拉取调度还必须按 `(ledger,epoch,author)` 预留独立 dependency-work 份额，防止 Byzantine author 通过无限 child/sibling 绕过 quarantine。旁路对象后来被精确引用时，先按 ID 查 cache，未命中则从引用来源、其他 Validator/Archive 重拉；完整验证并 fsync 提升后才把引用边标为 closed。驱逐只增加一次可重试 fetch，不改变 wire-validity。接收路径先写内容，再更新父/子和 round 索引；索引崩溃可重建。

提升流程使用本地 crash-consistent generation，而不是“先从 quarantine 删除、再尝试写 DAG”的移动：

```text
DependencyPromotionIntentV1 {
  promotion_id
  root_vertex_id
  trigger_kind        // ADMITTED_ROOT | VERTEX_REF | WITNESS_REF | CERT_REF | ANCHOR_REF
  trigger_id
  author_index
  phase               // FETCHING | CLOSURE_VERIFIED | INSTALLED
}

DependencyEdgeClosedV1 { promotion_id, child_vertex_id, parent_vertex_id }
DependencyRootInstalledV1 { promotion_id, root_vertex_id, closure_digest }
```

这些是带 checksum/sequence 的本地记录，不是 wire schema或共识 hash。每个对象先按 VertexID 写 content-addressed staging并 fsync，再append edge-closed；完整闭包验证后，在一个storage transaction中增加dependency pins、写root-installed并把root暴露给decider，最后才允许清理重复quarantine bytes。崩溃恢复只可继续 FETCHING、复验并安装 CLOSURE_VERIFIED，或幂等重放 INSTALLED；绝不能出现edge已关闭但对象缺失。未 INSTALLED generation可在author工作预算/保留策略下暂停并回收staging，但不得写invalid tombstone；同一exact ID晚引用可重新建立promotion。INSTALLED对象的pin只能由共识感知GC释放。

需要持久化或可确定重算：

- 每个 slot 的 direct/skip/undecided 状态及最小证据引用；
- sticky support 的来源 Vertex；
- S&P26 restricted round jump 验证结果；
- stable slot prefix cursor；
- 从上一个 stable cursor 到新 cursor 的 canonical delta。

direct 与 skip 对同一 slot 同时成立是 P0 协议不变量错误，不能以“先看到哪个”为准。stable prefix 每次扩展必须以前一 prefix 为前缀；发现 prefix disagreement 时停止执行和 attestation。

## 7. 排序到执行

stable slot prefix 的新增部分先生成本地不可变 `BlockDerivationCandidate`：

```text
BlockDerivationCandidate {
    network_id
    ledger_id
    epoch
    height
    parent_block_id
    parent_state_root
    parent_block_mmr_state
    committed_slot
    proposer_vertex_id
    stable_prefix_end
    causal_input_manifest_id
    causal_input_item_count
    causal_input_stream_byte_length
    ordered_vertex_ids
    ordered_batch_ids
    canonical_occurrences
    dag_commit_witness_ref optional
    validator_set_hash
    protocol_config_hash
}
```

`ordered_vertex_ids` 包含 causal delta 的依赖闭包中每一个不同 VertexID，包括同一 `(epoch,round,author)` 已提升分支的全部 equivocation；不包含未引用 quarantine 或纯本地 evidence cache。被引用闭包有任一对象未取回/验证/提升时不得生成 manifest。`ordered_batch_ids` 是随这些 Vertex 的规范 AvailabilityReference 顺序展开的 occurrence list，不是按 BatchID 去重的集合。`canonical_occurrences` 进一步按 BatchBody 内顺序展开交易，同一 Batch 或 transaction 的多次引用仍形成多个 raw occurrence；evidence 可以另外记录，但不能替代或删除任一已提升分支的 payload occurrence。

这些数组是逻辑视图。生产存储必须从协议 `CausalInputManifest` 的 `causal_input_manifest_id`、item count 和 stream byte length 驱动固定 1 MiB chunk 流，不要求整体驻留内存。每个 `CausalInputChunk` 在写 staging 前先验证 manifest ID、core hash 与 `siblings_bottom_up`；framed item 可跨 chunk，解码器不得按未验证长度预分配。P2P v1 直接用 bounded raw reader 读取 canonical envelope：最多接受 `1_050_823` bytes，只探测第 `1_050_824` byte 判定越界，且不得实例化解压器；HTTP Gateway Content-Encoding 或未来明确版本启用压缩时，才由 streaming decompressor/running counter 对解压输出执行同一 `MAX+1` 边界。第一个越界 byte 不得进入 parser/staging，任何按声明 payload/sibling 长度的大对象分配都必须发生在硬上限验证之后。

FinalizedBlockBody 只保存 `ordered_vertex_count`，不复制 `ordered_vertex_ids`。完整 ID/occurrence sidecar 保持在 causal-input generation 中，ordered-vertex leaf file 和 Header root 由它增量产生。这样任意大的有限 delta 都不会仅因 Body 的绝对字节上限而失去表示；发布时 Body、ordered-vertex proof 和 raw causal source 分别成为受管 history segment。GC 若删除 raw chunks，必须永久保留 `CausalValidationRecordV1`、核心中的 manifest/count/root 和对应 segment manifest/GC record；需要返回 ordered-vertex inclusion proof 的节点还必须按保留策略在线保存或从 Archive 重建对应叶与路径。

流式派生使用本地 durable progress：

```text
CausalInputProgress {
    generation_id
    causal_input_manifest_id
    next_chunk_index
    next_stream_byte_offset
    verified_item_count
    verified_ordered_vertex_count
    verified_occurrence_count
    framed_decoder_checkpoint
    occurrence_filter_checkpoint_hash
    temporary_leaf_files_hash
    status: STAGING | COMPLETE | ACTIVATED
}
```

每次 checkpoint 只能落在完整验证的 chunk 边界，且必须绑定 decoder carry bytes、规范过滤器状态和临时 leaf files；无法验证 checkpoint 时丢弃该 staging generation 并从已认证 manifest 重放。chunk验证/下载cursor与occurrence-filter cursor是两个不同进度：一个大item跨chunk时，前者可以位于当前frame内部或之后，而后者仍固定在`CausalOccurrenceCursorV1.frame_start_byte_offset`。只要存在in-flight scan/common/source attempt，覆盖该frame起点至当前verified offset的已验证staging chunks就不得回收；恢复必须从frame起点重新喂入bounded decoder并逐字节重验既有chunk proof/source binding，而不是把chunk cursor误当成已完成occurrence。若所需staging bytes丢失，只能从同一manifest重新取得并验证，或回滚到完整occurrence边界，不能保留已扣预算却从frame中间继续。

写 `GloballyEmittedVertices` 时使用不可见、copy-on-write 的 `DerivationStateGenerationV1`，验证 count/root 后再用单个原子 activation marker 同时推进认证 stable cursor；崩溃前未激活 generation 不参与后续 delta，避免“半个 Past 已消费”。

```text
DerivationStateBuilderV1 {
  builder_id: GenerationID
  network_id: NetworkID
  ledger_id: LedgerID
  epoch: Epoch
  target_committed_slot: ProposerSlot
  base_generation_id: GenerationID | NONE
  staged_sparse_set_nodes_checksum: Hash32
}

DerivationStateGenerationV1 {
  schema_version: uint16
  generation_id: GenerationID
  network_id: NetworkID
  ledger_id: LedgerID
  epoch: Epoch
  target_height: Height
  target_finalized_block_id: Hash32
  target_committed_slot: ProposerSlot
  base_generation_id: GenerationID | NONE
  epoch_emitted_vertex_count: uint64
  epoch_emitted_vertex_set_root: Hash32
  sparse_set_nodes_checksum: Hash32
  generation_manifest_checksum: Hash32
}
```

两者都是本地 storage schema，不是 wire 对象。尚未产生 FinalizedBlockID 时只能写可丢弃的 `DerivationStateBuilderV1`；它不含 target height/block ID、不是 generation、不可被查询或下一候选当作父集合。Header/FinalizedBlockID 算出后，以 builder 内容一次性封存不可变 `DerivationStateGenerationV1`，其中 target height/block ID 必须精确匹配该 Header，所有字段此后不可修改。签 `ExecutionAttestation` 前必须把完整 sparse-set nodes、manifest、count/root 和 generation fsync，并在 `DurablePreparedRecord` 中绑定 ID。获得 FinalityCertificate 后，publication 事务只把 active pointer 与 state/MMR/public cursor 原子切向它；失败候选丢弃 builder 或未激活 generation。epoch 首块的 base 为该 epoch 规范空树而非旧 epoch generation。authenticated lookup 必须返回 `PRESENT | ABSENT | CORRUPT_OR_MISSING`，只有完整 path 能重建到认证 root 时才允许前两种结果。

候选使用本地 `derivation_candidate_digest` 做 staging identity；它不是协议 ID、不得签名或跨节点信任。此时没有 `FinalizedBlockID`，因为 results、state root 和 Header 尚未产生。

完成 causal 验证和执行、取得 FinalityCertificate 并组装 certified publication 时，存储层按协议第四篇逐字段写 `CausalValidationRecordV1`。它必须绑定 manifest ID/chunks root、声明与 consumed 的 byte/chunk/item/Vertex/occurrence counts、Header 的 committed slot、epoch emitted count/root、`DerivationStateGenerationV1` ID、ordered-vertex/transaction/receipt/event/state roots、FinalizedBlockID、当前 block MMR root、publication generation ID、`validation_complete=true` 和 `generation_content_checksum`。已验证的 canonical `CausalInputManifestCore` 必须进入下述不可裁剪核心，使恢复可在本地重算 manifest ID 并逐字段匹配 record，而无需原始 chunks/source。Prepared 阶段可以持久化生成这些字段所需的 roots/counts，但在证书和最终 publication generation 尚未确定前不得伪造 complete record 或最终 checksum。

### 7.1 不可裁剪的 CertifiedCoreGeneration

每个已公开高度都有一个永久的 `CertifiedCoreGenerationV1`。它是本地 storage schema，不是 wire 共识对象；物理布局可变，但逻辑覆盖集合冻结。至少包含：

```text
CertifiedCoreGenerationV1 {
  schema_version: uint16
  generation_id: GenerationID
  network_id: NetworkID
  ledger_id: LedgerID
  height: Height
  core_entries: [GenerationChecksumEntryV1]
  generation_content_checksum: Hash32
}
```

- canonical `CausalInputManifestCore`、Header、FinalityCertificate 和相应 epoch/config 内容引用；
- height、parent/block/finality ID、全部 manifest counts、Header roots、state root、当前 block MMR root，以及维持 MMR/父链连续性所需的不可变元数据；
- finalized slot、认证 derivation cursor、epoch emitted count/root、active derivation generation ID、terminal-status 等发布事实；
- 本次原子发布所注册的 `HistorySegmentManifestV1` 及其稳定 segment ID；
- 按下式严格排序的核心 checksum entries 与 `generation_content_checksum`。

`CausalValidationRecordV1`、`CertifiedPublishMarkerV1` 和前者的独立 checksum 随这个核心永久保存，但为避免自引用，不进入 `generation_content_checksum`。当前 tip 的权威 state data 必须保持可读并验证到核心所绑定的 `state_root`；当前 epoch active derivation generation 的完整 authenticated set nodes 也必须保持可读并验证到核心的 emitted root/count，且永不作为普通 HistorySegment 裁剪。旧版本 SMT/set 节点、未注册 state-change proof cache 或重放材料不是核心，可由快照、DAG checkpoint 或区块重建；只有在首次 publication 中显式注册的历史状态证明包才成为受管 history segment。核心永远不包含 Body bytes、transaction/result/Receipt/Event payload、ordered-Vertex 叶/路径、DAG witness、raw causal chunks、已消费 DAG/Batch source、历史状态节点、查询索引、物理 SST/page、可变 active pointer 或任何 `HistoryGCRecordV1`。因此合法归档、索引重建、compaction 和 history GC 均不会改变核心 checksum。

storage schema v1 用逻辑内容而不是数据库页布局冻结核心 checksum。仅为下列本地 checksum，`CausalValidationRecordV1`、`CertifiedCoreGenerationV1`、`GenerationChecksumEntryV1`、`CertifiedPublishMarkerV1`、`HistorySegmentManifestV1` 与 `HistoryGCRecordV1` 使用协议 deterministic-CBOR 子集，并按各自规范字段顺序将字段编号为正整数 key `1..N`；未知/缺失/重复字段都拒绝。`CertifiedCoreGenerationV1.schema_version=1`，其 envelope 自身不作为 core entry；checksum 只按 `core_entries` 计算并必须逐字节等于 envelope 与 validation record 中的值。对核心中每个不可变逻辑 KV 构造：

```text
GenerationChecksumEntryV1 {
  logical_domain: byte_string   // 本文第 2 节的稳定 ASCII domain 名
  logical_key: byte_string      // 该 domain 的规范逻辑 key
  value_hash: Hash32
}

value_hash = SHA256(U64BE(len(canonical_value_bytes)) || canonical_value_bytes)

generation_content_checksum = SHA256(
  U32BE(len("FINALWEAVE_GENERATION_CONTENT_V1")) ||
  ASCII("FINALWEAVE_GENERATION_CONTENT_V1") ||
  U64BE(entry_count) ||
  concat(
    U64BE(len(canonical(entry_i))) || canonical(entry_i)
  )
)

causal_validation_record_checksum = SHA256(
  U32BE(len("FINALWEAVE_CAUSAL_VALIDATION_RECORD_V1")) ||
  ASCII("FINALWEAVE_CAUSAL_VALIDATION_RECORD_V1") ||
  U64BE(len(canonical(CausalValidationRecordV1))) ||
  canonical(CausalValidationRecordV1)
)
```

entries 必须按 `(logical_domain bytes,logical_key bytes)` 严格升序且拒绝重复；entry count、所有长度前缀与 framed 总和都用 checked `uint64`，溢出使 generation 无效。`canonical_value_bytes` 是对象的 deterministic-CBOR 或该 storage domain 明确定义的规范字节，不是 SST/page/压缩容器。改变逻辑 domain/key/value 编码、核心 entry 覆盖集合或 checksum 算法必须迁移 storage schema；仅改变物理文件布局、compaction、压缩、history segment 在线/离线状态或 GC 记录不得改变 checksum。

### 7.2 内容寻址的 HistorySegment

每个可能被本地策略裁剪、但已作为 certified publication 一部分公开的 payload 必须在首次原子发布前注册为受管 `HistorySegmentV1`；发布后不得向该不可变核心补加 segment。禁止把可裁剪 payload 直接混进核心 checksum。storage schema v1 注册以下 `segment_kind`：`FINALIZED_BODY=1`、`RECEIPT_EVENT_PROOF=2`、`ORDERED_VERTEX_PROOF=3`、`DAG_COMMIT_WITNESS=4`、`HISTORICAL_STATE_PROOF=5`、`CAUSAL_SOURCE=6`。后续版本新增 kind 必须升级 storage schema，不能复用数值。异步、可重建且未进入首次 publication 的查询索引/proof cache 不是受管 history segment，丢失时重建即可；若希望受 segment 完整性和 Archive SLA 保护，必须在首次发布时形成对应 kind 的 manifest 并进入核心。

```text
HistorySegmentManifestV1 {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  height: Height
  segment_kind: uint16
  canonical_stream_byte_length: uint64
  item_count: uint64
  chunk_count: uint64
  content_root: Hash32
  content_checksum: Hash32
}

HistorySegmentV1 {                 // 逻辑流对象，不要求整体驻留或编码成单个 CBOR blob
  manifest: HistorySegmentManifestV1
  canonical_stream: byte_stream
}

content_checksum = SHA256(
  U32BE(len("FINALWEAVE_HISTORY_SEGMENT_CONTENT_V1")) ||
  ASCII("FINALWEAVE_HISTORY_SEGMENT_CONTENT_V1") ||
  U64BE(canonical_stream_byte_length) || canonical_stream
)

history_segment_id = SHA256(
  U32BE(len("FINALWEAVE_HISTORY_SEGMENT_ID_V1")) ||
  ASCII("FINALWEAVE_HISTORY_SEGMENT_ID_V1") ||
  U64BE(len(canonical(HistorySegmentManifestV1))) ||
  canonical(HistorySegmentManifestV1)
)

history_chunk_leaf_i = SHA256(
  U32BE(len("FINALWEAVE_HISTORY_SEGMENT_CHUNK_LEAF_V1")) ||
  ASCII("FINALWEAVE_HISTORY_SEGMENT_CHUNK_LEAF_V1") ||
  U64BE(i) || U64BE(len(chunk_i)) || chunk_i
)

history_chunk_node = SHA256(
  U32BE(len("FINALWEAVE_HISTORY_SEGMENT_CHUNK_NODE_V1")) ||
  ASCII("FINALWEAVE_HISTORY_SEGMENT_CHUNK_NODE_V1") || left || right
)

content_root =
  if chunk_count == 0:
    SHA256(U32BE(len("FINALWEAVE_HISTORY_SEGMENT_ROOT_V1")) ||
           ASCII("FINALWEAVE_HISTORY_SEGMENT_ROOT_V1") ||
           U64BE(0) || U64BE(0))
  else:
    SHA256(U32BE(len("FINALWEAVE_HISTORY_SEGMENT_ROOT_V1")) ||
           ASCII("FINALWEAVE_HISTORY_SEGMENT_ROOT_V1") ||
           U64BE(canonical_stream_byte_length) ||
           U64BE(chunk_count) || merkle_top)
```

`HistorySegmentManifestV1.schema_version=1`。`canonical_stream` 是该 kind 按 storage schema 定义、由 `U64BE(item_length) || canonical_item` 连接的逻辑流；decoder 必须恰好消费 `item_count` 项和 `canonical_stream_byte_length` bytes，不允许截断、尾随字节或未验证长度分配。流固定按 1 MiB 切 `chunk_i`，非末 chunk 必须恰好 1 MiB，`chunk_count=ceil(byte_length/1 MiB)`，空流的 count 为 0。`merkle_top` 从 chunk leaves 自底向上合并，奇数层末节点复制自身。所有计数、长度与 offset 使用 checked `uint64`。实现必须流式重算 `content_checksum`、`content_root` 和 kind 对应的 Header/Receipt/MMR/SMT 承诺；仅文件 checksum 正确而协议 root 不匹配仍是损坏。manifest 和 segment ID 进入核心且永久保留，payload 可位于热存储、冷存储或 Archive。

### 7.3 耐久 publication marker 与 HistoryGCRecord

storage schema v1 冻结 publication marker；不能用开放字段 map、数据库文件名或实现私有 struct 参与 hash。受管 segment 的逻辑裁剪再通过 append-only、hash-chained GC record 表达：

```text
HistorySegmentRefV1 {
  segment_kind: uint16
  history_segment_id: Hash32
}

CertifiedPublishMarkerV1 {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  height: Height
  previous_publication_marker_kind: uint16
  previous_publication_marker_hash: Hash32
  parent_block_id: Hash32
  finalized_block_id: Hash32
  finality_id: Hash32
  causal_input_manifest_id: Hash32
  consumed_stream_byte_length: uint64
  consumed_chunk_count: uint64
  consumed_item_count: uint64
  consumed_ordered_vertex_count: uint64
  consumed_occurrence_count: uint64
  finalized_slot: ProposerSlot
  certified_slot_cursor: ProposerSlot
  epoch_emitted_vertex_count: uint64
  epoch_emitted_vertex_set_root: Hash32
  derivation_state_generation_id: GenerationID
  ordered_vertex_root: Hash32
  transaction_root: Hash32
  receipt_root: Hash32
  event_root: Hash32
  state_root: Hash32
  parent_block_mmr_root: Hash32
  block_mmr_root: Hash32
  generation_id: GenerationID
  generation_content_checksum: Hash32
  causal_validation_record_checksum: Hash32
  initial_history_segments: [HistorySegmentRefV1]
}

SnapshotInstallMarkerV1 {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  install_sequence: uint64
  previous_active_marker_kind: uint16
  previous_active_marker_hash: Hash32
  previous_active_height: Height
  trust_anchor_kind: uint16
  trust_anchor_id: Hash32
  target_epoch: Epoch
  target_height: Height
  target_finalized_block_id: Hash32
  target_finality_id: Hash32
  target_finality_certificate_envelope_hash: Hash32
  target_validator_set_hash: Hash32
  target_protocol_config_hash: Hash32
  target_state_root: Hash32
  target_block_mmr_root: Hash32
  target_committed_slot: ProposerSlot
  target_epoch_emitted_vertex_count: uint64
  target_epoch_emitted_vertex_set_root: Hash32
  snapshot_manifest_id: Hash32
  dag_derivation_checkpoint_manifest_id: Hash32
  state_generation_id: GenerationID
  state_generation_checksum: Hash32
  derivation_state_generation_id: GenerationID
  derivation_state_generation_checksum: Hash32
  block_mmr_peaks_checksum: Hash32
  proof_bundle_checksum: Hash32
}

QuerySnapshotInstallMarkerV1 {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  install_sequence: uint64
  previous_active_marker_kind: uint16
  previous_active_marker_hash: Hash32
  previous_active_height: Height
  trust_anchor_kind: uint16
  trust_anchor_id: Hash32
  target_epoch: Epoch
  target_height: Height
  target_finalized_block_id: Hash32
  target_finality_id: Hash32
  target_finality_certificate_envelope_hash: Hash32
  target_validator_set_hash: Hash32
  target_protocol_config_hash: Hash32
  target_state_root: Hash32
  target_block_mmr_root: Hash32
  target_committed_slot: ProposerSlot
  target_epoch_emitted_vertex_count: uint64
  target_epoch_emitted_vertex_set_root: Hash32
  snapshot_manifest_id: Hash32
  state_generation_id: GenerationID
  state_generation_checksum: Hash32
  block_mmr_peaks_checksum: Hash32
  proof_bundle_checksum: Hash32
}

HistoryGCRecordV1 {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  height: Height
  segment_kind: uint16
  history_segment_id: Hash32
  certified_publish_marker_hash: Hash32
  authorized_at_public_height: Height
  gc_sequence: uint64
  previous_gc_record_hash: Hash32
}

history_gc_record_hash = SHA256(
  U32BE(len("FINALWEAVE_HISTORY_GC_RECORD_V1")) ||
  ASCII("FINALWEAVE_HISTORY_GC_RECORD_V1") ||
  U64BE(len(canonical(HistoryGCRecordV1))) ||
  canonical(HistoryGCRecordV1)
)

certified_publish_marker_hash = SHA256(
  U32BE(len("FINALWEAVE_CERTIFIED_PUBLISH_MARKER_V1")) ||
  ASCII("FINALWEAVE_CERTIFIED_PUBLISH_MARKER_V1") ||
  U64BE(len(canonical(CertifiedPublishMarkerV1))) ||
  canonical(CertifiedPublishMarkerV1)
)

snapshot_install_marker_hash = SHA256(
  U32BE(len("FINALWEAVE_SNAPSHOT_INSTALL_MARKER_V1")) ||
  ASCII("FINALWEAVE_SNAPSHOT_INSTALL_MARKER_V1") ||
  U64BE(len(canonical(SnapshotInstallMarkerV1))) ||
  canonical(SnapshotInstallMarkerV1)
)

query_snapshot_install_marker_hash = SHA256(
  U32BE(len("FINALWEAVE_QUERY_SNAPSHOT_INSTALL_MARKER_V1")) ||
  ASCII("FINALWEAVE_QUERY_SNAPSHOT_INSTALL_MARKER_V1") ||
  U64BE(len(canonical(QuerySnapshotInstallMarkerV1))) ||
  canonical(QuerySnapshotInstallMarkerV1)
)
```

`CertifiedPublishMarkerV1` 的 31 个字段、`SnapshotInstallMarkerV1` 的 29 个字段、`QuerySnapshotInstallMarkerV1` 的 26 个字段分别按代码块顺序使用 integer key `1..N`；所有 schema version 固定为 1，未知/缺失/重复字段拒绝。`initial_history_segments` 按 `(segment_kind,history_segment_id)` 严格升序且无重复。`finalized_slot` 与 `certified_slot_cursor` 必须都等于 Header committed slot；epoch emitted count/root 与 derivation generation 必须逐项等于 Header、validation record 和核心。此前 SKIP 被本高度 q 个 attestation 间接认证；最后一个 COMMIT 之后尚无后继 COMMIT 认证的 trailing SKIP 只能作为可回滚本地 checkpoint，不能写入 marker。

`previous_publication_marker_kind` 只允许：`0=NONE`、`1=CERTIFIED_PUBLISH`、`2=SNAPSHOT_INSTALL`。从创世连续执行的 height 1 固定使用 `NONE/ZERO_ID`；普通连续高度使用 `CERTIFIED_PUBLISH` 并逐字节引用上一高度的 `certified_publish_marker_hash`；从 target height H 的 full-validator 快照基线继续时，H+1 使用 `SNAPSHOT_INSTALL` 并引用该 H 的 `snapshot_install_marker_hash`，此后恢复为普通 marker 链。其他 kind/hash、目标高度不连续、snapshot target Header 与当前 parent 不相等都拒绝。这样快速同步节点无需伪造本地 1..H marker。

`SnapshotInstallMarkerV1` 只用于已经同时验证并安装状态 Snapshot 与 DAGDerivationCheckpoint 的 full-validator 基线；`QuerySnapshotInstallMarkerV1` 只安装 state/MMR 并永久把该 active generation 标为 `QUERY_ONLY/SYNCING_DERIVATION`，没有 derivation generation，不能作为 `CertifiedPublishMarkerV1` 的 previous publication anchor。`trust_anchor_kind` 只允许 `1=GENESIS_REFERENCE` 或 `2=CHECKPOINT_TRUST_ANCHOR`，ID 必须等于本地请求前选定的 trust-store 项。target 全部字段必须等于所选 proof、Header 和 Snapshot manifest；full marker 还必须逐项等于 DAG checkpoint manifest。FinalityCertificate envelope、proof bundle、MMR peaks、state generation 和可选 derivation generation 分别流式重算对应 checksum，generation 必须重建到 target roots。marker 不把响应方自报 checkpoint 变成信任锚。

三种 active marker kind 固定为 `0=NONE`、`1=CERTIFIED_PUBLISH`、`2=FULL_SNAPSHOT_INSTALL`、`3=QUERY_SNAPSHOT_INSTALL`。snapshot 安装与 certified publication 使用同一 per-ledger 串行锁和 compare-and-swap 域。只有 Ledger 尚无任何 active marker 时，首个 install 才固定 `install_sequence=1/NONE/ZERO_ID/previous_height=0`；已有 certified publication 但从未安装 snapshot 时，sequence 仍为 1，但 previous ref 必须是锁内当前 certified marker。之后 `install_sequence = checked_add(last_snapshot_install_sequence,1)`，其中该 sequence 随每个后继 certified active pointer 继承，previous ref 始终逐字节等于锁内当前 active marker kind/hash/height。通常 `target_height > previous_active_height`；唯一等高转换是 QUERY → FULL，且两个 marker 的 target Header/Snapshot/state/MMR 字段必须完全相同。相同 sequence/previous ref 的不同 marker、两个并发 target 共用同一 previous ref、sequence 跳跃/回退或更低目标均 `SAFETY_HALT`，不能“选最高高度”。因此多个 marker 在 pointer CAS 前 durable 时仍由唯一 hash chain 决定顺序。

`certified_publish_marker_hash` 或经 kind 标记的 snapshot marker hash 是 publication/active chain 下一项使用的唯一值，不能另取数据库 key、文件名或 active-pointer 摘要。同一 `(ledger,height,previous_publication_marker_kind,previous_publication_marker_hash)` 只允许一个 certified marker hash；snapshot 唯一键包含 `(ledger,install_sequence,previous_active_marker_kind,previous_active_marker_hash)`。逐字节相同重放幂等，不同 marker 是存储分叉并 `SAFETY_HALT`，唯一例外是下一 sequence、显式引用 query marker、同 target 字段完全相等的规范 QUERY→FULL 升级；它不是同一唯一键。`HistoryGCRecordV1.schema_version=1`；每个 `(network_id,ledger_id)` 的首条 `gc_sequence=1` 且 `previous_gc_record_hash=ZERO_ID`，之后 sequence 必须 checked 加一、previous hash 必须等于上一条 record hash。一个 segment ID 最多有一条 record，record 的 network/ledger/marker/height/kind 必须逐项匹配永久核心注册项；`authorized_at_public_height` 必须等于 append 时的 durable public cursor，且不得低于 segment height。

certified marker、两类 snapshot-install marker 或等价单数据库事务 commit 是各自 publication/install 的唯一线性化点。commit-marker backend 必须先 fsync 完整 staging，再 append/fsync marker；任一种 marker durable 后，其 generation 已逻辑提交，不允许丢弃或回滚。`active_generation` 只是可重建的加速指针：正常进程在开放读路径前原子指向 marker；若断电恰好发生在 marker fsync 后、pointer CAS 前，恢复器必须验证 certified marker 的 core/segments/state/derivation，full snapshot marker 的 proof/manifests/MMR/state/derivation，或 query marker 的 proof/Snapshot/MMR/state 后按唯一 previous-active chain 幂等 roll-forward pointer，不能把它当无 marker staging。写下一 marker 前必须完成该 roll-forward。单数据库 backend 则以包含 commit record 和 active pointer 的同一事务 commit 为线性化点。

GC worker 在 segment 级互斥锁内重验协议窗口、引用、Archive 策略、manifest/segment ID 和 publication marker 后，先 append record 并 fsync；该 durable append 是逻辑裁剪的线性化点，恢复时据此把 segment 目录幂等置为 `PRUNED`，随后才可删除 payload bytes。record durable 前删除任何字节都属于存储损坏。record durable 后即使 bytes 尚未物理删除，查询也按 `HISTORY_PRUNED` 处理，避免崩溃使语义随残留文件变化。v1 的 GC 状态单调，不删除或撤销 record；从 Archive 重新取得同 ID bytes 仍须独立验证，且不能静默把本地权威目录改回 `PRESENT`。GC record、核心、marker 和 segment manifest 永久保留；GC 不重写核心清单、marker 或 `generation_content_checksum`。

随后冻结父认证状态中以 AccountAddress 为 raw key 的 `AccountMetadataState/AccountAuthState/AccountNonceState` 完整三元组；任一残缺三元组立即 `EXECUTION_HALT`。按 raw occurrence 顺序，先从已验证containing signed DAGVertex取得occurrence sponsor、从BatchHeader另取Batch author，并按完整tagged item长度向sponsor收取scan work；`PREFILTER_SCAN_CAP`只能完成固定chunk source stream compare，不得解码Envelope。scan成功后才运行 bounded canonical/cheap context、窗口、accepted-winner `tx_id`、块开始 active policy hash、exact `next_nonce` 与区块 count/Gas/resource reserve 前缀；只有仍可能成为 winner 的 candidate 才向同一sponsor扣取通用昂贵suffix work，执行 Envelope signatures、strict public keys、payload registry、governance approvals 与完整 reconfiguration bundle。Batch author可以不同但只认证来源。普通账户只有 Envelope policy hash 等于高度 h 已激活策略且 charged signatures 满足该策略时才能参与；唯一不存在账户例外是通过 `ACCOUNT_CREATE_V1` 地址 core、initial policy、`nonce=0`、空用户 scope、三项不存在、三个 `EXACT WRITE` system access 和 Gas operation `0x00010001` 完整 trace 预检的自证创建。同一块用独立 created-address set 防止第二个创建 winner，且普通交易仍看不到刚创建账户。创建成功时 immutable meta/auth/nonce 三项在同一 state journal 原子写入并把 next nonce 置 1；普通模块和用户 scope 不得写/删除这三个 namespace；预检失败不产 Receipt，本地写入失败不得留下部分三元组或签 attestation。h 内成功的策略轮换固定写 `pending_effective_height=h+1`。其他 `AUTH_INVALID_OCCURRENCE` 被跳过且不产 Receipt、不耗 nonce、不写 accepted set。通过前置条件后还必须用 checked arithmetic 预留其声明 `gas_limit`、Envelope、最大失败结果和 mandatory writes；若加入后超过任一 block cap，则标记本地派生原因 `BLOCK_CAP` 并跳过，同样不产 Receipt、不耗 nonce、不写 accepted set，因此较晚的更小交易仍可能成为同 nonce winner。`PREFILTER_VERIFY_CAP` 同样不成为 winner，且不能在未获得预算时启动昂贵后缀。`accepted_tx_ids_in_block` 只在 occurrence 真正成为 winner 后写入；任何较早跳过都不能仅因 tx id 已见而阻止后续 raw occurrence 重新走完整判断，其中随扫描推进而可能转为 winner 的典型情形是 future nonce。不得保存或查询 nonce-winner 映射，也不得维护可裁剪的历史 tx-id/intent seen 状态。

所有 occurrence-filter checkpoint 都必须绑定当前 causal item cursor、可选的`OccurrenceScanAttemptV1`、逐sponsor的completed-scan reserved累计量与completed-scan shared累计量、`attempted_prefilter_tx_results` exact map、每个 authenticated occurrence sponsor 的通用 prefilter reserve spend、shared spend与 checked 总 spend。in-flight scan记录绑定当前cursor、`OccurrenceScanSourceBindingV1` hash、`sponsor_author_index`、item length、scan work与charge receipt；source binding必须同时承诺containing Vertex author和Batch author，前者从签名Vertex重验并作为唯一sponsor，后者从BatchHeader重验但不扣款。记录建立必须与扣款原子，cursor只在完整occurrence完成、receipt并入completed累计量并清除记录时推进。common map值必须完整保存`STARTED|VALID|INVALID`、origin cursor、sponsor、cost与charge receipt；总spend必须能由completed累计量、可选in-flight receipt和全部common-attempt receipt逐sponsor/shared重算。激活跨账本时还要绑定 `working_consumption_keys`、`cross_ledger_proof_attempts` exact map、source-proof per-sponsor reserve/shared/total spend；source map还保存仅VALID存在的canonical verified artifact及其digest，source总spend由其全部receipt重算。任何`STARTED`都必须指向当前cursor，恢复时重跑对应worker但不再次扣费；本地超时、取消或存储错误不能改写成INVALID，cursor也不能越过它。

exact set/map 可用排序 spill file + checksum持久化，不能用 Bloom 恢复拒绝语义；checkpoint 必须绑定 active ValidatorSet/ProtocolConfig/FeatureSet/GasSchedule 内容 ID，以及“已验证containing signed DAGVertex → occurrence sponsor”和“已验证BatchHeader → Batch source author”两套独立映射。崩溃后若从中间 cursor 续扫，缺失、损坏、attempt/artifact digest失败或对象 ID 不一致时只能回滚到一个完整的completed-occurrence checkpoint；若没有这种checkpoint，则从该 FinalizedBlock causal stream开头重建。不能保留中间cursor却把任一预算、in-flight scan或attempt map清零，否则无效Envelope/bundle/proof会被重复解码/验签并可能改变后续`PREFILTER_SCAN_CAP`、`PREFILTER_VERIFY_CAP`或`CROSS_LEDGER_VERIFY_CAP`分类。

## 8. PreparedExecution 与 attestation

```text
PreparedExecution {
    causal_input_manifest_id
    causal_input_item_count
    causal_input_consumed_count
    causal_input_generation_id
    finalized_block_body
    parent_state_root
    exact_access_sets
    dependency_graph
    speculative_results
    certified_tx_index_prefix
    authoritative_fallback_start
    occurrence_filter_final_checkpoint
    state_write_batch
    computed_header
}

DurablePreparedRecord {
    derivation_candidate_digest
    derivation_state_generation_id
    target_committed_slot
    epoch_emitted_vertex_count
    epoch_emitted_vertex_set_root
    causal_input_manifest_id
    causal_input_item_count
    causal_input_consumed_count
    causal_input_generation_id
    occurrence_filter_final_checkpoint_hash
    body_blob_generation_id
    finalized_block_body
    computed_header
    finalized_block_id
    block_mmr_state
    block_mmr_root
    finality_statement
    state_write_batch_hash
}
```

执行器先验证 `BlockMMRRoot(parent_block_mmr_state)` 等于父证书 statement 的 `block_mmr_root`，完成 Body/results 和 Header，并把同一值写入 Header 的 `parent_block_mmr_root`。随后计算 `FinalizedBlockID`，以 `DomainHash("BLOCK_MMR_LEAF", network_id, ledger_id, canonical({height,finalized_block_id}))` 构造当前 leaf，追加到父 MMR peaks 得到当前 `block_mmr_root`；该当前 root 进入 `FinalityStatement`，不回填 Header。

只有 `causal_input_consumed_count == causal_input_item_count`、progress 为 COMPLETE 且 manifest/计数/leaf roots 全部复验后，才可形成 PreparedExecution。执行 scratch 可随时删除；签署 attestation 前必须把 `DurablePreparedRecord`、完整 Body 的不可变流式 blob generation、可恢复 state write batch 和 PREPARED derivation generation durable 保存。record 中 slot/count/root 必须等于 computed Header，generation 必须从认证父 root 对完整 delta 做 non-membership+insert 后重建到该 root。相同 derivation candidate 重算出不同 causal manifest、derivation root/count、Header、FinalizedBlockID、state root 或 block MMR root 时必须安全停机，不能覆盖旧结果。

`ExecutionAttestation` 可以先于完整 SMT 节点写入最终列，但前提是 durable record、可恢复 state write batch 和 `EXECUTION_ATTESTATION_INTENT` 均已 fsync。签名只使用 Consensus Key；DAG Key 不得签 attestation。这样重启后能恢复同一 statement，不能签第二个结果。

## 9. FinalityCertificate 与原子状态提交

收到证书后先独立验证：epoch validator set、`q=2f+1`、不同 signer、Ed25519 Consensus Key 签名、FinalityStatement 与 Header/FinalizedBlockID/current block MMR root 的精确关系、父链/MMR 连续性和配置边界。通过后执行一个原子提交批次：

1. 不可裁剪 `CertifiedCoreGenerationV1`、核心 entry 清单与 `generation_content_checksum`，以及独立 checksum 完整的 `CausalValidationRecordV1`；
2. Header、`FinalityCertificate`、`FinalityProof` 所需永久证明链元数据；DAGCommitWitness 只在选择保留时作为 history segment；
3. 本高度初始 `HistorySegmentManifestV1`、按 `(segment_kind,history_segment_id)` 严格排序且无重复的 `initial_history_segment_ids`，以及全部初始 segment payload/active refs；其中至少有完整 canonical Body，Receipt/Event/proof payload 可按 kind 分段；
4. SMT 节点、版本化值、state root、state write batch、当前 MMR peaks/root；
5. 已完成 causal-input generation 的 activation marker、authenticated `DerivationStateGenerationV1` 与 DAG/Batch consumed 标记；
6. epoch/config transition；
7. `state_commit_cursor`、finalized height 与新 `active_generation`。

异步查询索引不属于首次公开的正确性边界，可以在发布后构建；响应必须回退到权威 segment/状态扫描或标记索引滞后。作为本次 publication 初始内容的 segment payload、manifest 和 active ref 必须在 marker 可见前全部写完并 fsync，不能先公开核心再补 Body。marker 永久绑定初始 segment ID 列表；后续 GC 只追加 `HistoryGCRecordV1`，不从 marker 删除 ID。

若该 block 是 durable `EPOCH_CLOSING_FENCE` 指定的 C，同一原子批次必须在共享锁域验证 slot/proposer/height/digest、fence 中的 `closing_intent_hash` 与对应 `EpochClosingIntentV1`，并把 fence 提升为 `EPOCH_CLOSED(old_epoch,final_height,final_block_id,closing_intent_hash)`；不能先公开 closing block 再补关闭记录，也不能先关闭后留下不可见的最终块。正确实现已从 reservation fsync 起阻止 C+1；此处发现更高旧 epoch 分配/attestation intent 说明串行化损坏，事务整体失败并 `SAFETY_HALT`。

如果数据库不能覆盖所有列做原子批次，必须使用 redo/commit-marker 协议；marker 使用第 7.3 节冻结的完整 schema并绑定 generation/core/validation record、证书语义、Header roots、初始 history segment、权威 state/MMR 与前一 marker。marker fsync 是逻辑 commit；active pointer 只控制当前进程何时开放读路径。marker 前崩溃的 staging 可丢弃；marker 后、pointer 前崩溃必须校验并 roll-forward 到完整新状态，不能回到旧状态或出现第三种结果。

## 10. 快照

快照只在已经完成 certified publication、可提供匹配 `FinalityProof` 的高度生成。wire schema 逐字段复用协议规范，不另造存储版 manifest：

```text
SnapshotStateRecord {
    namespace: byte_string
    key: byte_string
    value: byte_string
}

SnapshotChunkCore {
    schema_version: uint16
    chunk_index: uint64
    first_byte_offset: uint64
    payload: byte_string
}

SnapshotChunk {
    schema_version: uint16
    manifest_id: Hash32
    core: SnapshotChunkCore
    siblings_bottom_up: [Hash32]
}

SnapshotManifestCore {
    schema_version: uint16
    network_id: NetworkID
    ledger_id: LedgerID
    target_epoch: Epoch
    target_height: Height
    target_finalized_block_id: Hash32
    target_finality_id: Hash32
    target_state_root: Hash32
    target_block_mmr_root: Hash32
    target_block_mmr_peaks: [MMRPeak]
    target_epoch_emitted_vertex_count: uint64
    target_epoch_emitted_vertex_set_root: Hash32
    target_validator_set_hash: Hash32
    target_protocol_config_hash: Hash32
    state_encoding: uint16
    compression: uint16
    record_count: uint64
    stream_byte_length: uint64
    chunk_payload_bytes: uint32
    chunk_count: uint64
    chunks_root: Hash32
}

SnapshotManifest {
    schema_version: uint16
    core: SnapshotManifestCore
}
```

v1 的 schema version、`state_encoding=SMT_RECORD_CBOR_FRAMED_V1`、`compression=NONE`、`chunk_payload_bytes=1_048_576` 和 `SNAPSHOT_CHUNK_MAX_CANONICAL_BYTES_V1=1_050_823` 都是协议常量。P2P v1 不压缩；HTTP Gateway Content-Encoding 或未来版本的 transport 压缩必须经 streaming decompressor/running counter，最多读取 `1_050_824` bytes 判定越界；第 `1_050_824` byte 不进入 parser/staging，声明 payload/sibling 长度必须在对应大对象分配前验证，禁止先完整解压再检查。基础 `FinalityProof` 或独立 `CheckpointFinalityProof` 与 manifest 并列传输且逐目标字段匹配，不嵌入 manifest；本地 trust-root 类型必须在请求前选择对应的独立响应 Schema，snapshot 响应不能触发类型 fallback 或安装 checkpoint。snapshot 发布者签名、peer 数量和证书 signer subset 也不进入 manifest ID。

导出必须使用同一数据库 read snapshot，只枚举存在的逻辑状态，不输出 tombstone、MVCC 版本、缓存或物理 SMT 节点。记录按重算 `key_hash` 严格升序且拒绝重复，编码为 `U64BE(canonical_record_length) || canonical(SnapshotStateRecord)` 的连续 framed byte stream；frame 和长度前缀都允许跨 chunk。每条 canonical record 先受协议绝对 `SNAPSHOT_STATE_RECORD_MAX_CANONICAL_BYTES_V1=17_891_328` 限制；active epoch 的 component cap 只限制新写入，不能在治理下调后拒绝仍低于绝对上限的旧认证值。空状态固定为零 record/byte/chunk，非空流从 offset 0 切成固定 1 MiB payload。先 hash ChunkCore、构造 chunk Merkle root，再 hash ManifestCore 得到 manifest ID，最后把 ID 和最长 64 项的 inclusion path 装入 chunk envelope，避免哈希环。

导入流程固定为：

1. 按本地 trust store 选择 `VerifyFinalityProof(expected_genesis_reference,proof)` 或 `VerifyCheckpointFinalityProof(expected_anchor_id,proof)`，拒绝响应驱动的类型切换；逐跳验证后对目标 FeatureSet/GasSchedule 调用 `ValidateExecutionConfigBundle`，再逐项匹配 manifest 的 network/ledger/epoch/height/block ID/finality ID/state root/MMR root/epoch emitted count/root/validator/config hash，以 `leaf_count=target_height` 和 `target_block_mmr_peaks` 重算 `BlockMMRRoot`，必须等于认证 root；
2. 验证 manifest schema、状态编码、无压缩语义、checked 计数与固定 chunk 大小；
3. 可向多个来源并行拉 chunk，但每块写 staging 前必须重算 core hash并用 `siblings_bottom_up` 验证到 manifest `chunks_root`；
4. 按连续 index/offset 重组流，用有界增量 decoder 校验 frame、record count、总字节和严格 key-hash 顺序；
5. 在新 generation 从空 SMT 构建全部 present leaves，重算 `target_state_root`，并核对 MMR/certified target；
6. 若目标节点将作为当前 epoch Validator，另按协议第 18.4 节取得与同一 Header 完全匹配的 `DAGDerivationCheckpointManifest/chunks`，验证严格排序 VertexID 流并从空树重建 emitted count/root；仅状态 snapshot 的节点只能通过 26-field `QuerySnapshotInstallMarkerV1` 线性化切入只读 query generation，状态为 `SYNCING_DERIVATION` 且禁止全部协议签名；
7. full-validator 安装通过 29-field `SnapshotInstallMarkerV1` 同时切换 active state generation、authenticated derivation generation、已验证 MMR peaks、`certified_resume_cursor=Header.committed_slot`、snapshot cursor 和 finalized cursor；两类 marker 都在共享 active-marker 锁中以 install sequence/previous kind/hash/height 形成唯一链，fsync 后即提交并在 pointer 崩溃时 roll-forward。后续以这些 peaks、exact set 与 cursor 为起点顺序重放快照后的 FinalizedBlock。旧 epoch final snapshot 配合已验证 next EpochSeal 时，新 epoch derivation generation 固定初始化为空。

P2P v1 envelope 不压缩；HTTP Gateway Content-Encoding 或未来版本可在受上述 streaming 解压计数器保护的 envelope 外做传输压缩，但落入规范验证器的 payload 必须还原为未压缩 bytes。来源多数不能替代证明；任一 snapshot/checkpoint chunk 缺失、重复、乱序、路径错误、尾部多字节，或状态根/emitted root 不符都只能废弃/重取 staging generation，不能改变 active state。

## 11. 裁剪与垃圾回收

裁剪必须满足所有保留条件的最大值：

- 尚未进入 stable prefix 的 DAG 因果依赖；
- 尚未完成执行或证书聚合的 ordered input；
- fragment 恢复与 DA 审计窗口；
- snapshot 回退和状态证明服务窗口；
- epoch ValidatorSet/ProtocolConfig 证明链，以及被 snapshot 目标或实际历史 replay 使用的完整 FeatureSet/GasSchedule；
- 法规与业务审计策略。

v1 永久保留每个已发布高度的 `CertifiedCoreGenerationV1`、核心 checksum 清单、canonical `CausalInputManifestCore`、Header、`FinalityCertificate`、`CausalValidationRecordV1`、publish marker、全部 `HistorySegmentManifestV1`/segment ID 和 `HistoryGCRecordV1`。还须永久保留 snapshot manifests，以及从所选 trust root 到所有保留目标 epoch 所需的治理策略、GenesisCertificate/显式 checkpoint、每一跳 EpochSealCertificate、next ValidatorSet 与 next ProtocolConfig。目标 epoch 的完整 FeatureSet/GasSchedule 必须保留；全历史 replay 只对实际重放经过的 epoch 按已认证 config hash 保留/获取对应 bundle。中间 bundle 缺失会暂停 replay，但不使仅依赖 set/config/seal 的目标最终性证明无效。

Body、Receipt/Event、ordered-Vertex proof、DAG witness、历史状态证明和原始 causal/DAG/Batch source 的在线保留期由非共识 `LocalHistoryPolicy` 明确，但只能在协议最短窗口和全部依赖满足后延长。每份已注册 payload 都通过自己的 immutable manifest/segment ID 管理；删除前重验核心/marker 对该 ID 的注册、segment checksum/root、引用、最小游标和 Archive 目标，然后按第 7.3 节先 durable append `HistoryGCRecordV1`，再删除 bytes。重复执行同一 `(ledger,height,history_segment_id)` 必须返回原 record 并保持幂等；不得为相同 segment 产生第二个 sequence。

原始 causal chunks、临时 leaf files及已消费 DAG/Batch source 只有在 matching publish marker、核心/record/FC、相应 `CAUSAL_SOURCE` manifest 和从完整 causal stream 派生出的全部 Header roots 均已复验，且更长争议/归档窗口允许后才可裁剪。恢复不得再要求有合法 GC record 的 source。没有 publish marker/等价 certified-publication commit record 的 staging 不享受该规则，复用前仍须从 manifest/chunks/source 全量验证。

受管 segment 的目录状态只有三种：`PRESENT`、`PRUNED(record_hash)`、`CORRUPT`。判定顺序固定为：先查找与核心 marker、segment ID、kind 和 GC hash chain 全部匹配的 durable record；存在则为 `PRUNED`，查询返回 `HISTORY_PRUNED`，即使旧 bytes 尚残留也只做幂等物理清理；不存在 record 时，payload 必须存在、能流式重算 manifest ID、content checksum/root 并匹配协议承诺，才是 `PRESENT`；否则为 `CORRUPT`，该 Ledger 撤销 Validator readiness 并 `SAFETY_HALT`。不得在发现非授权缺失或损坏后补写 record 掩盖事故。

Safety WAL、活跃签名 slot、`EPOCH_CLOSING_RESERVATION/EPOCH_CLOSING_INTENT/EPOCH_CLOSING_FENCE/EPOCH_CLOSED`、CertifiedCoreGeneration/marker/validation record、HistorySegmentManifest/HistoryGCRecord、BatchRetentionManifest/BatchGCRecord、最新可恢复快照及其匹配 DAG derivation checkpoint、当前 epoch authenticated emitted-set generation、当前权威 state data（包括全部跨账本 consumed markers）、当前及下一 epoch key 元数据永不进入通用 GC。Batch GC record只释放其manifest登记的payload义务，不删除manifest/record本身。旧 epoch 的 derivation checkpoint payload 只有在 epoch seal/proof chain、所选恢复点、Archive 与本地更长保留策略均不再引用后才可按带 durable GC record 的 history 流程裁剪；其认证 Header/count/root 永久保留。

## 12. 恢复状态机

启动顺序：

1. 校验数据库 schema、Safety WAL、History GC chain与Batch GC chain的hash、sequence、previous链接和checksum，并逐个解析永久Batch retention manifest；
2. 恢复所有签名 slot、closing reservation/intent/fence/closed 与 EpochSealVote lock，在同一 Consensus signer 串行锁域重建防冲突视图；有 reservation 无 intent/fence时固定只可重建并执行同一 C，有 intent 无 fence时只可校验并补同一 fence，有 fence 无 closed时只可认证/发布同一 C，任一 hash/slot/height/digest 不连续都 `SAFETY_HALT`；连接 signer 前先启用只读查询；
3. 验证各 cursor 单调关系和最终父链；
4. 对未激活且没有 publish marker 的 causal-input/state/body staging generation 校验 manifest、原始 chunks/source 与 progress：可从已认证、校验和完整的 durable chunk checkpoint 继续流式恢复，但在复用派生结果、签 attestation 或发布前，验证覆盖必须到达整个 stream/source；checkpoint 不得把未覆盖输入视为已验证，其余 staging 安全丢弃；
5. 对已有 PreparedExecution 但尚未发布者重算 causal manifest、完整消费计数、delta/root 与 blob generation 摘要；
6. 对已有 publish marker/原子 publication 者验证 marker/transaction commit chain、FinalityCertificate、`CausalValidationRecordV1`、record 与 `CertifiedCoreGenerationV1` 内 canonical manifest/Header 的字段关系及不变的核心 checksum；同时验证所有 full/query snapshot-install marker的previous-active chain、proof/manifests/MMR/state generation及可选derivation generation。marker durable而active pointer尚未切换时按唯一chain幂等roll-forward；分叉、跳sequence、query marker被当作H+1 publication anchor或不完整query→full转换都fail closed。当前 tip 的权威 state data 必须重算或增量验证到 marker 绑定的 state root，并逐项验证跨账本 consumed raw key/value可重算到相同 consumption key；缺值、错 target location或 key/value错配均保持不可签名并从可信 snapshot/replay恢复，不能当作索引缺失；
7. 枚举 marker 的全部初始 history segment ID：有匹配的 durable `HistoryGCRecordV1` 时标记 `PRUNED` 且不要求 payload；无 record 时必须存在 payload并重算 manifest ID、content checksum/root 和对应协议 root；missing/corrupt 且无 record 立即 `SAFETY_HALT`。除核心、record、FC、marker/GC chain 和当前权威 state 外不得要求外部输入，原始 chunks/source 已合法裁剪时不得因此判损坏；
8. 对每个Batch retention manifest重算canonical stream length/content checksum、safety record绑定和obligation kind；若没有对应GC record，payload必须存在且计入`retained_referenceable_batch_bytes`，若已有record则目录语义固定为已释放并只幂等清理残留。record前payload缺失、同manifest多record、release height/epoch-close/public cursor不符或重算counter溢出/不等于持久化counter均撤销readiness；
9. 重建可重建索引；
10. 同步 Batch/DAG/finality 缺口；
11. 交叉检查每个旧 epoch 的关闭高度、seal statement 与 attestation 最高高度；任何越过关闭边界或互相不一致的记录都进入 `SAFETY_HALT`；
12. 达到 readiness 门槛后才恢复 ACK、Vertex、attestation 和 seal vote 签名。

崩溃点期望：

| 崩溃位置 | 恢复行为 |
|---|---|
| intent fsync 前 | 未签名，可重新验证 |
| intent fsync 后、签名前 | 只可签同 digest |
| 签名后、发送前 | 返回已保存签名 |
| 推测执行中 | 丢弃 scratch，重放 |
| causal chunk/frame 中 | 只从最后完整验证且 durable 的 chunk checkpoint 恢复，未激活 emitted segment 不可见 |
| PreparedExecution 后 | 验证并复用同一结果 |
| 证书写入中 | commit marker、完整 `CertifiedCoreGenerationV1` 与 `CausalValidationRecordV1` 决定旧或新状态，禁止半公开 |
| marker 已提交、尚无 GC record | 每个初始 history segment 必须存在且 checksum/root 正确，否则按损坏停机 |
| `HistoryGCRecordV1` fsync 后、payload 删除前 | record 是逻辑裁剪线性化点；恢复目录为 `PRUNED`、返回 `HISTORY_PRUNED`，再幂等清理残留 bytes |
| payload 缺失但 GC record 未 durable | 非授权丢失，`SAFETY_HALT`；不得补写 record 掩盖损坏 |
| `BatchRetentionManifestV1` fsync前后 | manifest前不得广播BatchHeader/DA_ACK；manifest后该完整stream成为保留义务并计入counter |
| `BatchGCRecordV1` fsync 后、Batch payload删除前 | record是释放线性化点；counter按record扣减一次并幂等清理残留bytes |
| Batch payload缺失但GC record未durable | 不可掩盖的保留义务损坏，撤销readiness；不得恢复时补写record |
| marker 已提交后原始 causal chunks 被合法 GC | 验证 core/record/FC、marker/GC chain；不再要求已裁剪 segment payload |
| cursor 更新后 | 对应数据必须已在同一原子批次中 |
| `EPOCH_CLOSING_RESERVATION` fsync 后、C 执行前 | 只可重建/执行同一 C；C+1 不分配高度，不能另选 closing candidate |
| C 执行后、closing intent fsync 前 | 从同一 C post-state 重算唯一 mode/next bundle并补同一 intent；任何结果漂移都 `SAFETY_HALT` |
| closing intent fsync 后、fence 前 | 校验 reservation、intent、C identity 后只可补同一 fence；不能先签 C attestation |
| `EPOCH_CLOSING_FENCE` fsync 后、C 获证前 | 只可重建/执行/认证同一 C；C+1 不分配高度、不写 attestation intent |
| closing block 提交中 | block 与 `EPOCH_CLOSED` 必须同为旧状态或同为新状态 |
| `EPOCH_CLOSED` 后收到更高旧 epoch attestation 请求 | signer 硬拒绝并告警，不得创建 intent |
| 已有更高 attestation intent 后请求关闭或 seal | 拒绝关闭/seal 并进入 `SAFETY_HALT` |

## 13. 损坏与冲突

- fragment/Batch/Vertex hash 不符：隔离对象与来源，重新获取；
- SMT root 不符：停止 attestation，恢复自可信快照；
- 同 slot 本地 WAL 有两个 digest：视为 signer/storage P0；
- 两个冲突 stable prefix：视为 FinalDAG-C P0；
- 同高度两份各自满足 q 的冲突 FinalityCertificate：冻结该 Ledger，保全 WAL、二进制摘要、validator set 和网络证据，不自动选一份；
- 磁盘满、fsync 失败或 checksum 错误：撤销 Validator readiness，保持只读服务。

## 14. 配置与指标

v1 ProtocolConfig 中与 GC 直接相关的链上字段只有 `dag_gc_rounds` 和 `batch_retention_heights`，其精确锚点与 inclusive 边界以协议第四篇为准。snapshot 周期、Body/Receipt/Event/DAG witness 服务窗口、历史查询 SLA、archive 冗余和 locator 都属于部署/合规 `LocalHistoryPolicy`，不是共识对象，不进入 ProtocolConfig、治理 action、Header、epoch seal 或任何内容 hash。建议的本地结构是：

```text
LocalHistoryPolicy {                 // 非 canonical、非共识
  snapshot_interval_heights: uint64
  finalized_body_retention_heights: uint64
  receipt_event_retention_heights: uint64
  ordered_vertex_proof_retention_heights: uint64
  dag_witness_retention_heights: uint64
  causal_source_retention_heights: uint64
  historical_state_proof_retention_heights: uint64
  archive_redundancy_target: uint16
  archive_locators: [URI]
}
```

本地策略不得提前越过 `dag_gc_rounds`、`batch_retention_heights` 或任一未决依赖/原子发布条件，也不得删除协议要求长期保留的 Header、FinalityCertificate、epoch set/config/seal 证明链、MMR append state 和安全 WAL。生产部署在裁掉可选历史前应把相同可验证对象复制到至少两个独立组织的 Archive，并抽样回读；这是一项可审计服务保证，不改变区块有效性。Full Node 缺少已裁剪对象时返回明确 `HISTORY_PRUNED` 和不受信任的 archive locators，不能伪造 NOT_FOUND、空 proof 或降低 FINALIZED 结论；客户端从 locator 取得数据后仍按原协议 proof 独立验证。

至少暴露：

- `finalweave_safety_wal_fsync_seconds{kind}`；
- `finalweave_safety_slot_conflict_total{kind}`；
- `finalweave_storage_cursor{ledger,stage}`；
- `finalweave_storage_commit_seconds{ledger}`；
- `finalweave_execution_prepared_height{ledger}`；
- `finalweave_snapshot_verify_seconds{ledger}`；
- `finalweave_gc_pending_bytes{ledger,class}`；
- `finalweave_history_segment_total{ledger,kind,state}`，其中 state 只能是 `present|pruned|corrupt`；
- `finalweave_history_gc_sequence{ledger}`；
- `finalweave_history_unauthorized_missing_total{ledger,kind}`；
- `finalweave_storage_checksum_failure_total{class}`。
- `finalweave_dag_unreferenced_sibling_quarantine_entries{ledger}` 与 `..._bytes{ledger}`；
- `finalweave_dag_dependency_promotion_total{ledger,phase,result}`、`finalweave_dag_dependency_fetch_pending{ledger,author}` 与 `finalweave_dag_dependency_staging_bytes{ledger,author}`。

## 15. 验收清单

- [ ] ACK、Vertex、ExecutionAttestation 均能证明 intent 先于签名 durable。
- [ ] ACK 还可证明固定 fragment 与 `CODEWORD_VERIFIED` 先于 `DA_ACK_LOCK` durable。
- [ ] Batch author body与ACK fixed fragment都在签名前注册独立retention manifest；启动重算GC chain和retained counter，首次消费/publication绝不提前扣减。
- [ ] 同 slot 同 digest 幂等、异 digest 硬拒绝。
- [ ] 同一 Byzantine Vertex slot 中所有被共识依赖精确引用的冲突对象均按需拉取、原子提升并在 `Past(P)` 中确定排序；纯旁路 sibling 只占有界 quarantine，晚引用即使 cache 已驱逐仍可恢复，闭包完成前不产生 support/commit。
- [ ] causal input 以固定 1 MiB framed chunks 有界恢复；完整消费前不激活 emitted segment、不签 attestation。
- [ ] 任意注入崩溃后最终状态只有旧版本或完整新版本。
- [ ] 首次 publication 将 core、当前状态和全部初始 HistorySegment 原子公开；不能观察到有 marker 但无 Body 且无 GC record 的新高度。
- [ ] 合法裁剪前后 `generation_content_checksum` 逐字节相同；present segment 必须通过 content checksum/root 与协议 root，missing+GC record 返回 `HISTORY_PRUNED`，missing/corrupt without record 必须停机。
- [ ] certified/full-snapshot/query-snapshot active marker共享唯一previous chain；marker后pointer前崩溃会roll-forward，query-only绝不开放Validator signer或锚定后继publication。
- [ ] 在 GC record fsync 前后及 payload 删除前后逐点注入崩溃：只允许 `PRESENT` 或 `PRUNED(record_hash)`，不得用恢复时补 record 掩盖 `CORRUPT`。
- [ ] closing block、`EPOCH_CLOSED` 和 seal vote 在共享锁域中不能与更高旧 epoch attestation 共存。
- [ ] stable prefix、PreparedExecution、certificate 和 state cursor 不逆序。
- [ ] 从快照加 FinalizedBlock 重放得到相同 state/receipt/event roots。
- [ ] 裁剪后仍能验证保留高度的 `FinalityProof`。
- [ ] 跨账本 source/target 历史裁剪后 consumed marker 仍在 Snapshot/SMT，重放仍被拒绝且不耗 nonce；marker 缺失或 key/value错配安全停机。
- [ ] `BLOCK_CAP` 的 count/gas 边界使用 checked arithmetic，且跳过不产 Receipt、不耗 nonce、不写 accepted set。
- [ ] 账户 meta/auth/nonce 只允许完整三元组；`ACCOUNT_CREATE_V1` 的三个系统写与 `next_nonce=1` 在同一 journal/commit marker 原子可见，逐崩溃点都不会留下部分账户。
- [ ] 不存在 `nonce_winner` 或历史 seen-set 共识表。
- [ ] 磁盘满与 fsync 失败会撤销签名 readiness。
