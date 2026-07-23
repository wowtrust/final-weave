# FinalWeave v1 跨账本异步消息规范

> 状态：规范性设计基线（Draft）
>
> 适用版本：FinalWeave v1 / state machine v1

## 1. 目标、语义与非目标

本篇冻结一套可直接实现的、证明驱动的跨账本异步消息协议。它允许源账本上的一笔已最终交易产生一条面向目标账本的消息，由任意不受信任 relayer 携带源最终性与事件包含证明，在目标账本上完成一次且仅一次的协议消费。

协议提供的是**最终事件的异步传递与目标账本 exactly-once acceptance**，不是两条账本之间的同步事务：

- 源账本最终后不能因目标账本拒绝、停机或过期而回滚；
- 目标账本只接受由其当前已认证 `FeatureSet` 明确列出的源信任策略；
- relayer 只能影响延迟与可用性，不能伪造源消息、替换 trust root、改写目标、延长有效窗或重复消费；
- 同一 source event 最多产生一个目标成功 Receipt 和一个永久 consumed marker；
- v1 不声称原子交换、两阶段提交、跨账本同步调用、目标应用回调或源端退款。需要这些语义的应用必须在本协议之上构造显式的 request/ack/timeout 状态机。

FinalWeave 的账本高度和 epoch 都是账本局部坐标，不能把两个账本的同名高度解释为同一时间。因此 v1 消息有效窗明确使用**目标账本高度**，而源最终性只证明消息已经发生。

## 2. 安全目标与对象链

实现必须维持以下不变量：

1. `XL-SAFE-001`：目标只从目标区块 active `FeatureSet` 取得 `CrossLedgerSourcePolicyV1`；proof、RPC、peer、relayer 和本地缓存都无权选择或安装 trust root。
2. `XL-SAFE-002`：一条消息的目标 network、ledger、policy、channel、payload 和目标高度窗全部由源交易签名并由源 FinalityProof 认证。
3. `XL-SAFE-003`：消费身份来自最终 source event occurrence，而不是 proof envelope、证书 signer 子集、relayer 交易 ID 或消息 payload hash。
4. `XL-SAFE-004`：目标共识状态中的 `consumption_key` 一旦 present 就不可删除、覆盖或复用；snapshot、replay 和 pruning 后仍保持同一结论。
5. `XL-SAFE-005`：同一块内竞争同一 `consumption_key` 的多个合法 occurrence，只允许规范扫描中第一个完成全部预留的 occurrence 成为 winner。
6. `XL-SAFE-006`：源 proof 验证、目标 policy、目标窗口、账户 nonce、块资源与 consumed-key 选择都由所有节点以相同顺序执行；缓存命中和 worker 完成顺序不得改变结果。
7. `XL-SAFE-007`：被选择的 `CROSS_LEDGER_SEND_V1` 与 `CROSS_LEDGER_CONSUME_V1` 已在 winner 选择前完成精确成功预检；协议有效 winner 只能成功，本地故障只能阻止 attestation，不能伪造 FAILED Receipt。

证明链为：

```text
target active FeatureSet
  -> CrossLedgerSourcePolicyV1
  -> pinned source genesis_reference or checkpoint_anchor_id
  -> source FinalityProof or CheckpointFinalityProof
  -> source FinalizedBlockHeader / FinalityCertificate
  -> source transaction + Receipt + two Event inclusion paths
  -> exact CROSS_LEDGER_SEND_V1 event reconstruction
  -> SourceEventID
  -> target consumption_key non-inclusion
  -> canonical first target winner
  -> consumed state + target Receipt + target FinalityProof
```

`CrossLedgerProofEnvelopeV1` 是传输证明，不是信任根，也不是重放键。相同 source event 可以有不同合法 FinalityCertificate signer subset、不同 proof framing 或不同 relayer envelope；这些差异不得改变 `source_event_id` 或 `consumption_key`。

## 3. v1 注册值与绝对边界

### 3.1 payload、Feature 与 Gas operation

本篇分配以下未占用注册值：

| 类别 | 数值 | 名称 |
|---|---:|---|
| `payload_type` | `4` | `CROSS_LEDGER_SEND_V1` |
| `payload_type` | `5` | `CROSS_LEDGER_CONSUME_V1` |
| Feature tuple | `(2,1,1)` | `CROSS_LEDGER_V1` |
| Gas operation | `0x00010004` | `CROSS_LEDGER_SEND_V1` |
| Gas operation | `0x00010005` | `CROSS_LEDGER_CONSUME_V1` |
| Gas operation | `0x00020001` | `CROSS_LEDGER_PROOF_VERIFY_V1` |
| Gas operation | `0x00020002` | `CROSS_LEDGER_SOURCE_SIGNATURE_VERIFY_V1` |

Feature 未激活时，payload `4/5` 与两个 `0x0002....` operation 都未登记；任何包含它们的交易或 GasSchedule 都无效。Feature 激活时四项必须同时进入 exact operation registry，不能只开放 SEND、只开放 CONSUME 或漏掉证明计量。

### 3.2 绝对上限

下列常量不能由治理提高：

```text
CROSS_LEDGER_POLICIES_MAX_V1                         = 64
CROSS_LEDGER_CHANNELS_PER_POLICY_MAX_V1              = 128
CROSS_LEDGER_RELAYERS_PER_POLICY_MAX_V1              = 128
CROSS_LEDGER_MESSAGE_PAYLOAD_MAX_BYTES_V1            = 1_048_576
CROSS_LEDGER_PROOF_ENVELOPE_MAX_CANONICAL_BYTES_V1   = 8_388_608
CROSS_LEDGER_RELAYER_ENVELOPE_OVERHEAD_MAX_BYTES_V1  = 1_048_576
CROSS_LEDGER_EXPIRED_EVIDENCE_MAX_CANONICAL_BYTES_V1 = 10_485_760
CROSS_LEDGER_EPOCH_TRANSITIONS_MAX_V1                = 256
CROSS_LEDGER_SOURCE_SIGNATURE_VERIFICATIONS_MAX_V1   = 65_536
CROSS_LEDGER_TARGET_VALIDITY_WINDOW_HEIGHTS_MAX_V1   = 4_294_967_296
CROSS_LEDGER_MERKLE_PATH_SIBLINGS_MAX_V1             = 64
CROSS_LEDGER_PARAMETERS_MAX_CANONICAL_BYTES_V1        = 65_536
```

这些上限还受通用 `max_transaction_bytes`、Feature parameter 64 KiB、Event、state read/write、Body 和 block gas 上限约束。bounded decoder 必须在按 proof transition、signature、channel、relayer、Merkle sibling 或 payload 声明长度分配前执行绝对上限检查；完整 canonical object 的 `MAX+1` 必须在密码学工作、SMT 读取和 staging 写入前拒绝。

Genesis-root proof 经过太多 source epoch 而超过上述边界时，目标治理必须激活一个经审计的较新 checkpoint policy，或继续保留能够适配的旧 policy；实现不得偷偷提高本地上限、截断 transition chain 或信任 relayer 的“已验证前缀”。

### 3.3 新 DomainHash 名称

下列名称加入 v1 domain 注册表：

| Domain | 用途 |
|---|---|
| `CROSS_LEDGER_CHANNEL` | 应用 channel 的稳定 ID |
| `CROSS_LEDGER_TRUST_POLICY` | 目标账本作用域内的 source policy ID |
| `CROSS_LEDGER_MESSAGE_PAYLOAD` | 源消息 payload 内容承诺 |
| `CROSS_LEDGER_MESSAGE` | 源消息语义 ID |
| `CROSS_LEDGER_SOURCE_EVENT` | 最终 source event occurrence ID |
| `CROSS_LEDGER_PROOF_ENVELOPE` | proof 传输/审计哈希 |
| `CROSS_LEDGER_VERIFIED_CONSUME` | source proof 验证后的规范恢复 artifact 摘要 |
| `CROSS_LEDGER_CONSUMPTION_KEY` | 目标永久重放键 |
| `CROSS_LEDGER_CONSUMED_STATE` | consumed record 内容 ID |

这九个新增 domain 不得被实现成通用 `EVENT`、`TX_ENVELOPE`、`FINALITY_STATEMENT` 或 `STATE_KEY` 的别名。最终状态仍通过通用 StateKey/StateValue/SMT 进入 Header；本篇内容 ID提供的是具名语义与交叉检查。第 6 节位置对象里的 `source_event_hash` 是对原始 Event本身的既有通用 `EVENT` hash，属于被 `CROSS_LEDGER_SOURCE_EVENT` 继续承诺的字段，不是把两个 domain混用。

## 4. 目标账本信任策略

### 4.1 Feature 参数

`CROSS_LEDGER_V1` 的 `parameters_cbor` 必须恰好编码一个 `CrossLedgerParametersV1`：

```text
CrossLedgerParametersV1 {
  schema_version: uint16
  max_outbound_message_payload_bytes: uint32
  max_outbound_target_validity_window_heights: uint64
  source_policies: [CrossLedgerSourcePolicyV1]
}

CrossLedgerSourcePolicyV1 {
  schema_version: uint16
  source_network_id: NetworkID
  source_ledger_id: LedgerID
  trust_root_kind: GENESIS_REFERENCE | CHECKPOINT_ANCHOR
  trust_root_id: Hash32
  min_source_height: Height
  max_source_height: Height
  allowed_channel_ids: [Hash32]
  relayer_mode: ANY_ACCOUNT | ALLOWLIST
  allowed_relayer_accounts: [AccountAddress]
  max_message_payload_bytes: uint32
  max_proof_envelope_bytes: uint32
  max_relayer_envelope_overhead_bytes: uint32
  max_epoch_transitions: uint16
  max_source_signature_verifications: uint32
  max_target_validity_window_heights: uint64
}
```

以上字段按展示顺序占用 deterministic-CBOR integer key `1..N`；所有 schema version 固定为 1，枚举 wire 值分别是 `GENESIS_REFERENCE=1`、`CHECKPOINT_ANCHOR=2`、`ANY_ACCOUNT=1`、`ALLOWLIST=2`。unknown field、unknown enum、缺字段、尾随 item 与非规范整数均拒绝。

完整参数必须不超过 65,536 bytes。`max_outbound_*` 必须为正且不超过第 3.2 节绝对上限；`source_policies` 可以为空，以支持只发送、不接收的账本，但最多 64 项。`ValidateExecutionConfigBundle` 完成与 Ledger 无关的 typed/cap/sort 校验；Genesis 安装、EpochSeal authorization/readiness 与每个区块执行还必须调用 `ValidateCrossLedgerParametersForLedger(target_network_id,target_ledger_id,protocol_config,parameters)` 完成下列 contextual 检查。`protocol_config` 必须是同一已认证 bundle中与该 target Header/epoch绑定的对象，不能取本地默认值。每个 policy 必须满足：

- source network/ledger 均为完整 32-byte ID，且 source `(network,ledger)` 不得同时等于当前目标 `(network,ledger)`；
- `1 <= min_source_height <= max_source_height`；
- channel 数量为 `1..128`，按原始 Hash32 严格升序并拒绝重复；
- `ANY_ACCOUNT` 要求 relayer 数组严格为空；`ALLOWLIST` 要求 `1..128` 个 AccountAddress，按原始字节严格升序并拒绝重复；
- 六个上限字段 `max_message_payload_bytes`、`max_proof_envelope_bytes`、`max_relayer_envelope_overhead_bytes`、`max_epoch_transitions`、`max_source_signature_verifications`、`max_target_validity_window_heights` 均为正并不超过各自绝对上限。定义 `actual_relayer_envelope_overhead = checked_sub(len(canonical(TransactionEnvelope)),len(canonical(CrossLedgerProofEnvelopeV1)))`，其中 proof 在完整 Consume payload 中恰出现一次；任何 relayer CONSUME 都必须使该值不超过 policy cap；
- `max_relayer_envelope_overhead_bytes` 至少等于 `OneSignerCrossLedgerConsumeEnvelopeOverheadV1(max_proof_envelope_bytes)`。该 deterministic length function把一个长度恰为参数、已规范编码的 proof item嵌入 `CrossLedgerConsumePayloadV1`，再嵌入 `TransactionIntent.payload` byte string与完整 `TransactionEnvelope`；使用一个合法 Ed25519 signer/签名、空用户 scope、所有固定宽度 ID与最大宽度合法整数，最后以 `full_envelope_len - proof_len` 得到 overhead。它必须真实编码每层 CBOR byte-string/map长度头，因此 proof 长度跨 `23/24`、`255/256`、`65,535/65,536` 时可能改变结果，不能拿 proof 长度 0的常量估算。随后要求 `checked_add(max_proof_envelope_bytes,max_relayer_envelope_overhead_bytes) <= active max_transaction_bytes`。这保证 policy 宣称的最大 proof 至少可由一个单 signer relayer envelope 表示；实际账户 policy/签名更大时，其可用 proof cap相应变小，不能靠 transport绕过；
- policy 数组按 `canonical(CrossLedgerSourcePolicyV1)` 原始字节严格升序并拒绝重复；在当前目标 ledger 作用域内重算出的 `policy_id` 也不得重复。

目标作用域 policy ID 为：

```text
cross_ledger_policy_id = DomainHash(
  "CROSS_LEDGER_TRUST_POLICY",
  target_network_id,
  target_ledger_id,
  canonical(CrossLedgerSourcePolicyV1)
)
```

FeatureSet 内容对象本身是 network-scoped；policy ID 额外绑定实际 target LedgerID，防止把另一账本的 allowlist/trust-root 选择直接当成本账本的 policy 身份。

### 4.2 信任根选择不可由消息控制

验证 `CROSS_LEDGER_CONSUME_V1` 时，执行器先从**当前目标 epoch 已认证的 FeatureSet**构造 `policy_id -> policy` 的只读 map。消息中的 `destination_policy_id` 只用于查找精确 map entry；找不到即无效，不能：

- 使用 proof envelope 自报的 `trust_root_kind/trust_root_id` 创建 policy；
- 从本地通用 checkpoint trust store、DNS、HTTPS、peer 多数或 relayer 身份猜测替代项；
- 在 GENESIS verifier 失败后尝试 CHECKPOINT verifier，或反向 fallback；
- 把相同 source ledger 的“最新 policy”当作隐式 alias；
- 因本地曾缓存验证结果而绕过 active FeatureSet。

proof envelope 的 root kind/id 必须逐字节等于已选 policy，只是冗余交叉检查。真正传给 proof verifier 的 expected root 始终来自 policy。正常 CONSUME 路径只能使用当前 target active policy；第 9.3 节的历史过期证明可以使用一个**已经由调用方独立验证最终性的历史 target Header/FeatureSet context**中的同一 policy，不能直接相信 evidence 内自报的旧配置。

## 5. Channel 与源消息

### 5.1 ChannelID

应用可以用以下推荐 descriptor 生成稳定 channel；wire 消息只携 32-byte `channel_id`：

```text
CrossLedgerChannelDescriptorV1 {
  schema_version: uint16
  source_application: byte_string
  destination_application: byte_string
  channel_name_hash: Hash32
}

channel_id = DomainHash(
  "CROSS_LEDGER_CHANNEL",
  destination_network_id,
  destination_ledger_id,
  canonical({source_network_id, source_ledger_id, descriptor})
)
```

descriptor 不进入目标共识验证；安全授权只比较 policy 中的精确 Hash32。人类可读 channel 名、URL 和服务发现元数据不能代替 ID。

### 5.2 消息 schema 与身份

```text
CrossLedgerMessageCoreV1 {
  schema_version: uint16
  source_network_id: NetworkID
  source_ledger_id: LedgerID
  source_sender: AccountAddress
  destination_network_id: NetworkID
  destination_ledger_id: LedgerID
  destination_policy_id: Hash32
  channel_id: Hash32
  message_salt: Hash32
  target_valid_from_height: Height
  target_valid_until_height: Height
  application_payload_type: uint32
  application_payload: byte_string
}

CrossLedgerSendPayloadV1 {
  schema_version: uint16
  message: CrossLedgerMessageCoreV1
}
```

字段按展示顺序占用 CBOR key。`application_payload_type` 由 channel 应用解释，FinalWeave 核心只把它作为签名的 `uint32`；值 `0` 合法。payload 可以为空，但不得超过 source active `max_outbound_message_payload_bytes` 或绝对 1 MiB。

```text
message_payload_hash = DomainHash(
  "CROSS_LEDGER_MESSAGE_PAYLOAD",
  source_network_id,
  source_ledger_id,
  application_payload
)

cross_ledger_message_id = DomainHash(
  "CROSS_LEDGER_MESSAGE",
  source_network_id,
  source_ledger_id,
  canonical(CrossLedgerMessageCoreV1)
)
```

`message_salt` 由发送方 SDK 生成并签入消息，使相同业务 payload 可以被有意发送多次；它不是重放防护。真正的消费身份在最终 event position 形成。

### 5.3 SEND 的静态规则与源 Event

`payload_type=4` 仅在 source active `CROSS_LEDGER_V1` 时有效。Intent 的用户 `authorized_access_scope` 必须为空，message 的 source network/ledger/sender 必须分别等于 Intent context，destination tuple 必须与 source tuple 不同；目标窗口必须满足：

```text
target_valid_from_height >= 1
target_valid_from_height <= target_valid_until_height
span_minus_one = checked_sub(until, from)
span_minus_one < source.max_outbound_target_validity_window_heights
span_minus_one < CROSS_LEDGER_TARGET_VALIDITY_WINDOW_HEIGHTS_MAX_V1
```

执行器对通过精确 Gas、Event、Body 和 nonce-write 预留的 SEND winner 发出恰好一个 Event：

```text
CrossLedgerSourceEventDataV1 {
  schema_version: uint16
  message_id: Hash32
  message_payload_hash: Hash32
  message: CrossLedgerMessageCoreV1
}

Event {
  emitter: UTF8("finalweave/native/cross-ledger")
  topic:   UTF8("message/v1")
  data:    canonical(CrossLedgerSourceEventDataV1)
}
```

Event 必须是该交易的唯一 Event，局部 `event_index=0`；return data 固定为 `canonical({message_id,message_payload_hash})`。除普通 sender nonce write 外没有业务状态写。event data 必须逐字段从签名 payload 重建，不能信任 payload 内预编码 Event。通过预检的 winner 只能得到 `SUCCESS/NONE` Receipt；OOG、Event/Body 上限不足或非规范消息必须在选择 winner 前拒绝，不能形成“发送失败但占用消息身份”的 Receipt。

## 6. 最终 source event 身份

一条消息被源账本最终化后，以规范位置构造：

```text
CrossLedgerSourceEventPositionV1 {
  schema_version: uint16
  source_network_id: NetworkID
  source_ledger_id: LedgerID
  source_epoch: Epoch
  source_height: Height
  source_finalized_block_id: Hash32
  source_finality_id: Hash32
  source_tx_id: Hash32
  source_transaction_index: uint32
  source_event_index: uint32
  source_event_hash: Hash32
  message_id: Hash32
}

source_event_hash = DomainHash(
  "EVENT",
  source_network_id,
  source_ledger_id,
  canonical(source_event)
)

source_event_id = DomainHash(
  "CROSS_LEDGER_SOURCE_EVENT",
  source_network_id,
  source_ledger_id,
  canonical(CrossLedgerSourceEventPositionV1)
)
```

位置中的 block ID、finality ID、tx ID、index、event hash 和 message ID 都必须由同一 proof 重算。由于 native SEND 固定 `source_event_index=0`，任何其他 index 都无效。

`message_id` 表示签名业务语义；`source_event_id` 表示它在一个最终账本位置真实发生的一次 occurrence。两笔不同源交易即使故意使用相同 message core，仍有不同 tx/block position 和不同 source event ID，因而可各消费一次。proof envelope 或 certificate signer subset 改变不会改变这两个 ID。

## 7. Source proof envelope

### 7.1 Wire schema 与严格 union

```text
CrossLedgerProofEnvelopeV1 {
  schema_version: uint16
  trust_root_kind: GENESIS_REFERENCE | CHECKPOINT_ANCHOR
  trust_root_id: Hash32
  source_position: CrossLedgerSourceEventPositionV1
  source_transaction: TransactionEnvelope
  source_receipt: ReceiptCore
  source_event: Event
  source_transaction_proof: MerkleProof
  source_receipt_proof: MerkleProof
  source_per_tx_event_proof: MerkleProof
  source_block_event_proof: MerkleProof
  genesis_finality_proof: optional FinalityProof
  checkpoint_finality_proof: optional CheckpointFinalityProof
}
```

字段按展示顺序占用 CBOR key `1..13`。presence matrix 固定为：

| `trust_root_kind` | `genesis_finality_proof` | `checkpoint_finality_proof` |
|---|---|---|
| `GENESIS_REFERENCE` | 必须存在 | 必须缺失 |
| `CHECKPOINT_ANCHOR` | 必须缺失 | 必须存在 |

两种 finality proof 的 `merkle_proofs` 必须为空；本 envelope 的四个具名 proof 是唯一 inclusion 路径。这样响应不能同时放两份不一致 path，也不能按字段形状猜 trust mode。四个 proof 的 sibling 数都不得超过 64，并仍须满足通用树由 `count` 唯一决定的精确 path 长度。

```text
cross_ledger_proof_envelope_hash = DomainHash(
  "CROSS_LEDGER_PROOF_ENVELOPE",
  target_network_id,
  target_ledger_id,
  canonical(CrossLedgerProofEnvelopeV1)
)
```

该 hash 用于传输校验、缓存审计和日志关联，含 signer subset 与 proof framing，不能用作 consumption key、source event ID、排序种子或应用消息 ID。

### 7.2 确定性验证顺序

`VerifyCrossLedgerSourceProof(target_context,authenticated_feature_context,envelope)` 是昂贵的 source-proof 后缀，不是 occurrence filter 的入口。普通 CONSUME传入当前 target context；第 9.3 节稳定过期查询传入已经独立验证的历史 policy context。共识扫描只有依次完成完整item scan扣款/绑定、bounded outer parse、target交易窗口/accepted tx-id/`nonce == next`、policy/relayer声明上限、RequiredGas与完整成功reserve、common expensive suffix扣款并得到target账户认证`VALID`、source-attempt later-occurrence duplicate gate、tentative replay必要检查，以及source charge+`CrossLedgerProofAttemptV1{STARTED}`原子提交后才能调用它；任何更早调用都不符合 v1。进入该函数后仍必须按以下顺序执行并重新核对廉价前缀所见字段：

1. 用 bounded reader 验证 envelope/schema/union/完整 canonical bytes、绝对上限、四个 Merkle proof 结构和声明计数；尚不信任任何 ID。
2. 从 envelope 的 source event 解码 message，只把 `destination_policy_id` 当查找键；从调用方已经认证的 target FeatureSet context取得精确 policy。找不到、重复 map entry或 feature 未激活即拒绝。
3. 要求 message destination network/ledger 等于该 target context，source tuple 等于 policy；channel 在 policy allowlist，payload 与目标窗口 span 不超过 policy。然后要求 envelope root kind/id 等于 policy。relayer授权是目标 TransactionEnvelope的属性，由第 8.3 节廉价前缀单独检查，不属于可离线验证的 source proof结果。
4. 若 policy 为 genesis，调用 source 基础 FinalityProof verifier，expected network/ledger/genesis reference 全部取自 policy；若为 checkpoint，调用相同密码学规则的 `VerifySourceCheckpointFinalityProof(policy.trust_root_id,proof)`。后者的 expected anchor ID 来自目标链上 policy，不来自 proof 或本地可变 trust store。禁止跨 verifier fallback。
5. 要求 source Header 高度位于 policy `[min_source_height,max_source_height]`；proof transition 数与实际执行的 source signature verification 数分别不超过 policy 与绝对上限。所有 GenesisApproval、EpochSealVote 和最终 ExecutionAttestation 都按各自规范顺序逐签名验证，不能用“证书已缓存”改变逻辑计量。
6. 重算 source FinalizedBlockID、FinalityStatement/finality ID，逐字段匹配 position；transaction proof 必须为 `TRANSACTION` 且 index 等于 position，receipt proof 必须为 `RECEIPT` 且同 index，两者分别验证到同一 source Header root。
7. 重算 source tx ID；Receipt 必须 `SUCCESS`、`error_code=NONE`、tx ID/height/index/sender/nonce 与 transaction/Header 精确匹配。FAILED 或不存在 Receipt 不能产生跨账本消息。
8. per-tx event proof 必须为 `PER_TX_EVENT,index=0` 并验证到 Receipt.event_root；block event proof 必须为 `BLOCK_EVENT`，其 item 是 `{transaction_index, event_index:0, source_event}` 并验证到 Header.event_root。两条路径中的 Event bytes 必须完全相同。`source_event_hash` 必须用通用 `EVENT` domain 对原始 `source_event` 重算；它不是 block-event item 的 hash，也不含 transaction/event index。
9. 以 source proof 目标 epoch 的 FeatureSet/GasSchedule 解释 source epoch，要求 `CROSS_LEDGER_V1` 当时已激活；source transaction 必须是 canonical `CROSS_LEDGER_SEND_V1`。重新运行第 5.3 节静态规则并从 payload 重建唯一 Event、return hash 与成功 Receipt 的确定性关系。
10. 重算 message payload hash、message ID、event hash、source position 与 source event ID；任一 envelope 冗余字段不匹配都拒绝。最后才把 verified result 交给目标 occurrence filter。

source proof 最终性只证明 source 事件，不能证明目标未消费。目标重放结论必须读取目标父 state root 下的 consumed key。

## 8. 目标 CONSUME、winner 与重放保护

### 8.1 Consume payload 与状态 key

```text
CrossLedgerConsumePayloadV1 {
  schema_version: uint16
  source_event_id: Hash32
  proof_envelope: CrossLedgerProofEnvelopeV1
}

CrossLedgerConsumptionKeyCoreV1 {
  schema_version: uint16
  source_network_id: NetworkID
  source_ledger_id: LedgerID
  source_event_id: Hash32
}

consumption_key = DomainHash(
  "CROSS_LEDGER_CONSUMPTION_KEY",
  target_network_id,
  target_ledger_id,
  canonical(CrossLedgerConsumptionKeyCoreV1)
)
```

目标系统 namespace 固定为 UTF-8：

```text
finalweave/v1/cross-ledger/consumed
```

raw StateKey.key 恰为 32-byte `consumption_key`。用户 `authorized_access_scope` 必须为空；native resolver 注入该 key 的 `EXACT READ + EXACT WRITE` 与普通 sender nonce system access。普通 KV、未来 WASM、用户 scope 和治理迁移不得写、删或覆盖该 namespace。

`source_event_id` 必须等于完整 proof 重算结果。消费 key 不含 policy ID、relayer、目标 tx ID或证书 signer subset，因此 policy 轮换、重提交易和 proof 重包装都不能让同一 source event 再消费。

### 8.2 目标有效窗

在候选目标区块高度 `h`，消息与 relayer 交易的两个窗口都必须包含 `h`：

```text
tx.valid_from_height <= h <= tx.valid_until_height
message.target_valid_from_height <= h <= message.target_valid_until_height
```

message 窗口只有在 proof 验证后才能作为已认证事实或对外状态；但 bounded outer parse得到的声明窗口可以用于**reject-only prefilter**。若当前高度显然在声明窗口外，即使 proof无效也不可能成为合法 winner，因此可在 source crypto前确定性标记 `CROSS_LEDGER_FUTURE_HEIGHT`/`CROSS_LEDGER_EXPIRED_OCCURRENCE`；窗口内候选仍必须在 proof成功后重查同一字段。两类都不进 transaction/receipt/event tree、不耗 nonce。policy 的 `max_target_validity_window_heights` 只限制 span，不能延长消息签名的 until。

### 8.3 并发 winner 算法

`CROSS_LEDGER_CONSUME_V1` 不能把昂贵 source 证明验证塞进通用 `CanonicalAndStaticValid` 的最前面。否则一个拥有有效 outer Envelope、但携带巨大无效 source proof 的 stale/future nonce 或 `gas_limit=1` occurrence，会在确定不会成为 winner 时仍迫使每个 Validator 做数万次验签。v1 因此把 CONSUME 校验拆成严格的廉价前缀和昂贵后缀；该顺序本身是共识规则，而不是实现优化。

通用 occurrence filter 已按协议第四篇初始化 `OccurrenceScanAttemptV1`、`attempted_prefilter_tx_results: tx_id -> PrefilterAttemptV1`、`completed_scan_reserved_units[0..n-1]`、`completed_scan_shared_units`及全部 `n` 个 occurrence sponsor 的 common prefilter reserve/shared pool。sponsor固定为承载该 AvailabilityReference 的已签名 DAGVertex作者，允许与Batch作者不同。本 overlay 另外初始化：

```text
n = target_context.validator_set.validators.length
working_consumption_keys = empty exact set
cross_ledger_proof_attempts = empty exact map
  tx_id -> CrossLedgerProofAttemptV1
cross_ledger_verification_gas_spent = 0
sponsor_verification_gas_spent[0..n-1] = 0
shared_verification_gas_spent = 0
```

source-proof checkpoint 类型固定为：

```text
CrossLedgerProofChargeReceiptV1 {
  schema_version: uint16                 // 固定 1
  sponsor_author_index: uint16
  reserved_gas: uint64
  shared_gas: uint64
}

CanonicalVerifiedConsumeV1 {
  schema_version: uint16                 // 固定 1
  policy_id: Hash32
  proof_envelope_hash: Hash32
  source_position: CrossLedgerSourceEventPositionV1
  source_event_id: Hash32
  message_payload_hash: Hash32
  message: CrossLedgerMessageCoreV1
  consumption_key: Hash32
}

CrossLedgerProofAttemptV1 {
  schema_version: uint16                 // 固定 1
  status: uint16                         // 1=STARTED, 2=VALID, 3=INVALID
  origin_occurrence_cursor: CausalOccurrenceCursorV1
  sponsor_author_index: uint16
  work_cost: uint64
  charge_receipt: CrossLedgerProofChargeReceiptV1
  verified_consume: optional CanonicalVerifiedConsumeV1
  verified_consume_digest: optional Hash32
}

verified_consume_digest = DomainHash(
  "CROSS_LEDGER_VERIFIED_CONSUME",
  target_network_id,
  target_ledger_id,
  canonical(CanonicalVerifiedConsumeV1)
)
```

以上checkpoint对象按展示字段顺序使用deterministic CBOR；unknown schema/status、缺字段、多字段或非规范整数都不可恢复。attempt presence matrix固定为：

| `status` wire值 | `verified_consume` | `verified_consume_digest` |
|---:|---|---|
| `1` (`STARTED`) | 必须缺失 | 必须缺失 |
| `2` (`VALID`) | 必须存在 | 必须存在 |
| `3` (`INVALID`) | 必须缺失 | 必须缺失 |

`charge_receipt.sponsor_author_index` 必须等于 attempt 的 `sponsor_author_index`，并等于origin occurrence所在 containing Vertex的已验证作者；`checked_add(reserved_gas,shared_gas) == work_cost`。BatchHeader作者必须另由`OccurrenceScanSourceBindingV1.batch_author_index`绑定，但不参与扣款。恢复和每次读取 `VALID` artifact 时都要重算 digest，并重验 policy、proof hash、source position/event/message 与 consumption key 的全部冗余绑定。artifact只能从authenticated target policy/context与verified source-proof结果构造，不能从尚未认证的tentative message复制字段；tentative只用于之后逐字段相等性检查。

规范扫描顺序为：

```text

for each CROSS_LEDGER_CONSUME_V1 occurrence in canonical order:
  0. ChargeAndBindOccurrenceScan:
       从 VerifiedOccurrenceSourceAtCursor 取得cursor、vertex_ordinal、
       availability_reference_index、batch_id、batch_transaction_index、item_length
       、containing signed DAGVertex.author_index（occurrence sponsor）与
       authenticated BatchHeader.author_index，构造schema_version=1的
       OccurrenceScanSourceBindingV1，并按
       DomainHash("OCCURRENCE_SCAN_SOURCE_BINDING",target network/ledger,
       canonical(binding))重算source_binding_hash；在解码Envelope前按
       PrefilterScanWorkCostV1(item_length)扣通用预算，并把扣款与schema_version=1的
       OccurrenceScanAttemptV1{STARTED,origin cursor,source binding hash,receipt}原子提交；
       durable cursor仍指向当前 occurrence。scan cap时只流式消费并逐字节比对
       verified source，不解码字段、不算tx_id、不查SMT、不启动crypto，分类
       PREFILTER_SCAN_CAP；恢复STARTED scan时从item开头重扫但不再次扣费。
       完成当前occurrence时，Finish必须把inflight scan receipt恰好一次原子并入
       completed_scan_reserved_units[sponsor]与completed_scan_shared_units，再清除
       inflight记录并推进cursor；scan cap没有receipt，不并入aggregate。

  1. BoundedParseCrossLedgerConsumeOuter:
       流式验证完整 target TransactionEnvelope 与 proof 外层的 deterministic-CBOR；
       在分配数组/byte string 前检查绝对长度、声明计数、union、Merkle sibling、
       transition/signature 数和 actual_relayer_envelope_overhead。

  2. ValidateCheapTargetWinnerPrefix:
       验证 target network/ledger、bounded schema/Feature/type、空用户 scope与tx_id；
       依次处理tx height window、accepted tx_id、父块account存在性、
       signer_policy_hash == active policy hash、NONCE_EXHAUSTED和stale/future nonce；
       只有tx.nonce == working_next_nonce才继续；这里不做Envelope signatures、
       strict public-key或任何source proof密码学工作。

  3. TryPrepareCheapPayloadCandidateAndBlockReservation:
       只把尚未认证的 source message 当 bounded 候选结构；
       精确查找 current target policy_id，检查 source/destination/channel/relayer、
       proof root kind/id、policy 声明上限、proof bytes、target envelope overhead；
       从规范 proof bytes 与声明的逻辑 source signer 数计算完整 RequiredGas；
       要求 tx.gas_limit >= RequiredGas，并先检查 winner 的块 gas、交易数、
       Body/Event/state/write/return-data 完整成功预留是否可容纳；
       以声明 message window做reject-only高度检查；以selected policy source tuple
       与payload.source_event_id派生tentative consumption key。此步只保留key，
       尚不读取consumed SMT，也不执行source crypto。

  4. RunCommonChargedPrefilterSuffix:
       sponsor = AuthenticatedContainingDAGVertex(occurrence.vertex_ordinal).author_index
       common_attempt = attempted_prefilter_tx_results.get(tx.tx_id)
       if common_attempt is ABSENT:
         common_cost = PrefilterExpensiveWorkCostV1(tx)
         // 含恰好一个PREFILTER_SUFFIX_BASE_UNITS_V1，故common_cost >= 1
         common_charge = TryChargePrefilter(sponsor, common_cost)
           else PREFILTER_VERIFY_CAP；cap loser不写map
         原子写入 common_charge + PrefilterAttemptV1{
           schema_version=1, status=STARTED, occurrence.cursor, sponsor,
           common_cost, common_charge}
         durable cursor仍指向当前 occurrence
       if common_attempt.status == STARTED:
         origin必须等于当前cursor；重跑target Envelope signatures、strict keys、
         payload registry、access/Gas/resource static suffix，不再次扣费；
         deterministic invalid写INVALID，local failure保持STARTED并暂停且不推进cursor
       terminal INVALID -> STATIC_OR_AUTH_INVALID_OCCURRENCE
       terminal VALID可被相同tx_id后续occurrence复用；proof subtree的全部后代不计入
       PrefilterExpensiveWorkCostV1，且不得重跑第2～3步的policy/RequiredGas/reserve。

  5. CheckExistingSourceAttempt:
       proof_attempt = cross_ledger_proof_attempts.get(tx.tx_id)；若存在且origin不是
       当前cursor，STARTED表示checkpoint损坏，terminal VALID/INVALID都立即标记
       DUPLICATE_CROSS_LEDGER_ATTEMPT。target window、accepted-tx-id、account/nonce等
       更早通用cheap gate仍保留优先级；一旦到达source scheduler，本gate必须早于
       tentative replay，避免scheduler-level duplicate被重分类为replay。

  6. PrecheckTentativeReplay:
       读取tentative key的parent consumed state并检查working set；present则标记
       CROSS_LEDGER_REPLAY。absent只允许继续，不能证明source event有效。若map中
       已有属于当前origin的attempt，说明该检查曾在扣款前通过；恢复时present是
       checkpoint/derived-state不一致，必须RECOVERY_STATE_CORRUPT而不能带着STARTED
       分类返回或推进cursor。

  7. RunCrossLedgerChargedSuffix:
       sponsor = AuthenticatedContainingDAGVertex(occurrence.vertex_ordinal).author_index
       if proof_attempt is ABSENT:
         verification_cost = ProofAndSourceSignatureGas(prepared.cross_ledger_context)
         proof_charge = TryChargeCrossLedgerProofSponsorThenShared(
           sponsor, verification_cost) else CROSS_LEDGER_VERIFY_CAP
         原子写入 proof_charge + CrossLedgerProofAttemptV1{
           schema_version=1, status=STARTED, occurrence.cursor, sponsor, verification_cost,
           proof_charge, ABSENT, ABSENT}
         durable cursor仍指向当前 occurrence
       if proof_attempt.status == STARTED:
         origin必须等于当前cursor；执行source finality、epoch signatures、
         transaction/Receipt/Event/Merkle与source SEND重放，且不再次扣费；
         deterministic invalid写INVALID；local timeout/cancel/disk/resource failure保持
         STARTED并暂停且不推进cursor；VALID则原子保存CanonicalVerifiedConsumeV1
         与重算digest。cache hit/miss消耗相同verification_cost。
       origin上的terminal INVALID -> STATIC_INVALID

  8. CompleteTrustedChecksAndSelect:
       从VALID attempt读取并重验artifact/digest；要求payload.source_event_id等于
       verified source_event_id，检查已认证message target window与policy绑定；
       使用verified consumption_key，且必须等于tentative key；
       读取 parent consumed SMT 与 working_consumption_keys；
       replay 则跳过，否则重新核对第 3 步成功预留与 verified artifact 逐字节相同，
       选择 winner、写 working key、推进 nonce 与普通 block reservation。
```

第 0 步对完整 `CausalOccurrenceItem` bytes收一次scan费；第 1 步只做单遍有界解析、长度/排序/计数和哈希准备，不验证 source 签名或 Merkle 路径。target Envelope hash 必须流式计算，不能为了账户验签复制一份 8 MiB proof。第 2～3 步先排除窗口、nonce和资源上已不可能成为winner的候选。第 4 步只收 `PrefilterExpensiveWorkCostV1(tx)`：其中恰含一个`PREFILTER_SUFFIX_BASE_UNITS_V1=1`，保证任何真正创建common attempt并进入worker的候选至少收费1 unit；target账户验签属于所有交易共享的通用charged prefilter，但 `proof_envelope` 全部后代已从其typed field-path计数中排除。该base只覆盖common dispatcher/registry/exact-map开销，不能因proof内容重复增加，也不能算进第 7 步独立的source Finality/Merkle/signatures预算。因此完整item scan、common base+typed expensive suffix与source crypto三段各计一次，既无零成本worker/map增长或未计费CPU旁路，也无同一工作重复收费。

第 3步对尚未认证message做的policy/window/output/key检查只是确定性拒绝与资源估算，不能把它写入状态、cache成verified result或对外声称消息有效。第6步tentative parent non-inclusion只能作为继续验证的必要条件，proof成功后仍要重算key并复查本块working set；tentative present则无论proof最终有效还是无效都不可能成为该交易声明身份的合法消费，因而可以安全短路。通用cheap-prefix分类始终优先；对于已经通过这些gate并到达source scheduler的occurrence，第5步必须先保留different-origin terminal的`DUPLICATE_CROSS_LEDGER_ATTEMPT`分类，只有attempt absent或属于当前origin时才执行tentative replay。common 已为 `VALID` 后，第 7 步固定只接收第 3 步留下的 `prepared.cross_ledger_context`；不得再次解析廉价前缀、重新计算 RequiredGas、重复做声明窗口/tentative key检查或另做一次成功reserve。

`RequiredGas` 必须从规范 proof 的实际 byte 长度、结构中实际出现的 source signature 数与完整成功输出计算，不接受 relayer 自报 cost。重复 signer、错误签名或错误 Merkle root 仍会在第 7 步失败，但不能隐藏其逻辑 cost。通用prefilter与source-proof scheduler是两个独立预算：前者覆盖target Envelope、strict keys与canonical bytes，后者覆盖source Finality/Merkle/signatures；同一工作不能漏计或重复计入两边。

为同时给密码学工作设硬上界并阻止 Byzantine Batch 作者抢光全局预算，active bundle 定义：

```text
max_single_cross_ledger_verification_gas =
  max over active source policies of
    Gas(CROSS_LEDGER_PROOF_VERIFY_V1, policy.max_proof_envelope_bytes, 0)
    + policy.max_source_signature_verifications
      * Gas(CROSS_LEDGER_SOURCE_SIGNATURE_VERIFY_V1, 128, 0)

sponsor_reserved_total = n * max_single_cross_ledger_verification_gas
shared_verification_gas_cap =
  max_execution_gas_per_finalized_block - sponsor_reserved_total
```

其中 `n = validator_set.validators.length`，不是每轮 proposer slot数 `P`；全部 n个 Validator都可以签Vertex并成为 occurrence sponsor。全部运算 checked；bundle validation要求 `sponsor_reserved_total <= max_execution_gas_per_finalized_block`。无 inbound policy时 max为 0。每个 occurrence必须归属于承载该 AvailabilityReference 的、已经验证签名与VertexID的 containing DAGVertex `author_index in [0,n)`；不能归给 Batch author、relayer、首先 gossip的peer或当前slot proposer（除非它恰好就是该containing Vertex作者）。跨作者Batch引用继续合法，费用仍由选择引用的sponsor承担。

`TryChargeCrossLedgerProofSponsorThenShared(sponsor,cost)` 先消耗该 sponsor 在 `max_single_cross_ledger_verification_gas` 内的剩余额度，不足部分再消耗全体共享余量；任一不足都不部分扣款并返回 false。成功时返回`schema_version=1`的精确 `CrossLedgerProofChargeReceiptV1`。由于每个 policy 的实际 cost 不超过 max-single，一个尚未花费 sponsor 份额的 candidate 总能进入一次 proof 验证。f 个 Byzantine sponsor持续提交外层有效、source proof 无效的垃圾或反复引用他人Batch，只能烧自己的 sponsor reserve与可能的 shared pool，不能触碰 honest sponsor 的保留份额。标准 BFT 活性假设再要求至少一个 honest Vertex作者对待引用Batch公平调度，并且只引用已本地完整source-proof、当前policy/window/consumed-state预验通过的 CONSUME；因此它的保留份额每块至少能推进一个最大合法 proof，跨账本子系统不会被 f 个恶意sponsor永久饿死。

`cross_ledger_verification_gas_spent` 是 sponsor-reserved spend 与 shared spend 的 checked 总和。它不进入 Receipt、Header Gas root或 `reserved_gas`，invalid proof 也消耗它；总上限固定复用 active `max_execution_gas_per_finalized_block` 的数值。这样每块在 cache 全失效时触发的 source-proof 密码学工作有硬上界，而 valid winner 的正常执行 Gas仍按第 10 节结算。mandatory `cross_ledger_proof_attempts` 只在 source预算扣款成功时写入，且扣款receipt与`STARTED`必须处于同一原子checkpoint事务；`CROSS_LEDGER_VERIFY_CAP`不写map，后续由另一个occurrence sponsor承载的相同tx仍可使用其保留份额。

cursor不得越过任何 common/source `STARTED`。恢复时必须逐字节重验origin cursor、source binding、tx bytes、author、work cost与charge receipt，然后在相同origin occurrence重跑对应suffix且不重扣；只有确定性协议失败可以落盘`INVALID`。worker超时/取消、磁盘错误、进程资源不足和crypto provider不可用都是本地失败，只能保持/回滚到`STARTED`并暂停生成Header/attestation。source attempt一旦在origin得到terminal状态，本块后续相同tx-id occurrence若先通过已冻结的通用cheap gates并到达source scheduler，必须归类`DUPLICATE_CROSS_LEDGER_ATTEMPT`；已成为winner的同tx后续通常更早命中`DUPLICATE_OCCURRENCE`，cap loser没有attempt，因此不属于source duplicate。

`CROSS_LEDGER_VERIFY_CAP`、`DUPLICATE_CROSS_LEDGER_ATTEMPT` 与 `BLOCK_CAP` 都是无 Receipt、无 nonce、无 accepted-tx-id、无 working-key 的 occurrence 分类；它们不会回退扫描，也不会永久禁止交易在后续 causal input 中重试。stale/future target nonce、target tx 高度窗失败、账户未授权、required gas不足和结构上必然超出块成功 reserve 的 occurrence，必须在 source proof worker/crypto spy 被调用前短路。由于分类顺序已冻结，一个同时具有 stale nonce 与 invalid source proof 的 occurrence 规范分类为 stale，而不是 proof invalid；两者都不影响任何共识树。

source proof 完成后的 key 竞争规则为：

```text
key = VerifiedConsume.consumption_key
state = ReadParentConsumedState(key)
if state is malformed or key/value binding is inconsistent:
    EXECUTION_HALT
if state is PRESENT or key in working_consumption_keys:
    mark CROSS_LEDGER_REPLAY
    continue

if !ExactReservedArtifactMatchesVerifiedInput(...):
    EXECUTION_HALT

select ordinary account nonce winner
working_consumption_keys.add(key)
working_next_nonce[sender] += 1
```

这使不同 relayer、不同 target sender/nonce、不同 proof envelope 对同一 source event 的竞争由全局 canonical occurrence 顺序唯一决定。第一个入选者成功；其后同块或后续块的重放均无 Receipt、无 nonce 消费。实现不得按到达时间、relayer fee、peer 数量、证书 signer subset 或 proof envelope hash选 winner。

`working_consumption_keys` 必须是碰撞安全的 exact Hash32 set；Bloom filter 只能作否定缓存，不能单独拒绝 occurrence。parent lookup 必须以已认证 SMT generation 为准，索引 miss 不能当 non-inclusion。

## 9. 消费状态、Event、Receipt 与查询状态

### 9.1 永久 consumed record

```text
ConsumedCrossLedgerMessageStateV1 {
  schema_version: uint16
  consumption_key: Hash32
  source_network_id: NetworkID
  source_ledger_id: LedgerID
  source_event_id: Hash32
  message_id: Hash32
  message_payload_hash: Hash32
  destination_policy_id: Hash32
  channel_id: Hash32
  source_height: Height
  source_finalized_block_id: Hash32
  source_finality_id: Hash32
  consumer_tx_id: Hash32
  consumer_sender: AccountAddress
  consumed_target_height: Height
  consumed_target_transaction_index: uint32
}

consumed_state_id = DomainHash(
  "CROSS_LEDGER_CONSUMED_STATE",
  target_network_id,
  target_ledger_id,
  canonical(ConsumedCrossLedgerMessageStateV1)
)
```

字段按展示顺序占用 CBOR key `1..16`。执行器必须从 verified source result 与当前 target tx/context 重建全部字段，不能复制 payload 自报的 target location。state value 不保存 application payload，以保持长期重放集合紧凑；payload 由源 proof、目标交易 Body和消费 Event 认证。需要长期读取 payload 的部署必须保留相应历史或 Archive，不能删除 consumed marker 来节省空间。

### 9.2 目标消费 Event 与成功结果

CONSUME winner 原子执行：

1. 再断言 consumed key 在串行可见状态 absent；不一致表示 filter/executor 分叉，`EXECUTION_HALT`；
2. 写入唯一 `ConsumedCrossLedgerMessageStateV1`；
3. 提交普通 relayer sender nonce write；
4. 发出恰好一个消费 Event；
5. 返回固定小对象。

```text
CrossLedgerConsumedEventDataV1 {
  schema_version: uint16
  source_event_id: Hash32
  message_id: Hash32
  message_payload_hash: Hash32
  destination_policy_id: Hash32
  channel_id: Hash32
  application_payload_type: uint32
  application_payload: byte_string
}

Event {
  emitter: UTF8("finalweave/native/cross-ledger")
  topic:   UTF8("consumed/v1")
  data:    canonical(CrossLedgerConsumedEventDataV1)
}

return_data = canonical({source_event_id,message_id,consumption_key})
```

成功 `TransactionResult` 恰有两项 StateChange（consumed state 与 relayer nonce，按通用 `(namespace,key_hash)` 排序）和一个 Event。Receipt 必须 `SUCCESS/NONE`。由于 proof、policy、窗口、relayer、Gas 与全部成功输出已预检并预留，v1 不登记 `CROSS_LEDGER_PROOF_INVALID` 或 `ALREADY_CONSUMED` 失败错误码：这些 occurrence 不是 winner，不能制造一张消费 nonce 的伪失败 Receipt。

应用把 `consumed/v1` Event 当作目标账本上已排序的异步投递。应用处理成功/失败若需要上链，应提交后续普通交易并显式引用 `source_event_id`；它不能改写协议 consumed marker，也不会让原消息重新可消费。

### 9.3 稳定与诊断状态

跨账本查询使用以下状态：

```text
UNKNOWN
PENDING_SOURCE_FINALITY
AVAILABLE_UNCONSUMED
CONSUMED
EXPIRED_UNCONSUMED
POLICY_INACTIVE
```

- `CONSUMED` 是稳定结论：目标最终 Header 下 consumed state inclusion proof，加目标 FinalityProof/CheckpointFinalityProof；若返回成功交易/Receipt/Event，还必须各自验证 inclusion 与 state 字段映射。
- `EXPIRED_UNCONSUMED` 是相对特定 `destination_policy_id/source_event_id` 的稳定结论。它即使在 policy 已从当前 FeatureSet 删除后仍可验证，但必须使用下述历史 policy context + 后续 expiry tip 双上下文 evidence；仅有当前 policy miss 不能推出过期。
- `AVAILABLE_UNCONSUMED` 需要 verified source proof、当前目标最终 Header 位于消息窗口、active policy 精确存在，以及 non-inclusion proof；它只是时点事实，可以转为 CONSUMED 或 EXPIRED。
- `POLICY_INACTIVE` 与 `PENDING_SOURCE_FINALITY` 是诊断，不得附伪造终态；未来治理重新激活同一 policy 内容时前者可能恢复。
- 只有本地索引 miss、peer 未见或 relayer 队列为空时不得声称 EXPIRED/CONSUMED。

普通目标查询响应仍遵循“先建立已验证 Header context，再验 inclusion”的工程规则。服务端不能用一个 `consumed=true` 布尔值替代 SMT proof。

稳定过期 evidence 的 wire schema 固定为：

```text
CrossLedgerExpiredUnconsumedEvidenceV1 {
  schema_version: uint16
  source_proof_envelope: CrossLedgerProofEnvelopeV1
  policy_context_header: FinalizedBlockHeader
  policy_context_feature_set: FeatureSet
  tip_header: FinalizedBlockHeader
  consumption_noninclusion_proof: SparseMerkleProof
}
```

字段按展示顺序占用 CBOR key `1..6`，schema version 固定为 1，完整 canonical evidence不得超过 10,485,760 bytes；各内嵌对象仍分别受自己的更小上限，decoder必须按 checked remaining bytes流式解析。它故意不内嵌未类型化的 target `FinalityProof | CheckpointFinalityProof` union：调用方必须先按自己固定的 target trust mode，分别通过基础或 checkpoint verifier 建立两个 `VerifiedTargetHeaderContext`。第一个 context 必须含与 `policy_context_header/policy_context_feature_set` 逐字节相同的 Header、FeatureSet，以及该 proof 已认证的 ProtocolConfig/GasSchedule；第二个必须含与 `tip_header` 逐字节相同的 Header。API 若支持两种 trust mode，应提供两个显式方法或让 caller 先独立取两份具名 proof，不能看 response 字段后 fallback。

`VerifyCrossLedgerExpiredUnconsumedEvidence(expected_target,expected_destination_policy_id,expected_source_event_id,evidence,verified_policy_context,verified_tip_context)` 必须依次：

1. 验证 evidence 两个 Header/FeatureSet 与调用方两个 verified contexts 逐字节相同；两者 network/ledger 等于 expected target，且 `policy_context_header.height < tip_header.height`；
2. 对历史 context 运行完整 `ValidateExecutionConfigBundle`，再以该 context认证的 ProtocolConfig调用 `ValidateCrossLedgerParametersForLedger`，从其中精确取得 `expected_destination_policy_id` 对应 policy，不允许使用 current policy、response 自报 root或本地“最新 checkpoint”；
3. 用该历史 policy 调用第 7.2 节 source verifier，要求重算的 message policy ID/source event ID分别等于两个 expected ID，再派生 consumption key，并要求 `policy_context_header.height` 位于签名 message 的 `[target_valid_from_height,target_valid_until_height]`；
4. 要求 `tip_header.height > message.target_valid_until_height`，并在 tip 的 `state_root` 下验证 UTF-8 namespace `finalweave/v1/cross-ledger/consumed`、raw key=`consumption_key` 的严格 non-inclusion；索引 miss 不可替代 SMT proof；
5. 依赖 consumed marker 永不删除与窗口之后 CONSUME 永远不能入选，推出该 source event 在目标账本上过去未消费、未来也不能消费，因此返回稳定 `EXPIRED_UNCONSUMED`。

历史 policy context 可以位于窗口任一最终高度，包括 from 或 until；它证明目标曾在消息允许消费的高度认证该 exact policy。若不存在这样的已验证历史 context，只能返回 `POLICY_INACTIVE`/`UNKNOWN` 诊断，不能用 policy 删除动作本身伪造稳定过期结论。

## 10. Gas 与资源计量

### 10.1 新 Gas events

`CROSS_LEDGER_V1` 激活时，GasSchedule exact set 加入：

| operation | 触发 | input bytes | output bytes |
|---|---|---:|---:|
| `CROSS_LEDGER_SEND_V1` | SEND winner 一次 | `len(intent.payload)` | `0` |
| `CROSS_LEDGER_CONSUME_V1` | CONSUME winner 一次 | `len(intent.payload)` | `0` |
| `CROSS_LEDGER_PROOF_VERIFY_V1` | CONSUME source proof 一次 | `len(canonical(proof_envelope))` | `0` |
| `CROSS_LEDGER_SOURCE_SIGNATURE_VERIFY_V1` | 每个实际要求验证的 source GenesisApproval、EpochSealVote、ExecutionAttestation 一次 | `32 + 32 + 64` | `0` |

四项都要求 `base_cost>0 && per_input_byte>0`。proof verifier 即使命中本地 cache，也必须产生同样的逻辑 GasEvent 序列；cache 只能减少物理 CPU。signature events 按 proof 验证顺序生成：Genesis approvals、每个 transition 的 votes、目标 FinalityCertificate attestations，数组内部均按协议 signer 顺序。checkpoint proof 不产生 genesis approval events。

### 10.2 SEND 精确 trace

完整 trace 在通用 `TX_BASE -> ACCOUNT_SIGNATURE_VERIFY*` 后接：

```text
CROSS_LEDGER_SEND_V1(payload_bytes,0)
EVENT_EMIT(len(canonical({emitter,topic})), len(event.data))
RETURN_DATA(0, len(canonical({message_id,message_payload_hash})))
COMMIT_NONCE_UNMETERED
```

### 10.3 CONSUME 精确 trace

完整 trace 在通用 `TX_BASE -> ACCOUNT_SIGNATURE_VERIFY*` 后接：

```text
CROSS_LEDGER_CONSUME_V1(payload_bytes,0)
CROSS_LEDGER_PROOF_VERIFY_V1(proof_envelope_bytes,0)
CROSS_LEDGER_SOURCE_SIGNATURE_VERIFY_V1(128,0) for each logical source signature
STATE_READ(consumed_state_key_bytes, 0)              // winner 要求 absent
STATE_WRITE(consumed_state_key_bytes, consumed_state_value_bytes)
EVENT_EMIT(len(canonical({emitter,topic})), len(event.data))
RETURN_DATA(0, len(canonical(return_data)))
COMMIT_NONCE_UNMETERED
```

proof 的 canonical/static/hash/Merkle 工作由 payload/proof operations 覆盖；source 签名由逐项 signature operation 覆盖，不能额外伪装成 `HOST_ED25519_VERIFY_V1`。目标账户 Envelope 签名仍只计普通 `ACCOUNT_SIGNATURE_VERIFY_V1`。

`RequiredGasForCrossLedgerSend/Consume` 在 winner 选择前用 remaining-budget 算法精确计算；不足是 `STATIC_INVALID`，不是执行期 OOG。CONSUME success reserve 必须覆盖：完整 target Envelope、成功 Receipt、一个 Event、两项 StateChange、consumed value、return data、nonce write、Event/block Body array-header 增长与签名 `gas_limit` 的块预留。任一资源不适配均为无 Receipt 的 `BLOCK_CAP`，并且不写 `working_consumption_keys`。

### 10.4 资源与性能实现

实现必须分别计量并限制：canonical proof bytes、transition 数、source signature 数、四条 Merkle path、message payload、consumed value、Event、return data、state read/write、Body 与 block gas。任何 checked 加法/乘法溢出是 invalid config 或 `EXECUTION_HALT`，不能 saturate 后继续。

此外，每次 FinalizedBlock occurrence scan 先由通用filter初始化`inflight_occurrence_scan`、`completed_scan_reserved_units[0..n-1]`、`completed_scan_shared_units`、`attempted_prefilter_tx_results: tx_id -> PrefilterAttemptV1`及全部n个occurrence sponsor的common prefilter reserve/shared spend。每个raw occurrence先按verified source给出的containing Vertex sponsor与item length收`PrefilterScanWorkCostV1`，通过cheap winner准备后才向同一sponsor按`PrefilterExpensiveWorkCostV1(tx)`收common suffix；CONSUME proof subtree不进入common expensive field-path，但其完整item bytes仍付一次scan费。target Envelope signatures只允许在common suffix扣款与`STARTED`原子提交后验证。completed scan不永久保存逐笔receipt；`FinishOccurrenceAndCheckpoint`把当前inflight receipt原子折叠进上述aggregate后再清记录/推进cursor。

第8.3节另行初始化source-proof sponsor-reserved/shared scheduler、`cross_ledger_proof_attempts: tx_id -> CrossLedgerProofAttemptV1`与总`cross_ledger_verification_gas_spent=0`。通用cheap gates与charged target auth之后必须先查source attempt map：different-origin terminal直接duplicate，different-origin `STARTED`是恢复损坏；只有attempt absent或属于当前origin才做tentative replay。随后仅对replay必要检查通过且attempt absent的candidate，按同一containing Vertex sponsor扣减`ProofAndSourceSignatureGas`；增加前使用remaining-budget比较，不能先做可能溢出的加法。invalid common/source suffix各自在所属预算内不退款，跨块可选cache hit不减免，超过对应sponsor+shared可用额度的occurrence不调用相应worker。两套scheduler、scan aggregate/可选inflight attempt及两个attempt map都是不写SMT/Body/Receipt的filter状态，但任何中途resume checkpoint都必须保存足以按第13节重算全部spend的精确状态；若选择不保存，只能从块开头确定性重扫，不能从中间cursor以空map或空预算继续。

proof 验证可以：

- 按 `(authenticated_feature_set_hash,policy_id,cross_ledger_proof_envelope_hash)` 缓存完整 verified result；lookup 时 proof hash必须从本次完整 bounded canonical bytes重算，缓存值还要逐字段匹配 target/source context与重算 artifact。未经验证的 envelope自报 `source_event_id/source_finality_id` 绝不能作为跳过密码学验证的 cache key；
- 批量执行 strict Ed25519 后逐项 fallback，以得到与 individual verification 相同结论；
- 流式解码 transition chain、并行校验独立 Merkle path 与签名；
- 在 admission、mempool 和 Batch ingest 复用只读 cache。

但共识 occurrence filter 必须能在 cache 全失效时得到相同结果；cache key缺少 authenticated FeatureSet/policy/proof-envelope hash，或反过来把 proof envelope hash当 source event/consumption identity，都不安全。昂贵 proof进入 worker前必须完成第 8.3 节整个廉价前缀，不能只检查长度与 policy ID。链外 API/mempool配额可以更早背压，但不能取代 canonical scan的verification-work counter，也不能让一个已进入 canonical causal input的协议有效 occurrence被永久忽略。

## 11. Epoch、reconfiguration 与 trust-root 轮换

### 11.1 Source epoch

source event 所在 epoch 的 ValidatorSet、ProtocolConfig、FeatureSet 与 GasSchedule由 source proof 认证。EpochTransitionProof 必须从 policy 的 genesis/checkpoint root 连续验证到 event epoch，且每跳使用旧 source ValidatorSet 的 q 个 Consensus Key signer。source Validator key 轮换、P 变化或执行参数变化不会绕过这条链。

目标必须检查 event epoch 的 source FeatureSet 确实激活 `CROSS_LEDGER_V1`，并按该 epoch 的 outbound 参数重放 SEND 规则；仅因目标也认识 payload type 4 不足以接受源事件。

### 11.2 Target epoch

target source policies 随目标完整 `(ValidatorSet,ProtocolConfig,FeatureSet,GasSchedule)` generation 在 epoch 边界原子激活。它们不能由管理 API、relayer 或普通状态 PUT 热更新。

policy 内容任一字段变化都会产生新 `policy_id`。消息签入旧 ID，因此：

- 若治理希望 grace period，新的 FeatureSet 必须同时保留旧/new 两个 policy entry；
- 删除旧 policy 后，尚未消费且绑定旧 ID 的消息变为 `POLICY_INACTIVE`，不能自动套用新 root；
- checkpoint 轮换是新 policy，不是“更新同一个 ID”；
- 已写 consumed marker 永久有效，不因 policy 删除、source ledger 关闭或 target epoch 切换而删除。

Genesis 与 EpochSeal 的 `ValidateExecutionConfigBundle` 必须验证 CrossLedger typed parameters、policy 排序/上限，以及四个条件 Gas operations 的 exact set；账本绑定点另以同一已认证 ProtocolConfig调用 `ValidateCrossLedgerParametersForLedger`，验证 target-context规则、target-scoped policy IDs与Envelope representability。新 epoch signer在完整 Feature/Gas对象缺失、policy编码不合法或contextual validation失败时不得 readiness。

### 11.3 信任假设

GENESIS policy 表示目标治理明确接受 source `genesis_reference` 及其治理/Validator 证明链。CHECKPOINT policy 表示目标治理明确接受该 source checkpoint 之前历史不可独立审计的额外假设。普通 proof API 返回的 anchor 没有安装权；只有目标账本的治理 reconfiguration 能让其 ID进入 active policy。

## 12. API、SDK 与 relayer

### 12.1 API 表面

建议的最小 API：

```text
ListCrossLedgerSourcePolicies
GetCrossLedgerMessageBySourceEventID
GetCrossLedgerSourceProof
SubmitCrossLedgerConsumeTransaction
GetCrossLedgerConsumption
GetCrossLedgerConsumptionStatus
GetCrossLedgerExpiredUnconsumedEvidence
SubscribeFinalizedCrossLedgerMessages
SubscribeFinalizedCrossLedgerConsumptions
```

`GetCrossLedgerSourceProof` 返回第 7 节 envelope，并接受调用者显式指定的 `destination_policy_id/trust_root_kind/trust_root_id`；服务端可以因没有相应历史返回 `HISTORY_PRUNED`，但不能替调用者换一种 root。source 服务端不决定 target policy，target/relayer SDK仍必须从目标已认证 FeatureSet 取得并比对。

`GetCrossLedgerConsumption` 返回：目标 Header、consumed value 与 SMT proof，可选的 consume transaction/Receipt/Event 与各自 Merkle proof。它不携未类型化的 finality proof；SDK先按自己的 target trust mode调用 `GetFinalityProof` 或 `GetCheckpointFinalityProof`，验证 Header逐字节相同后再验 State/Receipt/Event。

`GetCrossLedgerExpiredUnconsumedEvidence` 请求必须绑定 expected target network/ledger、`destination_policy_id` 与 `source_event_id`，返回第 9.3 节 schema：服务端从索引选择一个 message window 内、包含 exact policy ID 的历史 policy context，再返回严格晚于 until 的 target tip与该 tip state root下 non-inclusion。两份 Header都只是待验证候选；调用方必须分别取得并验证基础或 checkpoint target finality proof，再把请求中的三个 expected值与两个 `VerifiedTargetHeaderContext` 一并传给 SDK verifier。历史 Header/FeatureSet或 source proof已裁剪时返回 `HISTORY_PRUNED`，不能改用 current policy或只返回 `POLICY_INACTIVE` 冒充稳定证据。

所有 pagination token 绑定 source/target network、ledger、policy ID、channel、finalized height 和排序游标。事件索引只用于定位候选；proof 缺失时不能返回可消费结论。

### 12.2 SDK verifier

SDK 至少提供：

```text
VerifyCrossLedgerSourceProof
DeriveCrossLedgerSourceEventID
DeriveCrossLedgerConsumptionKey
VerifyCrossLedgerConsumedState
VerifyCrossLedgerConsumptionResponse
VerifyCrossLedgerExpiredUnconsumedEvidence
```

普通 source/available 验证器的 expected target context 与 active policy 必须由调用方已验证的当前目标 Header/config链提供；expired verifier 则显式接收历史 policy context与后续 tip两个已验证上下文。任何 API response 内的“active policy”都只是待匹配内容，不能自证。

### 12.3 不受信 relayer 流程

```text
1. 验证 target finalized Header 与 active FeatureSet
2. 选择其中精确 source policy；记录 policy_id
3. 订阅 source finalized message/v1 Event
4. 获取 transaction/receipt/two-event paths + selected source finality proof
5. 本地运行 VerifyCrossLedgerSourceProof(policy,...)
6. 查询 target consumption_key 的 finalized SMT non-inclusion
7. 构造自己的 target account nonce、交易窗口和 gas_limit
8. 提交 CROSS_LEDGER_CONSUME_V1；超时只重试同一 tx_id
9. 以 target consumed-state/Receipt proof 判断输赢
```

多个 relayer 并发是正常情形。失败提交方不能从 `CROSS_LEDGER_REPLAY` 获得 FAILED Receipt，因为它不是 winner；它应查询 consumed state，确认哪个 target tx 已最终成功。relayer 信誉、奖励、费用和抢跑保护属于链外策略或未来 fee profile，不进入 v1 canonical winner。

## 13. 存储、崩溃恢复与裁剪

consumed namespace 是共识状态的一部分，必须进入 SMT、Snapshot、state generation checksum 与原子 publication。它没有 TTL，不能按 source proof、Receipt、Event、Body、policy 或 epoch 的裁剪窗口删除。

PreparedExecution 至少保存：

```text
verified_policy_id
source_event_id
source_finality_id
proof_envelope_hash
verified_consume_artifact
verified_consume_digest
consumption_key
consumed_state_bytes
success_result_reservation
state/event/return journal
occurrence_filter_final_checkpoint_hash
```

这些字段是本地恢复辅助，不替代对artifact binding/digest及source proof语义的重新核验。崩溃语义：

- 在 causal occurrence scan 中间持久化 resume cursor 时，checkpoint 必须同时保存/校验completed cursor、`completed_scan_reserved_units[0..n-1]`、`completed_scan_shared_units`、可选`OccurrenceScanAttemptV1{STARTED}`、`working_consumption_keys`、`attempted_prefilter_tx_results`、`cross_ledger_proof_attempts`及两套逐occurrence-sponsor reserve/shared/total spend。completed scan receipt不逐笔保留；aggregate绝不能包含当前inflight receipt；

- common spend必须可由`completed_scan_*`加可选inflight scan receipt，再加全部`PrefilterAttemptV1.charge_receipt`逐sponsor/shared精确重算；source spend必须完全由全部`CrossLedgerProofAttemptV1.charge_receipt`逐sponsor/shared重算。重算值必须分别等于保存的counter/total，每个receipt sponsor必须等于其origin的verified containing DAGVertex作者；source binding还须独立重验BatchHeader作者，所有checked和必须无溢出。任一aggregate/map/counter/receipt缺失或不一致时只能从块开头重扫，不能从中间cursor以空预算继续；

- scan/common/source扣款与各自`STARTED`必须原子提交，durable cursor仍指向同一`origin_occurrence_cursor`；恢复必须重验source binding与完整tx bytes，在origin重跑未完成worker而不重新扣款。`FinishOccurrenceAndCheckpoint`还必须原子执行“inflight scan receipt并入completed aggregate、清inflight、推进cursor”，不能在崩溃后漏算或重复并入。cursor与origin不一致、`STARTED`越过cursor、`VALID`缺artifact/digest或非`VALID`携带artifact均是checkpoint损坏；local failure保持`STARTED`并暂停，不能写`INVALID`或形成Header/attestation；

- winner 选择后、state generation fsync 前崩溃：丢弃 scratch，以同一 causal input 重验 proof/parent non-inclusion并重执行；
- state generation fsync 后、attestation 前崩溃：恢复并核对完整 journal/Header，不得另选 relayer；
- marker/certificate 原子发布后：consumed state、成功 Receipt、Event和 public cursor 必须一起可见；
- 任何只看到 consumed marker 而看不到认证它的 target Header generation，或 target root 宣称 present 但 value 缺失/损坏的情况，必须 `SAFETY_HALT`。

源 proof envelope、source Body/Event 与目标 consume Body/Event 可以按 `LocalHistoryPolicy` 裁剪，但只能返回 `HISTORY_PRUNED` 与不受信 Archive locator。裁剪不影响重放安全，因为永久 consumption key 已进入 state root；裁剪后若要向新客户端解释 payload，仍必须从 Archive 取回并完整验证原 proof，不能从 consumed marker 的 payload hash伪造原文。

## 14. 攻击面与必拒绝行为

实现必须显式抵御：

- proof 自报另一个 genesis/checkpoint root、错误 source ledger 或 verifier fallback；
- 合法 source Header 配伪造 transaction、FAILED Receipt、错误 event index 或只提供一条未绑定 Receipt 的 Event path；
- 把普通应用伪造的相同 emitter/topic 当 native SEND；
- 修改 message destination、policy、channel、有效窗、application type/payload 或 salt；
- 用不同 FinalityCertificate signer subset、proof field 顺序或 relayer tx 重放同一 source event；
- 相同 message ID 的不同真实 source occurrences 被错误合并；
- consumed index miss 被当作 SMT non-inclusion，Bloom false positive被当作已消费；
- policy 轮换后把旧消息隐式映射到新 policy；
- source/target epoch 数组、签名或 proof bytes 先分配后检查；
- proof cache 不绑定 active FeatureSet/policy，或 cache corruption 被当作共识事实；
- 在scan/common/source扣款与`STARTED`原子落盘前启动对应parser/crypto，恢复时重复扣款，或让cursor越过任一`STARTED`；
- 把proof subtree重复计入common expensive field-path、绕过完整item scan费，或common `VALID`后重跑policy/RequiredGas/success-reserve廉价前缀；
- 在 message window 外生成 FAILED Receipt，或让 replay occurrence 消耗 target account nonce；
- 删除 consumed marker 以回收状态、在 Snapshot 中漏掉 marker、或在 state cap 下调后把旧 marker 判非法；
- 以墙钟、NTP、source height 与 target height差值决定消息过期。

如果 source proof 在密码学上有效，但 proof envelope/transition 数超出目标已认证 policy 上限，它仍是对当前 policy 不可接受的交易输入。relayer 可等待目标治理激活适当 checkpoint policy，不能让单节点放宽规则。

## 15. 规范伪代码

下列函数是协议第四篇通用 occurrence filter 对 `CROSS_LEDGER_CONSUME_V1` 的**完整 specialization**，已包含raw-item scan扣款与common expensive suffix扣款。调用方进入时当前occurrence尚未被扣这两段预算；不得先运行通用scan/common charge再调用本函数，否则会双扣。若实现已经在通用循环内完成这两段，只能直接调用第8.3节的source overlay，不得再次进入本完整函数；两种集成方式必须互斥并产生相同分类与checkpoint。

```text
TrySelectCrossLedgerConsume(occurrence, target_context, scan):
  n = target_context.validator_set.validators.length
  source = scan.VerifiedOccurrenceSourceAtCursor(occurrence.cursor)
  require source.batch_id == occurrence.batch_id and
          source.batch_transaction_index == occurrence.transaction_index
    else RECOVERY_STATE_CORRUPT
  batch_author = target_context.authenticated_batch_headers[
    source.batch_id].author_index
  sponsor = target_context.authenticated_dag_vertices[
    source.vertex_ordinal].author_index
  require sponsor in [0,n) and batch_author in [0,n)
    else RECOVERY_STATE_CORRUPT
  item_length = source.causal_occurrence_item_canonical_length
  source_binding = OccurrenceScanSourceBindingV1(
    schema_version=1,
    cursor=occurrence.cursor,
    vertex_ordinal=source.vertex_ordinal,
    availability_reference_index=source.availability_reference_index,
    batch_id=source.batch_id,
    batch_transaction_index=source.batch_transaction_index,
    item_canonical_length=item_length,
    containing_vertex_author_index=sponsor,
    batch_author_index=batch_author)
  source_binding_hash = DomainHash(
    "OCCURRENCE_SCAN_SOURCE_BINDING",
    target_context.network_id,
    target_context.ledger_id,
    canonical(source_binding))

  scan_attempt = scan.inflight_occurrence_scan
  if scan_attempt is ABSENT:
    scan_cost = PrefilterScanWorkCostV1(item_length)
    scan_charge = scan.TryChargePrefilter(sponsor, scan_cost)
    if scan_charge is ABSENT:
      StreamCompareWholeItemAgainstVerifiedSourceWithoutEnvelopeDecode(source)
      return PREFILTER_SCAN_CAP
    scan_attempt = OccurrenceScanAttemptV1(
      schema_version=1,
      status=STARTED,
      origin_occurrence_cursor=occurrence.cursor,
      source_binding_hash=source_binding_hash,
      sponsor_author_index=sponsor,
      item_length=item_length,
      scan_work_cost=scan_cost,
      charge_receipt=scan_charge)
    scan.AtomicInstallOccurrenceScanAttemptAndChargeWithoutAdvancingCursor(
      scan_charge, scan_attempt)
  else:
    require scan_attempt.status == STARTED and
            scan_attempt.origin_occurrence_cursor == occurrence.cursor and
            VerifyOccurrenceScanAttemptBinding(
              scan_attempt, source_binding, source_binding_hash, sponsor)
      else RECOVERY_STATE_CORRUPT

  outer = BoundedParseCrossLedgerConsumeOuter(occurrence.bytes)
    else return STATIC_INVALID
  tx = outer.tx
  require target active FeatureSet contains exact CROSS_LEDGER_V1 tuple
  require tx.payload_type == CROSS_LEDGER_CONSUME_V1
  require tx.authorized_access_scope == []

  if tx.valid_from_height > target_context.height: return FUTURE_HEIGHT
  if tx.valid_until_height < target_context.height: return EXPIRED_OCCURRENCE
  if tx.tx_id in scan.accepted_tx_ids: return DUPLICATE_OCCURRENCE
  account = target_context.parent_account_view.get(tx.sender)
  if account MISSING or
     tx.signer_policy_hash != account.active_policy_hash:
    return AUTH_INVALID_OCCURRENCE
  next = scan.working_next_nonce[tx.sender]
  if next == UINT64_MAX: return NONCE_EXHAUSTED
  if tx.nonce < next: return STALE_OR_DUPLICATE_NONCE
  if tx.nonce > next: return FUTURE_NONCE

  if scan.winners.length >=
     target_context.config.max_transactions_per_finalized_block:
    return BLOCK_CAP
  remaining_gas = checked_sub(
    target_context.config.max_execution_gas_per_finalized_block,
    scan.reserved_gas) else EXECUTION_HALT
  if tx.gas_limit > remaining_gas: return BLOCK_CAP

  prepared = TryPrepareCheapPayloadCandidateAndBlockReservation(
    scan.body_and_mandatory_write_reservation,
    tx,
    target_context.height,
    scan.winners.length,
    target_context.active_bundle,
    target_context.parent_state)
    else return its deterministic STATIC_INVALID or BLOCK_CAP classification
  tentative = prepared.cross_ledger_context
  gas = prepared.required_gas
  reservation = prepared.success_result_reservation
  tentative_key = tentative.consumption_key

  common_attempt = scan.attempted_prefilter_tx_results.get(tx.tx_id)
  if common_attempt is ABSENT:
    common_cost = PrefilterExpensiveWorkCostV1(tx)
    require PREFILTER_SUFFIX_BASE_UNITS_V1 == 1 and
            common_cost >= PREFILTER_SUFFIX_BASE_UNITS_V1
    common_charge = scan.TryChargePrefilter(sponsor, common_cost)
      else return PREFILTER_VERIFY_CAP
    common_attempt = PrefilterAttemptV1(
      schema_version=1,
      status=STARTED,
      origin_occurrence_cursor=occurrence.cursor,
      sponsor_author_index=sponsor,
      work_cost=common_cost,
      charge_receipt=common_charge)
    scan.AtomicInstallCommonAttemptAndChargeWithoutAdvancingCursor(
      tx.tx_id, common_charge, common_attempt)
  if common_attempt.status == STARTED:
    require common_attempt.origin_occurrence_cursor == occurrence.cursor and
            VerifyPrefilterAttemptChargeAndSponsor(common_attempt, sponsor, tx)
      else RECOVERY_STATE_CORRUPT
    common_outcome = RunChargedCanonicalStaticAuthAndGovernanceSuffix(
      tx, target_context.height, target_context.parent_account_view,
      target_context.active_bundle, target_context.governance_policy)
    if common_outcome is LOCAL_FAILURE:
      return LOCAL_EXECUTION_PAUSE_WITHOUT_CURSOR_ADVANCE
    scan.AtomicSetCommonAttemptTerminal(
      common_attempt, common_outcome) // VALID或deterministic INVALID；不改charge
  if common_attempt.status == INVALID:
    return STATIC_OR_AUTH_INVALID_OCCURRENCE

  // 这里只覆盖通过更早通用cheap gates后的source-scheduler分类。
  proof_attempt = scan.cross_ledger_proof_attempts.get(tx.tx_id)
  if proof_attempt exists and
     proof_attempt.origin_occurrence_cursor != occurrence.cursor:
    require proof_attempt.status != STARTED else RECOVERY_STATE_CORRUPT
    return DUPLICATE_CROSS_LEDGER_ATTEMPT

  tentative_parent_value = ReadParentConsumedState(tentative_key)
  if tentative_parent_value malformed or tentative_key/value binding mismatches:
    EXECUTION_HALT
  if tentative_parent_value PRESENT or
     tentative_key in scan.working_consumption_keys:
    if proof_attempt is ABSENT: return CROSS_LEDGER_REPLAY
    else RECOVERY_STATE_CORRUPT

  if proof_attempt is ABSENT:
    verify_cost = ProofAndSourceSignatureGas(tentative)
    proof_charge = scan.TryChargeCrossLedgerProofSponsorThenShared(
      sponsor, verify_cost)
      else return CROSS_LEDGER_VERIFY_CAP
    proof_attempt = CrossLedgerProofAttemptV1(
      schema_version=1,
      status=STARTED,
      origin_occurrence_cursor=occurrence.cursor,
      sponsor_author_index=sponsor,
      work_cost=verify_cost,
      charge_receipt=proof_charge,
      verified_consume=ABSENT,
      verified_consume_digest=ABSENT)
    scan.AtomicInstallCrossLedgerAttemptAndChargeWithoutAdvancingCursor(
      tx.tx_id, proof_charge, proof_attempt)

  if proof_attempt.status == STARTED:
    require proof_attempt.origin_occurrence_cursor == occurrence.cursor and
            VerifyCrossLedgerAttemptChargeAndSponsor(
              proof_attempt, sponsor, tentative)
      else RECOVERY_STATE_CORRUPT
    proof_outcome = VerifyCrossLedgerSourceProof(
      target_context,
      target_context.active_cross_ledger_feature,
      outer.payload.proof_envelope)
    if proof_outcome is LOCAL_FAILURE:
      return LOCAL_EXECUTION_PAUSE_WITHOUT_CURSOR_ADVANCE
    if proof_outcome is deterministic INVALID:
      scan.AtomicSetProofAttemptInvalid(proof_attempt)
    else:
      artifact = CanonicalizeVerifiedConsumeV1(
        target_context, proof_outcome) // 只从authenticated policy与verified proof派生
      digest = DomainHash(
        "CROSS_LEDGER_VERIFIED_CONSUME",
        target_context.network_id,
        target_context.ledger_id,
        canonical(artifact))
      scan.AtomicSetProofAttemptValid(proof_attempt, artifact, digest)
  if proof_attempt.status == INVALID:
    return STATIC_INVALID

  verified = VerifyStoredConsumeArtifactAndDigest(
    proof_attempt, target_context, outer.payload.proof_envelope)
    else RECOVERY_STATE_CORRUPT
  if outer.payload.source_event_id != verified.source_event_id:
    return STATIC_INVALID
  if target_context.height < verified.message.target_valid_from_height:
    return CROSS_LEDGER_FUTURE_HEIGHT
  if target_context.height > verified.message.target_valid_until_height:
    return CROSS_LEDGER_EXPIRED_OCCURRENCE

  key = verified.consumption_key
  if key != tentative_key: EXECUTION_HALT
  verified_parent_value = ReadParentConsumedState(key)
  if verified_parent_value malformed or key/value binding mismatches:
    EXECUTION_HALT
  if verified_parent_value PRESENT or key in scan.working_consumption_keys:
    return CROSS_LEDGER_REPLAY
  if !ExactReservedArtifactMatchesVerifiedInput(reservation, tx, verified, key):
    EXECUTION_HALT

  scan.select_success_only_winner(tx, reservation)
  scan.working_consumption_keys.add(key)
  scan.working_next_nonce[tx.sender] = checked_add(next, 1)
  return SelectedCrossLedgerConsume{verified,key,gas,reservation}

ExecuteCrossLedgerConsume(serial_state, tx, verified):
  require serial_state.get(consumed_namespace, verified.key) == ABSENT
    else EXECUTION_HALT
  record = BuildConsumedState(tx, verified)
  event = BuildConsumedEvent(verified.message)
  serial_state.put(consumed_namespace, verified.key, canonical(record))
  CommitOrdinaryWinnerNonce(tx.sender)
  return SUCCESS(record,event,BuildReturnData(verified))
```

除 `LOCAL_EXECUTION_PAUSE_WITHOUT_CURSOR_ADVANCE` 外，上述每个分类返回与成功winner返回都隐含`FinishOccurrenceAndCheckpoint`：若当前存在inflight scan，先以checked arithmetic把其receipt的reserved部分并入`completed_scan_reserved_units[sponsor]`、shared部分并入`completed_scan_shared_units`；随后在同一原子事务中提交terminal attempt/map、budget、working set与派生状态、清除inflight，最后把cursor推进一项。`PREFILTER_SCAN_CAP`没有inflight receipt，不能改aggregate。completed aggregate、inflight receipt与cursor三者不得出现重复归集或中间可见状态。local failure必须保留或回滚到同一`STARTED`与origin cursor；任何实现都不得先推进cursor再异步补写attempt结果。

生产实现可以并行 proof 验证和投机执行，但 exact access resolver 必须声明 consumed key 与 nonce key。相同 key 的竞争自然形成依赖边；prefix certification仍必须得到与上述串行机逐字节相同的 winner、Gas trace、StateChanges、Event、Receipt和 roots。

## 16. 必测场景

### 16.1 编码、ID 与 trust policy

- 所有新 schema（含 `CrossLedgerExpiredUnconsumedEvidenceV1`、`CanonicalVerifiedConsumeV1`、`CrossLedgerProofAttemptV1`与charge receipt）的 canonical bytes、field-order golden与unknown-schema拒绝；attempt status恰为`1/2/3 = STARTED/VALID/INVALID`，逐一覆盖artifact/digest presence matrix及unknown status。`CROSS_LEDGER_VERIFIED_CONSUME`在两个target Ledger必须不同，artifact每个字段单 bit篡改均使digest失配；expired evidence完整 bytes做 10 MiB exact/+1和内嵌对象先越界向量。
- Feature tuple `(2,1,1)` 的空 source policy outbound-only 正例；policy/channel/relayer `MAX/MAX+1`、乱序、重复、错误 presence matrix与 unknown enum。
- `OneSignerCrossLedgerConsumeEnvelopeOverheadV1(proof_len)` 在 `0/23/24/255/256/65,535/65,536/policy-max` 的 exact length golden；policy overhead cap 恰等 template 接受、少 1拒绝，`proof_cap + overhead_cap == max_transaction_bytes` 接受、多 1/checked overflow拒绝；实际完整 Envelope overhead恰等 policy cap接受、多 1在 source crypto前拒绝。
- 相同 policy 内容在两个 target Ledger 上产生不同 ID；消息绑定一个 ID 时另一 Ledger拒绝。
- proof 自报 root 与 active policy 不同、proof type 与 policy kind 不同、两个 optional proof 同时存在/缺失、verifier fallback全部拒绝。

### 16.2 SEND 与 source identity

- 空/最大 payload、窗口单高度、最大 inclusive span、span 多 1、接近 `UINT64_MAX` checked subtraction。
- source tuple/sender、destination、policy、channel、salt、payload/type 任一改变都会改变 ID或验证失败。
- source transaction/Receipt/per-tx event/block event 任一路径错 kind/index/count/sibling/root拒绝；FAILED Receipt拒绝。
- 两笔不同 source tx 发送相同 message core：message ID相同、source event ID不同且可分别消费。
- 相同 source event 使用不同合法 FinalityCertificate signer subset：proof envelope hash可不同，source event ID必须相同。

### 16.3 Finality 与 epoch

- GENESIS 与 CHECKPOINT 两条正例；wrong expected root、旧/新 ValidatorSet混签、缺/重/乱 transition、q-1/q+1 signer拒绝。
- source event epoch 的 Feature 未激活 SEND、Gas registry缺项、source 目标 epoch Feature/Gas内容错配全部拒绝。
- transition/signature/proof canonical bytes恰等 policy limit接受，多 1在大分配/验签前拒绝。
- target epoch 同时保留 old/new policy 的 grace 正例；删除 old 后旧消息 POLICY_INACTIVE；不得套用 new root。

### 16.4 并发、dedup 与 nonce

- 2、64、1,024 个不同 relayer 对同一 source event 并发，所有节点只选择 canonical 第一个可完整预留的 occurrence。
- 第一个 occurrence 因 `BLOCK_CAP` 跳过时不写 working key，后一个较小合法 occurrence可赢。
- parent state 已 consumed、同块 working set 已占用、Bloom false positive、索引 miss与真实 SMT non-inclusion分别测试。
- replay/future/stale account nonce 的固定分类顺序；replay occurrence无 Receipt、不耗 nonce、不进 accepted tx-id/consumption set。
- `CausalOccurrenceCursorV1`与同时绑定containing Vertex author、Batch author的`OccurrenceScanSourceBindingV1`逐字段golden；manifest/item/occurrence/frame offset、vertex/reference/Batch/transaction index、item length或任一作者字段任一bit变化都改变`OCCURRENCE_SCAN_SOURCE_BINDING` hash。containing Vertex author是唯一occurrence sponsor，Batch author只认证来源；二者不同仍是合法输入。`PrefilterScanWorkCostV1(item_length)`在chunk边界的`limit-1/limit/limit+1`，以及scan/common/source三段charge receipt逐项golden；三类typed field count全为0时common suffix仍恰为`PREFILTER_SUFFIX_BASE_UNITS_V1=1`，proof subtree任意变化不增加该base且source scheduler绝不计此base。8 MiB proof完整字节只进入一次scan cost，proof subtree的keys/signatures/registry entries不进入common typed units，source Finality/Merkle/signatures只进入source scheduler。`PREFILTER_SCAN_CAP`路径必须完成verified-source逐字节stream compare，但Envelope decode、tx-id、SMT、target/source crypto spy调用数都为0，且不写common/source attempt map。完整specialization与“通用scan/common后只调source overlay”两种互斥集成必须得到相同结果；预扣后误入完整函数的double-charge mutant必须失败。
- 8 MiB规范proof分别装入未授权target account、target tx future/stale nonce、target tx窗口外、声明message窗口外、tentative parent/working replay与`gas_limit=1` occurrence；crypto spy必须证明source Finality/Merkle/Ed25519 verifier调用次数为0。组合invalid source proof时仍按廉价reject-only前缀分类；tentative absent后必须以verified key重算复核。common terminal `VALID`后设置spy，证明source suffix只消费既有`prepared.cross_ledger_context`，不会重新解析policy、计算RequiredGas、检查声明窗口/tentative key或申请第二份success reserve。
- proof-work scheduler 的 sponsor-reserved/shared `limit-1/limit/limit+1`、invalid proof不退款、跨块 cache hit/miss等额、同块相同 tx-id只执行一次source crypto、1/2/32 worker乱序完成；首个source cap loser不得写map，后续另一sponsor的相同tx可尝试。首个attempt terminal但未入选winner时，让later相同tx通过更早通用cheap gates，必须在tentative replay前得到`DUPLICATE_CROSS_LEDGER_ATTEMPT`；首个attempt已成为winner时，later同tx按已冻结的更早顺序得到`DUPLICATE_OCCURRENCE`或其他更早通用分类。attempt absent时仍先做tentative replay，replay loser不得扣source预算。设置`P<n`并使用`sponsor_author_index=P`、`n-1`证明按n分配无越界；超过者统一`CROSS_LEDGER_VERIFY_CAP`，所有实现与串行oracle一致。
- 对scan/common/source分别在“扣款前、charge+STARTED原子提交后、worker中、terminal写入前后、Finish归集inflight receipt前后、清inflight前后、cursor推进前后”逐点kill；恢复时origin worker可以重跑但logical charge恰为一次，cursor不得越过`STARTED`。timeout/cancel/disk/resource/crypto-provider故障保持`STARTED`并暂停，不得变成`INVALID`；cursor/origin、containing Vertex sponsor、Batch author、cost、receipt任一篡改，`VALID`缺artifact/digest、非`VALID`携带artifact或digest破坏都必须判checkpoint损坏。
- common对账golden必须以`completed_scan aggregates + optional inflight receipt + all common attempt receipts`重算，source对账golden必须以all source attempt receipts重算；completed scan不要求保留逐笔receipt。分别注入aggregate漏一次/重复一次、把inflight提前并入aggregate、清inflight但未归集、错误sponsor bucket/shared bucket、map删项和counter不等的mutant，均须拒绝checkpoint或从块首重扫；从中间cursor清空任一状态同样必须被捕获。
- target Envelope坏签名、坏strict key与source proof坏签名分别只消耗通用prefilter和source-proof预算；前者在common charge+`STARTED`成功前不得调用target crypto，后者在source charge+`STARTED`成功前不得调用source crypto，且source工作不得被common cost重复计数。
- f 个 Byzantine Vertex sponsor连续多块引用账户/nonce有效但 source proof无效的最大成本 occurrence，并耗尽自己的reserve与shared pool；honest Vertex sponsor保留份额仍必须验证并最终入选其本地完整预验、公平引用的合法 CONSUME。另构造Byzantine Vertex反复引用honest validator早先签发的大Batch、且不做BatchID去重的场景，证明scan/common/source三段receipt全部归给各自containing Vertex sponsor，honest Batch author的reserve保持不变；shared耗尽后，该honest validator作为新Vertex sponsor仍能用自己的reserve推进最大合法CONSUME。任何把费用归到Batch author、relayer、gossip peer或slot proposer，允许Byzantine sponsor消耗honest sponsor reserve，或让`PREFILTER_SCAN_CAP`/`PREFILTER_VERIFY_CAP`/`CROSS_LEDGER_VERIFY_CAP`写入不应存在attempt map的实现都必须拒绝。
- 同 message ID不同 source event不误去重；同 source event不同 proof/target sender仍必须去重。
- exact access graph为相同 consumption key建立依赖；1/2/32 worker、逆序完成与一次串行 suffix fallback均等于串行 oracle。

### 16.5 Gas、资源与 Receipt

- SEND/CONSUME完整 GasEvent golden，cache hit/miss、individual/batch Ed25519路径得到相同逻辑 trace与 gas_used。
- proof signature event顺序、数量少/多 1、proof byte/Event/state/return/Body/write/gas各 `limit-1/limit/limit+1`。
- gas 差 1、success reserve差 1均在 winner前拒绝；不能产生 OOG/STATE_LIMIT FAILED Receipt。
- 成功 CONSUME恰有一个 Event、两项 StateChange、正确 return hash和 SUCCESS Receipt；唯一 consumed value与 target tx/height/index精确匹配。
- target window 未来/过期、invalid proof、policy inactive、replay和 block cap均无 Receipt。

### 16.6 状态、证明、恢复与裁剪

- consumed SMT inclusion/non-inclusion、Snapshot round-trip、state cap下调后的保留与proof。
- PreparedExecution、SMT generation、attestation intent、certificate、publish marker前后逐点 kill；恢复后恰好零或一个完整消费结果。
- consumed state present但value缺失/损坏、Snapshot漏 marker、marker/root不一致必须停机。
- source/target历史裁剪后 marker仍阻止重放；payload查询返回 HISTORY_PRUNED，Archive对象仍需完整 source/target proof验证。
- `CONSUMED` 与 `EXPIRED_UNCONSUMED` evidence 正/负例：历史 policy仍 active、之后已删除、grace old/new三条正例；请求 expected policy/source-event ID错配、历史 policy context不在 message window、未独立最终验证、FeatureSet不匹配、tip不是同一 target ledger、tip高度等于 until、错误 state key/value、non-inclusion被伪造为索引 miss全部拒绝。current policy miss本身只能得到诊断状态。

### 16.7 Relayer 与 Byzantine 网络

- relayer 删除、重排、延迟或重复 proof；多 peer 返回不同 signer subset；恶意 majority 提供错 anchor均不能改变结论。
- ANY/ALLOWLIST relayer边界，账户 policy轮换 h→h+1 与 target消息窗口组合。
- proof洪泛、超长数组、慢流、压缩炸弹、cache eviction与跨 Ledger noisy neighbor下，P0 DAG/finality流量仍受保护。
- source最终而 target长期不可用时不回滚 source；target恢复后窗口内可消费，窗口外只能形成可验证 EXPIRED_UNCONSUMED。

## 17. 实现完成定义

- [ ] payload/Feature/Gas/domain 注册表与所有文档、SDK、编码器一致。
- [ ] trust root 只能从目标已认证 FeatureSet进入 verifier。
- [ ] source transaction、Receipt 和两条 Event path 均被验证并能重建 native Event。
- [ ] consumption key 的永久 SMT 状态与同块 exact set 都已实现。
- [ ] CONSUME winner 具有完整成功预检，replay 不产 Receipt、不耗 nonce。
- [ ] 完整item scan费、通用expensive target-auth后缀和独立source-proof后缀严格分层；两个exact attempt map使用`STARTED|VALID|INVALID`、origin cursor、sponsor/cost/receipt，source `VALID`另含可重验artifact+digest；charge+STARTED原子、恢复不重扣、Finish恰好一次归集scan receipt、两套spend可从aggregate/map重算、local failure暂停、cap loser不写map，且通过更早通用cheap gates的different-origin terminal occurrence为source duplicate。
- [ ] 全部预算均按containing signed DAGVertex occurrence sponsor的`n`份额实现，Batch author另行绑定但不扣款，`P<n`与跨作者Batch引用可用，f个恶意sponsor不能饿死honest通道。
- [ ] proof bytes、transition、signature、Gas、Event、state、Body与 API都具备前分配上限。
- [ ] source/target epoch、checkpoint轮换、Snapshot、crash与pruning矩阵全部通过。
- [ ] SDK 可以在不信任 relayer/Gateway的前提下独立验证 source proof、target消费结论与双target-context稳定过期证据。
