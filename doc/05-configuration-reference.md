# FinalWeave 配置规范

> 状态：规范性配置草案  
> 原则：本地配置决定节点如何运行；最终链上配置决定所有验证者必须如何解释协议

## 1. 三类配置

### 1.1 Local Node Config

只影响本地实现：路径、监听地址、KMS URI、worker、缓存、连接/流量上限、日志/指标、snapshot 目标和本地保留策略。本地配置不得改变 quorum、slot、排序、round-jump、执行语义或证明。

### 1.2 Genesis Config

创建 Ledger 时冻结初始 validator set、治理、FinalDAG-C/BatchAC/执行协议参数、编码/哈希/签名、状态机、初始状态和限制。LedgerID 由不含签名 envelope 的 `LedgerGenesisCore` 派生；离线治理签名形成以该 ID 为输入的 GenesisCertificate。精确次序见[数据模型与密码学规范](protocol/01-data-model-and-cryptography.md)。

### 1.3 On-chain Protocol Config

治理交易更新 validator set、协议参数、状态机版本和 feature activation；只能在 epoch 边界激活。同 epoch 不允许动态切换共识算法或安全关键规则。

## 2. 优先级与拒绝策略

```text
本地非共识项：flag > environment > node.yaml > safe default
共识项：finalized on-chain config > genesis
```

本地值只能更保守，不能放宽链上最大值。冲突时 Validator readiness 必须失败，并输出字段、local value、required value、epoch 和 config hash；不得静默选择。

## 3. 本地文件解析

- YAML，`configVersion` 必填；
- 未知字段、重复 key、隐式类型和无单位 duration 一律拒绝；
- bytes 使用非负整数；ID 使用固定长度 hex；地址使用结构化 multiaddr；
- secret 只存 URI，不允许内联私钥；
- 环境变量前缀 `FINALWEAVE_`，嵌套字段用双下划线；
- 解析后输出脱敏、规范化 effective config 和摘要供审计。

## 4. 本地配置示例

```yaml
configVersion: 1

node:
  name: validator-a
  mode: validator
  organizationId: "<32-byte-hex>"
  shutdownTimeout: 30s
  maxLedgers: 16

identity:
  peerKeyUri: "pkcs11:token=finalweave;object=peer-a"
  p2pTlsCertFile: /etc/finalweave/p2p-peer.crt # leaf SPKI 必须对应 peerKeyUri
  apiTlsCertFile: /etc/finalweave/api.crt
  apiTlsKeyUri: "pkcs11:token=finalweave;object=api-tls-a"
  apiTrustBundleFile: /etc/finalweave/api-clients.pem

p2p:
  listen:
    - /ip4/0.0.0.0/udp/2401/quic-v1
    - /ip4/0.0.0.0/tcp/2401
  bootstrapPeers: []
  maxConnections: 512
  maxConnectionsPerPeer: 4
  alpn: "finalweave-p2p/1"        # v1 固定值
  mutualCertificateRequired: true # v1 必须为 true
  handshakeTimeout: 5s
  idleTimeout: 2m
  maxFrameBytes: 16777216
  queues:
    dagControlBytes: 134217728
    availabilityBytes: 1073741824
    syncBytes: 536870912
    queryBytes: 268435456

api:
  grpcListen: 0.0.0.0:2402
  httpListen: 0.0.0.0:2403
  adminListen: 127.0.0.1:2410
  tlsEnabled: true
  mtlsRequired: true
  maxRequestBytes: 2097152
  maxHttpWireBytes: 3145728
  maxInclusionProofBytes: 8388608
  defaultDeadline: 10s

storage:
  engine: pebble
  dataDir: /var/lib/finalweave/data
  safetyWalDir: /var/lib/finalweave/safety-wal
  syncFinalizedWrites: true
  syncSafetyWal: true
  softFreeBytes: 53687091200
  stopAdmissionFreeBytes: 32212254720
  stopSigningFreeBytes: 10737418240
  blockCacheBytes: 4294967296
  maxOpenFiles: 8192

runtime:
  scheduler:
    maxWorkers: 64
    dagReservedWorkers: 8
    availabilityWorkers: 16
    executionWorkers: 32
    queryMaxWorkers: 8
  memory:
    maxBytes: 68719476736

batching:
  maxBatchAge: 50ms

ledgers:
  - ledgerId: "<32-byte-hex>"
    genesisFile: /etc/finalweave/ledger-a-genesis.cbor
    trust:
      mode: genesis
      expectedGenesisReference: "<32-byte-hex>"
    dagKeyUri: "pkcs11:token=finalweave;object=ledger-a-dag"
    consensusKeyUri: "pkcs11:token=finalweave;object=ledger-a-consensus"
    enabled: true
    resources:
      cpuWeight: 100
      maxMempoolBytes: 536870912
      maxDagBytes: 4294967296
      maxAvailabilityBytes: 8589934592
      maxQueryConcurrency: 128
      maxSyncBytesPerSecond: 209715200
    backlogBackpressure:
      undecidedSlotDepthLow: 1024
      undecidedSlotDepthHigh: 2048
      verifiedUnemittedDagBytesLow: 1073741824
      verifiedUnemittedDagBytesHigh: 2147483648
      retainedReferenceableBatchBytesLow: 4294967296
      retainedReferenceableBatchBytesHigh: 6442450944
      controlDagBytesLow: 268435456
      controlDagBytesHigh: 536870912
      reservedControlBytes: 1073741824
    localHistoryPolicy:
      snapshotIntervalHeights: 10000
      finalizedBodyRetentionHeights: 100000
      receiptEventRetentionHeights: 100000
      dagWitnessRetentionHeights: 100000
      archiveRedundancyTarget: 2
      archiveLocators:
        - "https://archive-a.internal.example/finalweave"
        - "https://archive-b.internal.example/finalweave"

execution:
  mode: hybrid
  maxParallelWorkers: 32
  exactAccessGraphEnabled: true
  optimisticMvccEnabled: true
  maxSpeculationsPerTx: 1
  maxAuthoritativeReexecutionsPerTx: 1
  serialFallbackOnPressure: true
  verifyAgainstSerialSampleRatio: 0.001

sync:
  mode: snapshot
  maxParallelPeers: 4
  maxParallelChunks: 16
  requestTimeout: 10s

observability:
  logLevel: info
  redactPayloads: true
  metricsListen: 127.0.0.1:9090
  tracingEndpoint: https://otel.internal.example
```

`maxSpeculationsPerTx` 和 `maxAuthoritativeReexecutionsPerTx` 在 v1 只能为 1；写入配置是为了显式校验和观测，而不是开放调优。`execution.mode=serial` 是安全恢复/诊断模式，输出必须与 `hybrid` 完全相同。

每个 Ledger 的 trust 配置只能选择一种模式。`genesis` 要求 `expectedGenesisReference` 与 `genesisFile` 重算结果一致，并只接受基础 `FinalityProof`。`checkpoint` 模式不把 checkpoint 塞进 `genesisFile`，而是要求 `expectedCheckpointAnchorId` 和经离线审批保存的 canonical `CheckpointTrustAnchor` 文件；两者以 `CHECKPOINT_TRUST_ANCHOR` domain 重算后必须一致，并只接受独立 `CheckpointFinalityProof`。这组字段不允许普通热更新；安装/轮换 checkpoint 必须走独立授权、原子 trust-store 切换和审计流程，P2P/API 响应不能写入它。

`api.maxInclusionProofBytes` 是本地单个 Merkle/SMT/MMR/status 响应预算，不进入 ProtocolConfig，也不得把密码学有效对象判为协议无效。超过时 API 使用受认证分页/stream 或返回资源错误；从 Genesis 开始的 epoch transition chain 由专用增量流和已验证 checkpoint 缓存承载，不受这个单响应上限截断。

`api.maxRequestBytes` 计量传输解压后、业务解码前的二进制 RPC message；启动时必须满足 `maxRequestBytes >= active.max_transaction_bytes + 65_536`，其中固定余量覆盖 SubmitTransaction framing、最长 128-byte idempotency key 和版本化元数据。HTTP/JSON 的 `maxHttpWireBytes` 计原始 wire bytes，必须覆盖 base64/JSON 膨胀；网关解码后仍执行二进制上限。不能把恰好达到协议 `max_transaction_bytes` 的合法 TransactionEnvelope 因外层开销误拒绝。

P2P v1 的本地身份和 transport 配置不是可自由协商项：`p2p.alpn` 必须精确为 `finalweave-p2p/1`，`mutualCertificateRequired` 必须为 `true`，`maxFrameBytes` 必须落在 `65_536..16_777_216`。启动时必须从 `p2pTlsCertFile` 的 leaf SPKI 提取 strict raw Ed25519 key，证明它与 `peerKeyUri` 的公钥一致；承载任一 Validator Ledger 时，还必须与该 Ledger 当前或即将激活 ValidatorDescriptor 的 `peer_public_key`/重算 `peer_id` 一致。任一错配都必须在开始监听或启用签名前使 readiness 失败。API TLS certificate/key/trust bundle 是独立身份，不得复用 Peer key，也不得因 API 证书轮换改变 PeerID。

## 5. Secret URI 与签名授权

支持示例：

```text
file:///run/secrets/test-key
pkcs11:token=finalweave;object=validator-a
aws-kms://region/key-id
gcp-kms://project/location/key-ring/key
vault://path/to/signing-key
```

文件 key 仅限开发。DAG signer 只允许 `BATCH_HEADER`、`DA_ACK`、`DAG_VERTEX` 等明确域；Consensus signer 只允许 ExecutionAttestation/epoch seal 域。即使同一 DAG key handle 承担 Batch、ACK 和 Vertex，不同结构化 intent 仍分别执行 slot、持久化和单轮唯一性检查。日志只输出 provider 和脱敏 key ID。

## 6. 资源与网络配置规则

| 字段 | 动态 | 规则 |
|---|---:|---|
| worker/concurrency | 是 | 可下调；必须保留 DAG/attestation worker |
| cache bytes | 是 | 受进程上限；缩容不能删除安全状态 |
| P2P queue/rate | 是 | 不能使控制消息永久饥饿 |
| data/safety WAL path | 否 | 迁移需离线验证和原子切换 |
| signer URI | 否 | 轮换需 fencing、WAL 和链上身份匹配 |
| local retention | 是 | snapshot/历史服务窗口是非共识 `LocalHistoryPolicy`；不得早于 ProtocolConfig 的 `dag_gc_rounds`、`batch_retention_heights` 或协议永久保留材料 |
| log/trace sampling | 是 | payload 脱敏不可关闭 |

`stopSigningFreeBytes < stopAdmissionFreeBytes < softFreeBytes` 必须成立。达到 stop-signing 水位时撤销 Validator readiness；不得继续产生无法持久化的 ACK、顶点或 attestation。

每个 production signer URI解析后还必须通过组织级KMS inventory challenge：provider不可变key-version identity与attested/exported SPKI fingerprint都要匹配本Ledger已认证公钥，并以`(network_id,ledger_id,role,semantic_key_id)`原子占用。不同URI字符串、alias或provider handle若解析到同一物理key version仍是复用并拒绝；相同公钥迁移到经证明承载同一secret的另一handle也不能假装成独立key。inventory服务不可达、快照版本回退、fencing lease丢失或发现跨network/ledger/role冲突时，Validator readiness失败；该本地安全检查不会改变协议对象或validator quorum。

### 6.1 每 Ledger backlog 背压

`ledgers[].backlogBackpressure` 是协议篇 `BacklogBackpressurePolicyV1` 的 camelCase 本地投影，生产 Validator 必填；上例是 4–7 Validator、8 MiB Batch 级别压测的起点，不是跨硬件默认承诺。五组值使用 uint64 bytes/count，解析时必须执行：四个 counter 的 `0 < low < high`、`reservedControlBytes > 0`、checked arithmetic 无溢出，以及下列 capacity challenge：

```text
maxDagBytes >=
  verifiedUnemittedDagBytesHigh +
  controlDagBytesHigh +
  reservedControlBytes +
  MaxDAGRecoveryWorkingSet(active_config, validator_set)

maxAvailabilityBytes >=
  retainedReferenceableBatchBytesHigh +
  MaxBatchRepairWorkingSet(active_config, validator_set)

reservedControlBytes >=
  MaxControlCompletionWorkingSet(active_config, validator_set)
```

三个 working-set 函数由实现版本冻结并公开逐项报告，至少覆盖一个最大 Vertex、`q` 个父依赖、一个最大 Batch repair、正在流式处理的最大 execution candidate、attestation/FC、closing/seal 对象、WAL frame、compaction/snapshot 不可复用空间；不得用物理压缩比或“通常消息较小”降低。全局 `memory.maxBytes`、磁盘 stop-signing 水位和实际剩余空间还必须容纳所有 enabled Ledger 的 challenge 总和。任一条件不满足，Validator readiness 失败而不是悄悄抬高/降低阈值。

policy 的 canonical local object、effective-config digest 与四个 checked counter/mode 必须在启用 production signer 前 durable；每次对象写入、认证 emitted-set 激活、Batch GC 或 policy 热更新以原子 counter delta + checksum提交。启动时从权威 manifests/GC records 重算并比对；错配进入只读 `BACKPRESSURE_STATE_CORRUPT`，不能把 counter 清零。任一 counter 到 high 或 execution lag 到链上上限进入 `PAYLOAD_BACKPRESSURE`；全部降到 low 以下才恢复。control counter 到 high或 reserve不足进入 `CONTROL_STORAGE_PAUSE`，停止新 DAGVertex/ACK/Batch签名但保留转发、repair、执行、独立 finality、已落 fence 的 close/seal和查询；不得删除 Safety WAL或把远端有效对象判无效。

热更新只允许在新 policy 对当前 counters 仍有定义且重新通过 capacity challenge 后原子切换。降低 high 到当前值以下会立即进入相应 pause；降低 reserve 前必须先证明新 reserve 足够。policy 差异不改变 wire validity、canonical order或其他节点的阈值。

## 7. Genesis / 链上协议配置示例

以下是可读表示；真正参与哈希的是规范 CBOR：

示例固定 `n=4`，四个 descriptor 的占位符表示已经按真实 ValidatorID 原始字节严格升序排列，且所有 DAG/Consensus/Peer key 在全集合跨角色两两不同；每个 PeerID 都从同项 Peer public key 与 network ID 重算。字母名称本身不参与排序或编码。

```yaml
schema_version: 1
network_id: "<32-byte-hex>"
ledger_nonce: "<fresh-32-byte-hex>"

initial_validator_set:
  schema_version: 1
  epoch: 0
  validators:
    - validator_id: "<validator-a-derived-32-byte-hex>"
      identity_nonce: "<validator-a-stable-32-byte-hex>"
      dag_public_key: "<validator-a-ed25519-dag-pubkey>"
      consensus_public_key: "<validator-a-ed25519-consensus-pubkey>"
      peer_public_key: "<validator-a-ed25519-peer-pubkey>"
      peer_id: "<validator-a-derived-32-byte-hex>"
      organization_id: "<organization-a-id-bytes>"
    - validator_id: "<validator-b-derived-32-byte-hex>"
      identity_nonce: "<validator-b-stable-32-byte-hex>"
      dag_public_key: "<validator-b-ed25519-dag-pubkey>"
      consensus_public_key: "<validator-b-ed25519-consensus-pubkey>"
      peer_public_key: "<validator-b-ed25519-peer-pubkey>"
      peer_id: "<validator-b-derived-32-byte-hex>"
      organization_id: "<organization-b-id-bytes>"
    - validator_id: "<validator-c-derived-32-byte-hex>"
      identity_nonce: "<validator-c-stable-32-byte-hex>"
      dag_public_key: "<validator-c-ed25519-dag-pubkey>"
      consensus_public_key: "<validator-c-ed25519-consensus-pubkey>"
      peer_public_key: "<validator-c-ed25519-peer-pubkey>"
      peer_id: "<validator-c-derived-32-byte-hex>"
      organization_id: "<organization-c-id-bytes>"
    - validator_id: "<validator-d-derived-32-byte-hex>"
      identity_nonce: "<validator-d-stable-32-byte-hex>"
      dag_public_key: "<validator-d-ed25519-dag-pubkey>"
      consensus_public_key: "<validator-d-ed25519-consensus-pubkey>"
      peer_public_key: "<validator-d-ed25519-peer-pubkey>"
      peer_id: "<validator-d-derived-32-byte-hex>"
      organization_id: "<organization-d-id-bytes>"

initial_protocol_config:
  protocol_name: FinalDAG-C
  protocol_version: 1
  feature_set_hash: "<32-byte-hex>"
  state_machine_version: 1
  gas_schedule_hash: "<32-byte-hex>"

  max_transaction_bytes: 1048576
  max_signer_policy_bytes: 65536
  max_account_signers: 128
  max_signatures_per_transaction: 128
  prefilter_verification_work_reserve_per_occurrence_sponsor: 524288
  max_prefilter_verification_work_per_finalized_block: 4194304
  max_validity_window_heights: 100000
  max_future_nonce_gap: 1024

  max_batch_body_bytes: 8388608
  max_batch_transactions: 10000
  proposer_slots_per_round: 2
  max_strong_parents: 4
  max_weak_parents: 64
  max_batches_per_vertex: 64
  max_vertex_bytes: 1048576
  max_future_round_gap: 64
  primary_timeout_ms: 500
  certificate_timeout_ms: 500
  schedule_lookahead_rounds: 128
  dag_gc_rounds: 256
  batch_retention_heights: 100000
  max_execution_lag_heights: 1024

  max_transactions_per_finalized_block: 100000
  max_execution_gas_per_finalized_block: 1000000000
  max_authorized_access_entries_per_tx: 1024
  max_authorized_access_bytes_per_tx: 262144
  max_exact_observed_access_keys_per_tx: 4096
  max_state_namespace_bytes: 256
  max_state_key_bytes: 65536
  max_state_value_bytes: 4194304
  max_state_read_bytes_per_tx: 67108864
  max_state_write_bytes_per_tx: 8388608
  max_state_write_bytes_per_finalized_block: 268435456
  max_events_per_tx: 4096
  max_event_bytes_per_tx: 4194304
  max_events_per_finalized_block: 1000000
  max_event_bytes_per_finalized_block: 134217728
  max_return_data_bytes_per_tx: 1048576
  max_call_depth: 64
  max_calls_per_tx: 100000
  max_finalized_block_body_bytes: 536870912
  execution_parallelism: 32
  mvcc_max_versions: 2
  mvcc_max_bytes: 1073741824
  mvcc_max_retries: 1
  max_dependency_edges: 10000000
  tx_index_prefix_size: 4096

genesis_state_manifest_hash: "<recomputed-with-zero-ledger-domain>"
ledger_governance_policy_hash: "<recomputed-with-zero-ledger-domain>"
```

这段 YAML 是 `LedgerGenesisCore` 的可读投影，内部 ProtocolConfig 字段一一对应；数字只是起始容量示例，必须在目标硬件上压测。`LedgerGenesisBundle` 携带完整 `GenesisStateManifest`、`GovernancePolicy`、`FeatureSet` 和 `GasSchedule`；每个 FeatureEntry 直接携带经过 `(feature_id,feature_version,parameter_schema_version)` typed validation 的 canonical `parameters_cbor`，GasSchedule 必须与 active operation registry 完全相等，不存在只有 hash、空表或默认 cost 的外部补全。精确 feature/payload/Gas/resource 规则见[执行注册表、Gas 与资源计量规范](protocol/05-execution-registry-gas-and-resource-metering.md)。Manifest 中每个创世账户必须以同一 AccountAddress raw key 完整提供 `finalweave/v1/account/meta|auth|nonce` 三项，重算 core/initial policy/address，要求 auth 无 pending、base hash 匹配且 `next_nonce=0`；孤立系统记录使 Genesis 无效。安装必须调用 `ValidateGenesisInstallation`，从完整 Manifest 重建 state root，并重算 LedgerID、policy/set/config/Feature/Gas ID、`epoch0_seed` 与空 MMR root；治理批准位于单独的 `GenesisCertificate.approvals`，不属于 Bundle，仍通过有绝对 count/byte 边界的 `ValidateGenesisCertificate` 检查 threshold、reference 和 strict signature。显示名称、图标、说明和 URL 不进入 LedgerID；`n/f/q/k`、`data_shards/parity_shards`、SHA-256、Ed25519、确定性 CBOR 与 RS profile 都由 validator set 或协议版本派生，不作为可产生矛盾的重复字段。

同槽未引用 sibling 的三项限制不是治理配置，v1 固定为 `UNREFERENCED_SIBLING_QUARANTINE_PER_SLOT_MAX_V1=4`、`...OBJECTS_PER_LEDGER_MAX_V1=65_536`、`...BYTES_PER_LEDGER_MAX_V1=67_108_864`。本地配置只能把 quarantine low/high watermarks 设得更低，不能扩大协议硬上限，也不能把该 cache 与 dependency-store reserve 合并。达到本地水位只会 deferred/evict；对象后来被精确引用时必须绕过 cache 配额、按 ID 拉取并提升，不能因未配置一个“更大 quarantine”而失败。

### 7.1 派生参数

实现必须从 validator set 验证：

```text
n = len(validators)
n >= 4 && n <= 253
n == 3*f + 1
q = 2*f + 1
k = f + 1
totalShards == n
dataShards == k
min(n, q+1) <= max_strong_parents <= n
max_batch_body_bytes >= checked_add(max_transaction_bytes, 5)
max_vertex_bytes >= MinRequiredVertexCanonicalBytes(validators)
n * prefilter_verification_work_reserve_per_occurrence_sponsor
  <= max_prefilter_verification_work_per_finalized_block
prefilter_verification_work_reserve_per_occurrence_sponsor
  >= MaxSinglePrefilterVerificationWorkV1(config)
```

`MaxSinglePrefilterVerificationWorkV1`故意不接收当前ValidatorSet：即使当前`n=4`，同一笔合法重配置也可能携带v1绝对上限253成员的next set，因此模板固定覆盖253个成员、1,024个approval与Feature/Gas registry绝对上限。实现若按当前n缩小reserve，会在shared pool被恶意作者耗尽后阻塞honest author携带的合法扩容交易。

此外必须执行统一 `ValidateValidatorSet(set,expected_epoch,network_id)`：严格重算并去重 ValidatorID，从 Peer public key 重算 PeerID，DAG/Consensus/Peer key 在全集合跨角色两两不同，PeerID 唯一，所有 Ed25519 key 通过 strict profile。仅满足人数公式、复用私钥或接受自报文本 PeerID 的集合无效。

常数 5 是 v1 单交易 `BatchBody` 的完整 canonical 外层开销，不是估算余量。`MinRequiredVertexCanonicalBytes` 使用协议尺寸模板：最大宽度 epoch/round、最大合法 author index、own parent、`min(n,q+1)` 个 strong parents、空的非必需数组和完整 64-byte signature。这些运算都属于 `ValidateProtocolConfigStructure`；不足不是性能告警，而是无效 epoch 配置。

通用 prefilter work 是共识筛选预算，不是本地 worker 参数：它覆盖raw occurrence scan、account/governance Ed25519、strict public-key、完整 reconfiguration bundle 与 registry 验证，按全部 n 个 authenticated containing DAGVertex author（occurrence sponsor）保留份额；P 不参与。Batch author只认证Batch来源，可以与sponsor不同；谁签Vertex并选择该`AvailabilityReference`，谁为其展开的全部raw occurrence付费。上例为 n=4 预留每sponsor 524,288 units、总 4,194,304 units。增大它提高恶意输入下单块最坏 CPU但减少合法交易因 cap 重提；降低它仍必须容纳一个最大合法 v1 occurrence item的scan与transaction suffix模板并满足 `n*reserve<=total`。生产容量验收必须用cache全失效、无效签名不退款、最大重配置payload，以及恶意Vertex反复引用其他作者大Batch的场景压测。

`q` 不作为可编辑字段重复配置。4 validators 时 `f=1,q=3,k=2`；7 时 `f=2,q=5,k=3`。

### 7.2 `proposer_slots_per_round`

v1 默认 `2`，合法范围 `1..q`，仅 epoch 边界可变。它表示每 round 参与确定性 leader-slot 序列的 proposer 数量，而不是每 round 允许多少作者建顶点——所有验证者仍各建一个顶点。

- `2` 是吞吐、leader 频率和未决次级 slot head-of-line 风险的默认折中；
- `1` 降低 slot 扫描/未决复杂度，但降低 proposer 机会；
- `q` 仅作为低故障实验性能 profile，不作为生产默认；
- 更多 slot 在 Byzantine/异步条件下可能产生更多 undecided 次级 slot，并扩大稳定前缀阻塞；
- v1 每轮只为 slot 0（primary）timer 提供活性保证；secondary slots 依赖 DAG/indirect rule，不应各自启动会改变协议语义的 timer。

具体映射和决策由协议固定，配置不能指定任意 proposer 列表。

### 7.3 FinalDAG-C 固定规则

以下项目不开放普通配置：

- direct/indirect commit/skip 阈值；
- slot 扫描和稳定前缀规则；
- strong edge 计数语义；
- restricted round-jump：跨过 `r'` 且 `DecisionRound[r'-2] == UNDECIDED` 时补发 `r'` 顶点；
- canonical causal ordering/tie-break；
- DAGCommitWitness/FinalizedBlockHeader/FinalityStatement/FinalityCertificate schema。

改变它们意味着新协议版本和新 epoch，而非调参。

## 8. BatchAC 参数校验

| 字段 | 规则 |
|---|---|
| `max_batch_body_bytes` | 完整 `len(canonical(BatchBody))`；必须至少为 `MaxSingleTxBatchCanonicalBytes(max_transaction_bytes)` |
| `max_batch_transactions` | 防止 Merkle/解码 CPU 放大 |
| 本地 `batching.maxBatchAge` | 仅本地成批建议；不进入 ProtocolConfig，不得改变已签 Batch 含义 |
| `max_batches_per_vertex` | 链上上限；接收方在昂贵 fetch 前检查 |
| `max_vertex_bytes` | 完整 signed `len(canonical(DAGVertex))`，含 core 与 64-byte signature；不得小于 `MinRequiredVertexCanonicalBytes(set)` |
| erasure implementation | 由 protocol/version 固定，必须有跨实现 vectors |

验证者 ACK 前完整重构/重编码，因此 BatchSize 必须由最慢生产 validator 的 CPU、RAM 和 ingress p99 决定，不能只看作者出站。

声明 `transaction_count/body_length` 在 fragment fetch、RS matrix/shard buffer 分配前检查，恢复后再按实际完整 canonical Body 复验；Vertex 的 strong/weak/reference count 和完整 signed-envelope bytes 在 parent/AC fetch 前检查。实现不能只在配置生成器中验证这些关系。

### 8.1 Lag 与保留边界

- `max_execution_lag_heights` 使用 `highest_ordered_candidate_height-public_finalized_height` 的 checked 差；恰好达到上限即停止新 Batch 和 AvailabilityReference，但 control-only Vertex、repair、执行、attestation、FC 与 epoch close 继续；
- `dag_gc_rounds` 使用 `gc_boundary_round=highest_complete_round-dag_gc_rounds`；不足以相减时 boundary 为 0，`r==boundary` 时 inclusive 到期；
- `batch_retention_heights` 只从 Batch 所属 epoch 的 `EPOCH_CLOSED.final_height` 起算；第一次被 certified causal stream 消费、当前未见引用或 DAG window 前进都不能终止同一 BatchAC 的未来 epoch 内引用权。仅在 `public_finalized_height >= terminal_height+batch_retention_heights` 时 inclusive 到期；runtime checked addition 溢出表示永不到期，不能 wrap 后删除。

上述三个字段只决定诚实生产背压和 GC 资格；所有其他未决依赖、archive、证书、restricted-jump 条件，以及只会延长保留的非共识合规/争议 `LocalHistoryPolicy` 仍须同时满足。

## 9. 执行配置边界

共识语义固定为 canonical `tx_index` 串行 Apply。以下是本地等价优化：worker 数、exact-access 图缓存、MVCC 内存、串行采样率、预取；它们可以动态变化但不能进入 FinalizedBlockHeader/FinalityStatement。

以下是协议限制，只能 epoch 变更：单 FinalizedBlock 的交易/Gas/Body/写入/Event 上限、`AuthorizedAccessEntry` schema 与条目/字节上限、运行时精确访问 key、state component、read/write、event、return、call 上限、状态机版本、FeatureSet、GasSchedule、Receipt/FinalizedBlockHeader/FinalityStatement schema。v1 `fee_limit` 只能为 0；收费不是调一个本地 gas price，而是未来完整 state-machine/protocol 升级。

无法安全验证 exact access 的交易自动进入 serial lane。不得通过本地配置拒绝这类本来合法的交易或要求所有应用提前声明访问集。

## 10. Epoch 变更矩阵

| 变更 | 同 epoch | 新 epoch | 说明 |
|---|---:|---:|---|
| 日志、缓存、worker | 是 | 否 | 输出必须相同 |
| validator endpoints | 受控 | 可 | 身份仍由链上状态确定 |
| Batch/交易上限 | 否 | 是 | 影响合法性 |
| proposer slots | 否 | 是 | 影响 slot 序列 |
| timer 边界 | 否 | 是 | 影响活性行为 |
| validator set/keys | 否 | 是 | 改变 quorum |
| FinalDAG-C 规则 | 否 | 新协议 | 需 ADR/模型/升级 |
| restricted round-jump | 否 | 新协议 | 不允许关闭 |
| execution schema/gas | 否 | 是 | 影响 state/receipt root |
| hash/encoding/signature | 否 | 新协议 | 全量向量和迁移 |

## 11. 配置加载与热更新

热更新流程：解析到新对象；严格校验；计算脱敏 diff；预创建资源；原子交换 immutable config pointer；观测确认；失败回滚本地对象。共识配置只从已经 FinalityProof 验证的 epoch config 加载。

节点收到未知 protocol version/config hash 时停止签名并保持只读同步；不得选择“最接近”的本地配置。

## 12. 启动校验清单

- Genesis/epoch FinalityProof、LedgerID 和 config hash 一致；
- `n=3f+1`、`q=2f+1`、`k=f+1`；
- `1 <= proposer_slots_per_round <= q`；
- P2P v1 固定 ALPN、双向证书、frame 范围有效，leaf SPKI、Peer key、PeerID 与各承载 Ledger 的当前/待激活 descriptor 一致；
- 本地 validator keys 与链上 public keys/domain 一致；
- DAG/Consensus semantic key ID 按已认证 descriptor、role、network/ledger重算，KMS handle/generation 与其映射唯一；
- Safety WAL authored round/attestation height 不倒退；
- execution speculation/re-execution 上限均为 1；
- 磁盘水位严格递增且空间充足；
- 控制队列和 reserved workers 非零；
- 每 Ledger backlog low/high/reserved 值、durable counter checksum 与 manifests重算一致，且 DAG/availability/control working-set capacity challenge通过；
- retention 不低于协议/恢复要求；
- 同 epoch protocol version 不被本地 feature 覆盖。

## 13. 相关文档

- [节点角色与部署](03-node-roles-and-deployment.md)
- [FinalDAG-C 共识](protocol/03-finaldag-consensus.md)
- [最终性、执行与纪元](protocol/04-finality-execution-and-epochs.md)
- [ADR-002](decisions/ADR-002-finaldag-c-direct-dag.md)
