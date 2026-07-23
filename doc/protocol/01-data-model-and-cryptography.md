# FinalWeave v1 数据模型与密码学规范

> 协议：FinalDAG-C v1
> 后续：[BatchAC 与已签名、无独立顶点证书的元数据 DAG](02-data-availability-and-blockdag.md) · [共识与排序](03-finaldag-consensus.md) · [最终性与 Epoch](04-finality-execution-and-epochs.md)

## 1. 范围

本文冻结所有跨实现对象的类型、规范编码、域隔离哈希、签名用途、Merkle 规则和轻客户端证明结构。实现可以采用不同的内存布局、数据库和网络 framing，但不得改变本文定义的规范字节和语义 ID。

本文中的 `MUST`、`MUST NOT`、`SHOULD` 具有规范含义。所有整数均为无符号，除非字段明确声明为有符号类型。

## 2. v1 密码学套件

| 用途 | v1 算法 |
|---|---|
| 协议哈希 | SHA-256，完整 32 字节 |
| Validator DAG 签名 | Ed25519 |
| Validator 共识签名 | Ed25519 |
| 账户签名 | Ed25519；其他算法必须由 feature 激活 |
| P2P 会话 | 不属于共识编码；必须认证 peer 身份 |
| Merkle Tree | 二叉树、域隔离、重复末叶补齐 |
| 状态树 | 256 位 Sparse Merkle Tree |
| Batch 纠删码 | Reed-Solomon，`n=3f+1`，`k=f+1` |
| 共识编码 | RFC 8949 deterministic CBOR 严格子集 |

不得截断协议哈希。不得把调试 JSON、Protobuf、数据库 key 或编程语言默认序列化作为共识 preimage。

### 2.1 Ed25519 严格验证 profile

所有协议签名使用 RFC 8032 的 pure Ed25519，不使用 Ed25519ph、Ed25519ctx 或自定义 context；应用层始终签本文定义的完整 32-byte DomainHash。跨实现验证必须同时满足：

1. public key `A` 与 signature 的 `R` 都是 canonical compressed Edwards25519 encoding，编码的 y-coordinate `< p=2^255-19`；
2. `A`、`R` 可解压为曲线点，均非 identity，且属于素数阶 `L` 子群；拒绝小阶点和带非零 torsion component 的点；
3. 标量 `S` 使用 canonical little-endian encoding 且 `0 <= S < L`；
4. 验证未乘 cofactor 的 RFC 8032 方程 `[S]B = R + [SHA-512(R || A || message) mod L]A`；
5. 禁止 ZIP-215 式宽松解码、接受 non-canonical point 或仅做“库返回 true”而未确认 profile；
6. 批量验签只能是优化，接受/拒绝集合必须与逐签名严格验证完全相同。

密钥生成和确定性签名按 RFC 8032。协议向量必须覆盖 RFC 正例、`S>=L`、non-canonical y、identity、小阶/torsion public key 与 R、错误 domain、错误 key-use。不能满足该 profile 的密码库不得用于 Validator 或账户共识验签。

## 3. 基础类型

| 名称 | 编码 | 约束 |
|---|---|---|
| `Hash32` | 32-byte byte string | 必须恰好 32 字节 |
| `SignatureEd25519` | 64-byte byte string | 必须恰好 64 字节 |
| `PublicKeyEd25519` | 32-byte byte string | 必须声明密钥用途 |
| `NetworkID` | `Hash32` | 网络级域隔离 |
| `LedgerID` | `Hash32` | 账本级域隔离 |
| `ValidatorID` | `Hash32` | Validator 稳定身份 |
| `OrganizationID` | 1..256-byte byte string | 联盟治理主体标识；强类型，不得与 PeerID/Hash32 互换 |
| `PeerID` | `Hash32` | network-scoped P2P 身份；由 v1 Peer Ed25519 公钥唯一派生，强类型，不得作为账户或组织身份 |
| `AccountAddress` | `Hash32` | network-scoped 稳定账户地址；状态与交易另按账本绑定，且与其他 Hash32 类型不可互换 |
| `ValidatorIndex` | `uint16` | 当前 epoch 规范 ValidatorSet 下标 |
| `Epoch` | `uint64` | 从 0 开始，单调递增 |
| `Round` | `uint64` | 每个 epoch 从 1 开始 |
| `Height` | `uint64` | 创世为 0，首个最终块为 1 |
| `Nonce` | `uint64` | 账户顺序号 |
| `SignatureAlgorithmV1` | `uint16` enum | `ED25519=1`；其他值 v1 拒绝 |
| `AccountAddressSchemeV1` | `uint16` enum | `POLICY_V1=1`；其他值 v1 拒绝 |

ValidatorSet 按 `ValidatorID` 原始字节升序排列并分配 index。任何 bitmap、fragment index 和签名数组都使用该 index。

## 4. 规范编码

FinalDAG-C v1 使用 RFC 8949 deterministic CBOR 的严格子集：

- map key 必须是 schema 中冻结的正整数；
- key 按 deterministic CBOR 顺序编码；
- 整数使用最短编码；
- 禁止 indefinite-length item；
- 禁止浮点、tag、NaN、负零和文本形式数字；
- byte string 与 text string 不可互换；
- text 必须是有效 UTF-8，禁止实现自行做 Unicode normalization；
- array 顺序具有语义，除非 schema 明确规定排序；
- set 必须先按规范 key 排序并拒绝重复；
- 未知字段、重复 key、非最短编码和尾随字节一律拒绝。

签名对象采用 core/envelope 分离：

```text
SignedX {
  core: XCore
  signer_index: ValidatorIndex
  signature: SignatureEd25519
}
```

对象 ID 通常只覆盖 `core`。签名 envelope 只有在专用 transport/audit domain 中才允许哈希。

### 4.1 Schema 记法与字段编号

本文代码块以及本协议其他篇明确标为“共识对象、证据对象或规范 wire schema”的结构体，不是语言相关伪代码，而是 v1 规范 Schema 的紧凑记法；算法局部变量和明确标为本地的结构不参与 wire 编码：

- 每个具名结构体编码为 CBOR map；字段按代码块中自上而下的声明次序分配正整数 key `1..N`；
- v1 已发布结构体不得在中间插入字段。未来版本只能定义新 schema/version，不能重解释旧 key；
- 必填字段即使为零值也必须出现；`optional` 字段不存在时省略对应 key，存在时不得编码为 `null`；
- 匿名记录 `canonical({a,b,c})` 同样按写出顺序使用 key `1,2,3`，不得按字段名文本排序；
- 枚举按本文写出的次序从 `0` 连续编码为 `uint16`，除非字段旁明确给出其他值；布尔值只使用 CBOR `false/true`；
- tagged union 编码为 `{variant:uint16, value:...}`；不得根据 payload 形状猜测 variant；
- `schema_version` 不替代外层 domain，二者都必须校验。

因此，实现生成器必须以本规范或同版本的冻结 CDDL 为单一输入。任何使用字符串字段名、语言枚举默认值或声明顺序之外排序的编码都不是 FinalWeave v1 共识编码。

## 5. DomainHash

```text
DomainHash(domain, network_id, ledger_id, payload) = SHA256(
  "FINALWEAVE\0" ||
  U16BE(len(domain)) || ASCII(domain) ||
  network_id || ledger_id ||
  U64BE(len(payload)) || payload
)
```

要求：

- `domain` 必须是下表登记的 ASCII 常量；
- 最长 64 字节；
- `network_id` 和 `ledger_id` 都固定为 32 字节；
- 创建 NetworkID 或 LedgerID 本身时，缺失层级使用 32 字节零值；
- 不得复用“结构看起来相同”的 domain。

### 5.1 v1 domain 注册表

| Domain | 用途 |
|---|---|
| `NETWORK_ID` | NetworkID |
| `LEDGER_ID` | LedgerID |
| `VALIDATOR_ID` | ValidatorID |
| `VALIDATOR_SIGNING_KEY_ID` | 账本作用域的 Validator DAG/Consensus signing key 稳定 ID |
| `VALIDATOR_SET` | ValidatorSet 语义哈希 |
| `PROTOCOL_CONFIG` | ProtocolConfig 语义哈希 |
| `FEATURE_SET` | 激活 feature 集合 |
| `GAS_SCHEDULE` | 确定性执行 gas 表 |
| `GOVERNANCE_KEY_ID` | 治理公钥的稳定 key ID |
| `GOVERNANCE_POLICY` | 网络或账本治理策略承诺 |
| `GOVERNANCE_ACTION` | v1 账本重配置 action 语义 ID 与治理批准摘要 |
| `PEER_ID` | network-scoped Peer transport 公钥身份 |
| `PEER_HELLO` | 与 TLS exporter 绑定的 P2P 应用握手签名摘要 |
| `GENESIS_STATE_MANIFEST` | 创世状态记录清单承诺 |
| `GENESIS_APPROVAL` | 创世批准签名摘要 |
| `GENESIS_REFERENCE` | 创世信任锚 |
| `CHECKPOINT_TRUST_ANCHOR` | 带外受信 checkpoint 的内容 ID |
| `ACCOUNT_ADDRESS` | 账户地址派生 |
| `ACCOUNT_KEY_ID` | 账户签名公钥的稳定 key ID |
| `SIGNER_POLICY` | 交易签名策略承诺 |
| `TX_INTENT` | TransactionIntent 哈希 |
| `TX_ENVELOPE` | 完整交易 ID |
| `BATCH_BODY` | BatchBody 哈希 |
| `BATCH_CODING_CONTEXT` | 纠删码上下文 |
| `BATCH_HEADER` | BatchID |
| `FRAGMENT_BYTES` | fragment item 哈希 |
| `FRAGMENT_LEAF` / `FRAGMENT_NODE` / `FRAGMENT_ROOT` | fragment Merkle Tree |
| `DA_ACK` | DataAvailabilityAck 签名摘要 |
| `AC_STATEMENT` | 与 signer subset 无关的 BatchAC 语义 ID |
| `AC_ENVELOPE` | BatchAC 传输/审计哈希 |
| `DAG_VERTEX` | DAGVertexID |
| `DAG_EQUIVOCATION` | Vertex equivocation evidence |
| `DAG_GENESIS_ANCHOR` | 每个 epoch 的 synthetic round-0 锚 |
| `PROPOSER_SCHEDULE` | proposer schedule 承诺 |
| `CAUSAL_INPUT_CHUNK` | committed slot 规范 causal input chunk 内容 |
| `CAUSAL_INPUT_CHUNK_LEAF` / `CAUSAL_INPUT_CHUNK_NODE` / `CAUSAL_INPUT_CHUNK_ROOT` | causal input chunk 有序树 |
| `CAUSAL_INPUT_MANIFEST` | committed slot 规范 causal input stream 承诺 |
| `EPOCH_EMITTED_VERTEX_LEAF` / `EPOCH_EMITTED_VERTEX_NODE` / `EPOCH_EMITTED_VERTEX_ROOT` | epoch 内累计已输出 VertexID 的精确稀疏集合 |
| `DAG_DERIVATION_CHECKPOINT_CHUNK` | DAG 派生恢复检查点 chunk 内容 |
| `DAG_DERIVATION_CHECKPOINT_CHUNK_LEAF` / `DAG_DERIVATION_CHECKPOINT_CHUNK_NODE` / `DAG_DERIVATION_CHECKPOINT_CHUNK_ROOT` | DAG 派生恢复检查点 chunk 有序树 |
| `DAG_DERIVATION_CHECKPOINT_MANIFEST` | DAG 派生恢复检查点 manifest |
| `FINALIZED_BLOCK` | FinalizedBlockHeader ID |
| `EXECUTION_ATTESTATION` | ExecutionAttestation 签名摘要 |
| `FINALITY_STATEMENT` | FinalityCertificate 语义 ID |
| `FINALITY_CERT_ENVELOPE` | FinalityCertificate 传输/审计哈希 |
| `EPOCH_SEAL_STATEMENT` | EpochSeal 签名摘要及语义 ID |
| `EPOCH_SEAL_ENVELOPE` | EpochSealCertificate 传输哈希 |
| `EPOCH_SEED` | 下一 epoch 确定性 seed |
| `TX_ITEM` | transaction tree item hash |
| `TX_LEAF` / `TX_NODE` / `TX_ROOT` | transaction Merkle Tree |
| `RECEIPT` | ReceiptCore item hash |
| `RECEIPT_LEAF` / `RECEIPT_NODE` / `RECEIPT_ROOT` | receipt Merkle Tree |
| `EVENT` | Event item hash |
| `EVENT_LEAF` / `EVENT_NODE` / `EVENT_ROOT` | event Merkle Tree |
| `BLOCK_EVENT_ITEM` | 带 tx/event 位置的块级事件 item |
| `BLOCK_EVENT_LEAF` / `BLOCK_EVENT_NODE` / `BLOCK_EVENT_ROOT` | 块级展平事件树 |
| `ORDERED_VERTEX_ITEM` | ordered Vertex item hash |
| `ORDERED_VERTEX_LEAF` / `ORDERED_VERTEX_NODE` / `ORDERED_VERTEX_ROOT` | ordered Vertex tree |
| `STATE_KEY` | 状态 key 哈希 |
| `STATE_VALUE` | 状态 value 哈希 |
| `STATE_LEAF` / `STATE_NODE` / `STATE_ROOT` | Sparse Merkle Tree |
| `STATE_CHANGE` | Receipt 状态变化 item |
| `STATE_CHANGE_LEAF` / `STATE_CHANGE_NODE` / `STATE_CHANGE_ROOT` | 状态变化树 |
| `RETURN_DATA` | 执行返回字节 |
| `BLOCK_MMR_LEAF` / `BLOCK_MMR_NODE` / `BLOCK_MMR_ROOT` | 累计最终块 MMR |
| `TRANSACTION_STATUS_EVIDENCE` | 状态证明传输 ID |
| `CHECKPOINT_TRANSACTION_STATUS_EVIDENCE` | 以预置 checkpoint 为信任根的状态证明传输 ID |
| `CROSS_LEDGER_CHANNEL` | 跨账本应用 channel 的稳定 ID |
| `CROSS_LEDGER_TRUST_POLICY` | 目标账本作用域内的 source trust policy ID |
| `CROSS_LEDGER_MESSAGE_PAYLOAD` | 源消息 application payload 内容承诺 |
| `CROSS_LEDGER_MESSAGE` | 源消息签名语义 ID |
| `CROSS_LEDGER_SOURCE_EVENT` | 最终 source event occurrence ID |
| `CROSS_LEDGER_PROOF_ENVELOPE` | 跨账本 proof 传输/审计哈希 |
| `CROSS_LEDGER_CONSUMPTION_KEY` | 目标账本永久重放键 |
| `CROSS_LEDGER_CONSUMED_STATE` | consumed record 内容 ID |
| `CROSS_LEDGER_VERIFIED_CONSUME` | 目标 occurrence-filter checkpoint 中已验证 source artifact 的完整性摘要 |
| `OCCURRENCE_SCAN_SOURCE_BINDING` | 本地可恢复 filter checkpoint 对 canonical occurrence 来源的完整绑定 |
| `SNAPSHOT_MANIFEST` | 已最终状态 snapshot manifest |
| `SNAPSHOT_CHUNK` | snapshot chunk 内容 |
| `SNAPSHOT_CHUNK_LEAF` / `SNAPSHOT_CHUNK_NODE` / `SNAPSHOT_CHUNK_ROOT` | snapshot chunk 有序树 |

预留名称不等于已启用 feature。新增 WASM、聚合签名或阈值密码学必须增加独立 domain、schema 和测试向量。

## 6. 身份和密钥隔离

| 密钥 | 允许签名 | 禁止签名 |
|---|---|---|
| DAG Key | BatchHeader、DA_ACK、DAGVertex | ExecutionAttestation、EpochSeal、账户交易 |
| Consensus Key | ExecutionAttestation、EpochSealVote | Batch、DA_ACK、DAGVertex、账户交易 |
| Account Key | `tx_intent_hash`（其内绑定 signer_policy_hash） | Validator 协议对象 |
| Governance Key | GenesisApproval、治理审批和离线授权 | 日常 DAG 或执行投票 |
| Peer Key | P2P 会话身份 | 链上协议对象 |

实现必须通过不同 key URI 或 HSM slot 暴露 DAG Key 与 Consensus Key。Consensus signer 至少执行：

```text
(network_id, ledger_id, epoch, message_type, height_or_seal)
```

防双签约束。DAG signer 至少执行 own Batch slot、DA_ACK slot 和 own round 防双签约束。

Safety WAL、KMS/HSM 审计与 key activation/retirement 使用下面的账本作用域语义 key ID；它不是 `ValidatorDescriptor` 的冗余 wire 字段，也不是 provider 自报的 handle 名称：

```text
ValidatorSigningKeyIdentityV1 {
  schema_version: uint16
  key_role: enum { DAG = 1, CONSENSUS = 2 }
  validator_id: ValidatorID
  algorithm: SignatureAlgorithmV1
  public_key: PublicKeyEd25519
}

validator_signing_key_id = DomainHash(
  "VALIDATOR_SIGNING_KEY_ID", network_id, ledger_id,
  canonical(ValidatorSigningKeyIdentityV1)
)
```

`schema_version` 固定为 1。节点必须从已认证的当前或待激活 `ValidatorSet` 取得 validator ID 与对应角色公钥，执行 strict Ed25519 检查后重算 ID；DAG role 只能选择 `dag_public_key`，Consensus role 只能选择 `consensus_public_key`。`ValidateValidatorSet` 会拒绝同一集合内跨 role/Validator 的公钥复用；它看不到另一 network/ledger 的集合，因此不能替全局部署发现跨账本物理 key 复用。不同 scope 的 semantic ID 与签名 domain仍不同并防止协议重放，但生产节点必须以全局KMS inventory/readiness策略禁止Peer/DAG/Consensus/Account/Governance handle跨network/ledger复用。物理KMS URI、slot、版本和provider generation作为本地key-generation metadata另存；换物理handle但公钥不变时ID不变，公钥变化时必须经新epoch激活并产生新ID。

### 6.1 Network、Ledger、Validator 与 Genesis

FinalWeave 不把人类可读名称直接当作 NetworkID 或 LedgerID。以下派生次序避免“ID 包含状态根、状态根又以 ID 做域隔离”的哈希循环。`ZERO_ID` 表示 32 字节零值。

```text
GovernanceSigner {
  key_id: Hash32
  algorithm: SignatureAlgorithmV1
  public_key: PublicKeyEd25519
  weight: uint16
}

GovernancePolicy {
  schema_version: uint16
  threshold: uint32
  signers: [GovernanceSigner]
}

NetworkGenesisCore {
  schema_version: uint16
  protocol_family: "FINALWEAVE"
  network_nonce: Hash32
  root_governance_policy_hash: Hash32
}

NetworkGenesisBundle {
  schema_version: uint16
  core: NetworkGenesisCore
  root_governance_policy: GovernancePolicy
}
```

治理信任根具有协议绝对边界：

```text
GOVERNANCE_SIGNERS_MAX_V1                    = 1_024
GOVERNANCE_POLICY_MAX_CANONICAL_BYTES_V1     = 262_144
GOVERNANCE_APPROVALS_MAX_V1                  = 1_024
GENESIS_CERTIFICATE_MAX_CANONICAL_BYTES_V1   = 1_048_576
```

任何 Network/Ledger `GovernancePolicy` 都必须满足 `1 <= len(signers) <= 1_024` 且完整 canonical bytes 不超过 262,144；这些边界在按数组 count 分配、重算 key ID 或验签前由 bounded reader 强制，不能由治理提高。后续 governance action 的 approvals 复用同一 approval-count 边界；完整 action 还受交易与 payload 上限约束。

治理 signer 按 `key_id` 排序并拒绝重复；key ID、policy hash 与 NetworkID 为：

```text
governance_key_id = DomainHash(
  "GOVERNANCE_KEY_ID", ZERO_ID, ZERO_ID,
  canonical({algorithm, public_key})
)

root_governance_policy_hash = DomainHash(
  "GOVERNANCE_POLICY", ZERO_ID, ZERO_ID,
  canonical(GovernancePolicy)
)

network_id = DomainHash(
  "NETWORK_ID", ZERO_ID, ZERO_ID,
  canonical(NetworkGenesisCore)
)
```

`network_nonce` 必须由建网仪式生成并永久保存；同名网络不得复用它。Network genesis bundle 必须携带与 hash 一致的完整 root policy。该 bundle 本身是部署信任锚，不能用其中的自签名“证明自己可信”。

ValidatorID 与可轮换的 DAG/Consensus/Peer key 分离：

```text
ValidatorIdentityCore {
  schema_version: uint16
  organization_id: OrganizationID
  identity_nonce: Hash32
}

validator_id = DomainHash(
  "VALIDATOR_ID", network_id, ZERO_ID,
  canonical(ValidatorIdentityCore)
)
```

`identity_nonce` 在该 Validator 生命周期内固定；epoch 治理可以轮换其 DAG、Consensus 和 Peer key，而不改变 ValidatorID。一个组织可以通过不同 nonce 注册多个明确可审计的 Validator 身份。

Peer transport identity 使用独立的 pure Ed25519 key，并在 network 域内唯一派生：

```text
PeerIdentityCore {
  schema_version: uint16
  peer_public_key: PublicKeyEd25519
}

peer_id = DomainHash(
  "PEER_ID", network_id, ZERO_ID,
  canonical(PeerIdentityCore)
)
```

`schema_version` 固定为 1，公钥必须通过第 2 节 strict Ed25519 public-key 检查。`PeerID` 是原始 32-byte Hash32，不是 base58/multibase 文本、libp2p multihash、X.509 DER 或其截断；这些格式只能作为链外显示/transport 包装。Peer key 轮换会改变 PeerID，Validator 必须在 epoch 边界随完整 ValidatorSet 更新两者。

账本创世对象为：

```text
GenesisStateRecord {
  namespace: byte_string
  key: byte_string
  value: byte_string
}

GenesisStateManifest {
  schema_version: uint16
  records: [GenesisStateRecord]
}

LedgerGenesisCore {
  schema_version: uint16
  network_id: NetworkID
  ledger_nonce: Hash32
  initial_validator_set: ValidatorSet
  initial_protocol_config: ProtocolConfig
  genesis_state_manifest_hash: Hash32
  ledger_governance_policy_hash: Hash32
}

LedgerGenesisBundle {
  schema_version: uint16
  core: LedgerGenesisCore
  genesis_state_manifest: GenesisStateManifest
  ledger_governance_policy: GovernancePolicy
  feature_set: FeatureSet
  gas_schedule: GasSchedule
}
```

Manifest records 按 `(namespace bytes, key bytes)` 排序并拒绝重复。创世承诺在 LedgerID 尚不存在时使用零 ledger 域：

```text
genesis_state_manifest_hash = DomainHash(
  "GENESIS_STATE_MANIFEST", network_id, ZERO_ID,
  canonical(GenesisStateManifest)
)

ledger_governance_policy_hash = DomainHash(
  "GOVERNANCE_POLICY", network_id, ZERO_ID,
  canonical(GovernancePolicy)
)

ledger_id = DomainHash(
  "LEDGER_ID", network_id, ZERO_ID,
  canonical(LedgerGenesisCore)
)
```

LedgerGenesisCore 的 `initial_validator_set` 必须通过 `ValidateValidatorSet(set,0,network_id)`；初始配置与 Bundle 中完整 FeatureSet/GasSchedule 必须通过 `ValidateProtocolConfigStructure` 和 `ValidateExecutionConfigBundle`。Bundle 中 Manifest、治理策略或执行配置内容与 core 内对应 hash 不一致时整个 Genesis 无效。

账本显示名称、说明、图标和部署 URL 属于可变治理/目录元数据，不进入 LedgerGenesisCore 或 LedgerID；需要认证时由 GenesisCertificate 外的版本化治理声明绑定 `ledger_id`，不能反向进入 ID preimage。

得到 LedgerID 后，节点才以真实 `(network_id,ledger_id)` 构建 256 位 SMT、ValidatorSet hash、ProtocolConfig hash、空 MMR root 与 epoch 0 seed：

```text
validator_set_hash = DomainHash(
  "VALIDATOR_SET", network_id, ledger_id,
  canonical(initial_validator_set)
)

protocol_config_hash = DomainHash(
  "PROTOCOL_CONFIG", network_id, ledger_id,
  canonical(initial_protocol_config)
)

epoch0_seed = DomainHash(
  "EPOCH_SEED", network_id, ledger_id,
  canonical({source: 0, ledger_id})
)

GenesisStatement {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  genesis_state_root: Hash32
  empty_block_mmr_root: Hash32
  validator_set_hash: Hash32
  protocol_config_hash: Hash32
  epoch0_seed: Hash32
  ledger_governance_policy_hash: Hash32
}

genesis_reference = DomainHash(
  "GENESIS_REFERENCE", network_id, ledger_id,
  canonical(GenesisStatement)
)
```

Genesis state root 必须从 Manifest 的全部 records 以第 18.1 节规则重算，不能直接信任 bundle 中的 root。治理批准结构为：

```text
GenesisApproval {
  schema_version: uint16
  genesis_reference: Hash32
  signer_key_id: Hash32
  signature: SignatureEd25519
}

GenesisCertificate {
  schema_version: uint16
  statement: GenesisStatement
  approvals: [GenesisApproval]
}
```

批准者签署 `DomainHash("GENESIS_APPROVAL", network_id, ledger_id, canonical(GenesisStatement))`。proof 路径的唯一入口 `ValidateGenesisCertificate(certificate,ledger_governance_policy,genesis_validator_set,genesis_protocol_config,expected_network_id,expected_ledger_id,expected_genesis_reference)` 必须：

1. 在数组分配、hash 或验签前要求 policy canonical bytes、signer count、certificate canonical bytes 和 approval count 不超过上述绝对边界，且 `len(approvals) <= len(policy.signers)`；随后要求 policy、certificate、statement 和每项 approval 的 `schema_version==1`，statement 的 network/ledger 等于两个 expected ID；
2. 对 policy 的每个 signer 重算 `GOVERNANCE_KEY_ID`，验证 strict Ed25519 key、正 weight、按 key ID 严格升序且无重复，并以 checked arithmetic 要求 `1 <= threshold <= total_weight`；
3. 调用 `ValidateValidatorSet(genesis_validator_set,0,expected_network_id)` 与 `ValidateProtocolConfigStructure(genesis_protocol_config,genesis_validator_set)`，重算两个 ledger-scoped hash 并逐字节等于 statement；
4. 以协议公式重算 `epoch0_seed` 与 `empty_block_mmr_root` 并逐字节等于 statement；重算 policy hash并等于 `ledger_governance_policy_hash`，再以完整 statement 重算 `genesis_reference` 并逐字节等于带外 expected reference；
5. approvals 按 `signer_key_id` 严格升序且无重复，每项 `genesis_reference` 等于刚重算的 reference，signer 必须存在于 policy，并对上述完整 `GENESIS_APPROVAL` digest 通过 strict Ed25519；
6. 只累计已验证的不同 signer weight，checked 总和必须达到 policy threshold；未知 signer、重复 signer、错误派生字段/reference/domain/key generation、无效签名、weight 溢出、超出边界或少于 threshold 一律拒绝。

实际建网/账本安装使用更强的 `ValidateGenesisInstallation(bundle,certificate,expected_network_id,expected_genesis_reference)`：从 `LedgerGenesisCore` 重算 LedgerID；验证 manifest/policy/FeatureSet/GasSchedule 的内容 ID与两个 config 谓词；从完整 `GenesisStateManifest` 重建 `genesis_state_root`；按上述公式构造唯一预期 GenesisStatement 并要求与 certificate statement 逐字段相同；最后调用 `ValidateGenesisCertificate`。proof-only 路径没有完整 state manifest，因此 `genesis_state_root` 只因带外固定 `expected_genesis_reference` 而被信任，不能声称重新执行了 Genesis；纯派生的 epoch seed、空 MMR、set/config/policy hash 仍必须无条件重算。

height 0 的信任锚是通过相应谓词的 GenesisCertificate，而不是一个未验证 approvals 的 statement envelope。height 1 Header 的 `parent_block_id == genesis_reference`、`parent_block_mmr_root == empty_block_mmr_root`，执行父状态为 `genesis_state_root`。Genesis 不是普通 FinalizedBlock，也不占 MMR leaf。

### 6.2 账户认证状态

```text
AccountAddressCore {
  schema_version: uint16
  address_scheme: AccountAddressSchemeV1
  creation_salt: Hash32
  initial_signer_policy_hash: Hash32
}

account_address = DomainHash(
  "ACCOUNT_ADDRESS", network_id, ZERO_ID,
  canonical(AccountAddressCore)
)

AccountNonceState {
  schema_version: uint16
  next_nonce: Nonce
}
```

Account key ID、SignerPolicy hash 与 AccountAddress 都是 network-scoped 内容身份，统一使用 `(network_id,ZERO_ID)`；这使 Genesis Manifest 能在 LedgerID 形成前列出创世账户，也允许同一账户身份显式加入多个 Ledger。交易 Intent、账户状态、nonce 和签名摘要仍绑定真实 LedgerID，因此不能跨账本重放。

`creation_salt` 必须由账户创建者选择并纳入已授权的创建交易；相同初始策略可以通过不同 salt 创建不同地址。地址一旦创建不随 policy 轮换变化。v1 固定系统 namespace 字节为 UTF-8 `finalweave/v1/account/meta`、`finalweave/v1/account/nonce` 与 `finalweave/v1/account/auth`，三者 StateKey 的原始 key 都是 32-byte AccountAddress。账户身份在认证状态中始终由完整三元组表示：三项必须全部存在或全部不存在；只存在一项或两项是认证状态损坏，执行节点必须 `EXECUTION_HALT`，不能把它解释成新账户。

```text
AccountMetadataState {
  schema_version: uint16
  address_core: AccountAddressCore
  initial_signer_policy: SignerPolicy
}

AccountAuthState {
  schema_version: uint16
  base_policy_hash: Hash32
  pending_policy_hash: optional Hash32
  pending_effective_height: optional Height
}

ResolveActivePolicy(auth, height):
  if auth.pending_policy_hash exists and
     height >= auth.pending_effective_height:
    return auth.pending_policy_hash
  return auth.base_policy_hash
```

pending 两字段必须同时存在或同时缺失。账户认证状态至少能据此解析当前生效的 `active_signer_policy_hash`。TransactionEnvelope 的自包含签名有效还不够；其 policy hash 必须等于 sender 在本高度块开始时的认证状态，否则攻击者可用自造 policy 抢占他人的 nonce。

`AccountMetadataState` 在账户生命周期内不可变。验证它时必须重算其中完整 `initial_signer_policy` 的 key ID、排序、threshold、checked weight 和 `SIGNER_POLICY` hash，要求该 hash 等于 `address_core.initial_signer_policy_hash`，再从 core 重算 AccountAddress 并要求等于三项状态使用的原始 key。Genesis manifest 中每个账户必须恰好具有这一 meta/auth/nonce 三元组：三个 state schema version 都为 1，auth 的 `base_policy_hash` 等于 initial policy hash 且两个 pending 字段缺失，nonce 的 `next_nonce == 0`；任何孤立系统记录、地址/core 错配、无完整 policy preimage、非法 policy、非零初始 nonce 或重复地址都使 Genesis 无效。普通交易只用已经由 state root 认证的三元组和 active policy 做授权，不要求、也不能假设 TransactionEnvelope 再携带 AccountAddressCore。

策略轮换交易成功后，执行器先把当前高度解析出的 active policy 规范化写入 `base_policy_hash`，再写入新 pending policy 与 `pending_effective_height=current_height+1`；checked addition 溢出时交易失败。一个块内的所有 occurrence 都针对块开始时已激活的 policy 校验；同块后续交易不能依赖尚未最终执行的 policy 变化。认证无效的 occurrence 不进入 transaction tree、不产生 Receipt、也不消费 nonce。该一高度延迟是共识语义，不是 mempool 策略。

## 7. Transaction

```text
AuthorizedAccessEntry {
  scope_kind: EXACT | PREFIX
  mode: READ | WRITE
  namespace: byte_string
  key_or_prefix: byte_string
}

TransactionIntent {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  sender: AccountAddress
  nonce: Nonce
  valid_from_height: Height
  valid_until_height: Height
  gas_limit: uint64
  fee_limit: uint64
  priority_class: uint16
  payload_type: uint16
  authorized_access_scope: [AuthorizedAccessEntry]
  payload: byte_string
  memo_hash: optional Hash32
  signer_policy_hash: Hash32
}

AccountSigner {
  key_id: Hash32
  algorithm: SignatureAlgorithmV1
  public_key: PublicKeyEd25519
  weight: uint16
}

SignerPolicy {
  schema_version: uint16
  threshold: uint32
  signers: [AccountSigner]
}

AccountSignature {
  key_id: Hash32
  algorithm: SignatureAlgorithmV1
  signature: SignatureEd25519
}

TransactionEnvelope {
  intent: TransactionIntent
  signer_policy: SignerPolicy
  signatures: [AccountSignature]
}

CreateAccountPayloadV1 {
  schema_version: uint16
  address_core: AccountAddressCore
}
```

v1 固定 `payload_type=1` 为 `ACCOUNT_CREATE_V1`，`payload` 必须是 `canonical(CreateAccountPayloadV1)`，两个 schema version 都必须为 1。它是父状态中 sender 三元组全部不存在时唯一允许的自证入口，并同时要求：

1. `intent.nonce == 0`，从 payload core 以 `(network_id,ZERO_ID)` 重算出的 AccountAddress 逐字节等于 `intent.sender`；
2. 重算 Envelope 的完整 SignerPolicy，要求其 hash 同时等于 `intent.signer_policy_hash` 与 `address_core.initial_signer_policy_hash`，并用该 policy 验证全部账户签名和 threshold；
3. meta/auth/nonce 三个 StateKey 在父状态都不存在，且同一块尚未选择该地址的另一创建交易；任一项已存在、三元组残缺或地址已在本块创建都不能走普通账户路径；
4. 用户签名的 `authorized_access_scope` 必须为空；原生 resolver 另行注入对该 sender 三个保留 StateKey 的 `EXACT WRITE` system access，用户不得重复声明或覆盖。创建 Gas 按已认证 GasSchedule 中 operation `0x00010001` 和[执行注册表规范](05-execution-registry-gas-and-resource-metering.md)的完整固定 trace 精确计量；payload、空 scope、Gas 和全部语义错误必须在选择 winner 前预检；通过预检的原生创建转移不可由用户 revert，也不会产生普通 `FAILED` Receipt。

被选中的创建交易原子写入 `AccountMetadataState{schema_version:1,address_core,initial_signer_policy:envelope.signer_policy}`、`AccountAuthState{schema_version:1,base_policy_hash:intent.signer_policy_hash,pending:absent}` 和 `AccountNonceState{schema_version:1,next_nonce:1}`，并产生成功 Receipt。meta 此后不可修改；三个保留 namespace 只能由该原生创建路径和协议 signer-policy 轮换/nonce 逻辑访问，普通模块、WASM 与用户 scope 不得写入、删除或重置。数据库提交、崩溃恢复和 SMT root 必须把三项作为一个交易 journal 处理：成功时三项全有，任何本地失败时三项全无且节点停止认证该结果；禁止公开部分账户。由于所有确定性拒绝发生在 winner 选择前，失败尝试不进 transaction tree、不产 Receipt，也不需要违反“一个 tx id 最多一张 Receipt”的历史 seen-set。创建高度内的普通交易仍按块开始认证视图看不到新账户，最早从下一高度使用它。

`authorized_access_scope` 按 `(scope_kind, mode, namespace, key_or_prefix)` 规范排序并拒绝重复；`PREFIX` 是授权边界，不是允许无界扫描。协议隐式访问和实际精确访问的推导见[最终性与执行规范](04-finality-execution-and-epochs.md)。

账户 key ID 与策略哈希为：

```text
key_id = DomainHash(
  "ACCOUNT_KEY_ID", network_id, ZERO_ID,
  canonical({algorithm, public_key})
)

signer_policy_hash = DomainHash(
  "SIGNER_POLICY", network_id, ZERO_ID,
  canonical(SignerPolicy)
)
```

`SignerPolicy.signers` 和 `TransactionEnvelope.signatures` 均按 `key_id` 升序并拒绝重复。策略至少有一个 signer，每个 weight 必须大于 0，checked weight 总和必须可表示，且 `1 <= threshold <= total_weight`；项数和总字节受 ProtocolConfig 限制。v1 每份 AccountSignature 对 `tx_intent_hash` 的完整 32 字节签名；验签后按 policy 中对应 signer 的 weight 求和，必须达到 threshold。普通 Envelope 中策略的重算哈希必须等于 Intent 的 `signer_policy_hash`，并等于 sender 在本高度块开始时的 `active_signer_policy_hash`；唯一例外是满足上面全部条件的 `ACCOUNT_CREATE_V1`，它以地址 core 和同一 Envelope policy 自证并建立首个认证状态。这样策略替换、签名重排或删除都会使 Envelope 无效或改变 tx_id；其他未获账户认证的自签 policy 不能消费 nonce。

```text
tx_intent_hash = DomainHash(
  "TX_INTENT", network_id, ledger_id,
  canonical(TransactionIntent)
)

tx_id = DomainHash(
  "TX_ENVELOPE", network_id, ledger_id,
  canonical({tx_intent_hash, signer_policy, signatures})
)
```

`CanonicalAndStaticValid` 对这些配置字段的消费语义固定为：

- `len(canonical(TransactionEnvelope)) <= max_transaction_bytes`；`max_signer_policy_bytes` 单独计 `len(canonical(SignerPolicy))`，两者都使用完整 deterministic-CBOR bytes；
- 要求 `valid_from_height <= valid_until_height`，以 checked subtraction 计算 `span_minus_one=valid_until_height-valid_from_height`，并要求 `span_minus_one < max_validity_window_heights`；因此可执行高度的含首尾数量最多恰为该上限；
- `len(authorized_access_scope) <= max_authorized_access_entries_per_tx`，且 `len(canonical(authorized_access_scope)) <= max_authorized_access_bytes_per_tx`；长度包含 array header 和全部 entry 的规范 CBOR，不包含 TransactionIntent 其他字段；
- 用户 scope 中任何对三个 `finalweave/v1/account/*` 或 `finalweave/v1/cross-ledger/consumed` 保留 namespace 的 `WRITE`（无论 EXACT/PREFIX）都是 `STATIC_INVALID`；协议原生 resolver 的 system access 不编码进该用户数组；
- `nonce < UINT64_MAX`，`gas_limit > 0` 且 `gas_limit <= max_execution_gas_per_finalized_block`；v1 还要求 `fee_limit == 0`；
- `payload_type=ACCOUNT_CREATE_V1` 时 payload、地址、初始 policy、`nonce=0`、空用户 scope、三项隐式系统写和精确预检 Gas 必须满足上述创建规则；其他 payload type 不得解码为该 schema；
- 任一超限都是 `STATIC_INVALID` occurrence：不进树、不产 Receipt、不耗 nonce。

认证状态中的 `next_nonce == UINT64_MAX` 是永久耗尽哨兵，任何 occurrence 都不能再消费该账户 nonce。这样接受 `nonce=UINT64_MAX-1` 后可以把状态安全推进到耗尽哨兵，而不会发生 `next_nonce+1` 溢出。

`max_future_nonce_gap` 只约束 mempool/deferred-pool 资源，不改变共识 winner：admission 以 checked/saturating ceiling `min(UINT64_MAX-1,next_nonce+max_future_nonce_gap)` 判断，超过者可返回可重试的 `NONCE_TOO_FAR` 或不持久化；Byzantine Batch 中同一 occurrence 到达共识 filter 时仍统一按 `nonce > next_nonce` 标记 `FUTURE_NONCE`。实现不得把本地 future-lane 是否保存带入 tx tree 或 Receipt。

`gas_limit`、`fee_limit` 和 `priority_class` 都是用户签名语义。`gas_limit` 必须大于 0 且不超过 `max_execution_gas_per_finalized_block`；FinalWeave v1 没有原生费用状态，因而只接受 `fee_limit=0`，也不得产生费用写。v1 同样固定 `priority_class=0`，任何非零值都是 `STATIC_INVALID`；节点只能按经过认证的连接/租户配额实施链外 QoS，不能让用户自报字段改变 mempool、Batch、causal 顺序或执行。未来收费或多优先级能力必须升级 state-machine/protocol 版本，并分别冻结 FeeSchedule、资产/payer、失败收费、优先级授权/配额与调度公平性。本地调度不得把未签名费用或优先级写入执行结果。`memo_hash` 只承诺链下 memo，不要求在链上公开原文。

协议状态不得新增历史 tx-id、intent seen 集合或 nonce winner 映射。普通账户交易重放保护的唯一权威状态是账户认证的 `next_nonce`；`CROSS_LEDGER_CONSUME_V1` 另以协议第六篇冻结的 source-event-scoped 永久 consumed key 防止同一外部事件被不同 relayer/target nonce 重放，该专用集合不能被泛化成交易 seen-set。

## 8. Batch 与 BatchAC

### 8.1 BatchBody

```text
BatchBody {
  schema_version: uint16
  transactions: [TransactionEnvelope]
}
```

Batch 至少含一笔交易。交易数组顺序有语义。`body_hash`：

```text
DomainHash("BATCH_BODY", network_id, ledger_id, canonical(BatchBody))
```

v1 对“任一达到交易字节上限的合法 Envelope 至少能单独成 Batch”冻结下列尺寸函数。按第 4.1 节，单交易 `BatchBody` 的外层恰为 5 bytes：二字段 map header、key 1、值 `schema_version=1`、key 2 和单元素 array header 各 1 byte；嵌套 `TransactionEnvelope` 的规范字节原样出现，不增加 byte-string wrapper：

```text
BATCH_BODY_SINGLE_TX_WRAPPER_BYTES_V1 = 5

MaxSingleTxBatchCanonicalBytes(max_transaction_bytes):
  return checked_add(
    uint64(max_transaction_bytes),
    BATCH_BODY_SINGLE_TX_WRAPPER_BYTES_V1
  )
```

checked addition 溢出使 ProtocolConfig 无效。该函数是配置交叉约束和测试向量，不允许实现以语言对象估算、压缩后长度或 transport framing 代替完整 `len(canonical(BatchBody))`。

### 8.2 BatchHeader

```text
BatchHeaderCore {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  epoch: Epoch
  author_index: ValidatorIndex
  batch_seq: uint64
  body_hash: Hash32
  transaction_count: uint32
  transaction_root: Hash32
  body_length: uint64
  data_shards: uint16
  parity_shards: uint16
  fragment_root: Hash32
  coding_context_hash: Hash32
}

BatchHeader {
  core: BatchHeaderCore
  author_signature: SignatureEd25519
}
```

编码上下文在 fragment root 和 BatchID 之前计算：

```text
coding_context_hash = DomainHash(
  "BATCH_CODING_CONTEXT", network_id, ledger_id,
  canonical({
    protocol_version,
    coding_profile: "RS_GF256_V1",
    body_hash,
    body_length,
    data_shards,
    parity_shards,
    zero_padding_rule,
    generator_matrix_id
  })
)
```

`coding_profile`、零填充、generator matrix 和 shard length 推导由协议版本及测试向量冻结。相同上下文必须逐字节产生相同 codeword。

```text
batch_id = DomainHash(
  "BATCH_HEADER", network_id, ledger_id,
  canonical(BatchHeaderCore)
)
```

签名摘要等于 `batch_id`。唯一槽位是 `(epoch, author_index, batch_seq)`。

### 8.3 Fragment

```text
BatchFragment {
  batch_id: Hash32
  fragment_index: ValidatorIndex
  fragment_bytes: byte_string
  merkle_path: [Hash32]
}
```

fragment index 必须对应当前 ValidatorSet index。fragment Merkle Tree 的叶不编码 `BatchFragment` envelope，因为 envelope 中的 `batch_id` 又依赖 Header 的 `fragment_root`，那会形成循环。规范公式是：

```text
fragment_item_i = DomainHash(
  "FRAGMENT_BYTES", network_id, ledger_id,
  canonical({coding_context_hash, fragment_index: i, fragment_bytes_i})
)

fragment_leaf_i = DomainHash(
  "FRAGMENT_LEAF", network_id, ledger_id,
  U16BE(i) || fragment_item_i
)

fragment_root = DomainHash(
  "FRAGMENT_ROOT", network_id, ledger_id,
  canonical({fragment_count: n, coding_context_hash, tree_top})
)
```

内部节点使用 `FRAGMENT_NODE` 对 `left || right` 哈希，奇数规则沿用第 18 节。`merkle_path` 长度必须恰好等于把 `n` 反复上取整除以 2 直到 1 的层数；因 v1 `n<=253`，最长为 8。左右位置由逐层 fragment index bit 推导，奇数复制层的 sibling 必须等于当前 hash。先算 codeword 与 fragment root，再构造 BatchHeaderCore 和 BatchID；最后才把 BatchID、fragment bytes 与 path 装进传输 envelope。仅验证单一 Merkle path 不能证明 fragments 构成有效 Reed-Solomon codeword；ACK 规则见[第二篇](02-data-availability-and-blockdag.md)。

### 8.4 DataAvailabilityAck 与 AvailabilityCertificate

```text
DataAvailabilityAckCore {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  epoch: Epoch
  batch_id: Hash32
  batch_author_index: ValidatorIndex
  batch_seq: uint64
  fragment_index: ValidatorIndex
}

DataAvailabilityAck {
  core: DataAvailabilityAckCore
  signer_index: ValidatorIndex
  signature: SignatureEd25519
}

AvailabilityCertificate {
  schema_version: uint16
  statement: DataAvailabilityStatement
  signer_bitmap: byte_string
  acknowledgements: [DataAvailabilityAck]
}
```

`BatchAC` 是本文与其余文档对 `AvailabilityCertificate` 的唯一显示别名；二者是同一个 wire schema，绝不能编码、存储或哈希为两个不同对象。实现 API 可以使用更短的 `BatchAC` 类型名，但规范 CBOR 字段仍完全由上式定义。

FinalDAG-C v1 冻结一一映射：

```text
DataAvailabilityAck.core.fragment_index
  == DataAvailabilityAck.signer_index
```

Validator `i` 只能为自己持久化的 shard `i` 签 ACK。签名有效但 fragment index 与 signer index 不相等的 ACK 必须拒绝，不能计入 bitmap 或 quorum。该等式是“q 个 ACK 至少留下 k 个不同诚实 shards”证明的必要前提。

```text
DataAvailabilityStatement {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  epoch: Epoch
  batch_id: Hash32
  batch_author_index: ValidatorIndex
  batch_seq: uint64
}
```

```text
ac_id = DomainHash(
  "AC_STATEMENT", network_id, ledger_id,
  canonical(DataAvailabilityStatement)
)

ac_envelope_hash = DomainHash(
  "AC_ENVELOPE", network_id, ledger_id,
  canonical(AvailabilityCertificate)
)
```

`ac_id` 不含 bitmap 和 ACK 数组。不同合法 signer subset 是同一语义证书，但其完整 certificate 规范字节不同，因而 `ac_envelope_hash` 可以不同。接收者必须先完成完整证书规范编码、bitmap、排序、签名和 quorum 验证，才可把该 hash 用于传输去重、存储校验或审计；`AC_ENVELOPE` 绝不能进入 BatchID、`ac_id`、DAG parent、排序或任何共识选择。

### 8.5 AvailabilityReference

```text
AvailabilityReference {
  batch_id: Hash32
  ac_id: Hash32
}
```

DAGVertex 只承诺 BatchID 与 signer-subset 无关的 AC 语义 ID。完整 AC 可 inline 或按 ID 获取，但不进入 VertexID。

## 9. DAGVertex

```text
ExecutionAttestationRef {
  statement_id: Hash32
  signer_index: ValidatorIndex
  signature: SignatureEd25519
}

DAGRejoinCheckpointRef {
  schema_version: uint16
  target_height: Height
  target_finalized_block_id: Hash32
  target_finality_id: Hash32
  target_committed_slot: ProposerSlot
  previous_own_round: Round
  previous_own_vertex_id: Hash32
  previous_own_was_emitted: bool
  emitted_set_siblings_bottom_up: [Hash32]
}

DAGVertexCore {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  epoch: Epoch
  round: Round
  author_index: ValidatorIndex
  own_parent: optional Hash32
  rejoin_checkpoint: optional DAGRejoinCheckpointRef
  strong_parents: [Hash32]
  weak_parents: [Hash32]
  availability_references: [AvailabilityReference]
  epoch_closing: bool
  execution_attestations: [ExecutionAttestationRef]
  evidence_refs: [Hash32]
}

DAGVertex {
  core: DAGVertexCore
  author_signature: SignatureEd25519
}
```

`max_vertex_bytes` 始终计完整签名 envelope 的 `len(canonical(DAGVertex))`，不是只计 core、VertexID preimage 或网络 framing。为保证所有诚实作者即使同时承担 timely-primary parent 与 q 个 certificate supporters 仍能构造最小控制 Vertex，v1 冻结：

```text
MinRequiredVertexSizingTemplate(set):
  n = len(set.validators)
  f = (n - 1) / 3
  q = 2*f + 1
  required_strong_parent_count = min(n, q + 1)

  return DAGVertex {
    core: DAGVertexCore {
      schema_version: 1,
      network_id: ZERO_HASH32,
      ledger_id: ZERO_HASH32,
      epoch: UINT64_MAX,
      round: UINT64_MAX,
      author_index: n - 1,
      own_parent: ZERO_HASH32,
      rejoin_checkpoint: null,
      strong_parents:
        [DistinctHash32(0) ..
         DistinctHash32(required_strong_parent_count - 1)],
      weak_parents: [],
      availability_references: [],
      epoch_closing: true,
      execution_attestations: [],
      evidence_refs: []
    },
    author_signature: ZERO_SIGNATURE_64
  }

MinRequiredVertexCanonicalBytes(set):
  return len(canonical(MinRequiredVertexSizingTemplate(set)))
```

这是纯尺寸模板，不是可验签协议对象；零值和 `DistinctHash32(i)` 只提供固定宽度，`UINT64_MAX` 与最大合法 author index 则覆盖最宽规范整数编码。它包含普通 round 必需的 own parent、最坏情况下 `q+1` 个 required strong parents 和完整 64-byte signature；因此也覆盖更小的 round-1 q-anchor Vertex。所有 `n/f/q`、`q+1`、array 长度和规范字节运算必须 checked。

```text
vertex_id = DomainHash(
  "DAG_VERTEX", network_id, ledger_id,
  canonical(DAGVertexCore)
)
```

`DAGVertex.author_signature` 使用 DAG Key 对完整 `vertex_id` 签名。`ExecutionAttestationRef.statement_id` 必须等于对应 `FinalityStatement` 的 `finality_id`，其 signature 必须逐字节等于该 signer 对同一 statement 的 ExecutionAttestation signature；接收者必须先按 statement ID 取得并验证完整 statement，不能只相信引用。resolved statement 的 network、ledger、epoch 必须分别等于 containing DAGVertex；旧 epoch closing attestation 的迟到重传走旧 epoch control sync，不能塞入新 epoch Vertex。相同 `(statement_id,signer_index)` 已由规范排序去重；同 signer/height 的冲突 statement 只形成 evidence，绝不能在任一证书中重复计票。FinalDAG-C v1 没有 VertexAck 或 VertexCertificate。VertexID 不包含任何证书 envelope。

所有集合型数组都必须使用下列逐字段字节序 key 严格升序编码并拒绝重复 key：

```text
strong_parents          (parent.author_index, parent.vertex_id)
weak_parents            (parent.round, parent.author_index, parent.vertex_id)
availability_references (batch_id, ac_id)
execution_attestations  (statement_id, signer_index)
evidence_refs           (evidence_id bytes)
```

parent 的 round/author 必须从已经完成签名与 ID 验证的父 Vertex 取得，不能相信响应方附加的排序元数据。support DFS 的逻辑访问次序固定为 own parent、strong parents、weak parents；数组规范顺序见[第三篇](03-finaldag-consensus.md)。

`evidence_refs` 不属于 parent/dependency 边，也不参与 support DFS。它只是固定 32-byte evidence ID 的签名审计 hint；接收者在 Vertex 关键路径不得按 ref 拉取完整 evidence，缺失、错误或被本地 cache 驱逐的 ref 不改变 containing Vertex 的有效性。只有异步 evidence worker在独立硬预算内取得完整对象后，才验证 ID、上下文与冲突签名并交给治理/审计。

对诚实 signer，普通 `own_parent` 必须指向其 durable WAL 中已签署的最高 lower-round Vertex，不要求恰好来自 `round-1`。这是 restricted round jump 可以略过已经不再影响未决前缀的中间 round 的必要条件。接收者无法证明 Byzantine 作者没有隐藏更高 Vertex，因此不验证“最高”，但 wire-validity 要求普通 own parent 属于同一 network/ledger/epoch/作者、签名有效，并满足 checked `1 <= child.round-parent.round < dag_gc_rounds`；这阻止古老隐藏分支重新引用已裁剪历史。若 own parent 恰好来自 `round-1`，它也可以出现在 strong parent 集中，DFS 只访问一次。

为避免一个长期离线 Validator 恰在另一节点故障后使可响应节点仍凑不齐 q，round>1 允许一种受认证的 rejoin：`own_parent=null` 当且仅当 `rejoin_checkpoint` 存在。接收者必须取得 checkpoint 指向的同 epoch Header 与 FinalityCertificate，逐项验证 block/finality/committed slot，并要求 `target_committed_slot.proposer_round < child.round` 且 checked `child.round-target_committed_slot.proposer_round < dag_gc_rounds`。proof 数组必须恰好 256 项，按 VertexID MSB→LSB 用 Header 的 epoch emitted set root 验证 `previous_own_vertex_id` 的 membership 或 non-membership，结果必须等于 `previous_own_was_emitted`；`previous_own_round < child.round` 且不得晚于 target committed slot round。该 ref 是新 Vertex 签名 core 的一部分。

诚实作者只有在 WAL 中的最高 prior own Vertex 正是 ref 的 `(round,id)`、普通 gap 已达 `dag_gc_rounds`，并已 fsync `VERTEX_REJOIN_INTENT(epoch,author,target_finality_id,previous_own_id,new_round)` 后才能使用 rejoin；此后旧 own branch 永久 abandoned，不能再签引用它的新 Vertex。若 checkpoint 证明 prior Vertex 未 emitted，其 payload 也明确放弃且因 age rule 不能重新进入未来 Past；若已 emitted，exact set 保证不会重复输出。Byzantine 对隐藏历史撒谎不比原有不可验证的“最高 own parent”能力更强，而诚实 WAL 锁与同 round 唯一签名保持安全。round 1 固定使用 synthetic genesis，不携 rejoin。

## 10. ValidatorSet 与 ProtocolConfig

```text
ValidatorDescriptor {
  validator_id: ValidatorID
  identity_nonce: Hash32
  dag_public_key: PublicKeyEd25519
  consensus_public_key: PublicKeyEd25519
  peer_public_key: PublicKeyEd25519
  peer_id: PeerID
  organization_id: OrganizationID
}

ValidatorSet {
  schema_version: uint16
  epoch: Epoch
  validators: [ValidatorDescriptor]
}
```

定义统一的 `ValidateValidatorSet(set, expected_epoch, expected_network_id)`。Genesis、每一跳 EpochSeal、FinalityProof、快照导入和新 epoch 启动都必须先调用它，不能各自实现弱化版本。该谓词要求：

- `schema_version == 1` 且 `set.epoch == expected_epoch`；
- `4 <= n=len(validators) <= 253`、`(n-1) mod 3 == 0`，从而存在唯一 `f=(n-1)/3 >= 1`；
- validators 按 ValidatorID 原始字节严格升序，因此 ValidatorID 不重复；
- 对每项使用 `ValidatorIdentityCore{schema_version:1,organization_id,identity_nonce}` 重算 ValidatorID 并逐字节相等；
- DAG、Consensus 和 Peer public key 均通过本篇 strict Ed25519 public-key 检查；三类 key 在整个集合中跨角色两两不同，任何 key 都不能占据两个 signer index 或承担两个用途；
- 对每项以 `expected_network_id` 和 descriptor 的 `peer_public_key` 重算 `PeerIdentityCore`/`PeerID` 并逐字节相等；`peer_id` 在集合内唯一，`OrganizationID` 满足规范长度，全部固定长度字段精确满足基础类型宽度。

因此 key 轮换必须产生一个仍满足上述谓词的完整集合；仅用不同 ValidatorID 包装同一 Consensus key 是无效换届。v1 的 quorum 公式是：

```text
n = len(validators) = 3f + 1
q = 2f + 1
k = f + 1
```

不允许把任意 `n>3f` 自动映射到 v1。`n<=253` 同时是 `RS_GF256_V1` 使用互异非零求值点的硬边界，不是本地性能建议。

```text
FeatureEntry {
  feature_id: uint32
  feature_version: uint32
  parameter_schema_version: uint16
  parameters_cbor: byte_string
}

FeatureSet {
  schema_version: uint16
  entries: [FeatureEntry]
}

GasCostEntry {
  operation_id: uint32
  base_cost: uint64
  per_input_byte: uint64
  per_output_byte: uint64
}

GasSchedule {
  schema_version: uint16
  entries: [GasCostEntry]
}
```

Feature entries 按 `feature_id` 严格升序且每个 ID 恰好激活至多一个 version；同一 FeatureSet 中并存同 ID 的多个版本无效，不存在“最高版本获胜”的隐式规则。Gas entries 按 `operation_id` 升序并拒绝重复。`parameters_cbor` 必须是恰好一个 RFC 8949 deterministic CBOR item 的完整规范字节，并通过 `(feature_id,feature_version,parameter_schema_version)` 在当前 protocol/state-machine 版本中登记的 typed parameter schema；即使没有参数也编码该 schema 的规范空对象，不能用空 byte string、JSON 或未登记 opaque bytes。v1 的完整 Feature/payload/Gas-operation 注册表、byte metric、exact-set 约束和 remaining-budget 公式位于[执行注册表、Gas 与资源计量规范](05-execution-registry-gas-and-resource-metering.md)；代码认识但该表未登记或当前 FeatureSet 未激活的 tuple/operation 仍然无效。

v1 最多 256 个 FeatureEntry，单项参数最多 64 KiB，完整 FeatureSet 规范字节最多 1 MiB；GasSchedule 最多 65,536 项且完整规范字节最多 4 MiB。所有长度、weight 和资源计数使用 checked arithmetic；Gas 乘加必须采用先与 remaining budget 比较的无溢出算法，不能先计算一个可能 wrap 的 `uint64` cost。

完整参数直接进入 FeatureSet 内容承诺，因此不存在“只有 parameters hash、却拿不到重放规则”的悬空引用。它们的承诺是：

```text
feature_set_hash = DomainHash(
  "FEATURE_SET", network_id, ZERO_ID,
  canonical(FeatureSet)
)

gas_schedule_hash = DomainHash(
  "GAS_SCHEDULE", network_id, ZERO_ID,
  canonical(GasSchedule)
)
```

FeatureSet 与 GasSchedule 是 network-scoped、可被多个 Ledger 复用的不可变内容对象，因此其内容 ID 始终使用 `(network_id,ZERO_ID)`。每个 Ledger 的 ProtocolConfig 再以真实 LedgerID 绑定所选内容 ID；同样内容可以跨 Ledger 复用，但不能把某 Ledger 的 ProtocolConfig 或执行结果搬到另一 Ledger。Ledger genesis、epoch 变更、FinalityProof 和 snapshot 同步都必须取得并验证完整 FeatureSet（含每项 typed parameter bytes）与 GasSchedule，不能只接受调用者提供的 Hash32。

```text
ProtocolConfig {
  protocol_name: "FinalDAG-C"
  protocol_version: 1
  feature_set_hash: Hash32
  state_machine_version: uint32
  gas_schedule_hash: Hash32
  max_transaction_bytes: uint32
  max_signer_policy_bytes: uint32
  max_account_signers: uint16
  max_signatures_per_transaction: uint16
  prefilter_verification_work_reserve_per_occurrence_sponsor: uint64
  max_prefilter_verification_work_per_finalized_block: uint64
  max_validity_window_heights: uint64
  max_future_nonce_gap: uint64
  max_batch_body_bytes: uint64
  max_batch_transactions: uint32
  proposer_slots_per_round: uint16
  max_strong_parents: uint16
  max_weak_parents: uint16
  max_batches_per_vertex: uint16
  max_vertex_bytes: uint32
  max_future_round_gap: uint64
  primary_timeout_ms: uint64
  certificate_timeout_ms: uint64
  schedule_lookahead_rounds: uint64
  dag_gc_rounds: uint64
  batch_retention_heights: uint64
  max_execution_lag_heights: uint64
  max_transactions_per_finalized_block: uint32
  max_execution_gas_per_finalized_block: uint64
  max_authorized_access_entries_per_tx: uint32
  max_authorized_access_bytes_per_tx: uint32
  max_exact_observed_access_keys_per_tx: uint32
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
  execution_parallelism: uint16
  mvcc_max_versions: uint16
  mvcc_max_bytes: uint64
  mvcc_max_retries: uint16
  max_dependency_edges: uint64
  tx_index_prefix_size: uint32
}
```

v1 数据面/控制面配置具有不可由治理提高的绝对上限：

```text
TRANSACTION_MAX_CANONICAL_BYTES_V1       = 16_777_216
SIGNER_POLICY_MAX_CANONICAL_BYTES_V1     = 1_048_576
ACCOUNT_SIGNERS_MAX_V1                   = 1_024
SIGNATURES_PER_TRANSACTION_MAX_V1        = 1_024
PREFILTER_WORK_RESERVE_PER_SPONSOR_MAX_V1 = 1_048_576
PREFILTER_WORK_PER_FINALIZED_BLOCK_MAX_V1 = 268_435_456
BATCH_BODY_MAX_CANONICAL_BYTES_V1        = 268_435_456
BATCH_TRANSACTIONS_MAX_V1                = 65_536
VERTEX_MAX_CANONICAL_BYTES_V1            = 16_777_216
WEAK_PARENTS_MAX_V1                      = 4_096
BATCH_REFERENCES_PER_VERTEX_MAX_V1       = 4_096
EXECUTION_ATTESTATIONS_PER_VERTEX_MAX_V1 = 1_024
EVIDENCE_REFS_PER_VERTEX_MAX_V1          = 1_024
UNREFERENCED_SIBLING_QUARANTINE_PER_SLOT_MAX_V1 = 4
UNREFERENCED_SIBLING_QUARANTINE_OBJECTS_PER_LEDGER_MAX_V1 = 65_536
UNREFERENCED_SIBLING_QUARANTINE_BYTES_PER_LEDGER_MAX_V1 = 67_108_864
DAG_EQUIVOCATION_EVIDENCE_MAX_CANONICAL_BYTES_V1 = 33_554_437
FUTURE_ROUND_GAP_MAX_V1                  = 1_048_576
FUTURE_NONCE_GAP_MAX_V1                  = 1_048_576
VALIDITY_WINDOW_HEIGHTS_MAX_V1            = 4_294_967_296
EPOCH_FINALIZED_BLOCKS_MAX_V1              = 65_536
EPOCH_EMITTED_VERTEX_ROLLOVER_TRIGGER_V1   = 4_194_304
PACEMAKER_TIMEOUT_MS_MIN_V1                = 10
PACEMAKER_TIMEOUT_MS_MAX_V1                = 300_000
SCHEDULE_LOOKAHEAD_ROUNDS_MAX_V1           = 1_048_576
DAG_GC_ROUNDS_MAX_V1                       = 1_048_576
BATCH_RETENTION_HEIGHTS_MAX_V1             = 16_777_216
EXECUTION_LAG_HEIGHTS_MAX_V1               = 1_048_576
TRANSACTIONS_PER_FINALIZED_BLOCK_MAX_V1    = 1_048_576
EXECUTION_GAS_PER_FINALIZED_BLOCK_MAX_V1   = 1_000_000_000_000
AUTHORIZED_ACCESS_ENTRIES_PER_TX_MAX_V1    = 1_048_576
AUTHORIZED_ACCESS_BYTES_PER_TX_MAX_V1      = 16_777_216
EXACT_OBSERVED_ACCESS_KEYS_PER_TX_MAX_V1   = 1_048_576
EXECUTION_PARALLELISM_MAX_V1               = 1_024
MVCC_VERSIONS_MAX_V1                       = 1_024
MVCC_BYTES_MAX_V1                          = 4_294_967_296
DEPENDENCY_EDGES_MAX_V1                    = 67_108_864
TX_INDEX_PREFIX_SIZE_MAX_V1                = 1_048_576
```

P2P、canonical decoder、Batch recovery/RS worker、DAG quarantine 和 config validator 必须引用同一组常量；不能各自维护更大的隐藏默认值。前三个 `UNREFERENCED_SIBLING_*` 上限只约束尚未被共识对象按精确 `VertexID` 引用的旁路 sibling gossip；对象一旦因依赖提升进入 dependency store，就改受因果闭包保留/GC 规则约束，绝不能因 quarantine 已满而判 wire-invalid。`DAG_EQUIVOCATION_EVIDENCE_MAX_CANONICAL_BYTES_V1` 是两个最大 Vertex 加固定 deterministic-CBOR 外壳的全局硬上限，即 `5 + 2 * VERTEX_MAX_CANONICAL_BYTES_V1`；对 active config 的更紧上限是 checked `5 + 2 * max_vertex_bytes`。达到上限的对象仍以流式/bounded reader、磁盘 spill 和有界 worker 处理；`MAX+1` 在按声明数组、shard、parent 或 transaction 大对象分配前拒绝。本地限制可以更早背压诚实生产或暂缓同步，但承载 Validator 角色的节点若资源不足以最终处理链上允许的绝对范围，readiness 必须失败，不能签署该 epoch 后再把合法对象永久判无效。

`proposer_slots_per_round` 默认 2，范围 `1..q`。`P=q` 只允许在显式实验 profile 中启用。配置和 ValidatorSet 只在 epoch 边界切换。

ProtocolConfig 验证拆成两个不可混淆的谓词。

`ValidateProtocolConfigStructure(config,set)` 不需要取得内容对象，用于 epoch trust-chain 的中间跳；它在计算/接受 config hash 前至少要求：

- `protocol_name == "FinalDAG-C"`、`protocol_version == 1`，两个内容 ID 都是完整 Hash32；
- `0 < max_transaction_bytes <= TRANSACTION_MAX_CANONICAL_BYTES_V1`，`0 < max_signer_policy_bytes <= min(max_transaction_bytes,SIGNER_POLICY_MAX_CANONICAL_BYTES_V1)`，`0 < max_signatures_per_transaction <= max_account_signers <= ACCOUNT_SIGNERS_MAX_V1`，signatures还不超过`SIGNATURES_PER_TRANSACTION_MAX_V1`；`0 < prefilter_verification_work_reserve_per_occurrence_sponsor <= PREFILTER_WORK_RESERVE_PER_SPONSOR_MAX_V1`，`0 < max_prefilter_verification_work_per_finalized_block <= PREFILTER_WORK_PER_FINALIZED_BLOCK_MAX_V1`，checked `n * per_sponsor_reserve <= total`，且per-sponsor reserve至少为第四篇冻结的`MaxSinglePrefilterVerificationWorkV1(config)`；该最大值故意按v1绝对253成员重配置模板计算而不依赖当前set大小；validity/future-nonce gap大于0且不超过各自绝对上限；
- `MaxSingleTxBatchCanonicalBytes(max_transaction_bytes) <= max_batch_body_bytes <= BATCH_BODY_MAX_CANONICAL_BYTES_V1`，`0 < max_batch_transactions <= BATCH_TRANSACTIONS_MAX_V1`；`MinRequiredVertexCanonicalBytes(set) <= max_vertex_bytes <= VERTEX_MAX_CANONICAL_BYTES_V1`，比较完整 signed envelope；`max_weak_parents`、`max_batches_per_vertex` 和 `max_future_round_gap` 分别不超过其 v1 绝对上限；`execution_attestations/evidence_refs` 没有治理配置，始终使用上述固定 count cap；
- `1 <= proposer_slots_per_round <= q`，`min(n,q+1) <= max_strong_parents <= n`，`max_weak_parents` 的加法不溢出对象上限；这个额外位置用于同时容纳上一轮及时 primary 与前一 primary 的 q 个兼容 certificate supporters；
- 两个 timeout 都在 `PACEMAKER_TIMEOUT_MS_MIN_V1..MAX_V1` 且 `certificate_timeout_ms >= primary_timeout_ms`；`0 < schedule_lookahead_rounds <= SCHEDULE_LOOKAHEAD_ROUNDS_MAX_V1`；`2 <= dag_gc_rounds <= DAG_GC_ROUNDS_MAX_V1`，使普通 own/weak gap 至少存在一个合法值；`0 < batch_retention_heights <= BATCH_RETENTION_HEIGHTS_MAX_V1`、`0 < max_execution_lag_heights <= EXECUTION_LAG_HEIGHTS_MAX_V1`，且 `batch_retention_heights > max_execution_lag_heights`；
- `0 < max_transactions_per_finalized_block <= TRANSACTIONS_PER_FINALIZED_BLOCK_MAX_V1`、`0 < max_execution_gas_per_finalized_block <= EXECUTION_GAS_PER_FINALIZED_BLOCK_MAX_V1`；access 条目/字节/observed-key cap 分别在对应 v1 absolute max 内，且 `max_authorized_access_bytes_per_tx <= max_transaction_bytes`；`0 < execution_parallelism <= EXECUTION_PARALLELISM_MAX_V1`；
- state component、per-tx/per-block write、event、return、call 与 FinalizedBlockBody cap 全部大于 0，满足[执行注册表规范](05-execution-registry-gas-and-resource-metering.md)的绝对上限、最小 mandatory-write reserve 和 checked 交叉约束；
- `2 <= mvcc_max_versions <= MVCC_VERSIONS_MAX_V1`，v1 的 `mvcc_max_retries == 1`，`0 < mvcc_max_bytes <= MVCC_BYTES_MAX_V1`，`0 < max_dependency_edges <= DEPENDENCY_EDGES_MAX_V1`；
- `0 < tx_index_prefix_size <= min(max_transactions_per_finalized_block,TX_INDEX_PREFIX_SIZE_MAX_V1)`；
- 所有乘法、加法、字节预算和从 `n` 推导 `f/q/k/parity` 的运算 checked，无窄化截断。

`ValidateExecutionConfigBundle(config,feature_set,gas_schedule,set)` 先调用结构谓词，再重算两个 network-scoped 内容 ID、逐字节匹配 config，检查 FeatureSet/GasSchedule 的项数与总字节上限，对所有 FeatureEntry `parameters_cbor` 执行登记的 typed validation，并要求 Gas entries 与当前 active operation registry 完全相等、所有必需系数为正。空 GasSchedule、缺 operation、额外/未激活 operation 或零成本绕过都无效。Genesis、EpochSeal 签署/新 epoch readiness、目标 FinalityProof 解释、snapshot 安装和该 epoch 区块执行都必须调用 bundle 谓词；中间 trust-chain 仅需结构谓词。全历史重放若缺任一 epoch 的完整 bundle 必须暂停获取，不能使用本地默认值。

FeatureSet 是 network-scoped 可复用对象，因此 bundle 谓词只执行与 Ledger 无关的 typed 校验。若激活 `CROSS_LEDGER_V1`，bundle 谓词还必须结合当前 GasSchedule、ValidatorSet与 `max_execution_gas_per_finalized_block` 验证 `n * max_single_cross_ledger_verification_gas` 可表示且不超过块 Gas cap，其中 `n=ValidatorSet.validators.length`，为全部 n个 occurrence sponsor分别保留proof-work份额；这里不得误用 proposer slot数P。每个把该 bundle 绑定到具体 Ledger 的入口还必须以已认证的 `(network_id,ledger_id,protocol_config)` 调用 `ValidateCrossLedgerParametersForLedger`：Genesis installation、EpochSeal authorization/readiness、新 epoch activation、目标 FinalityProof/CheckpointFinalityProof 解释、snapshot 安装与区块执行都不能省略。它验证 source/target 不自环、target-scoped policy IDs、proof + relayer-envelope overhead 可被同一 config的完整 target transaction表示，以及 contextual bounds；响应方自报 target context不能替代调用方已认证 Header/Genesis context。

不满足相应谓词的 Genesis/EpochSeal/目标执行配置无效。接收者不能用本地默认值补字段；本地资源限制可以更保守地背压/串行化，但不能把协议有效对象判为永久无效。

## 11. ProposerSlot 与决策状态

ProposerSlot 是派生语义，不单独签名：

```text
ProposerSlot {
  epoch: Epoch
  proposer_round: Round
  proposer_rank: uint16       // 0..P-1
  proposer_index: ValidatorIndex
}
```

slot 全局顺序：

```text
(proposer_round ASC, proposer_rank ASC)
```

状态只有：

```text
UNDECIDED
COMMIT(vertex_id)
SKIP
```

状态转换只能由[第三篇](03-finaldag-consensus.md)的 direct/indirect 规则产生。

## 12. Receipt、Event 与状态变化

```text
Event {
  emitter: byte_string
  topic: byte_string
  data: byte_string
}

StateChange {
  namespace: byte_string
  key_hash: Hash32
  previous_value_hash: Hash32
  new_value_hash: Hash32
}

ReceiptCore {
  tx_id: Hash32
  sender: AccountAddress
  nonce: Nonce
  block_height: Height
  transaction_index: uint32
  status: SUCCESS | FAILED
  error_code: uint32
  gas_used: uint64
  return_data_hash: Hash32
  event_root: Hash32
  state_change_root: Hash32
}
```

`ReceiptCore.error_code` 使用固定 `uint32` 注册表；名称不是 wire 值：

| 数值 | 名称 | 规范含义 |
|---:|---|---|
| `0` | `NONE` | 执行成功，无错误 |
| `1` | `ACCESS_SCOPE_VIOLATION` | 用户执行尝试访问授权 scope 外的 key 或 mode |
| `2` | `OUT_OF_GAS` | 签名 gas limit 已耗尽 |
| `3` | `ARITHMETIC_OVERFLOW` | 状态机要求失败而非 wrap 的算术溢出 |
| `4` | `DIVISION_BY_ZERO` | 除零 |
| `5` | `MEMORY_BOUNDS` | 确定性内存边界错误 |
| `6` | `INVALID_INSTRUCTION` | 当前 state-machine/feature 不允许的指令或入口 |
| `7` | `FEE_LIMIT_EXCEEDED`（v1 未启用） | 为未来完整 fee profile 预留；v1 Receipt 出现即无效 |
| `8` | `STATE_MACHINE_REJECTED` | 版本化状态机的通用确定性拒绝 |
| `9` | `CONTRACT_REVERT` | 用户程序显式回滚 |
| `10` | `STATE_LIMIT_EXCEEDED` | 单交易状态、事件或返回数据的协议上限被超过 |
| `11` | `INVALID_STATE_TRANSITION` | payload 请求不满足当前认证状态的业务前置条件 |

`7` 与 `12..65535` 在 v1 Receipt 中出现即无效；`65536..2147483647` 只能由与 `state_machine_version` 和 FeatureSet 一起冻结的状态机错误注册表分配，未登记值无效；`2147483648..UINT32_MAX` 保留且 v1 禁用。`status == SUCCESS` 当且仅当 `error_code == 0`；`status == FAILED` 当且仅当 error code 是当前配置下已登记的非零值。认证失败、nonce/高度过滤、`BLOCK_CAP` 与 `NONCE_EXHAUSTED` 不产生 Receipt；resolver 分叉、数据库损坏等本地致命错误进入 `EXECUTION_HALT`，也不能伪装成 Receipt。

只有通过 canonical/static、签名与块开始时账户认证检查，且 `nonce == next_nonce` 的 winner occurrence 产生 Receipt。业务失败仍产生 `FAILED` Receipt 并消费 nonce。认证无效、future、stale、duplicate、expired 和 nonce-conflict loser occurrence 均不进入 transaction tree，也不产生 Receipt。

```text
receipt_hash = DomainHash("RECEIPT", ..., canonical(ReceiptCore))
event_hash   = DomainHash("EVENT", ..., canonical(Event))
state_change_hash = DomainHash("STATE_CHANGE", ..., canonical(StateChange))
return_data_hash  = DomainHash("RETURN_DATA", ..., canonical(return_data_bytes))
```

空返回值也按零长度 byte string 计算 `return_data_hash`，不能用全零 Hash32 代替。Event/StateChange 的列表 root 按第 13.2、18 节计算。

## 13. FinalizedBlock

```text
TransactionResult {
  receipt: ReceiptCore
  events: [Event]
  state_changes: [StateChange]
}

FinalizedBlockBody {
  schema_version: uint16
  epoch: Epoch
  height: Height
  committed_slot: ProposerSlot
  proposer_vertex_id: Hash32
  ordered_vertex_count: uint64
  transactions: [TransactionEnvelope]
  results: [TransactionResult]
}

FinalizedBlockHeader {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  epoch: Epoch
  height: Height
  parent_block_id: Hash32
  committed_slot: ProposerSlot
  proposer_vertex_id: Hash32
  ordered_vertex_root: Hash32
  epoch_emitted_vertex_count: uint64
  epoch_emitted_vertex_set_root: Hash32
  transaction_root: Hash32
  receipt_root: Hash32
  event_root: Hash32
  state_root: Hash32
  parent_block_mmr_root: Hash32
  validator_set_hash: Hash32
  protocol_config_hash: Hash32
}
```

Body/Header 的 `schema_version` 都必须为 1。Body 的 `epoch`、`height`、`committed_slot`、`proposer_vertex_id` 必须分别与被验证 Header 的同名字段逐字节相等；这些重复字段不是响应方可自述的索引。`ordered_vertex_count` 必须大于 0，并精确等于已完整验证的 `CausalInputManifestCore.ordered_vertex_count`。Body 中 `transactions` 只包含 occurrence filter 的 winners，顺序与 tx_index 一致；`results[i]` 必须对应 `transactions[i]`。

`ordered_vertex_root` 从第 13.1 节 causal input stream 的 `VERTEX` items 增量重算；`epoch_emitted_vertex_count/epoch_emitted_vertex_set_root` 按第 13.3 节从同一组 VertexID 更新父块认证的 epoch 累计精确集合。transaction、receipt、event 和 state-change roots 从 canonical Body 重算，执行后 state root 也必须相等。任一重复元数据、计数、数组长度、root 或结果对应关系不匹配都拒绝整个 Body。Body 不包含 ordered VertexID 数组、raw occurrences、AC、FinalityCertificate 或其他 signer-subset envelope；完整因果输入由 manifest/chunks sidecar 保存，单块顺序由 count-bound `ordered_vertex_root` 认证，跨块 exactly-once 集合由 epoch emitted root/count 认证。

这个拆分是协议语义而非存储优化：`max_finalized_block_body_bytes` 只约束 `canonical(FinalizedBlockBody)`，任意大的有限 causal delta 由固定 1 MiB chunks 流式处理，不能因为把 ID 数组内联进 Body 而使一个已经 COMMIT 的 slot 变得不可表示。sidecar 不受 Body cap 不等于可以省略或截断；未完整消费并验证它就不能形成 Header、Body 或 execution attestation。

### 13.1 Canonical causal input stream

一个 COMMIT slot 的因果输入在语义上不得因本地内存、消息大小或数据库事务上限被截断。为允许超大 `Past(P)` 使用有界内存恢复和执行，v1 冻结以下可内容寻址、但不进入 FinalizedBlockID 的 staging/同步对象：

```text
CausalVertexItem {
  vertex_ordinal: uint64
  vertex_id: Hash32
}

CausalOccurrenceItem {
  vertex_ordinal: uint64
  availability_reference_index: uint32
  batch_id: Hash32
  batch_transaction_index: uint32
  transaction: TransactionEnvelope
}

CausalInputItem = VERTEX(CausalVertexItem) |
                  OCCURRENCE(CausalOccurrenceItem)

CausalInputChunkCore {
  schema_version: uint16
  chunk_index: uint64
  first_byte_offset: uint64
  payload: byte_string
}

CausalInputChunk {
  schema_version: uint16
  manifest_id: Hash32
  core: CausalInputChunkCore
  siblings_bottom_up: [Hash32]
}

CausalInputManifestCore {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  epoch: Epoch
  height: Height
  parent_block_id: Hash32
  parent_state_root: Hash32
  committed_slot: ProposerSlot
  proposer_vertex_id: Hash32
  validator_set_hash: Hash32
  protocol_config_hash: Hash32
  ordered_vertex_count: uint64
  occurrence_count: uint64
  item_count: uint64
  stream_byte_length: uint64
  chunk_payload_bytes: uint32
  chunk_count: uint64
  chunks_root: Hash32
}

CausalInputManifest {
  schema_version: uint16
  core: CausalInputManifestCore
}
```

`CausalInputItem` 使用第 4.1 节 tagged-union 编码，variant 顺序为 `VERTEX=0`、`OCCURRENCE=1`。规范 item 序列的生成规则是：按 `VertexDelta` 顺序为每个 Vertex 先写一个连续 ordinal 的 `VERTEX` item，再按该 Vertex 的 AvailabilityReference 数组顺序和 BatchBody transaction 顺序写全部 `OCCURRENCE` item。source index、BatchID 和交易 envelope 必须与已验证 Vertex/Batch 逐字节一致。

每个 occurrence 都派生一个不写入`CausalOccurrenceItem`本体的`sponsor_author_index`：它固定等于承载对应 `AvailabilityReference`、且已完成签名与 VertexID 验证的 `DAGVertex.core.author_index`；`batch_author_index` 则从该引用指向的已验证 `BatchHeaderCore.author_index` 取得。执行checkpoint中的`OccurrenceScanSourceBindingV1`以`containing_vertex_author_index`和`batch_author_index`同时承诺这两个来源，attempt/receipt统一使用`sponsor_author_index`扣款。二者可以不同，且都必须落在当前 ValidatorSet 的 `[0,n)`。sponsor 是选择把这份 Batch 引入该因果位置、因而对本次 raw occurrence 扫描与后续验证工作负责的签名主体；Batch author只证明Batch来源，不能替代 sponsor 归因。同一 BatchAC 被不同 Vertex 或同一作者的不同 Vertex再次引用时，每个 raw occurrence 都分别归到各自 containing Vertex sponsor，绝不能因为 BatchID相同而把费用转嫁给 Batch author。

为了让单个大 transaction 也不要求整项驻留内存，chunk 切分作用于规范 framed byte stream，而不是 item 数：

```text
causal_item_frame(item) = U64BE(len(canonical(item))) || canonical(item)
causal_byte_stream       = causal_item_frame(item_0) || ... || causal_item_frame(item_n-1)
```

长度是 canonical item 的精确字节数，使用 checked `uint64`；解码器必须先按 schema 和 ProtocolConfig 推导的对象上限检查长度，再用增量 canonical-CBOR decoder 消费恰好该长度，不能按恶意前缀预分配同等内存。item 可以跨任意多个 chunk，frame 长度前缀也可以跨 chunk；末尾残缺前缀、少字节、多字节或非规范 CBOR 都使 manifest 无效。

v1 常量 `CAUSAL_INPUT_CHUNK_PAYLOAD_BYTES_V1 = 1_048_576`。`chunk_payload_bytes` 必须等于该值；从 offset 0 开始按字节连续切分，除最后一块外每个 `payload` 恰好 1 MiB，最后一块为 `1..1_048_576` 字节，不得按本地 worker、item 或 Batch 边界重新切分。`item_count` 必须是 `ordered_vertex_count + occurrence_count` 的 checked `uint64` 和；`stream_byte_length` 必须等于全部 frame 长度的 checked 和；`chunk_count = ceil(stream_byte_length / 1_048_576)`。每个 chunk 的 `chunk_index` 从 0 连续，`first_byte_offset = chunk_index * 1_048_576`，全部 payload 长度和必须等于 `stream_byte_length`。COMMIT 至少含 proposer Vertex，因此 item stream 和 byte stream 都不为空。

先计算与 manifest 无关的 chunk 内容哈希，避免循环：

```text
causal_chunk_hash_i = DomainHash(
  "CAUSAL_INPUT_CHUNK", network_id, ledger_id,
  canonical(CausalInputChunkCore_i)
)

causal_chunk_leaf_i = DomainHash(
  "CAUSAL_INPUT_CHUNK_LEAF", network_id, ledger_id,
  U64BE(i) || causal_chunk_hash_i
)

causal_chunk_node = DomainHash(
  "CAUSAL_INPUT_CHUNK_NODE", network_id, ledger_id,
  left || right
)

causal_chunks_root = DomainHash(
  "CAUSAL_INPUT_CHUNK_ROOT", network_id, ledger_id,
  U64BE(chunk_count) || tree_top
)

causal_input_manifest_id = DomainHash(
  "CAUSAL_INPUT_MANIFEST", network_id, ledger_id,
  canonical(CausalInputManifestCore)
)
```

chunk tree 非空，奇数层末节点复制自身。每个 envelope 的 `siblings_bottom_up` 是该 `chunk_index` 到 manifest `chunks_root` 的 inclusion path：长度必须恰好等于把 `chunk_count` 反复上取整除以 2 直到 1 的层数，左右位置由逐层 index bit 推导，奇数复制层的 sibling 必须等于当前 hash，最大长度为 64；必须先验证该路径再把 payload 交给 framed decoder。`CausalInputManifest.schema_version` 和其 core 的版本都必须为 1。先得到全部 core hashes/root 和 manifest ID，最后才把 `manifest_id` 与路径装入 `CausalInputChunk` envelope；chunk envelope 的两个 schema version 必须为 1，且 `manifest_id` 必须等于已验证 manifest 的 ID，因此不存在 manifest/chunk 哈希环或跨 manifest 替换。按第 4 节编码时，含最大 payload 与 64 个 siblings 的 chunk envelope 上限固定为 `CAUSAL_INPUT_CHUNK_MAX_CANONICAL_BYTES_V1 = 1_050_823`。P2P v1 只传未压缩 canonical bytes；HTTP Gateway Content-Encoding 或未来版本的 transport 压缩必须经 streaming decompressor 和 running output counter：最多读取/产生 `MAX+1` bytes 用于判定越界，计数到第 `1_050_824` byte 时立即中止且不得把该 byte 交给 CBOR parser 或 staging。解析器在依据声明长度分配 payload、siblings 或其他大对象前必须先证明相应长度不超过硬上限；禁止先完整解压到内存再检查。

节点必须验证 manifest 的 slot、proposer、父块、父状态、ValidatorSet、配置和完整 DAG/Batch 来源，再顺序消费所有 chunks，增量重组 frame 并逐项验证上述计数与来源；本地上限只能触发磁盘 spill、背压、换 peer 或暂停，不能跳过 item、提前停止 scan、改变 `GloballyEmittedVertices` 或把协议有效输入永久判无效。ordered-vertex tree、occurrence filter 和 winner trees 可以边读边写叶哈希文件；输入结束后逐层顺序读取，每两个 hash 合并、奇数复制并写下一层，直到得到与第 18 节完全相同的 root。该算法内存为固定 chunk/decoder 窗口，不能用另一种“流式 Merkle”改变 root。

`CausalInputManifest` 是可重建的 staging/同步承诺，不替代 DAGCommitWitness、FinalizedBlockHeader 或 FinalityCertificate，也不单独产生最终性。实现必须把 manifest ID、最后已验证 chunk、临时 leaf files 和 occurrence-filter 状态放在 prepared namespace；只有完整 stream 验证和执行完成后才可形成 Header/attestation。

### 13.2 Body 与 Header roots

第 18 节通用有序树的具体 item 固定如下：

```text
ordered_vertex_item[i] = {
  vertex_id: causal_vertex_items[i].vertex_id
}
transaction_item[i]    = body.transactions[i]
receipt_item[i]        = body.results[i].receipt

per_tx_event_item[i,j] = body.results[i].events[j]
state_change_item[i,j] = body.results[i].state_changes[j]

block_event_item[g] = {
  transaction_index: i,
  event_index: j,
  event: body.results[i].events[j]
}
```

- `causal_vertex_items` 是已完整验证 causal input stream 中按出现顺序抽取的全部 `VERTEX` items，其长度必须精确等于 Body 和 manifest 的 `ordered_vertex_count`；
- `ordered_vertex_root` 使用 `ORDERED_VERTEX_ITEM/LEAF/NODE/ROOT`，列表 index 为 `i`，其 ROOT preimage 按第 18.1 节包含 `U64BE(ordered_vertex_count)`，因此 Header root 同时绑定顺序和计数；
- `transaction_root` 使用 `TX_ITEM/LEAF/NODE/ROOT`；
- `receipt_root` 使用 `RECEIPT/RECEIPT_LEAF/RECEIPT_NODE/RECEIPT_ROOT`；
- 每个 ReceiptCore 的 `event_root` 使用 `EVENT/EVENT_LEAF/EVENT_NODE/EVENT_ROOT`，局部 index 为 `j`；
- 每个 ReceiptCore 的 `state_change_root` 使用 `STATE_CHANGE/STATE_CHANGE_LEAF/STATE_CHANGE_NODE/STATE_CHANGE_ROOT`，局部 index 为 `j`；
- Header 的 `event_root` 先按 `(transaction_index ASC,event_index ASC)` 展平全部事件，再使用 `BLOCK_EVENT_ITEM/BLOCK_EVENT_LEAF/BLOCK_EVENT_NODE/BLOCK_EVENT_ROOT`，全局叶 index 为连续 `g`。

`ReceiptCore.transaction_index` 必须等于结果数组 index，`tx_id` 必须等于对应 transaction 的重算 ID。Event 数组保持 VM 的确定性发出顺序；一笔交易对同 key 多次写只输出最终 StateChange，数组按 `(namespace bytes,key_hash bytes)` 排序并拒绝重复。每个局部 root 必须与 ReceiptCore 相等，展平块 root 必须与 Header 相等。空列表使用各自 ROOT domain 的空树规则。实现不得把“per-tx roots 的列表”当作 Header event root，也不得按 topic/emitter 重排事件。

执行实现可以使用以下本地接口对象，但它不是共识 schema、没有协议 ID，且不得跨节点直接信任：

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
```

```text
finalized_block_id = DomainHash(
  "FINALIZED_BLOCK", network_id, ledger_id,
  canonical(FinalizedBlockHeader)
)
```

Header 只承诺父高度已经认证的 MMR root，不能包含“追加当前 FinalizedBlockID 后”的 root，否则 `FinalizedBlockID = Hash(Header)` 与 MMR 叶之间形成哈希自引用。执行节点在算出 ID 后再做：

```text
block_mmr_leaf = DomainHash(
  "BLOCK_MMR_LEAF", network_id, ledger_id,
  canonical({height, finalized_block_id})
)

block_mmr_state = Append(parent_block_mmr_state, height, finalized_block_id)
block_mmr_root  = BlockMMRRoot(block_mmr_state)
```

`Root(parent_block_mmr_state)` 必须等于 Header 的 `parent_block_mmr_root`。创世使用协议固定的空 MMR state/root。当前 `block_mmr_root` 进入下方 FinalityStatement，并成为下一高度 Header 的 `parent_block_mmr_root`。MMR state/peaks 是确定性派生辅助数据，不进入 FinalizedBlockID。

创世初始状态是 height 0，不是普通 FinalizedBlock。首个普通 FinalizedBlock 高度为 1；随后严格 `parent.height+1`。SKIP slot 不占高度。

### 13.3 EpochEmittedVertexSet

Header 中的累计集合是 epoch-scoped、精确的 256 层 Sparse Merkle Set，key 为 VertexID 原始 256 bits，路径从最高位到最低位。它不能用 Bloom filter、概率集合、排序数组 root 或本地数据库 checksum 替代。令 `e` 为 Header epoch：

```text
EVEmpty[e,256] = DomainHash(
  "EPOCH_EMITTED_VERTEX_LEAF", network_id, ledger_id,
  U64BE(e) || 0x00
)

EVLeaf[e,v] = DomainHash(
  "EPOCH_EMITTED_VERTEX_LEAF", network_id, ledger_id,
  U64BE(e) || 0x01 || v
)

EVNode[e,d,left,right] = DomainHash(
  "EPOCH_EMITTED_VERTEX_NODE", network_id, ledger_id,
  U64BE(e) || U16BE(d) || left || right
)

EVEmpty[e,d] = EVNode(e,d,EVEmpty[e,d+1],EVEmpty[e,d+1])
               for d = 255 down to 0

EpochEmittedVertexSetRoot(e,count,tree_top) = DomainHash(
  "EPOCH_EMITTED_VERTEX_ROOT", network_id, ledger_id,
  U64BE(e) || U64BE(count) || U16BE(256) || tree_top
)
```

空 epoch 的 count 为 0、tree top 为 `EVEmpty[e,0]`。验证一个 COMMIT 块时，先完整验证 causal input stream，再取其中所有 `VERTEX` item 的 VertexID 作为 `delta_ids`：

```text
base =
  parent Header 属于当前 epoch
    ? parent 的 durable authenticated set generation
    : EmptyEpochEmittedSet(current_epoch)

require delta_ids 内无重复
require 每个 ID 对应的完整 DAGVertex.epoch == current_epoch
require 每个 ID 在 base 中有可重建到 base root 的精确 non-membership path
new_set   = InsertAll(base, delta_ids)
new_count = checked_add(base.count, len(delta_ids))
require Header.epoch_emitted_vertex_count == new_count
require Header.epoch_emitted_vertex_set_root == Root(new_set)
```

`ordered_vertex_root` 认证本块 delta 的顺序，累计 set root 认证跨块 membership；二者用途不同且都必须验证。epoch 0 首块从 `EmptyEpochEmittedSet(0)` 开始；epoch `e>0` 的首块必须以已验证的上一 epoch seal 所指 final block 为父块，但 emitted set 从 `EmptyEpochEmittedSet(e)` 重新开始，不继承旧 epoch。每个 COMMIT 的 delta 非空，因此首块 count 必须大于 0。SKIP 不产生 Header，也不改变集合；Header 的 `committed_slot` 是本高度认证的派生恢复游标，最后一个 COMMIT 后仅由本地推导的 trailing SKIP 不得推进认证游标或 GC。

authenticated set 查询必须区分 `PRESENT`、`ABSENT` 与 `CORRUPT_OR_MISSING`。只有从完整 path 重算到认证 root 才能返回前两者；缺节点、checksum 错误或未激活 generation 绝不能被解释成 non-membership。

## 14. ExecutionAttestation 与 FinalityCertificate

```text
FinalityStatement {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  epoch: Epoch
  height: Height
  finalized_block_id: Hash32
  block_mmr_root: Hash32
  state_root: Hash32
  validator_set_hash: Hash32
  protocol_config_hash: Hash32
}

ExecutionAttestation {
  schema_version: uint16
  statement: FinalityStatement
  signer_index: ValidatorIndex
  signature: SignatureEd25519
}

FinalityCertificate {
  schema_version: uint16
  statement: FinalityStatement
  signer_bitmap: byte_string
  attestations: [ExecutionAttestation]
}
```

```text
finality_id = DomainHash(
  "FINALITY_STATEMENT", network_id, ledger_id,
  canonical(FinalityStatement)
)

finality_certificate_envelope_hash = DomainHash(
  "FINALITY_CERT_ENVELOPE", network_id, ledger_id,
  canonical(FinalityCertificate)
)
```

ExecutionAttestation 签名摘要：

```text
DomainHash(
  "EXECUTION_ATTESTATION", network_id, ledger_id,
  canonical(FinalityStatement)
)
```

FinalityCertificate 恰好含 `q` 个不同 signer，按 ValidatorIndex 排序。`finality_id` 不含 signer subset；不同合法 signer subset 可以产生不同 `finality_certificate_envelope_hash`。该 envelope hash 只能在完整证书验证后用于传输去重、存储校验和审计，不能作为 FinalizedBlockID、父引用、MMR leaf、epoch seed 或查询结果身份。

## 15. FinalityProof 与 DAGCommitWitness

以 Genesis 为根的基础最终性证明固定为：

```text
EpochTransitionProof {
  schema_version: uint16
  seal_certificate: EpochSealCertificate
  next_validator_set: ValidatorSet
  next_protocol_config: ProtocolConfig
}

ValidatorSetProof {
  schema_version: uint16
  genesis_certificate: GenesisCertificate
  genesis_governance_policy: GovernancePolicy
  genesis_validator_set: ValidatorSet
  genesis_protocol_config: ProtocolConfig
  transitions: [EpochTransitionProof]
  target_feature_set: FeatureSet
  target_gas_schedule: GasSchedule
}

FinalityProof {
  schema_version: uint16
  finalized_block_header: FinalizedBlockHeader
  finality_certificate: FinalityCertificate
  validator_set_proof: ValidatorSetProof
  merkle_proofs: [MerkleProof]
}
```

`merkle_proofs` 按 `(tree_kind,index,item_hash)` 排序并拒绝重复；每份 proof 的预期 root 必须从同一 Header、Receipt 或被该 Header 认证的对象取得，不能由响应方另行提供未绑定 root。

验证者在带外配置中持有期望的 network、ledger 与 `genesis_reference`。验证从完整参数的 `ValidateGenesisCertificate` 开始；该入口同时验证 approvals/policy、epoch-0 ValidatorSet/ProtocolConfig hash、协议派生 `epoch0_seed` 与空 MMR root。不得仅因 reference 匹配就跳过这些检查；proof 不含 state manifest，因此 state root 仍明确依赖带外 reference，而不是伪称已重建 Genesis。随后对 old epoch `e` 的每一项 transition，调用只依赖 wire 对象的 `ValidateEpochSealStatementIntrinsic` 重算 next set/config hash 与 `next_epoch_seed`，再使用“当前 ValidatorSet”的 Consensus Key 验证 seal 的 `q` 个签名，最后把下一组 set/config 切换为当前集合。旧 signer 使用的 closed record/governance action 属于更强 authorization 谓词，不是公共 proof 可伪造的输入。transitions 必须从 old epoch 0 连续递增且最后得到 FinalizedBlockHeader 的目标 epoch；无 transition 表示目标 epoch 0。最后调用 `ValidateExecutionConfigBundle(target_protocol_config,target_feature_set,target_gas_schedule,target_validator_set)`。

FeatureSet/GasSchedule 是按内容哈希寻址的执行配置对象，不必在每个过渡证明中重复内嵌：验证 seal 信任链只需要被 seal 绑定的完整 next ProtocolConfig；解释目标 Header 需要本 proof 的完整 target 两对象；从 Genesis 重放所有区块则必须另行按每个已认证 ProtocolConfig 的 hash 流式取得并验证各 epoch 两对象。缺少中间执行配置时可以验证目标最终性，但不能声称具备全历史重放能力。只携带目标 ValidatorSet 而缺少中间公钥集合仍无法验证多跳签名。基础 transitions 数组允许增量 canonical-CBOR 解码与已验证前缀缓存，不能用目标 epoch 的本地字节上限把一个密码学有效的历史链判无效；API 应提供分段获取。

### 15.1 独立 checkpoint 信任根与证明

基础 `FinalityProof` 永远以带外固定的 `genesis_reference` 为根。需要从运营方明确批准的较新 checkpoint 起步时，v1 使用下列独立 Schema，不能把 checkpoint 填进 `ValidatorSetProof.genesis_*` 字段：

```text
CheckpointTrustAnchor {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  epoch: Epoch
  height: Height
  finalized_block_id: Hash32
  state_root: Hash32
  block_mmr_root: Hash32
  block_mmr_peaks: [MMRPeak]
  validator_set_hash: Hash32
  protocol_config_hash: Hash32
  validator_set: ValidatorSet
  protocol_config: ProtocolConfig
  feature_set: FeatureSet
  gas_schedule: GasSchedule
}

CheckpointFinalityProof {
  schema_version: uint16
  anchor_id: Hash32
  anchor: CheckpointTrustAnchor
  transitions: [EpochTransitionProof]
  target_feature_set: FeatureSet
  target_gas_schedule: GasSchedule
  finalized_block_header: FinalizedBlockHeader
  finality_certificate: FinalityCertificate
  merkle_proofs: [MerkleProof]
}
```

所有新增 schema version 必须为 1。Checkpoint 内容 ID 为：

```text
checkpoint_anchor_id = DomainHash(
  "CHECKPOINT_TRUST_ANCHOR", anchor.network_id, anchor.ledger_id,
  canonical(CheckpointTrustAnchor)
)
```

`VerifyCheckpointFinalityProof(expected_anchor_id,proof)` 必须按以下顺序执行：

1. 从本地只读 trust store 取得运营方预置的 `expected_anchor_id`；重算 `proof.anchor` 的 ID，并要求它同时等于 `proof.anchor_id` 和该带外期望值。只与 proof 内自报 ID 相等没有任何信任意义；API、peer、snapshot 或多数响应都不得新增或替换 trust anchor。
2. 要求 anchor 的 network/ledger 等于调用方期望，`height>=1`；调用 `ValidateValidatorSet(anchor.validator_set,anchor.epoch,anchor.network_id)`、`ValidateProtocolConfigStructure` 和 `ValidateExecutionConfigBundle`，以真实 `(network_id,ledger_id)` 重算 set/config hash 并逐字节匹配。以 `MMRState{leaf_count:anchor.height,peaks:anchor.block_mmr_peaks}` 验证 peaks 规范且重算 `block_mmr_root`；anchor 中的 block/state 身份除此之外属于运营方显式接受的新信任假设，不由嵌入对象自证。
3. 令当前 epoch、`ValidatorSet` 和 `ProtocolConfig` 分别取 anchor 对应值。`transitions` 必须从 `old_epoch=anchor.epoch` 连续递增；每跳先以当前旧集合验证恰好 `q` 个 EpochSeal signer、核对 next set/config hash并验证 next 对象，再切换当前集合。第一跳 seal 的 `final_height` 不得小于 anchor height；若相等，其 final block/state/MMR root 必须与 anchor 完全相同。
4. 零 transition 只允许目标 Header 位于 anchor epoch；否则最后一跳必须恰好得到目标 epoch。目标高度不得小于 anchor height；若高度相等，Header ID、state root、FinalityStatement MMR root 和 validator/config hash 必须与 anchor 完全相同。
5. 将当前 set/config 与 `target_feature_set/target_gas_schedule` 组成目标执行配置 bundle，重算内容 ID 并调用 `ValidateExecutionConfigBundle`。目标 Header 和 FinalityStatement 中的 validator/config hash 必须分别等于当前 set/config hash；随后按基础证明相同规则验证 Header ID、FinalityStatement、恰好 `q` 个当前 Consensus Key 签名，以及排序唯一的 `merkle_proofs`。

Checkpoint anchor 是运营方通过治理、离线审核或受控部署明确引入的新信任根，不是由链上响应自动推导的“更短证明”。接受它会放弃独立验证 anchor 之前历史的能力，但不会改变 anchor 之后的 `n/f/q`、EpochSeal、执行或最终性规则。trust store 更新必须是单独的授权操作并记录旧/new anchor ID、批准者和生效时间；普通同步和查询代码只能读取，不能写入。

`FinalityProof` 与 `CheckpointFinalityProof` 是两个具名 Schema 和两个验证入口；解码器不得按字段形状猜测、把一个包装成另一个或在失败时静默降级。交易/Receipt 的 `MerkleProof` 可以位于相应证明的 `merkle_proofs`；`SparseMerkleProof` 与 `BlockMMRProof` 仍是查询 evidence 在证明对象之外携带的独立结构，以其 Header/Statement root 验证。

基础与 checkpoint 两种最终性证明都不要求携带 DAGCommitWitness。验证者通过相应的本地信任根和后续 EpochSeal 验证 ValidatorSet，再检查 FinalityCertificate 与所需 Merkle proof。

```text
DAGCommitWitness {
  schema_version: uint16
  target_slots: [ProposerSlot]
  proposal_vertices: [DAGVertex]
  support_vertices: [DAGVertex]
  decision_vertices: [DAGVertex]
  indirect_anchors: [DAGVertex]
  dependency_vertices: [DAGVertex]
}
```

DAGCommitWitness 只用于全节点同步、审计、争议分析和模型测试，不属于轻客户端 FinalityProof 的必选字段，也不进入 FinalizedBlockID。

## 16. TransactionStatusEvidence

```text
TransactionAuthorizationContextV1 {
  schema_version: uint16
  candidate_height: Height
  parent_anchor_kind: TRUST_ROOT_STATE | FINALIZED_PARENT
  parent_finality_proof: optional FinalityProof
  candidate_validator_set: ValidatorSet
  candidate_protocol_config: ProtocolConfig
  candidate_feature_set: FeatureSet
  candidate_gas_schedule: GasSchedule
  activation_transition: optional EpochTransitionProof
  account_metadata_proof: SparseMerkleProof
  account_auth_proof: SparseMerkleProof
  account_nonce_proof: SparseMerkleProof
}

CheckpointTransactionAuthorizationContextV1 {
  schema_version: uint16
  candidate_height: Height
  parent_anchor_kind: TRUST_ROOT_STATE | FINALIZED_PARENT
  parent_finality_proof: optional CheckpointFinalityProof
  candidate_validator_set: ValidatorSet
  candidate_protocol_config: ProtocolConfig
  candidate_feature_set: FeatureSet
  candidate_gas_schedule: GasSchedule
  activation_transition: optional EpochTransitionProof
  account_metadata_proof: SparseMerkleProof
  account_auth_proof: SparseMerkleProof
  account_nonce_proof: SparseMerkleProof
}

TransactionStatusEvidence {
  schema_version: uint16
  queried_tx_id: Hash32
  queried_transaction: TransactionEnvelope
  queried_receipt: optional ReceiptCore
  status: FINALIZED_SUCCESS | FINALIZED_FAILED | REPLACED | EXPIRED
  finality_proof: FinalityProof
  queried_transaction_proof: optional MerkleProof
  queried_receipt_proof: optional MerkleProof
  replacement_transaction: optional TransactionEnvelope
  replacement_receipt: optional ReceiptCore
  replacement_transaction_proof: optional MerkleProof
  replacement_receipt_proof: optional MerkleProof
  queried_authorization_context: optional TransactionAuthorizationContextV1
  account_nonce_proof: optional SparseMerkleProof
}

CheckpointTransactionStatusEvidence {
  schema_version: uint16
  queried_tx_id: Hash32
  queried_transaction: TransactionEnvelope
  queried_receipt: optional ReceiptCore
  status: FINALIZED_SUCCESS | FINALIZED_FAILED | REPLACED | EXPIRED
  checkpoint_finality_proof: CheckpointFinalityProof
  queried_transaction_proof: optional MerkleProof
  queried_receipt_proof: optional MerkleProof
  replacement_transaction: optional TransactionEnvelope
  replacement_receipt: optional ReceiptCore
  replacement_transaction_proof: optional MerkleProof
  replacement_receipt_proof: optional MerkleProof
  queried_authorization_context: optional CheckpointTransactionAuthorizationContextV1
  account_nonce_proof: optional SparseMerkleProof
}
```

两者是字段 presence matrix 相同、但 authorization context 内父证明类型不同的两个具名 v1 wire schema；不能包装、按字段形状猜测或在验证失败后互相 fallback。基础 schema 只能交给 `VerifyTransactionStatusEvidence(expected_genesis_reference,evidence)`，checkpoint schema 只能交给 `VerifyCheckpointTransactionStatusEvidence(expected_anchor_id,evidence)`。下文以 `selected_finality_proof` 表示终态 proof，以 `selected_authorization_context` 表示相应具名 context；context 的父证明与终态 proof 必须通过同一个本地 trust root 验证入口，不能一份走 Genesis、一份走 checkpoint。

两个 authorization context 各自按代码块顺序使用 deterministic-CBOR integer key `1..12`；两个 status evidence 各自使用 key `1..14`。`schema_version=1`，`parent_anchor_kind` 固定编码为 `TRUST_ROOT_STATE=1`、`FINALIZED_PARENT=2`；未知 enum、未知/缺失/重复字段、错误 optional presence、非最短整数或尾随字节一律拒绝。基础与 checkpoint context 即使除 proof 类型外字段相同也不能互相包装或共享未经类型标记的 cache bytes。

共同验证规则：

- 每个具名 MerkleProof 必须逐字段等于 `selected_finality_proof.merkle_proofs` 中恰好一个对应项；具名字段用于确定用途，不建立第二套 root；
- 所有状态在解释 status 前都必须对 `queried_transaction` 运行与状态无关的完整 intrinsic validation：确定性 CBOR/schema/必需字段与 v1 绝对对象 cap 有效；Intent 的 network/ledger 逐字节等于调用方期望和 selected proof Header；重算 tx ID 等于 `queried_tx_id`；完整 SignerPolicy 规范有效且其 hash 等于 Envelope 承诺；sender、`nonce < UINT64_MAX`、`valid_from_height <= valid_until_height` 和 checked 字段运算有效。FINALIZED 状态的完整静态/账户验证由已认证 inclusion与对应执行配置复核；REPLACED/EXPIRED 必须使用下述 candidate context，不能拿终态配置代替历史配置；
- 对 `REPLACED/EXPIRED`，`queried_authorization_context` 必须存在；对两个 FINALIZED status 必须缺失，因为链上 inclusion 已证明 occurrence filter 当时完成账户授权。`candidate_height` 必须满足 `valid_from_height <= candidate_height <= valid_until_height` 且 `candidate_height <= selected terminal Header.height`。授权状态永远是 candidate 执行前的父状态：parent height 精确等于 `candidate_height-1`，绝不能用 candidate 同高度执行后的 Header.state_root；
- `parent_anchor_kind=TRUST_ROOT_STATE` 时，父证明必须缺失。基础 schema 只允许 `candidate_height=1`，父状态 root取已由 `expected_genesis_reference` 认证的 GenesisStatement.genesis_state_root；checkpoint schema 只允许 `candidate_height=expected_anchor.height+1`，父状态 root取本地预置 anchor.state_root。`parent_anchor_kind=FINALIZED_PARENT` 时父证明必须存在、与终态 proof同 network/ledger和同 trust-root类型，目标 Header.height精确等于 `candidate_height-1`，且不低于 checkpoint anchor；其 Header.state_root是三份账户 proof的唯一 root；
- candidate epoch只能等于父 context epoch或恰好加一。若相同，`activation_transition` 必须缺失，candidate ValidatorSet/ProtocolConfig hashes逐字节等于父 Header或trust-root anchor，完整 FeatureSet/GasSchedule匹配其 config并通过 `ValidateExecutionConfigBundle`；同时 selected terminal proof 的已认证 transition chain不得包含以该父 height/block为final old-epoch边界的 seal。若链中存在该边界，H+1不可能仍属旧epoch，same-epoch context必须拒绝。若candidate epoch加一，`activation_transition`必须存在且逐字段等于selected terminal proof chain中唯一对应跳：old epoch/final height/final block精确绑定父context，使用父集合验证seal，next set/config逐字节等于candidate对象，完整Feature/Gas匹配并通过intrinsic transition与bundle谓词。Genesis candidate固定epoch 0且无transition。这样跨epoch首块的规则来自已认证next bundle，而不是终态节点臆测；
- 验证器必须在 `candidate_height` 与刚认证的 candidate bundle 下重跑完整 `CanonicalAndStaticValid(queried_transaction,context)`：validity-window长度、fee/gas、SignerPolicy结构、authorized access、payload/feature registry、typed参数和全部协议/绝对资源cap均须通过，不能只验证签名与状态proof。对`CROSS_LEDGER_CONSUME_V1`，这里执行bounded outer parse、target context/policy/relayer/envelope overhead/RequiredGas与声明字段等target静态前缀；source FinalityProof密码学、consumed-state和动态窗口仍属于真实occurrence执行验证，authorization context不伪称它们已成功；
- context 的三份 SparseMerkleProof 分别绑定固定 meta/auth/nonce namespace和queried sender raw key，并验证到上述父状态 root。普通账户要求三项都present，解码为同address的完整 `AccountMetadataState/AccountAuthState/AccountNonceState`；按 `candidate_height` 解析出的 active policy hash必须等于queried Envelope policy hash并验证signatures，且 `next_nonce <= queried.nonce < UINT64_MAX`。`max_future_nonce_gap`只控制本地admission资源，绝不能进入终态证据：较远nonce可能在同一candidate block被更早canonical winners推进后成为winner；
- 账户创建要求父状态三项都non-inclusion，并按candidate bundle重新执行 `ACCOUNT_CREATE_V1` 地址core/initial policy/creation salt/nonce=0/空user scope/三个EXACT WRITE/Gas静态规则。普通不存在账户、残缺三元组、candidate开始尚未激活的policy或`next_nonce > queried.nonce`都不能生成终态evidence。同块刚创建后才出现的普通交易、或同块较早winner已推进nonce后的post-state都不能被误用，因为proof root固定在candidate父状态；
- 上述授权 context只说明queried Envelope在某个candidate开始时由sender真实授权且具备成为canonical occurrence的状态前提，不声称其曾进mempool/Batch，也不要求nonce落入本地future gap。服务必须选择窗口内、candidate配置接受该Envelope的context；父状态、transition或中间Feature/Gas已裁剪且Archive不可得时返回`PROOF_UNAVAILABLE/HISTORY_PRUNED`，不能降级为自签Envelope或使用同高post-state；
- `FINALIZED_SUCCESS`：queried transaction/Receipt 及两份 proof 必须存在，Receipt 为 `SUCCESS`；replacement、authorization 与 account 字段必须缺失；
- `FINALIZED_FAILED`：queried transaction/Receipt 及两份 proof 必须存在，Receipt 为 `FAILED`；replacement、authorization 与 account 字段必须缺失；
- `REPLACED`：queried Receipt及queried transaction/receipt inclusion proofs必须缺失，authorization context、replacement transaction、nonce-consuming Receipt及两份inclusion proof必须存在。replacement还必须通过其终态Header已认证执行配置的完整静态Envelope验证，并证明不同tx_id、相同sender/nonce；终态`account_nonce_proof`缺失。来自另一network/ledger的queried或replacement Envelope即使sender/nonce字节相同也拒绝；
- `EXPIRED`：除queried transaction、该schema必填的终态proof、authorization context和终态account nonce proof外的optional字段必须缺失；最终tip高度严格大于`valid_until_height`。终态nonce SMT proof必须绑定固定nonce namespace与queried sender raw key：present时解码`AccountNonceState{schema_version:1}`并要求`next_nonce <= queried nonce`；non-inclusion只对authorization context已经用父状态三项non-inclusion证明为合法账户创建的queried Envelope成立，普通不存在账户交易不能借此取得EXPIRED；
- 若 `next_nonce > queried nonce`，不得返回 EXPIRED，必须提供原交易最终证明或 replacement winner 证明；
- `UNKNOWN` 与 `PENDING` 是本地观察，不携带不可回退证据。

协议不维护历史 tx-id seen、intent seen 或 nonce winner 状态。REPLACED 完全由 winner 交易和 Receipt 证明。

完整 evidence 的传输/缓存 ID 为：

```text
status_evidence_id = DomainHash(
  "TRANSACTION_STATUS_EVIDENCE", network_id, ledger_id,
  canonical(TransactionStatusEvidence)
)

checkpoint_status_evidence_id = DomainHash(
  "CHECKPOINT_TRANSACTION_STATUS_EVIDENCE", network_id, ledger_id,
  canonical(CheckpointTransactionStatusEvidence)
)
```

两种 ID 和缓存 namespace 不得互换。它们都不是新的账本终态来源；验证者仍必须逐层验证内含的对应具名最终性证明、Merkle/SMT proofs 和状态专属 presence matrix。服务端选择哪一种必须由客户端请求的本地 trust-root 类型决定，响应本身没有安装或切换信任根的权限。

## 17. EpochSeal

```text
EpochSealStatement {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  old_epoch: Epoch
  final_height: Height
  final_block_id: Hash32
  final_state_root: Hash32
  final_block_mmr_root: Hash32
  next_validator_set_hash: Hash32
  next_protocol_config_hash: Hash32
  next_epoch_seed: Hash32
}

EpochSealVote {
  schema_version: uint16
  statement: EpochSealStatement
  signer_index: ValidatorIndex
  signature: SignatureEd25519
}

EpochSealCertificate {
  schema_version: uint16
  statement: EpochSealStatement
  signer_bitmap: byte_string
  votes: [EpochSealVote]
}
```

```text
epoch_seal_id = DomainHash(
  "EPOCH_SEAL_STATEMENT", network_id, ledger_id,
  canonical(EpochSealStatement)
)

epoch_seal_envelope_hash = DomainHash(
  "EPOCH_SEAL_ENVELOPE", network_id, ledger_id,
  canonical(EpochSealCertificate)
)
```

语义 ID `epoch_seal_id` 不含 signer subset。证书恰好含旧 ValidatorSet 的 `q` 个 Consensus Key 签名；不同合法 signer subset 可以产生不同 `epoch_seal_envelope_hash`。envelope hash 只能在完整证书验证后用于传输去重、存储校验和审计，不进入 next epoch seed、transition 身份或任何共识选择。

## 18. Merkle Tree

有序列表树：

```text
MerkleTreeKind =
  ORDERED_VERTEX | TRANSACTION | RECEIPT |
  PER_TX_EVENT | BLOCK_EVENT | STATE_CHANGE

MerkleProof {
  schema_version: uint16
  tree_kind: MerkleTreeKind
  index: uint64
  count: uint64
  item_hash: Hash32
  siblings_bottom_up: [Hash32]
}
```

tree kind 唯一映射到本节注册的 ITEM/LEAF/NODE/ROOT domain；证明携带 item hash，具体查询对象必须在 proof 外按相应 item schema 重算并与之相等。

```text
item_hash_i = DomainHash(ITEM_DOMAIN, ..., canonical(item_i))
leaf_i = DomainHash(LEAF_DOMAIN, ..., U64BE(i) || item_hash_i)
node = DomainHash(NODE_DOMAIN, ..., left || right)
root = DomainHash(ROOT_DOMAIN, ..., U64BE(count) || tree_top)
```

- 空树使用对应 ROOT domain 对 `U64BE(0)` 哈希；
- 奇数层最后节点复制自身；
- inclusion proof 要求 `0 <= index < count`；path 每层都携带一个 sibling，按叶层到根排序，左右方向由该层 index bit 推导；奇数复制层也必须携带与当前 hash 相同的 sibling；
- path 长度必须恰好等于把 `count` 反复上取整除以 2 直到 1 的层数，`count=1` 时为 0；
- transaction、receipt、event 和 ordered vertex 使用各自 domain；fragment 额外绑定 coding context，采用第 8.3 节的专用叶/root 公式，不能把传输 envelope 当 item。

### 18.1 Sparse Merkle Tree

```text
key_hash = DomainHash(
  "STATE_KEY", ...,
  canonical({namespace, key})
)
absent_value_hash = DomainHash(
  "STATE_VALUE", ...,
  canonical({presence: 0})
)

value_hash(value) = DomainHash(
  "STATE_VALUE", ...,
  canonical({presence: 1, value})
)

present_leaf(key_hash,value_hash) = DomainHash(
  "STATE_LEAF", ..., 0x01 || key_hash || value_hash
)

empty[256] = DomainHash("STATE_LEAF", ..., 0x00)

empty[d] = DomainHash(
  "STATE_NODE", ...,
  U16BE(d) || empty[d+1] || empty[d+1]
) for d = 255 down to 0

node(d,left,right) = DomainHash(
  "STATE_NODE", ...,
  U16BE(d) || left || right
)

state_root = DomainHash(
  "STATE_ROOT", ...,
  canonical({tree_depth: 256, tree_top: root_node})
)
```

树深固定 256，`key_hash` 从最高位到最低位选择 depth `0..255` 的左右分支。存在叶必须使用 `0x01` 前缀；空叶只使用 `0x00`，因此合法的空 byte-string value 仍是“存在”而非删除。创建/删除时，StateChange 的缺失一侧使用 `absent_value_hash`；SMT 本身的删除则把该位置恢复为 `empty[256]` 并向上重算。数据库 tombstone、版本号和压缩路径都只是本地表示，不进入共识哈希。

```text
SparseMerkleProof {
  schema_version: uint16
  namespace: byte_string
  key: byte_string
  value: optional byte_string
  siblings_top_down: [Hash32]  // 必须恰好 256 项
}
```

`siblings_top_down[d]` 是 depth `d` 节点中目标 child 的兄弟，索引必须覆盖 `0..255`。存在 proof 从 `present_leaf` 开始，缺失 proof 从 `empty[256]` 开始，对 `d=255..0` 逆序读取 sibling，按 `key_hash` 第 d bit 放置左右 child，重建带 depth 的 node，最后套 `STATE_ROOT`。空状态的 `root_node=empty[0]`。证明必须绑定原始 namespace/key；不同原始 key 若产生同一 key_hash，节点进入 `SAFETY_HALT`，不能任选一个值。账户 meta、nonce 与 auth 的权威 key 位于三个协议固定系统 namespace 下；同一地址三项全有或全无，分别承诺 immutable metadata/initial policy、严格单调的 `next_nonce` 和版本化认证状态。

### 18.2 FinalizedBlock MMR

普通 FinalizedBlock 高度从 1 开始，创世状态不作为叶，因此 `leaf_count == finalized_height`。MMR state 是按从左到右顺序排列的 peaks：

```text
MMRPeak {
  level: uint16
  hash: Hash32
}

MMRState {
  leaf_count: uint64
  peaks: [MMRPeak]
}
```

对高度 `h` 的 FinalizedBlock：

```text
leaf = DomainHash(
  "BLOCK_MMR_LEAF", network_id, ledger_id,
  canonical({height: h, finalized_block_id})
)
```

追加算法固定为二进制进位：

```text
Append(state, h, finalized_block_id):
  require h == state.leaf_count + 1
  carry = Peak(level=0, hash=leaf)

  while state.peaks is not empty and last(state.peaks).level == carry.level:
    left = pop_last(state.peaks)
    carry = Peak(
      level = carry.level + 1,
      hash = DomainHash(
        "BLOCK_MMR_NODE", network_id, ledger_id,
        canonical({level: carry.level + 1, left: left.hash, right: carry.hash})
      )
    )

  append carry to state.peaks
  state.leaf_count = h
```

peaks 始终按覆盖区间从左到右排列；由上述算法可唯一确定，解码时拒绝重复/非规范 level 组合。root 是：

```text
BlockMMRRoot(state) = DomainHash(
  "BLOCK_MMR_ROOT", network_id, ledger_id,
  canonical({leaf_count: state.leaf_count, peaks: state.peaks})
)
```

空 MMR root 使用 `leaf_count=0, peaks=[]` 的同一公式。Header 的 `parent_block_mmr_root` 只认证父 state；root 本身不足以执行 append，节点还需取得 parent peaks，并先用上述公式验证其 root。追加当前 FinalizedBlockID 后得到的 root 必须等于 FinalityStatement 的 `block_mmr_root`。

```text
BlockMMRProof {
  schema_version: uint16
  leaf_height: Height
  finalized_block_id: Hash32
  target_peak_level: uint16
  mountain_siblings: [{level, side: LEFT | RIGHT, hash}]
  mmr_leaf_count: uint64
  other_peaks: [MMRPeak]
}
```

验证者从叶开始按 `mountain_siblings` 的 level 和方向重建目标 peak，把它与 `other_peaks` 按覆盖区间插回，检查所得 peaks 的 level/leaf-count 组合规范，再重算 `BLOCK_MMR_ROOT`。证明不得省略 leaf height、总 leaf count、方向或 peak level；否则同一 sibling 序列可能被解释到不同位置。

### 18.3 Snapshot v1

Snapshot 是对某个已获 FinalityCertificate 的完整逻辑状态做的可重建传输对象，不信任导出节点的数据库格式、压缩格式或索引。v1 wire schema 固定为：

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

所有 schema version 必须为 1。v1 仅接受 `state_encoding=1`（`SMT_RECORD_CBOR_FRAMED_V1`）、`compression=0`（`NONE`）和 `SNAPSHOT_CHUNK_PAYLOAD_BYTES_V1 = 1_048_576`。含最大 payload 与 64 个 siblings 的 `SNAPSHOT_CHUNK_MAX_CANONICAL_BYTES_V1 = 1_050_823`。P2P v1 只传未压缩 envelope；HTTP Gateway Content-Encoding 或未来版本可以压缩，但接收端必须经 streaming decompressor 和 running output counter 还原：最多读取/产生 `MAX+1` bytes 用于判定越界，到第 `1_050_824` byte 立即中止且不交给 parser/staging；在依据声明长度分配 payload、siblings 或其他大对象前先验证长度。禁止先完整解压到内存再检查。传输压缩算法、文件容器、数据库 SST 或平台 endianness 都不进入 snapshot 身份。

导出者取得目标 certified publication 的单一只读数据库 snapshot，枚举其中全部“存在”的逻辑 SMT 记录。每条记录按第 18.1 节重算 `key_hash`，再按 `key_hash` 原始 32 字节严格升序输出；相同 key_hash（包括重复的同一 namespace/key）必须拒绝，不能覆盖或任选一项。空 byte-string 是存在值，tombstone、MVCC version、缓存、索引和物理压缩节点不得输出。

记录字节流固定为：

```text
snapshot_record_frame(record) =
  U64BE(len(canonical(record))) || canonical(record)

snapshot_byte_stream =
  snapshot_record_frame(record_0) || ... || snapshot_record_frame(record_n-1)
```

长度与总和全部使用 checked `uint64`。解析器首先强制协议绝对上限 `SNAPSHOT_STATE_RECORD_MAX_CANONICAL_BYTES_V1 = 17_891_328`，再用有界增量 canonical-CBOR decoder 消费恰好该长度；不得按未验证长度预分配。active ProtocolConfig 的 namespace/key/value cap 只限制该 epoch 的新写入，治理下调后不能用它拒绝旧状态中仍低于 v1 绝对上限的认证记录；精确兼容规则见[执行注册表规范](05-execution-registry-gas-and-resource-metering.md)。frame 和它的八字节长度前缀都允许跨 chunk。对非空 stream，从 offset 0 按字节切成固定 1 MiB payload，除最后一块外必须恰好 1 MiB；`first_byte_offset = chunk_index * 1_048_576`，chunk index 必须从 0 连续。空状态固定为 `record_count=0`、`stream_byte_length=0`、`chunk_count=0` 且无 chunk；其他情况下 `chunk_count=ceil(stream_byte_length/1_048_576)`。记录计数、总字节数、chunk 数或尾部 framing 不精确匹配都使 snapshot 无效。

chunk core 先独立哈希并构造有序树：

```text
snapshot_chunk_hash_i = DomainHash(
  "SNAPSHOT_CHUNK", network_id, ledger_id,
  canonical(SnapshotChunkCore_i)
)

snapshot_chunk_leaf_i = DomainHash(
  "SNAPSHOT_CHUNK_LEAF", network_id, ledger_id,
  U64BE(i) || snapshot_chunk_hash_i
)

snapshot_chunk_node = DomainHash(
  "SNAPSHOT_CHUNK_NODE", network_id, ledger_id,
  left || right
)

snapshot_chunks_root =
  if chunk_count == 0:
    DomainHash(
      "SNAPSHOT_CHUNK_ROOT", network_id, ledger_id,
      U64BE(0)
    )
  else:
    DomainHash(
      "SNAPSHOT_CHUNK_ROOT", network_id, ledger_id,
      U64BE(chunk_count) || tree_top
    )

snapshot_manifest_id = DomainHash(
  "SNAPSHOT_MANIFEST", network_id, ledger_id,
  canonical(SnapshotManifestCore)
)
```

非空 chunk tree 的奇数层末节点复制自身；空树 root 只哈希 `U64BE(0)`。非空 SnapshotChunk 必须携带与 causal chunk 相同规则的 `siblings_bottom_up`，路径长度由 manifest `chunk_count` 唯一决定且不超过 64，左右位置由 core `chunk_index` 推导；接收者据此在写入 staging 前独立验证每个 chunk 对 manifest root 的 inclusion。先由 core hashes 得到 root 和 manifest ID，再把 ID 与路径装入每个 `SnapshotChunk` envelope，因此无哈希环。chunk envelope 的 `manifest_id` 必须逐字节等于已验证 manifest ID，不能跨 manifest 拼接；任何 bitmap、FinalityCertificate signer subset 或 snapshot 发布者签名都不进入 manifest ID。

导入者必须按本地 trust-root 类型验证与 manifest 目标完全一致的基础 `FinalityProof` 或独立 `CheckpointFinalityProof`，不能由响应选择类型或在失败后 fallback。所选 proof 的 Header/statement network、ledger、epoch、height、FinalizedBlockID、finality ID、state root、block MMR root、epoch emitted Vertex count/root、ValidatorSet hash 和 ProtocolConfig hash 必须逐项匹配，且 `target_height >= 1`。`target_block_mmr_peaks` 必须满足第 18.2 节的规范 level/覆盖组合，并以 `leaf_count=target_height` 重算出 `target_block_mmr_root`；仅接受 root 而无 peaks 的快照不能用于继续追加后续块。随后验证全部 chunk core hashes、tree root、连续 offset、framing、record 数和 key-hash 严格顺序，从空 SMT 按第 18.1 节重建全部 present leaves，并要求结果精确等于 `target_state_root`。缺记录、多记录、改值、重复 key、错误排序或错误 chunk 都会被拒绝。Snapshot 自身不授予最终性；即使来自多数 peer，也不能替代匹配的、由本地 trust store 选定的 proof。

### 18.4 DAGDerivationCheckpoint v1

Snapshot 只传输状态，不能单独恢复第 13.3 节的累计 exact set。全功能 Validator 在当前 epoch 的中途恢复时，必须另外取得与同一认证 Header 精确匹配的 `DAGDerivationCheckpoint`。它是 Header 已认证 set root 的可分块 preimage，不是新的共识证书，也不能替代 `DAGCommitWitness`：

```text
DAGDerivationCheckpointChunkCore {
  schema_version: uint16
  chunk_index: uint64
  first_byte_offset: uint64
  payload: byte_string
}

DAGDerivationCheckpointChunk {
  schema_version: uint16
  manifest_id: Hash32
  core: DAGDerivationCheckpointChunkCore
  siblings_bottom_up: [Hash32]
}

DAGDerivationCheckpointManifestCore {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  target_epoch: Epoch
  target_height: Height
  target_finalized_block_id: Hash32
  target_finality_id: Hash32
  target_committed_slot: ProposerSlot
  emitted_vertex_encoding: uint16
  epoch_emitted_vertex_count: uint64
  epoch_emitted_vertex_set_root: Hash32
  stream_byte_length: uint64
  chunk_payload_bytes: uint32
  chunk_count: uint64
  chunks_root: Hash32
}

DAGDerivationCheckpointManifest {
  schema_version: uint16
  core: DAGDerivationCheckpointManifestCore
}
```

所有 schema version 必须为 1。v1 只接受 `emitted_vertex_encoding=1`（`SORTED_HASH32_CONCAT_V1`）和 `DAG_DERIVATION_CHECKPOINT_CHUNK_PAYLOAD_BYTES_V1=1_048_576`：将全部 VertexID 按原始 32 bytes 严格升序、无重复地直接连接。`stream_byte_length` 必须由 checked `count*32` 得到；除末块外 payload 恰好 1 MiB，每块长度必须是 32 的倍数，index/offset 从零连续，`chunk_count=ceil(stream_byte_length/1_048_576)`。chunk envelope 的协议绝对上限同样为 `DAG_DERIVATION_CHECKPOINT_CHUNK_MAX_CANONICAL_BYTES_V1=1_050_823`；P2P v1 只接受 raw canonical bytes，HTTP Gateway 或未来压缩 profile 遵循 Snapshot 的 streaming `MAX+1` 解压边界。

```text
checkpoint_chunk_hash_i = DomainHash(
  "DAG_DERIVATION_CHECKPOINT_CHUNK", network_id, ledger_id,
  canonical(DAGDerivationCheckpointChunkCore_i)
)

checkpoint_chunk_leaf_i = DomainHash(
  "DAG_DERIVATION_CHECKPOINT_CHUNK_LEAF", network_id, ledger_id,
  U64BE(i) || checkpoint_chunk_hash_i
)

checkpoint_chunk_node = DomainHash(
  "DAG_DERIVATION_CHECKPOINT_CHUNK_NODE", network_id, ledger_id,
  left || right
)

checkpoint_chunks_root =
  if chunk_count == 0:
    DomainHash(
      "DAG_DERIVATION_CHECKPOINT_CHUNK_ROOT", network_id, ledger_id,
      U64BE(0)
    )
  else:
    DomainHash(
      "DAG_DERIVATION_CHECKPOINT_CHUNK_ROOT", network_id, ledger_id,
      U64BE(chunk_count) || tree_top
    )

dag_derivation_checkpoint_manifest_id = DomainHash(
  "DAG_DERIVATION_CHECKPOINT_MANIFEST", network_id, ledger_id,
  canonical(DAGDerivationCheckpointManifestCore)
)
```

非空 chunk tree 的奇数层复制末节点，proof 路径、最大 64 层、左右位置和 envelope 组装规则与 Snapshot 完全相同。验证器必须先验证调用方预先选定的 `FinalityProof` 或 `CheckpointFinalityProof`，再要求 manifest 的 network、ledger、epoch、height、block ID、finality ID、committed slot、emitted count/root 逐字段等于认证 Header/statement；随后验证 checked 计数、所有 chunk inclusion path、连续 offset 和严格升序 ID 流，从当前 epoch 的空 sparse set 批量构建并重算第 13.3 节 root/count。少一个、多一个、重复、乱序、错 epoch 或 root 不符都拒绝，只有全部完成并 fsync 后才能激活该 derivation generation。

禁止把 checkpoint manifest ID 写入 Header：manifest 含 `target_finalized_block_id`，反向引用会形成 Header → FinalizedBlockID → manifest → Header 的哈希环。正确时序是先验证 delta 并 fsync 本地 exact-set generation，再签 Header/attestation，得到 FinalityCertificate 后异步导出 wire checkpoint。当前 epoch 的 Validator 快速恢复必须把同一 target 的状态 Snapshot、DAG checkpoint、MMR peaks 和 `certified_resume_cursor=Header.committed_slot` 原子安装；只有状态 Snapshot 的节点可以只读查询，但保持 `SYNCING_DERIVATION`，不得签 ACK、Vertex、ExecutionAttestation 或 EpochSealVote。若目标是旧 epoch final block且随后验证了 next EpochSeal，新 epoch derivation state 从空集合开始，不继承旧 checkpoint。

## 19. 证书通用验证

BatchAC、FinalityCertificate 和 EpochSealCertificate 都必须：

1. 先验证 network、ledger、epoch 和 statement；
2. 从已认证 ValidatorSet 得到 `n/f/q`；
3. 要求 bitmap 恰好 `ceil(n/8)` bytes；ValidatorIndex `i` 对应 `bitmap[i/8]` 的低位起第 `i mod 8` bit，最后 byte 未使用的高 bits 必须为零；
4. 要求恰好 `q` 个 bit；
5. 要求签名数组与 bitmap 一一对应并按 index 升序；
6. 拒绝重复 signer；
7. 对 BatchAC，检查每个 ACK 的 `fragment_index == signer_index`；
8. 验证每个签名使用正确 key 和 domain；
9. 按 statement 语义 ID 聚合缓存，而不是按 envelope hash 锁定。

证书 envelope 可以有不同合法 signer subset，但不得导致不同 FinalizedBlock、epoch seed、DAG parent 或状态结果。

## 20. 分层验证管线

接收对象必须依次执行：

1. framing 和资源上限；
2. deterministic CBOR；
3. schema、network、ledger、epoch；
4. 重算对象 ID；
5. key usage 与签名；
6. 父对象、BatchAC 或 FinalityCertificate 依赖；
7. round、slot、height 和状态机语义；
8. WAL 防双签或单调性检查；
9. 才能进入可投票、可执行或可对外证明状态。

未知依赖与未被引用的同槽 sibling 使用不同 lane：前者进入有独立保留容量的 dependency fetch/store，后者只能进入受 `UNREFERENCED_SIBLING_*` 三重硬上限约束的旁路 quarantine。quarantine 驱逐只丢本地 cache，不改变对象的 wire-validity；以后出现精确 `VertexID` 依赖时必须重新按需拉取并提升。结构或签名错误立即拒绝；可能意味着本机安全状态冲突的错误必须进入 `SAFETY_HALT`。

## 21. 测试向量

协议仓库必须为以下对象维护正反向向量：

- DomainHash 前缀、长度和大小端；
- deterministic CBOR；
- FeatureSet 的 typed `parameters_cbor` 正例、非规范/尾随/未知 schema、单项/总字节边界，以及同一 feature_id 多版本并存拒绝；GasSchedule 项数/总字节和 checked gas 边界；
- ValidatorSet 的 expected network/epoch、4/253 边界、非法人数、ValidatorID 重算/排序、从 strict Peer public key 重算固定 32-byte PeerID、重复 PeerID，以及 DAG/Consensus/Peer key 在全集合任何同角色或跨角色复用拒绝；
- PeerHello 的双向 TLS 1.3 leaf raw Ed25519 key、固定 ALPN `finalweave-p2p/1`、`PEER_ID`、本连接 TLS exporter、network、精确 `[version=1,compression=NONE]`、frame effective-min 和 `PEER_HELLO` strict signature；跨连接/恢复会话重放、错 exporter、错 ALPN、缺少任一方向证书、文本/multihash PeerID，以及 leaf/hello/descriptor key 任一错配均拒绝；
- GenesisCertificate policy/approval/reference/checked-weight 验证，以及 Genesis 每账户 meta/auth/nonce 全量三元组；`ACCOUNT_CREATE_V1` 的地址/policy/nonce/空 scope/system access/gas、同块 created set 和三项原子写；
- TransactionIntent 全字段覆盖、AccountKeyID、SignerPolicy、threshold/signature 负例与 TransactionID；v1 对 `fee_limit`/`priority_class` 仅接受 0，1 和各类型 MaxUint 等非零边界均为 `STATIC_INVALID`；
- `MaxSingleTxBatchCanonicalBytes(max_transaction_bytes)` 的 5-byte canonical wrapper golden、最大单交易完整 BatchBody 恰好命中边界的正例，以及 `max_batch_body_bytes` 少 1 时 ProtocolConfig 结构无效；
- BatchID、fragment tree、由 `n` 唯一导出的精确 path 层数/奇数复制、错误声明 shard/path 在分配前拒绝、DA_ACK 和不同 signer subset 的同一 `ac_id`；
- `AC_ENVELOPE`、`FINALITY_CERT_ENVELOPE`、`EPOCH_SEAL_ENVELOPE` 对完整 canonical certificate 的固定哈希，以及 signer subset 改变时语义 ID 不变、envelope hash 改变；
- DAGVertexID 和 parent 排序；每个合法 ValidatorSet 的 `MinRequiredVertexCanonicalBytes(set)` golden、完整 signed q+1-parent 控制 Vertex 恰好命中边界的正例，以及 `max_vertex_bytes` 少 1 时 ProtocolConfig 结构无效；unreferenced sibling 三重 hard cap 的 MAX/MAX+1，以及 `DAGEquivocationEvidence` active `5+2*max_vertex_bytes`/全局 `33_554_437` bytes边界；
- FinalizedBlockID、父 MMR root、追加当前 ID 后的 `block_mmr_root` 与 MMR proof；
- 不同 signer subset 的同一 `finality_id`；
- EpochSeal 签署/readiness 对完整 next FeatureSet/GasSchedule 缺失、错配或 typed parameter 篡改的拒绝向量；FinalityProof 对 target FeatureSet/GasSchedule 缺失/错配的拒绝；全历史 replay 缺中间 epoch bundle 时暂停获取但不推翻已验证目标最终性；
- `CheckpointTrustAnchor` 的 canonical bytes/ID/MMR peaks/bundle 正反向量；`CheckpointFinalityProof` 对错或未预置 anchor ID、自报 anchor 替换、首跳 epoch/高度不连续、等高根不匹配、混入 Genesis 字段、目标 bundle/证书/包含证明错误的拒绝向量；
- 所有 Merkle 树空值、奇数叶和 proof；
- causal item variant/framing、长度前缀和单个 item 跨多个 1 MiB chunk；chunk proof、缺失、重排、重复、尾部追加、截断、manifest ID 错配，以及所有新增 causal domain 的固定哈希；P2P v1 原始 canonical envelope 恰好 `MAX` 可进入解码、第 `MAX+1` byte 在分配与 staging 前拒绝，HTTP Gateway Content-Encoding/未来版本另验证流式解压输出的同一边界；
- Snapshot 空状态/单记录/多 chunk、record frame 跨 chunk、严格 key-hash 顺序、MMR peaks、chunk tree/proof/manifest ID，以及缺失、重复、改值、乱序、错误 target proof 和所有 snapshot domain 的拒绝向量；P2P v1 原始 canonical envelope 恰好 `MAX` 可进入解码、第 `MAX+1` byte 在分配与 staging 前拒绝，HTTP Gateway Content-Encoding/未来版本另验证流式解压输出的同一边界；
- future/stale/expired/nonce-exhausted occurrence，以及“早期 future 的同 tx_id 较后成为 winner”、“winner 后重复不再入树”和 `UINT64_MAX-1` 到耗尽哨兵；
- 每个 v1 `ReceiptErrorCode` 的 SUCCESS/FAILED 配对，以及保留、未登记和状态不匹配 code 的拒绝向量；
- REPLACED 与 EXPIRED 终态证明；
- 基础与 checkpoint 两种 TransactionStatusEvidence 的完整 schema/presence matrix、独立 domain/ID/验证入口，以及错误 proof 类型、错误预置 anchor、包装和 fallback 的拒绝向量。

任何 domain 或 schema 变更都必须让旧向量显式失败，禁止静默接受两种编码。
