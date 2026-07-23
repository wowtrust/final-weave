# FinalWeave 执行注册表、Gas 与资源计量规范

> 状态：规范性设计基线（Draft）
>
> 适用版本：FinalWeave v1 / state machine v1

## 1. 目的与安全边界

本篇冻结 `payload_type`、Feature tuple、Gas operation、字节计量、资源预算和失败语义。它解决的不是本地性能调优，而是一个共识问题：相同 `TransactionEnvelope`、父状态和 epoch 配置必须在所有实现中得到相同 winner、`gas_used`、Receipt、Event、StateChange、Body bytes 和 state root。

实现可以改变 worker、缓存、数据库、JIT、批量验签和物理布局，但不能改变逻辑 Gas event trace 或资源计数。未在本篇或已激活 Feature tuple 中登记的 payload、operation、host call 和参数 schema 一律拒绝；不能用“本实现支持”代替协议激活。

v1 基线作出三项明确取舍：

1. 许可账本没有原生货币费用，`fee_limit` 必须为 `0`；Gas 只承担确定性资源预算和 DoS 边界；
2. v1 原生状态机只开放账户创建、策略轮换、账本重配置，以及可选的有界 KV 与证明驱动跨账本消息 feature；不开放 WASM、任意 range scan 或动态插件；
3. Gas 不能替代硬字节/数量上限；二者都必须通过后交易才可成功。

## 2. v1 payload 注册表

| `payload_type` | 名称 | 激活条件 | Gas operation |
|---:|---|---|---:|
| `1` | `ACCOUNT_CREATE_V1` | state machine v1 固有 | `0x00010001` |
| `2` | `ACCOUNT_POLICY_ROTATE_V1` | state machine v1 固有 | `0x00010002` |
| `3` | `LEDGER_RECONFIGURE_V1` | state machine v1 固有 | `0x00010003` |
| `4` | `CROSS_LEDGER_SEND_V1` | `CROSS_LEDGER_V1` 已激活 | `0x00010004` |
| `5` | `CROSS_LEDGER_CONSUME_V1` | `CROSS_LEDGER_V1` 已激活 | `0x00010005` |
| `16` | `KV_PUT_V1` | `NATIVE_KV_V1` 已激活 | `0x00010010` |
| `17` | `KV_DELETE_V1` | `NATIVE_KV_V1` 已激活且 `allow_delete=true` | `0x00010011` |

其他 `uint16` 值在 v1 都是 `STATIC_INVALID`。未来新增 payload 必须分配新 ID、Feature tuple、规范 payload schema、access resolver、Gas operation、失败矩阵和跨实现向量；不能在同一 `(protocol_version,state_machine_version)` 下重新解释旧 ID。

### 2.1 账户创建

`ACCOUNT_CREATE_V1` 的 schema、自证授权、空 scope、三个隐式 system writes、同块去重和原子初始化见[数据模型与密码学](01-data-model-and-cryptography.md)。其用户 `authorized_access_scope` 必须为空；resolver 增加以下三个协议隐式 `EXACT WRITE`，用户不得重复声明或用 `PREFIX` 代替：

```text
finalweave/v1/account/meta  || sender
finalweave/v1/account/auth  || sender
finalweave/v1/account/nonce || sender
```

选择 winner 前必须计算第 5.5 节的完整创建 Gas trace，并要求 `gas_limit >= required_gas`；同时预留三项 protocol write 、它们的三项 StateChange 和完整成功 TransactionResult 空间。因此协议有效的创建 winner 只能成功，资源不足在 winner 选择前表现为 `STATIC_INVALID` 或 `BLOCK_CAP`，不能留下“失败但账户仍不存在、nonce 却已消费”的状态。

### 2.2 账户策略轮换

```text
RotateAccountPolicyPayloadV1 {
  schema_version: uint16
  next_signer_policy: SignerPolicy
}
```

固定 `schema_version=1`。完整 payload 必须是一个 deterministic-CBOR item；新 policy 必须通过 key ID、严格排序、去重、threshold 和 checked-weight 校验。Envelope 仍由高度开始时 active policy 授权，`authorized_access_scope` 必须为空；resolver 隐式读取 meta/auth/nonce 并精确写 auth/nonce。

执行成功时，把当前高度解析出的 active policy hash 规范化写入 `base_policy_hash`，将新 policy hash 与 `pending_effective_height=height+1` 成对写入 pending 字段。多个同 sender 轮换按 `tx_index` 串行生效，后者可以覆盖前者的 pending 值，但同块认证始终使用块开始视图。无效新 policy 是 `STATIC_INVALID`；`height+1` 溢出是 `INVALID_STATE_TRANSITION` 的 FAILED Receipt，回滚 auth 变化但消费 winner nonce。

### 2.3 账本重配置

v1 的账本 GovernancePolicy 在 Genesis 后不可轮换；其完整对象和 hash 已由 GenesisCertificate/GenesisStatement 认证。重配置交易需要一个现有账户作为 `TransactionIntent.sender` 并通过普通账户签名/nonce 规则，同时携带达到该账本治理策略 threshold 的离线批准。双层签名把“谁占用交易资源和 nonce”与“谁有权改变协议”分开。

```text
ReconfigureActionCoreV1 {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  current_epoch: Epoch
  target_epoch: Epoch
  next_validator_set: ValidatorSet
  next_protocol_config: ProtocolConfig
  next_feature_set: FeatureSet
  next_gas_schedule: GasSchedule
  migration_manifest_hash: optional Hash32
}

GovernanceActionApprovalV1 {
  schema_version: uint16
  signer_key_id: Hash32
  signature: SignatureEd25519
}

ReconfigureLedgerPayloadV1 {
  schema_version: uint16
  action: ReconfigureActionCoreV1
  approvals: [GovernanceActionApprovalV1]
}
```

全部 schema version 固定为 1。语义 ID 是：

```text
governance_action_id = DomainHash(
  "GOVERNANCE_ACTION", network_id, ledger_id,
  canonical(ReconfigureActionCoreV1)
)
```

每个 approval 使用 Genesis 认证的 ledger Governance Key 直接签完整 32-byte `governance_action_id`。在按声明 count 分配或验签前，要求 `len(approvals) <= min(len(ledger_governance_policy.signers), GOVERNANCE_APPROVALS_MAX_V1=1_024)`；完整 payload 还必须位于 active transaction/payload canonical-byte 上限。approvals 按 `signer_key_id` 严格升序、拒绝重复，逐项执行 strict Ed25519，并以 checked weight 达到 policy threshold；未知 key、错误 network/ledger 或把账户签名当治理批准都无效。

`CanonicalAndStaticValid` 与 block context 共同要求：

1. `payload_type=3` 且 payload 是唯一 canonical `ReconfigureLedgerPayloadV1`，用户 `authorized_access_scope` 为空；
2. `current_epoch` 等于执行区块 epoch，`target_epoch=checked_add(current_epoch,1)`；
3. `ValidateValidatorSet(next_validator_set,target_epoch,network_id)`、`ValidateProtocolConfigStructure(next_protocol_config,next_validator_set)` 和 `ValidateExecutionConfigBundle(next_protocol_config,next_feature_set,next_gas_schedule,next_validator_set)` 全部通过；
4. 四个内容 ID 重算一致，完整 payload 连同 Envelope 受当前 epoch `max_transaction_bytes` 限制；若下一 bundle 太大，治理必须先在更早 epoch 提高该上限，不能用链外未认证引用绕过；
5. approvals 对当前 immutable ledger GovernancePolicy 达到 threshold。

上述是最终语义谓词，不允许在 occurrence filter 最前端无预算地执行。bounded cheap prefix 只能验证 deterministic-CBOR 外壳、schema、全部声明 count/byte 上限、epoch/target 的便宜字段、账户 active policy hash、exact nonce 与 block reserve；approvals strict Ed25519、next ValidatorSet keys、ProtocolConfig structure、FeatureSet typed parameters、GasSchedule exact registry及四个内容 ID都属于[最终性与执行规范](04-finality-execution-and-epochs.md#32-单遍算法)的 charged prefilter suffix。完整raw occurrence先取得scan预算才能解码Envelope；上述昂贵字段再逐项进入`PrefilterExpensiveWorkCostV1`，且固定base使其最少为1；两段之和才是`PrefilterVerificationWorkCostV1(item_length,tx)`。最坏完整 bundle必须包含在 `MaxSinglePrefilterVerificationWorkV1(config)` sizing template中。没有对应scan预算不得解码Envelope，没有suffix预算不得启动验签或完整bundle worker；坏approval/bundle已消耗的逻辑work不退款。

resolver 隐式读取当前 epoch 的 pending key，并写 pending key 与 sender nonce；普通用户 scope 不得写治理 namespace：

```text
namespace = "finalweave/v1/governance/reconfiguration"
key       = U64BE(current_epoch)

PendingReconfigurationStateV1 {
  schema_version: uint16
  governance_action_id: Hash32
  target_epoch: Epoch
  next_validator_set_hash: Hash32
  next_protocol_config_hash: Hash32
  next_feature_set_hash: Hash32
  next_gas_schedule_hash: Hash32
  migration_manifest_hash: optional Hash32
}
```

该 key 在父状态必须不存在。若同一 FinalizedBlock 有多个有效治理交易，按 `tx_index` 第一个成功写 pending，后续得到 `INVALID_STATE_TRANSITION` FAILED Receipt并消费各自账户 nonce；不得由本地到达时间挑选。只有成功 Receipt 获得 FinalityCertificate 后才触发[Epoch close](04-finality-execution-and-epochs.md)，并从该 FinalizedBlock 取得已验证的四个完整对象。Archive/配置对象库必须至少保留完整对象到目标 epoch 原子激活完成；ValidatorSet/ProtocolConfig 再按最终性证明链永久保留，FeatureSet/GasSchedule 按当前执行与历史重放需求保存。治理交易 Body 的更长服务期由非共识 `LocalHistoryPolicy` 决定；只剩 pending hashes 而取不到 preimage 时不能签 EpochSeal。

v1 不允许 action 改写 ledger GovernancePolicy。`migration_manifest_hash` 缺失表示无额外迁移；state machine v1 尚未登记非空迁移 schema，因此非空值是 `STATIC_INVALID`。治理 key 轮换和状态迁移都需要后续 protocol/state-machine 版本明确迁移信任根与执行规则。

Snapshot 是对已认证状态的可重建传输/恢复产物，不是 reconfiguration action 字段，也不作为 EpochSeal 或新 epoch readiness 的共识门槛。运营方可以在 close frontier 后异步导出并复制 snapshot；生成失败会降低恢复能力并触发运维告警，但不得让已获得 FinalityCertificate 的 epoch 永久无法关闭。未来若要把状态迁移或 snapshot availability 变成共识条件，必须先冻结独立的可用性证明、超时与恢复协议，不能复用一个无消费语义的布尔值。

### 2.4 `CROSS_LEDGER_V1` Feature tuple

跨账本异步消息分配 tuple：

```text
(feature_id=2, feature_version=1, parameter_schema_version=1)
```

它同时激活 `CROSS_LEDGER_SEND_V1`、`CROSS_LEDGER_CONSUME_V1`、`CROSS_LEDGER_PROOF_VERIFY_V1` 与 `CROSS_LEDGER_SOURCE_SIGNATURE_VERIFY_V1`。typed `CrossLedgerParametersV1`、source policy、proof envelope、永久 consumed key、目标窗口、并发 winner 和精确 Gas trace 全部由[跨账本异步消息规范](06-cross-ledger-async-messaging.md)冻结。参数完整 canonical bytes 仍受单 Feature 64 KiB 上限；unknown policy field、root kind、proof union 或条件 Gas operation 缺失使执行 bundle 无效。

`ValidateExecutionConfigBundle(config,feature_set,gas_schedule,validator_set)` 还必须为 proof-work 公平调度计算每个 active source policy 的保守最大单次验证 Gas：一次 `PROOF_VERIFY(policy.max_proof_envelope_bytes)` 加 `policy.max_source_signature_verifications` 次 `SOURCE_SIGNATURE_VERIFY(128)`，取最大值得 `max_single_cross_ledger_verification_gas`。所有乘加使用 remaining-budget/checked 算法，并要求：

```text
n * max_single_cross_ledger_verification_gas
  <= config.max_execution_gas_per_finalized_block
```

其中 `n = validator_set.validators.length`；全部 n个Validator都可以签DAGVertex并成为occurrence sponsor，不能使用 `proposer_slots_per_round=P`。无 inbound policy时该值为 0。这个交叉约束为每个已认证containing Vertex sponsor保留一次最大合法 source proof的物理工作额度；Batch author可与sponsor不同且只认证数据来源，不参与扣款。不成立的bundle无效，不能等执行时缩小policy cap、少验签或退回纯全局先到先得预算。

### 2.5 `NATIVE_KV_V1` Feature tuple

有界 KV 分配 tuple：

```text
(feature_id=1, feature_version=1, parameter_schema_version=1)
```

其 `parameters_cbor` 必须恰好编码：

```text
NativeKVParametersV1 {
  allowed_namespace_prefixes: [byte_string]
  allow_delete: bool
}
```

前缀数组必须含 `1..256` 项，按原始字节严格升序、拒绝重复；每项非空、长度不超过 active `max_state_namespace_bytes`，且不得以 ASCII `finalweave/` 开头。一个 namespace 被允许，当且仅当它以至少一个登记前缀开头。由于数组禁止相同项但可以有包含关系，实现不能做“最长前缀获胜”等附加解释；任一匹配即允许。

FeatureSet 为空是合法的账户/治理 state machine；此时 KV 与跨账本 payload 均为 `STATIC_INVALID`。v1 只登记本节的 `NATIVE_KV_V1` 与 `CROSS_LEDGER_V1`；任何其他 feature tuple 都无效，而不是 opaque passthrough。

KV schema 为：

```text
KVPutPayloadV1 {
  schema_version: uint16
  namespace: byte_string
  key: byte_string
  value: byte_string
}

KVDeletePayloadV1 {
  schema_version: uint16
  namespace: byte_string
  key: byte_string
}
```

两者固定 `schema_version=1`，`key` 必须非空，空 value 合法。PUT 的 namespace/key/value 必须满足当前第 6 节新写入 component cap；DELETE 为了删除配置下调前的旧 key，namespace/key 使用 v1 绝对 component cap，不再受当前新写入 cap 限制。两者的 namespace 都必须被 Feature 参数允许，且不得以 ASCII `finalweave/` 开头。用户 scope 必须恰好含一项与 payload `(namespace,key)` 相同的 `EXACT WRITE`，不得含额外项或 `PREFIX`。resolver 隐式把同一 key 的旧值读纳入 observed access：PUT 覆盖 value；DELETE 对存在或不存在 key 都成功，将最终状态恢复为 absent leaf。v1 KV 不发 Event，返回数据为空。

DELETE 不向共识状态、Snapshot 或 Body 写入 tombstone value。仅用于资源计量的 journal record 规范编码为 `canonical({state_key,value_presence:0})`；PUT 为 `canonical({state_key,value_presence:1,value})`。两个匿名记录均按字段写出顺序分配 CBOR key。这两个长度是第 6.3 节 tx/block write bytes 的唯一 `value_or_tombstone` 定义。

## 3. FeatureSet 的完整验证

`ValidateExecutionConfigBundle` 必须以本篇注册表验证 FeatureSet：

1. `schema_version==1`、entries 按 `feature_id` 严格升序且最多 256 项；
2. 同一 `feature_id` 只能有一个 active version；
3. tuple 必须在本篇登记，`parameters_cbor` 是恰好一个 deterministic-CBOR item，且满足该 typed schema；
4. 每项参数不超过 64 KiB、完整 FeatureSet 不超过 1 MiB，长度求和 checked；
5. Feature 参数与 ProtocolConfig 的交叉限制同时成立。

未知 tuple、空 byte string、JSON、第二个 CBOR item、尾随字节、非最短整数和 unknown field 都使 bundle 无效。新增 Feature 必须升级相应规范 registry；“代码认识但链上未激活”仍不可执行。

## 4. v1 Gas operation 注册表

### 4.1 固有 operation

| ID | 名称 | 触发次数 | `input_bytes` | `output_bytes` |
|---:|---|---|---:|---:|
| `0x00000001` | `TX_BASE_V1` | 每个 winner 一次，任何 payload operation 之前 | `len(canonical(TransactionEnvelope))` | `0` |
| `0x00000002` | `ACCOUNT_SIGNATURE_VERIFY_V1` | Envelope 中每个已规范验证的 AccountSignature 一次 | `32 + 32 + 64`：intent hash、公钥、签名 | `0` |
| `0x00000003` | `STATE_READ_V1` | 每次逻辑 point read；cache hit 和重复读取也计 | `len(canonical(ConsensusStateKey))` | absent 为 `0`，present 为 value raw bytes 长度 |
| `0x00000004` | `STATE_WRITE_V1` | 每次 payload 逻辑 put/delete，在 journal 变更前；不含普通 winner 的强制 nonce commit | `len(canonical(ConsensusStateKey))` | put 为 value raw bytes 长度，delete 为 `0` |
| `0x00000005` | `EVENT_EMIT_V1` | 每次逻辑 emit，在追加前 | `len(canonical({emitter,topic}))` | data raw bytes 长度 |
| `0x00000006` | `RETURN_DATA_V1` | payload 正常到达 return 点时一次，在接受 return 前；在此前 FAILED/OOG 则不产生 | `0` | return raw bytes 长度 |
| `0x00000007` | `HOST_SHA256_V1` | 状态机显式调用 host SHA-256 时每次 | message raw bytes 长度 | `32` |
| `0x00000008` | `HOST_ED25519_VERIFY_V1` | 状态机显式调用 strict Ed25519 verify 时每次 | `32 + 64 + len(message)` | `0` |

协议为构造 tx ID、SMT、Merkle/MMR、Receipt root 或签名证书执行的哈希/验签不产生 host Gas event；账户 Envelope 签名只由 operation `2` 计量。数据库 cache、SMT node 数、线程等待、网络、重试和物理 I/O 不进入 Gas。

### 4.2 payload operation

每个 winner 在 `TX_BASE` 和账户签名 events 之后、执行 payload 前，恰好产生一次 `0x00010000 + payload_type` event：`input_bytes=len(intent.payload)`，`output_bytes=0`。v1 因而登记第 2 节表中的七个 payload operation ID；未激活相应 Feature 时 GasSchedule 不得携带 KV 或跨账本 operation。

`CROSS_LEDGER_V1` 还条件登记两个非 payload operation：`0x00020001 CROSS_LEDGER_PROOF_VERIFY_V1` 对完整 source proof envelope 触发一次，`0x00020002 CROSS_LEDGER_SOURCE_SIGNATURE_VERIFY_V1` 对每个逻辑 source GenesisApproval/EpochSealVote/ExecutionAttestation 触发一次。两者的 byte metric 与严格顺序见协议第六篇；cache hit 不能省略逻辑 event。

未来 Feature 可以登记额外 host operation，但必须逐项冻结调用点和 byte metric。状态机尝试发出 active registry 外 operation 是实现分叉风险，节点必须 `EXECUTION_HALT`，不能映射成用户 Receipt。

### 4.3 GasSchedule 的 exact-set 规则

GasSchedule entries 必须按 `operation_id` 严格升序且与当前 registry **完全相等**：

- 始终包含 8 个固有 operation、ACCOUNT_CREATE、ACCOUNT_POLICY_ROTATE 与 LEDGER_RECONFIGURE；
- `CROSS_LEDGER_V1` 激活时同时加入 SEND、CONSUME、PROOF_VERIFY 与 SOURCE_SIGNATURE_VERIFY，未激活时四项都不得出现；
- `NATIVE_KV_V1` 激活时加入 KV_PUT，且仅在 `allow_delete=true` 时加入 KV_DELETE；
- 不得缺项、重复或包含未知/未激活项；空表无效；
- 每项 `base_cost > 0`；
- `TX_BASE`、payload operations 和两个 host crypto operations 的 `per_input_byte > 0`；
- 两个 cross-ledger proof operations 的 `per_input_byte > 0`；
- `STATE_READ`、`STATE_WRITE` 的 `per_input_byte > 0 && per_output_byte > 0`；
- `EVENT_EMIT`、`RETURN_DATA` 的 `per_output_byte > 0`；
- 未要求为正的系数可以为零，但仍进入内容 hash。

entries 不超过 65,536，完整 canonical GasSchedule 不超过 4 MiB。`ValidateExecutionConfigBundle` 在接受 bundle 前执行 exact-set 和系数约束；不能在执行时用默认 cost 补缺项。

## 5. Gas 计算与失败

### 5.1 单 event 公式

对 registry 中一次逻辑 event：

```text
event_cost = base_cost
           + per_input_byte  * input_bytes
           + per_output_byte * output_bytes
```

所有 byte 长度来自未压缩的规范对象或表中指定的 raw byte string，转换为 `uint64` 前检查宿主长度可表示。实现不得先做可能溢出的乘加；必须用 remaining-budget 形式逐项比较：

```text
Charge(entry, input, output, gas_limit, gas_used):
  remaining = checked_sub(gas_limit, gas_used) else EXECUTION_HALT
  consume(entry.base_cost)
  consume_mul(entry.per_input_byte, input)
  consume_mul(entry.per_output_byte, output)

consume(x):
  if x > remaining: OUT_OF_GAS
  remaining -= x

consume_mul(rate, units):
  if rate != 0 and units > remaining / rate: OUT_OF_GAS
  remaining -= rate * units
```

只有全部项可扣减时才把 event 的完整 cost 加入 `gas_used` 并执行其副作用。任一项超过 remaining，交易立即 `OUT_OF_GAS`，`gas_used` 固定为签名的 `gas_limit`，不执行当前 operation；业务 journal、Event 和 return data 回滚，协议 nonce 写保留。这样超大输入自然成为 OOG，而不是发生 `uint64` wrap、饱和或平台差异。

`STATE_READ` 允许存储层先读取不向状态机暴露的 value-length metadata；meter 用该长度计算，但必须按下节先做 scope/资源检查、再成功扣完整 event，最后才可载入或返回 value。WRITE/EVENT/RETURN/HOST 必须在分配输出、变更 journal、追加 Event 或向调用者暴露结果前知道长度并扣费。不能先生成无界输出再补记 Gas，也不能因 cache 已持有 value 而跳过 output bytes。

### 5.2 扣费顺序

每笔普通 winner 的共同前缀固定为：

```text
TX_BASE
-> ACCOUNT_SIGNATURE_VERIFY（按 key_id 升序）
-> payload operation
```

共同前缀之后只能跟随第 5.5/5.6 节登记的 payload trace，不允许实现从数据库访问路径自行推导读写次数。普通 winner（除账户创建）在 trace 结束后恰好提交一次 sender nonce write：它不发出 `STATE_WRITE_V1` GasEvent，因为 OOG/失败也必须能消费 nonce；其固定 CPU 成本由 `TX_BASE_V1` 覆盖。该写仍是完整共识写：必须在 winner 选择前预留，计入 per-tx/per-block write bytes，并在 `TransactionResult.state_changes` 中产生 nonce StateChange。

每个可发出 READ/WRITE/EVENT/RETURN/HOST 的尝试按以下唯一顺序处理：

1. 用已验证 schema、有界 length metadata 和 checked arithmetic 确定类型、key 及 input/output 长度，不分配或暴露内容；
2. 检查访问权限/scope；越权立即 `ACCESS_SCOPE_VIOLATION`；
3. 检查 component 绝对上限、当前新写入上限及 tx/block resource counters；超限立即 `STATE_LIMIT_EXCEEDED`；
4. 调用 `Charge`；余额不足才是 `OUT_OF_GAS`；
5. 只有前四步都通过才加载 value、分配输出、修改 journal 或追加 Event。

因而同一尝试同时越权、超资源与 Gas 不足时，错误优先级固定为 `ACCESS_SCOPE_VIOLATION > STATE_LIMIT_EXCEEDED > OUT_OF_GAS`。被前两者拒绝的 operation 不产生 GasEvent，`gas_used` 保留之前完成的 events；OOG 取满 `gas_limit`。任一非 OOG 失败在失败点终止 payload trace，不再发出 `RETURN_DATA`；Receipt 的 return hash 仍是规范空 byte string hash。被 occurrence filter 跳过的交易不是 winner，不产生 Gas/Receipt。推测执行、被丢弃 suffix 和权威重执行只是在本地重复计算同一逻辑 trace，最终 `gas_used` 只记录一次；worker 数、完成顺序和 fallback 不能改变它。

### 5.3 业务失败与本地错误

- 用户业务错误、revert、`STATE_LIMIT_EXCEEDED` 和 `ACCESS_SCOPE_VIOLATION`：产生 FAILED Receipt，消费 winner nonce，提交允许的协议写；
- OOG：规则见上，业务输出清空；
- unknown operation、registry 分叉、meter 顺序分叉、checked counter 损坏：`EXECUTION_HALT`，不产 Receipt、不签 attestation；
- 本地 watchdog/OOM/cancellation/数据库错误：本地失败，不能伪造 OOG。

### 5.4 v1 费用语义

v1 `CanonicalAndStaticValid` 要求 `fee_limit == 0`。没有 gas price、fee asset、余额扣减、收款人、销毁或费用状态 namespace；Receipt 也不含 `fee_charged`。错误码 `7` 仅为未来 `FEE_LIMIT_EXCEEDED` 预留，v1 Receipt 出现该值无效。

启用费用必须通过新 state-machine/protocol 版本同时冻结 `FeeSchedule` 内容承诺、资产与 payer 状态、失败/OOG 收费、收款/销毁、Receipt 字段、snapshot/proof 和迁移规则，不能仅把 `fee_limit` 从零改为非零。

### 5.5 创建账户的精确预检 Gas

`RequiredGasForAccountCreate` 使用同一 GasSchedule 和第 5.2 节顺序，完整 trace 为：

```text
TX_BASE(envelope_bytes, 0)
ACCOUNT_SIGNATURE_VERIFY(128, 0) for every signature
ACCOUNT_CREATE_V1(payload_bytes, 0)
STATE_READ(meta_key_bytes, 0)
STATE_READ(auth_key_bytes, 0)
STATE_READ(nonce_key_bytes, 0)
STATE_WRITE(meta_key_bytes, canonical(metadata).length)
STATE_WRITE(auth_key_bytes, canonical(auth).length)
STATE_WRITE(nonce_key_bytes, canonical(nonce).length)
RETURN_DATA(0, 0)
```

三项 read 必须在父状态 absent；同块 created-address set 是 occurrence-filter 状态，不另收 Gas。预检用 remaining-budget 算法；若 exact required Gas 大于 `gas_limit`，该创建 occurrence 是 `STATIC_INVALID`。执行器必须重放同一 trace 并断言结果相等，否则 `EXECUTION_HALT`。

### 5.6 其他 v1 payload 的精确 Gas trace

下列 trace 均紧接第 5.2 节共同前缀。`READ(k,len)` 的 output 是 absent 时 0、present 时规范 value raw bytes 长度；`WRITE(k,len)` 是 put，`DELETE(k,0)` 是删除。末尾的 `COMMIT_NONCE_UNMETERED` 不是 GasEvent，但是必须预留和输出 StateChange 的共识写。

```text
ACCOUNT_POLICY_ROTATE_V1 success:
  READ(account_meta_key, current_meta_len)
  READ(account_auth_key, current_auth_len)
  READ(account_nonce_key, current_nonce_len)
  WRITE(account_auth_key, canonical(next_auth_state).length)
  RETURN_DATA(0, 0)
  COMMIT_NONCE_UNMETERED

ACCOUNT_POLICY_ROTATE_V1 height+1 overflow:
  READ(account_meta_key, current_meta_len)
  READ(account_auth_key, current_auth_len)
  READ(account_nonce_key, current_nonce_len)
  FAIL(INVALID_STATE_TRANSITION)
  COMMIT_NONCE_UNMETERED

LEDGER_RECONFIGURE_V1 success:
  READ(pending_reconfiguration_key, 0)
  WRITE(pending_reconfiguration_key, canonical(pending_state).length)
  RETURN_DATA(0, 0)
  COMMIT_NONCE_UNMETERED

LEDGER_RECONFIGURE_V1 pending-key present:
  READ(pending_reconfiguration_key, current_pending_len)
  FAIL(INVALID_STATE_TRANSITION)
  COMMIT_NONCE_UNMETERED

KV_PUT_V1 success:
  READ(kv_key, old_value_len_or_zero)
  WRITE(kv_key, new_value.length)
  RETURN_DATA(0, 0)
  COMMIT_NONCE_UNMETERED

KV_DELETE_V1 success, whether key is present or absent:
  READ(kv_key, old_value_len_or_zero)
  DELETE(kv_key, 0)
  RETURN_DATA(0, 0)
  COMMIT_NONCE_UNMETERED

CROSS_LEDGER_SEND_V1 success:
  EVENT_EMIT(len(canonical({emitter,topic})), len(source_message_event.data))
  RETURN_DATA(0, len(canonical({message_id,message_payload_hash})))
  COMMIT_NONCE_UNMETERED

CROSS_LEDGER_CONSUME_V1 success:
  CROSS_LEDGER_PROOF_VERIFY(canonical(proof_envelope).length, 0)
  CROSS_LEDGER_SOURCE_SIGNATURE_VERIFY(128, 0) for each logical source signature
  STATE_READ(consumed_state_key_bytes, 0) // winner 必须 absent
  STATE_WRITE(consumed_state_key_bytes, canonical(consumed_state).length)
  EVENT_EMIT(len(canonical({emitter,topic})), len(consumed_event.data))
  RETURN_DATA(0, len(canonical({source_event_id,message_id,consumption_key})))
  COMMIT_NONCE_UNMETERED
```

新 policy 的 schema/weight、重配置的 bundle/approvals、KV payload/scope，以及跨账本 source proof/target policy/窗口/relayer/success shape 都在 winner 选择前完成规范验证与精确预留，不会在上述 trace 中变成用户失败。共识验证器为 tx ID、账户授权、治理 approvals、ValidatorSet 或 config bundle 所做的哈希/验签不产生 HOST GasEvent；跨账本 source proof 则只产生本节专用 proof/signature events，不能同时产生 HOST event。普通 payload 的 READ/WRITE/RETURN 按第 5.2 节优先级失败时立即截断剩余 trace，然后只执行已预留的 nonce commit；账户创建与两个 CrossLedger payload 的 trace/输出若偏离预检，必须 `EXECUTION_HALT`，不得转成 FAILED Receipt。

## 6. 硬资源上限

### 6.1 协议绝对上限

下列常量限制任何 v1 配置，不能由治理提高：

```text
STATE_NAMESPACE_MAX_BYTES_V1                   = 1_024
STATE_KEY_MAX_BYTES_V1                         = 1_048_576
STATE_VALUE_MAX_BYTES_V1                       = 16_777_216
STATE_READ_BYTES_PER_TX_MAX_V1                 = 4_294_967_296
STATE_WRITE_BYTES_PER_TX_MAX_V1                = 1_073_741_824
STATE_WRITE_BYTES_PER_FINALIZED_BLOCK_MAX_V1   = 4_294_967_296
EVENTS_PER_TX_MAX_V1                           = 1_048_576
EVENT_BYTES_PER_TX_MAX_V1                      = 1_073_741_824
EVENTS_PER_FINALIZED_BLOCK_MAX_V1              = 16_777_216
EVENT_BYTES_PER_FINALIZED_BLOCK_MAX_V1         = 4_294_967_296
RETURN_DATA_MAX_BYTES_V1                       = 16_777_216
CALLS_PER_TX_MAX_V1                            = 1_048_576
SNAPSHOT_STATE_RECORD_MAX_CANONICAL_BYTES_V1   = 17_891_328
FINALIZED_BLOCK_BODY_MAX_CANONICAL_BYTES_V1    = 4_294_967_296
CALL_DEPTH_MAX_V1                              = 1_024
```

这些是协议安全上限，不是建议生产默认值。Snapshot record 上限大于三个 component 绝对上限与 deterministic-CBOR framing 的最大开销，但它不替代三个 component 各自的绝对上限。解码器先以 record 常量限制 frame，再在分配前逐字段检查 namespace/key/value 绝对上限；不能按未验证长度分配。

### 6.2 ProtocolConfig 字段

每个 epoch 还冻结：

```text
max_state_namespace_bytes: uint32
max_state_key_bytes: uint32
max_state_value_bytes: uint32
max_state_read_bytes_per_tx: uint64
max_state_write_bytes_per_tx: uint64
max_state_write_bytes_per_finalized_block: uint64
max_events_per_tx: uint32
max_event_bytes_per_tx: uint64
max_events_per_finalized_block: uint64
max_event_bytes_per_finalized_block: uint64
max_return_data_bytes_per_tx: uint32
max_call_depth: uint16
max_calls_per_tx: uint32
max_finalized_block_body_bytes: uint64
```

全部必须大于零。`ValidateProtocolConfigStructure` 必须按下列唯一算法验证，其中所有 sizing template 都使用第 4.1 节 deterministic-CBOR 规则完整编码，所有加法/乘法均 checked：

1. 每个字段不得超过第 6.1 节对应绝对上限；`max_state_write_bytes_per_tx <= max_state_write_bytes_per_finalized_block`，per-tx Event count/bytes 分别不大于 per-block 值，且 `max_event_bytes_per_finalized_block <= max_finalized_block_body_bytes`；
2. `max_state_namespace_bytes >= MaxReservedNamespaceRawBytesV1`，后者是 v1 注册表中所有固定系统 namespace（含账户三项、治理 pending 与 `finalweave/v1/cross-ledger/consumed`）的最大 raw UTF-8 长度；`max_state_key_bytes >= 32`；
3. 结构谓词要求 `max_state_value_bytes >= MaxFixedBaseProtocolStateValueBytesV1(config)`，取 AccountMetadata/Auth/Nonce 与 PendingReconfigurationState sizing template 的最大值；AccountMetadata 使用 canonical 长度不超过 `max_signer_policy_bytes` 的最大合法 SignerPolicy template。完整 bundle 谓词再计算 `MaxFixedProtocolStateValueBytesV1(config,feature_set)`，激活跨账本时把 `ConsumedCrossLedgerMessageStateV1` template 纳入最大值；任一结果超过 value 绝对上限的 config/bundle 无效；
4. `max_state_read_bytes_per_tx >= MaxRequiredNativeReadBytesV1(config,feature_set)`，函数按第 5.5/5.6 节 trace 计算；对可来自旧 epoch 的 present namespace/key/value 使用 v1 绝对 component cap，不使用当前新写入 cap，因而治理下调不会把历史 component 视为非法；
5. `max_state_write_bytes_per_tx >= MaxRequiredNativeWriteBytesV1(config,feature_set)`，至少覆盖账户创建三项写、普通 nonce write、auth/pending write、激活 KV PUT 的最大当前新写入 component，以及激活跨账本时 consumed marker + relayer nonce 的完整写；
6. 令 `N = MandatoryNonceWriteJournalBytesV1`、`C = AccountCreateJournalBytesV1(config)`，要求 `max_state_write_bytes_per_finalized_block >= max(checked_mul(max_transactions_per_finalized_block,N),C)`；
7. `max_events_per_tx <= EVENTS_PER_TX_MAX_V1`、`max_event_bytes_per_tx <= EVENT_BYTES_PER_TX_MAX_V1`、`max_events_per_finalized_block <= EVENTS_PER_FINALIZED_BLOCK_MAX_V1`、`max_event_bytes_per_finalized_block <= EVENT_BYTES_PER_FINALIZED_BLOCK_MAX_V1`，其他 read/write/return/call 字段同理匹配对应常量；
8. `max_finalized_block_body_bytes >= MinEmptyFinalizedBlockBodyCanonicalBytesV1`；该 template 使用已冻结 Body schema、`ordered_vertex_count` 摘要和空 transactions/results，不内联 CausalInput 中的 VertexID 列表；
9. `max_call_depth <= CALL_DEPTH_MAX_V1`、`max_calls_per_tx <= CALLS_PER_TX_MAX_V1`，且 v1 原生 payload 的两者最小值均为 1。

`MaxRequiredNativeRead/WriteBytesV1` 需要 FeatureSet 的激活信息，因而结构谓词先检查与 Feature 无关的下界，`ValidateExecutionConfigBundle` 在 typed Feature 验证后完成上述精确交叉检查。配置不必保证所有理论最大业务输出能同时进入一块；确定性预算负责背压和失败。

### 6.3 计量单位

- component cap 计 raw byte-string 长度。任何 state component 首先受 v1 绝对上限；当前 ProtocolConfig 的 namespace/key/value cap 只在创建或覆盖 present value 时作为新写入 cap。读取或 DELETE 已有/不存在的历史 key 允许 namespace/key 达到 v1 绝对上限；new value 在分配/写 journal 前检查当前 cap；
- read bytes 每次逻辑 read 累加 `len(canonical(ConsensusStateKey)) + present_value_length`，重复 read 也计；存储层必须先读 length metadata，预算不足时不得载入 value；
- tx write bytes 对业务 journal 中每个最终 distinct key 计第 2.4 节冻结的 `canonical({state_key,value_presence[,value]})`；同 key 覆盖按新旧 encoded length 的 checked delta 更新；协议写单独保留并计入；
- block write bytes只计最终提交的业务与协议 writes；失败交易回滚的业务 journal 不占最终 block budget；
- event bytes 计 `len(canonical(Event))`，count/bytes 都在 append 前检查；失败或 revert 清空本交易 events；
- return cap 计 raw bytes，在分配/接受前检查；
- call count 对每次 native module/contract entry 计一，depth 是当前同步调用栈；host READ/WRITE/HASH 不额外算 module call。v1 七个 payload 都只有一次顶层 entry，因而 count/depth 都为 1；未来开放嵌套调用时必须同时登记新的 Gas operation 和精确 enter/return 计量点；
- Body cap 计完整 `canonical(FinalizedBlockBody)`，不是压缩后、protobuf 或数据库大小。

任何计数、delta、array header 增长或 block 累计都使用 checked `uint64`。per-tx 超限按第 5.2 节优先级，在触碰第一个越界资源前产生 `STATE_LIMIT_EXCEEDED`；普通 winner 回滚业务输出、提交已预留 nonce write、产生含 nonce StateChange 的 FAILED Receipt。registry/resolver 自己违反已声明静态界限属于 `EXECUTION_HALT`。

### 6.4 block 预算与最小失败结果预留

occurrence filter 在选择 winner 前必须从已包含完整 Body 固定字段和 `ordered_vertex_count` 摘要的 `EmptyBlockReservation` 开始，然后预留：

1. 该 TransactionEnvelope 在 Body transaction array 中的 exact canonical 增量；
2. 普通交易的一个 `FailureResultReserve(tx,height,tx_index)`；账户创建使用 `AccountCreateSuccessResultReserve`，跨账本 SEND/CONSUME 分别使用 `CrossLedgerSendSuccessResultReserve` / `CrossLedgerConsumeSuccessResultReserve`；
3. 必定提交的 nonce protocol write；账户创建预留完整 meta/auth/nonce 三元组写，CONSUME 另预留永久 consumed state write。

`FailureResultReserve` 是长度函数，不是链上对象：用该 tx 的固定字段、`status=FAILED`、当前已登记最长 error-code encoding、`gas_used=UINT64_MAX`、Receipt 内的固定 32-byte 字段、空 Event 数组，以及恰好一项已预留 nonce write 的 StateChange 构造字段宽度上界。`AccountCreateSuccessResultReserve` 使用 `status=SUCCESS`、`error_code=0`、`gas_used=UINT64_MAX`、空 Event 和恰好三项 meta/auth/nonce StateChange。两个 CrossLedger success reserve 同样是 exact length function：SEND 含唯一完整 `message/v1` Event、一项 nonce StateChange 与固定 return hash；CONSUME 含唯一完整 `consumed/v1` Event、consumed + nonce 两项 StateChange、永久 state value与固定 return hash。四类 reserve 都必须连同 FinalizedBlockBody transactions/results array header 增长计算 exact encoded length；StateChange 的 namespace/key/value hashes 由已知 tx、已验证 source proof和父状态构造，不允许以空数组代替。若加入 reserve 后 Body 或 mandatory block-write/Event cap 超限，该 occurrence 是 `BLOCK_CAP`，不进树、不耗 nonce；CONSUME也不得占用 working consumption key。v1 FAILED 不产生任何协议失败 Event，所以普通 Failure reserve 的空 Event 数组是精确值而非假设。

所有 winners 确定后，按 `tx_index` 执行。接受一笔成功结果前，计算“已完成实际 results + 当前候选 result + 所有未来普通 winner 的 FailureResultReserve + 未来账户创建/CrossLedger winner 的对应 SuccessResultReserve”对应的 exact Body upper bound，并检查 block write/event/body budgets。若候选业务输出使预算超限，当前普通交易转为 `STATE_LIMIT_EXCEEDED`，清空业务 write/event/return 后再验证含 nonce StateChange 的 reserve；mandatory nonce write 和失败 Receipt 仍提交。账户创建与两个 CrossLedger payload 的真实结果必须逐字节等于已预留 success shape；不匹配、reserve自身不再适配或尝试把它们降级为 FAILED 都说明实现/计数器损坏，进入 `EXECUTION_HALT`。

该规则保证一个 COMMIT slot 不需要因本地大小重新切块，且任何节点都在相同 tx_index 失败。实现可用增量 CBOR length calculator 做 O(1) 记账，但必须与完整 canonical encode 的 differential oracle 相等。

### 6.5 配置下调与 Snapshot

`max_state_namespace/key/value_bytes` 是**新写入 cap**。治理下调后，父状态中已认证且三个 component 各自不超过 v1 绝对上限的旧值仍存在、可证明、可 snapshot、可读取或删除。读取/删除的 namespace/key 只检查绝对上限，读取值仍受当前 `max_state_read_bytes_per_tx` 限制；PUT/更新的 namespace/key/new value 必须满足当前新写入 cap。不能因 cap 下调把旧 state root 判无效，也不能静默截断旧值。

Snapshot 导入先强制 `SNAPSHOT_STATE_RECORD_MAX_CANONICAL_BYTES_V1`，然后在分配前分别要求 `namespace<=STATE_NAMESPACE_MAX_BYTES_V1`、`key<=STATE_KEY_MAX_BYTES_V1`、`value<=STATE_VALUE_MAX_BYTES_V1`，再验证 framing、排序、SMT root 和目标 proof；不能只使用目标 epoch 的新写入 cap 拒绝合法旧记录。Genesis state records 则必须同时满足 epoch-0 active component caps、三个绝对 component caps 和 record 绝对 cap。

## 7. 并行执行约束

resolver 可以提前计算 access set 和静态 resource upper bound，但 Gas 与动态资源 counters 属于每个 tx 的规范 artifact。投机 worker 必须记录有序 `GasEvent`、resource deltas、read versions、writes、events 和 return；prefix certification 逐项重放或验证。

首个不匹配 index 触发既有的一次 suffix serial fallback。并行 artifact 与串行 oracle 必须在以下内容逐字节相等：

```text
GasEvent trace
gas_used / error_code
read/write/event/return/call counters
write set / events / return hash
Receipt / roots / canonical Body bytes
```

本地优化不能少计 cache hit、合并重复逻辑 write、把失败推测 Gas 加入结果，或用墙钟/OOM 映射链上错误。

## 8. 验证与测试义务

至少冻结以下跨实现 vectors：

- FeatureSet 空集、`NATIVE_KV_V1` 与 `CROSS_LEDGER_V1` 的全部组合，所有 tuple/parameter canonical bytes、内容 ID、未知/重复/乱序/尾随输入；
- 跨账本四个条件 Gas operation 的全有/全无 exact-set、proof byte/signature logical trace、cache hit/miss 等价和 success preflight 差 1 向量；
- 跨账本 `max_single_cross_ledger_verification_gas` 与 `n * max_single` 的 `limit-1/limit/limit+1`、checked overflow、空 inbound policy，以及每个containing Vertex sponsor保留份额 + shared remainder的逐 occurrence golden；专门令 `P<n`并用`sponsor_author_index=n-1`证明不会按P分配越界，并用跨作者Batch引用证明费用不会转嫁给Batch author；
- GasSchedule exact-set，始终激活的三个原生 payload operation、条件激活的两个 KV operation、跨账本四项 operation 的全有/全无组合、缺项、额外项、未激活 operation、零系数违规和 4 MiB 边界；
- 每个 operation 的 `(0,1,边界,UINT64_MAX)` byte 组合和 remaining-budget 乘加；
- ACCOUNT_CREATE exact precheck Gas 与执行 trace 一致；其他四个 payload 的成功/失败 trace 、`COMMIT_NONCE_UNMETERED` 不产生 GasEvent 但输出 StateChange；
- KV put/delete、策略轮换、账本重配置、业务失败、revert、OOG、resource exceeded 的 Receipt/nonce/Gas；同一 operation 同时越权/超资源/OOG 必须得到固定优先级且被拒绝 operation 不进 Gas trace；
- 重配置四对象/四 hash、target epoch、immutable GovernancePolicy threshold、同块多 action 的首个成功、pending state 与 FinalityCertificate close trigger；
- `fee_limit=0` 接受与任意非零值 `STATIC_INVALID`；
- 14 个资源配置字段的零值、绝对上限 `limit/limit+1`、固定系统 namespace/key/value 下界、native read/write sizing template、per-tx/per-block 关系和 checked 乘法；
- component/per-tx/per-block/Body cap 恰等于上限与多 1 byte，越界前没有 allocation/journal/event side effect；
- Body reserve：大量最小交易、最后一个 winner、成功转失败、FAILED 中恰好一项 nonce StateChange、创建成功的三项 StateChange、CBOR array header 跨 23/24、255/256、65,535/65,536 边界；
- KV 空 key static-invalid、空 value 成功、PUT/DELETE journal sizing bytes，DELETE 最终 absent 且 Snapshot 不含 tombstone；
- cap 下调后旧 namespace/key/value 在各自绝对上限内的 snapshot/read/delete，以及同尺寸新 PUT 的拒绝；Snapshot 需分别检查三个 component 绝对上限与 record 总上限；
- 不同 worker 数、调度 seed、fallback 点、平台和两个独立实现与串行 oracle 完全相同。

任一 registry、计量单位、absolute cap、错误语义或 reserve 算法变化都需要新 protocol/state-machine version、ADR、迁移规则和 epoch 边界激活。
