# FinalWeave 网络、同步、查询与 API 规范

> 状态：设计基线（Draft）
>
> 适用范围：FinalWeave v1

## 1. 目标与边界

网络层负责传输、限流和来源选择，不决定正确性。Batch、DAG、最终块、状态和查询结果都必须由规范 hash、签名、Merkle/SMT path 与本地 trust store 选定的基础 `FinalityProof` 或独立 `CheckpointFinalityProof` 验证；peer 数量、到达先后、DNS、DHT 和节点声誉只能影响获取顺序。

协议必须同时做到：Validator 控制平面低延迟；Batch/fragment 数据面高吞吐；慢节点可从证明和快照恢复；客户端能离线验证终态；一个 Ledger 的洪泛不能饿死其他 Ledger。

## 2. 身份与连接

一个节点至少区分：

- `PeerID`：当前 network 下由独立 Peer Ed25519 公钥派生的连接身份；
- `ValidatorID`：由 epoch validator set 激活的协议签名身份；
- 组织身份：治理、审计和运维授权，不直接替代 Validator 签名。

v1 Peer key 只允许本规范 strict profile 的 pure Ed25519。TLS 1.3 leaf certificate 的 SubjectPublicKeyInfo 算法必须是 Ed25519，取出的原始 32-byte key 必须通过 strict public-key 检查；`peer_id` 必须按数据模型 `PEER_ID` domain 从该 key 和期望 `network_id` 重算。base58/multibase、libp2p multihash、证书 DER/hash、DNS 名或 IP 都不能作为 `PeerID` 的 wire bytes。

TLS 完成后双方必须交换并验证：

```text
PeerHelloCoreV1 {
  schema_version: uint16
  network_id: NetworkID
  peer_id: PeerID
  supported_protocol_versions: [uint16]
  max_frame_bytes: uint64
  compression_algorithms: [uint16]
  tls_exporter: Hash32
}

PeerHelloV1 {
  schema_version: uint16
  core: PeerHelloCoreV1
  signature: SignatureEd25519
}
```

上面两个对象是[数据模型第 4.1 节](../protocol/01-data-model-and-cryptography.md#41-schema-记法与字段编号)所称的“规范 wire schema”。`PeerHelloCoreV1` 的 CBOR map key 按声明顺序固定为 `1..7`，依次对应 schema/network/peer/version-list/frame-limit/compression-list/exporter；`PeerHelloV1` 固定为 `1=schema_version,2=core,3=signature`。禁止文本 key、不同字段号或语言默认枚举。

两个 schema version 都固定为 1。FinalWeave v1 不留下协商歧义：`supported_protocol_versions` 必须精确为 `[1]`，`compression_algorithms` 必须精确为 `[0]`，其中 `0=NONE`；未知/额外值直接拒绝，不做“取最高已知值”降级。传输压缩若要进入 P2P，必须以新协议版本冻结算法 ID、内存/window 上限、逐消息标识与炸弹防护；v1 可在 HTTP Gateway 层使用独立、受输出上限保护的 Content-Encoding，但它不改变 P2P PeerHello。

`P2P_FRAME_MIN_BYTES_V1=65_536`，`P2P_FRAME_HARD_MAX_BYTES_V1=16_777_216`；hello 的 `max_frame_bytes` 必须在该闭区间，连接的 `effective_max_frame_bytes=min(local.max_frame_bytes,peer.max_frame_bytes,P2P_FRAME_HARD_MAX_BYTES_V1)`。可靠有序 protocol stream 上的每条规范消息编码为 `U64BE(canonical_length) || canonical_bytes`，可以跨任意数量 transport frame；每帧 payload 不超过 effective max，frame 边界不进入 hash。接收端必须先以消息类型的 canonical `MAX_CANONICAL_BYTES` 验证长度前缀，再用增量 decoder 读取恰好该长度，禁止按前缀一次性分配；多/少字节、流结束和尾随数据都拒绝。无共同版本、frame 范围无效或任何一端不接受 `NONE` 都关闭连接。

`tls_exporter` 是当前 TLS 1.3 会话使用 label `EXPORTER-FinalWeave-Peer-Hello-v1`、空 context、长度 32 得到的 exporter bytes；它是每条完整握手/恢复握手的 channel binding，跨连接重放会自然失配，因此 v1 不再增加一个没有 challenge/echo 语义的应用 nonce。签名摘要为：

```text
DomainHash(
  "PEER_HELLO", core.network_id, ZERO_ID,
  canonical(PeerHelloCoreV1)
)
```

接收端必须要求 hello 的 `network_id` 等于本地期望、`peer_id` 等于 TLS leaf raw key 的规范派生值，并用同一 raw key strict 验签；exporter、版本或数组编码任一不符都立即关闭连接。TLS CA、IP allowlist、DHT 或对端自报 ValidatorID 都不能授予 Validator 权力。非 Validator peer 同样使用派生 PeerID，但只能获得本地策略允许的 Full/Archive/Observer 能力。

P2P 的 TLS profile 固定为 TLS 1.3、双向 certificate authentication 和 ALPN byte string `finalweave-p2p/1`。无论 QUIC 还是 TCP，server 必须请求 client certificate，双方都必须发送 leaf certificate 并完成 TLS CertificateVerify；缺 certificate、ALPN 缺失/不同、TLS 1.2 fallback 都以 fatal handshake 结束。leaf SPKI 的 raw Ed25519 key 就是 Peer key：P2P TLS 没有另一把 `tlsKey`。证书可以由组织 PKI 签发或按部署策略自签；CA/path 校验可以加强连接准入，但 Validator 权力只来自链上 descriptor 与对象签名。session resumption 只有在实现能取回并绑定原 peer leaf SPKI 时才允许，恢复后仍必须用新 exporter 重跑 PeerHello；否则禁用 resumption。

传输优先 QUIC + TLS 1.3；TCP + TLS 1.3 是兼容路径。两条路径使用相同 ALPN、双向 certificate、PeerHello、身份派生、授权和重放防护。证书在保持相同 leaf key 时可以续期而不改变 PeerID；Peer key 轮换会改变 PeerID，Validator 只能通过 epoch 边界的完整 ValidatorSet 激活。TLS 已证明 key possession，应用层签名进一步把 network、协商参数和 exporter 绑定到可记录的规范对象；两项都必须验证。

连接认证上下文只缓存 `(network_id,peer_id,peer_public_key)`，严禁缓存 connection-wide `is_validator=true`。每个带 Validator 权限或 P0/P1 资源优先级的入站消息必须执行：

```text
AuthorizeValidatorMessage(peer_context, ledger_id, message_epoch, protocol_id):
  runtime = LookupLedgerRuntime(ledger_id)
  require runtime accepts message_epoch for this protocol_id
  set = runtime.AuthenticatedValidatorSet(message_epoch)
  descriptor = set.find_by_peer_id(peer_context.peer_id)
  require descriptor exists
  require descriptor.peer_public_key == peer_context.peer_public_key
  require ProtocolAllowsValidatorMessage(protocol_id,message_epoch,runtime.phase)
  return (descriptor.validator_id, descriptor.validator_index,
          set.validator_set_hash)
```

授权缓存键必须完整包含 `(ledger_id,message_epoch,validator_set_hash,peer_id,protocol_id)`。其中 `ledger_id/message_epoch` 必须来自当前消息的有界 envelope，`protocol_id` 必须来自实际协商并打开的 stream，`validator_set_hash` 必须来自该 Ledger 对该 epoch 的已认证 active/runtime set；任何字段都不能由连接标签、上一条消息或 payload 中另一个自报字段替代。授权在**每条消息**进入 P0/P1 队列或取得 Validator 专属资源前执行，cache hit 也必须确认对应 set generation 仍为 active/允许的旧 epoch generation，不能把一次成功提升为 stream-wide 或 connection-wide 权力。

epoch 四元组原子激活时同步失效该 Ledger 的旧 cache/队列 capability；被移除 Peer 的 TLS 连接可以降级为普通同步连接，但不能继续获得新 epoch P0/P1 队列、ACK slot、Vertex/attestation 接收或 Validator 配额。旧 epoch 对象只有在对应 runtime phase 明确允许 repair/audit 时才按已认证旧 set 验证，并放入旧 epoch 的隔离预算，不能继承当前 epoch 权力。共享一条 network connection 不得跨 Ledger 传播角色；每个消息在进入高优先级队列和大对象 fetch 之前完成上述有界 pre-auth，随后对象级签名仍必须独立验证。

未知 Ledger、epoch、协议版本或超限 frame 必须在大内存分配和规范解码前拒绝；HTTP Gateway/未来压缩 profile 还必须在解压前完成其可由外层 framing 判定的拒绝。0-RTT 不允许承载 ACK、Vertex、ExecutionAttestation 等可改变安全状态的请求。

### 2.1 本地连接配置启动校验

节点必须在开放 P2P listener、宣告 readiness 或允许任一协议 signer 工作前原子校验连接配置：

- `p2p.alpn` 必须逐字节等于 `finalweave-p2p/1`，`p2p.mutualCertificateRequired` 必须为 `true`，`p2p.maxFrameBytes` 必须位于 `[65_536,16_777_216]`；v1 P2P compressor/decompressor 表必须只暴露 `NONE`，配置任何其他算法都启动失败；
- `identity.peerKeyUri` 必须可读取/签名且对应 strict Ed25519 public key；`identity.p2pTlsCertFile` 必须可解析，leaf SPKI 算法为 Ed25519，提取的 raw key 必须逐字节等于 `peerKeyUri` 的 public key；
- 对本节点启用 Validator 角色的每个 Ledger/active 或 pending epoch，必须从该 raw key 和配置的 `network_id` 重算 PeerID，并逐项匹配已认证 `ValidatorDescriptor.peer_id/peer_public_key`；不匹配时该 Ledger 的 Validator readiness 与全部签名路径保持关闭，不能退化为只信证书、PeerHello 或自报 ValidatorID；
- API TLS 的 `identity.apiTlsCertFile/apiTlsKeyUri/apiTrustBundleFile` 属于独立管理面身份，不得充当 Peer key；其 key 必须与 Peer key 不同，API 证书轮换不能改变 PeerID；
- 若实现启用 TLS session resumption，必须证明恢复会话可取回原 peer leaf SPKI、生成新的 exporter 并重跑 PeerHello；做不到时启动校验必须禁用 resumption。

热重载先在隔离候选上执行同一组校验，全部通过后一次切换；失败只保留旧配置并报告 not-ready/reload error，不得形成“新证书配旧 Peer key”或部分 Ledger 已提升权限的中间态。

## 3. P2P 协议注册表

v1 核心 protocol id：

```text
/finalweave/1/batch
/finalweave/1/fragment
/finalweave/1/ack
/finalweave/1/ac
/finalweave/1/dag/vertex
/finalweave/1/dag/sync
/finalweave/1/causal-input/sync
/finalweave/1/finality/attestation
/finalweave/1/finality/certificate
/finalweave/1/finalized/sync
/finalweave/1/snapshot/sync
/finalweave/1/dag-derivation-checkpoint/sync
/finalweave/1/cross-ledger-proof/sync
/finalweave/1/tx/submit
/finalweave/1/query
```

职责：

| 协议 | 传输内容 | 主要验证 |
|---|---|---|
| `batch` | BatchHeader、交易清单或 body 引用 | author、schema、batch hash、上限 |
| `fragment` | erasure-coded fragment 与 Merkle path | index、fragment root、尺寸、codeword |
| `ack` | `DA_ACK` 请求/响应 | 固定 signer slot、durable fragment、签名 |
| `ac` | `BatchAC` | q 个不同 signer、bitmap、Batch commitment |
| `dag/vertex` | FinalDAG-C Vertex | author slot、父边、round jump、BatchAC 引用 |
| `dag/sync` | round/author 缺口、祖先与证据 | 内容 hash、因果闭包、速率限制 |
| `causal-input/sync` | `CausalInputManifest` 与按 manifest ID/index 获取的 `CausalInputChunk` | manifest/core hash、逐 chunk inclusion proof、framing/计数、因果来源 |
| `finality/attestation` | 执行确认 | FinalityStatement、epoch、Ed25519 Consensus Key signer |
| `finality/certificate` | 聚合后的 `FinalityCertificate` | q、validator set、同一 `FinalityStatement` |
| `finalized/sync` | Header、Body、proof、配置链 | `FinalityProof` 与父链 |
| `snapshot/sync` | manifest 和 chunks | proof、chunk hash、state root |
| `dag-derivation-checkpoint/sync` | epoch emitted exact-set manifest/chunks | proof、target Header、chunk path、严格 ID 集与 emitted root |
| `cross-ledger-proof/sync` | source transaction/Receipt/Event paths 与调用方选定 root kind 的 proof envelope | bounded schema、source finality；信任 policy 仍由目标账本决定 |

协议升级新增 id，不在相同 id 下按节点本地配置改变语义。规范对象使用协议 CBOR；Protobuf/JSON 只用于外部 API，不能直接成为 hash 或签名输入。

## 4. 拓扑、传播与优先级

小型 Validator set 默认保持控制平面全连接；Batch body 可使用 fanout 和按需恢复。连接管理必须跨组织、网络路径和故障域分散，避免只连同一运营方。

推荐优先级：

| 优先级 | 流量 |
|---|---|
| P0 | Vertex、ExecutionAttestation、FinalityCertificate、安全证据摘要 |
| P1 | ACK、BatchAC、DAG 缺失父对象 |
| P2 | BatchHeader、关键 fragment、causal-input manifest/执行前缺失 chunk、finalized header/proof 同步 |
| P3 | 普通 fragment、Body、causal-input 预取 chunk、快照与 DAG-derivation checkpoint chunk |
| P4 | 历史查询、索引回填、审计导出 |

P0/P1 必须预留独立队列、并发和带宽；大 fragment stream 不得阻塞控制 frame。每个 Ledger、peer、组织和协议都有 token bucket、最大 in-flight、最大队列字节和公平调度权重。

dependency promotion 的 P1 请求还按 authenticated DAG author 分 lane：每个 active author有独立 reserve，shared 份额只能在 reserve之外使用。unreferenced sibling/evidence 异步流量不能进入该 reserve。响应必须回显 requested exact VertexID；内容hash不符在写staging前拒绝。对同一 ID合并in-flight fetch，但逻辑author账仍归属于触发promotion的root，不能借cache hit把攻击工作转嫁给另一author。

## 5. 对象接收流水线

统一顺序（第三步是互斥分支，不是 P2P v1 的隐式解压）：

```text
frame 上限
 -> 身份/ledger/epoch 预检查
 -> P2P v1 原始 canonical 字节计数
    或 HTTP Gateway/未来版本的流式解压输出计数
 -> 规范解码
 -> 内容 hash
 -> 签名或 proof
 -> 协议语义
 -> durable/缓存写入
 -> 传播或响应
```

P2P v1 的 PeerHello 只允许 `NONE`，因此网络核心没有隐式解压步骤。HTTP Gateway Content-Encoding 或未来已版本化 P2P 压缩的解压上限必须由该 protocol id/消息类型的解压后硬上限 `MAX_CANONICAL_BYTES` 驱动：streaming decompressor/running counter 最多接受 `MAX_CANONICAL_BYTES` bytes，只探测下一个输出 byte，产生第一个越界 byte 时立即中止且不得交给规范 decoder、staging 或业务 handler。实现不得先按 peer 声明长度分配对象或完整解压后再检查；所有声明长度、数组 count 和嵌套容器容量都必须先证明可由剩余硬预算容纳。若某消息类型没有冻结或本地配置的有限解压后上限，就不得为它启用 Content-Encoding/未来压缩。

签名正确但父对象缺失的 Vertex 进入 author-fair、有独立容量的 dependency fetch lane，并通过 `dag/sync` 按精确 VertexID 拉依赖；递归闭包未完成且每条依赖未 durable 提升前不能产生 support。完全相同的 `VertexID` 按内容 id 幂等。相同 `(epoch,round,author)` 的纯旁路 sibling gossip 只进入 unreferenced-sibling quarantine：v1 每 slot 最多 4 个、每 Ledger 最多 65,536 个且 canonical bytes 总和最多 67,108,864；peer/author token bucket 只能更严。满额可驱逐或返回 `RESOURCE_DEFERRED`，不继续泛洪，也不能写永久 invalid 结论。

若已接纳 Vertex、证书/witness 或 anchor 后来按 exact VertexID 引用了 cache 中或已驱逐的 sibling，promotion 请求不受旁路 cache 配额阻挡：先查 cache，未命中则优先向引用来源并并行向其他 Validator/Archive 有界拉取；验证成功后先 fsync dependency store，再关闭引用边。提升对象及其 payload occurrence 不得被 evidence 替代；提升后只能按共识感知 DAG GC 删除。取不到时该分支保持 `PENDING_DEPENDENCY`，不能 support/commit，但其他 author 的完整分支可继续。每个 `(ledger,epoch,author)` 都有 root/dependency work reserve，单一 Byzantine author 的递归 ancestry 不能占用诚实 author 份额。

旁路 evidence cache 每 slot 只保留已观察有效 VertexID 中字节序最小的两份完整对象，看到更小者时替换当前较大者；它计入上述硬预算，可整体驱逐，只服务审计/治理，不进入 support、direct/skip、`Past(P)` 或 ordered output。于是到达顺序和 peer 声誉都不能选择共识 payload，同时一把 Byzantine DAG key 也不能制造无限持久化义务。

`DAGVertex.evidence_refs` 只随 Vertex 验证固定 Hash32 数组与 canonical 排序；不得在 P0 Vertex handler 中同步下载完整 evidence。异步 evidence fetch 使用低优先级、共享 quarantine bytes/objects cap，每个 ref 可超时或丢弃；不存在、hash错、上下文错或 cache驱逐只产生审计指标，不改变 containing Vertex 的 validity/support。否则 1,024 个 refs 可把一个小控制消息放大为约 32 GiB 下载并饿死共识。

消息去重键至少包含 `(ledger,epoch,type,semantic_id)`。Bloom filter 只能减少工作，不能成为拒绝合法对象的唯一依据。

`CausalInputChunk` 与 `SnapshotChunk` 共用 v1 canonical envelope 硬上限 `1_050_823` bytes，分别对应 `CAUSAL_INPUT_CHUNK_MAX_CANONICAL_BYTES_V1` 和 `SNAPSHOT_CHUNK_MAX_CANONICAL_BYTES_V1`。P2P v1 传输原始 canonical bytes；若 HTTP Gateway 使用 Content-Encoding，接收端必须以流式解压器限制解压后输出，在为 canonical envelope/payload/sibling 数组分配内存或进入 CBOR 解码前对第 `1_050_824` byte 立即拒绝。压缩比、Content-Length 或 peer 声称的原始长度不能放宽该上限。

## 6. Batch 与 DA 网络流程

1. 作者通过 `batch` 发布 BatchHeader；
2. 按固定编码把 fragment 经 `fragment` 发送到对应 Validator；
3. signer 完整验证 codeword，并 durable 保存自己的 fragment；
4. signer 在 `DA_ACK_LOCK` durable 后经 `ack` 返回签名；
5. 收集 q 个不同 ACK 形成 `BatchAC`，经 `ac` 传播；
6. Vertex 只引用有效 `BatchAC`。

ACK 或 AC 不代表 DAG 顺序和最终性。节点必须限制单作者未完成 Batch、fragment 重传和 codeword 重建 CPU；错误 fragment 惩罚来源但不污染同 hash 的其他来源。

## 7. DAG 与 finality 网络流程

### 7.1 Vertex 传播

Vertex 广播应优先发送小型元数据；其 Batch body 已由 AC 独立保证。接收者验证可由 wire 证明的 own-parent、父 author 去重、父 quorum、BatchAC 和作者签名后，仍须等 dependency-store 闭包完整才更新 support；它无法证明 Byzantine 作者是否隐藏了更高 own Vertex。restricted round jump 是诚实本地 signer、WAL、pacemaker、恢复和追赶路径的强制义务，不能伪装成远端 Vertex 的可验证 wire 条件。从 stable prefix 展开 payload 时，`Past(P)` 中所有不同 `VertexID` 都按 `(round,author_index,vertex_id)` 的规范顺序进入派生，包括 committed proposer以及同 slot所有**被因果依赖引用并已提升**的 Byzantine sibling；纯旁路 quarantine 不在其中。每个精确 `VertexID` 在全局 ordered output 中只发出一次。不得按 author、`BatchID` 或 `tx_id` 预去重；相同 Batch/交易的每个 raw occurrence 都要交给规范 occurrence filter，其work sponsor固定为承载该`AvailabilityReference`的签名Vertex作者，而不是被引用的Batch作者。因而恶意Vertex反复引用honest作者旧Batch不会转嫁scan/common/source成本。

### 7.2 Canonical causal input 获取

COMMIT slot 的完整因果输入通过 `/finalweave/1/causal-input/sync` 内容寻址获取。v1 协议对象固定为：

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

manifest 可由本地 DAG/Batch 重建，也可先从 proposer、跨组织 Validator 或 archive peer 获取；正确性不取决于来源。请求/响应路径为：

```text
GetCausalInputManifestRequest {
    ledger_id
    epoch
    height
    parent_block_id
    committed_slot
    proposer_vertex_id
}

GetCausalInputManifestResponse {
    manifest: CausalInputManifest
}

GetCausalInputChunkRequest {
    manifest_id: Hash32
    chunk_index: uint64
}

GetCausalInputChunkResponse {
    chunk: CausalInputChunk
}
```

节点先重算 `causal_input_manifest_id = DomainHash("CAUSAL_INPUT_MANIFEST",network_id,ledger_id,canonical(CausalInputManifestCore))`，再核对 slot、proposer、父块/父状态、ValidatorSet/config 与完整 DAG/Batch 来源。已选定 manifest ID 后，chunk 可从多 peer 并行拉取，但每个请求都绑定 `(manifest_id,chunk_index)`；响应不得在重试时静默切换 manifest。

item variant 固定为 `VERTEX=0/OCCURRENCE=1`。顺序为每个 `VertexDelta` 先写连续 ordinal 的 Vertex，再按 AvailabilityReference 数组与 BatchBody transaction 顺序写其全部 occurrence。传输字节流为：

```text
causal_item_frame(item) = U64BE(len(canonical(item))) || canonical(item)
causal_byte_stream       = causal_item_frame(item_0) || ... || causal_item_frame(item_n-1)
```

`chunk_payload_bytes` 必须等于 `CAUSAL_INPUT_CHUNK_PAYLOAD_BYTES_V1=1_048_576`。从 byte offset 0 连续切块，除末块外 payload 恰为 1 MiB，末块为 `1..1_048_576` bytes；frame 和其 8-byte 长度前缀都可跨 chunk。所有长度与计数用 checked `uint64`；`item_count=ordered_vertex_count+occurrence_count`，`chunk_count=ceil(stream_byte_length/1_048_576)`，index/offset 必须从 0 连续，COMMIT stream 不得为空。解码器在分配前根据已认证 schema/config 限制 frame length，再增量消费恰好该长度的 canonical CBOR；残缺前缀、少/多字节和非规范 CBOR 都拒绝。

每个 chunk 先重算 `CAUSAL_INPUT_CHUNK` core hash 与绑定 `U64BE(chunk_index)` 的 leaf，再用 `siblings_bottom_up` 独立验证到 manifest `chunks_root`。path 长度必须恰好等于把 `chunk_count` 反复上取整除以 2 至 1 的层数且不超过 64；左右位置由逐层 index bit 推导，奇数复制层 sibling 必须等于当前 hash。只有 manifest ID、schema version、index/offset、core hash 和 proof 全部通过，且 P2P v1 原始 canonical envelope（或 HTTP Gateway/未来版本流式解压后的 canonical envelope）不超过 `1_050_823` bytes，才能把 payload 写入 prepared staging 并交给 framed decoder。

消费必须按 chunk/byte/item 顺序；已 proof-verified 的乱序 chunk 只能进入有界磁盘缓冲。执行或磁盘落后时，背压通过减少 in-flight、暂停请求、磁盘 spill 或换 peer 处理，不得截断 `Past(P)`、跳过 item 或改变 roots。断点保存 manifest ID、最后已验证 chunk、下一 byte/item/Vertex/occurrence 游标、framed-decoder 边界状态、临时 leaf files 校验信息和 occurrence-filter 状态。重启时先重验 manifest 与最后完整 chunk proof，再从精确游标幂等继续；不完整 stream 绝不得形成 Header 或 attestation。

### 7.3 执行确认与最终性

stable slot prefix 派生 `BlockDerivationCandidate` 后，每个 Validator 本地执行得到完整 `FinalizedBlock`，并将 `ExecutionAttestation` 搭载在后续 Vertex。也可通过 `finality/attestation` 定向补发，但二者的签名对象完全相同。收集器聚合 q 个不同 Validator 对同一 `FinalityStatement` 的 attestation，形成并广播 `FinalityCertificate`。

同 signer/height 两个 digest、同高度两份冲突证书或 prefix disagreement 都是安全事件。网络层必须保留原始字节和来源，不能自动选择“多数 peer 看到”的版本。

## 8. 反滥用与声誉

可降低 peer 本地声誉的行为包括无效签名、错误 hash、HTTP Gateway/未来压缩 profile 的解压炸弹、重复缺失依赖、超额 orphan、错误 fragment 和持续请求不存在对象。单次网络超时、未拥有某 fragment、暂时执行落后不能直接视为 Byzantine。

封禁是本地可恢复动作，不能阻止协议要求的跨组织连通。Validator 身份违规证据进入安全/治理流程；IP 地址不能替代 ValidatorID 归责。

## 9. 分层同步

### 9.1 Trust anchor

同步从创世文件或已治理、已离线验证的 checkpoint 开始。基础 `FinalityProof` 以带外固定的 `genesis_reference` 为信任根。Checkpoint 路径使用协议冻结的 `CheckpointTrustAnchor` 与 `CheckpointFinalityProof`：anchor 固定 network、ledger、epoch、height、block/state/MMR root、规范 MMR peaks、validator/config hash 和完整 ValidatorSet/ProtocolConfig/FeatureSet/GasSchedule，其内容 ID 使用 `CHECKPOINT_TRUST_ANCHOR` domain。节点只能从本地只读 trust store 取得期望 anchor ID；peer 返回的 anchor 仅是待匹配内容，不能自证或触发 trust-store 更新。基础与 checkpoint proof 使用不同解码器和 API 方法，不得把 checkpoint 伪装成基础 Schema 的创世。

### 9.2 同步阶梯

```text
发现目标
 -> 同步 epoch validator/config 链
 -> 同步 Header + FinalityProof
 -> 选择 Body replay 或 snapshot
 -> Validator 快照同步同一 target 的 DAG derivation checkpoint
 -> 验证并原子切换状态、MMR、derivation 与 cursor
 -> 补齐近期 Batch/DAG
 -> 追至当前 stable/finality cursor
```

节点应向至少三个、优先跨组织来源采样状态摘要，但目标高度最终由可验证 proof 决定。

### 9.3 Header 与 FinalityProof

响应包含连续 Header，以及按调用方本地 trust-root 类型选择的基础 `FinalityProof` 或独立 `CheckpointFinalityProof`。验证者逐项检查父 id、连续高度、配置激活点、q 个 Consensus Key signer、Header roots，以及两阶段 MMR 连续性：Header 的 `parent_block_mmr_root` 等于父已认证 statement root；当前 `{height,FinalizedBlockID}` 追加后得到 certificate statement 的 `block_mmr_root`。证书 signer 子集不同不应改变区块语义 id；缓存键使用 Header/block id，不使用证书 envelope hash。

### 9.4 Body replay

Body 与 CausalInput sidecar 可从多个来源并行获取。先要求 Body/Header schema version 为 1，且 Body 的 `epoch/height/committed_slot/proposer_vertex_id` 逐字段等于已认证 Header；`ordered_vertex_count` 必须等于已验证 manifest 的同名计数。随后从 causal stream 的 `VERTEX` items 重算 count-bound ordered-vertex root，从 raw occurrences 重跑 canonical occurrence filter，并从 Body 重算 transaction/Receipt/Event/state-change roots 和最终 state root，逐项匹配 Header。Body 不内联 ordered VertexID；缺失或不完整 sidecar 不能用“其余 roots 正确”代替。任一来源的重复元数据、计数或 root 错配都拒绝，不能把同一认证 Header 配成多个自称 canonical 的 Body。状态提交仍按父高度连续；并行执行必须通过串行 oracle 等价规则。

### 9.5 Snapshot sync

Snapshot 是已获 `FinalityCertificate` 的完整逻辑状态传输对象，不承诺导出节点的数据库、SST、索引或压缩格式。v1 wire schema 固定为：

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

所有 schema version 必须为 1；`state_encoding=1` 固定为 `SMT_RECORD_CBOR_FRAMED_V1`，`compression=0` 固定为 `NONE`，`chunk_payload_bytes` 必须等于 `SNAPSHOT_CHUNK_PAYLOAD_BYTES_V1=1_048_576`。含最大 payload 和 64 个 siblings 的 `SNAPSHOT_CHUNK_MAX_CANONICAL_BYTES_V1=1_050_823`；P2P v1 只传未压缩 envelope，HTTP Gateway Content-Encoding 或未来版本压缩则必须用 streaming decompressor/running counter，最多读取 `1_050_824` bytes 判定越界，第 `1_050_824` byte 不进入 CBOR parser 或 staging，并在按声明长度分配 payload/siblings 前验证硬上限；禁止先完整解压到内存再检查。传输压缩和文件容器不进入 snapshot 身份。

导出者从目标 certified publication 的单一只读数据库 snapshot 枚举全部 present SMT 记录，重算 `key_hash` 并按其原始 32 bytes 严格升序输出。相同 key hash——包括重复的同 namespace/key——必须拒绝；空 byte-string 是 present value，tombstone、MVCC version、索引和物理压缩节点不得输出。规范字节流为：

```text
snapshot_record_frame(record) =
    U64BE(len(canonical(record))) || canonical(record)

snapshot_byte_stream =
    snapshot_record_frame(record_0) || ... || snapshot_record_frame(record_n-1)
```

所有长度/求和使用 checked `uint64`；导入器先强制协议绝对 `SNAPSHOT_STATE_RECORD_MAX_CANONICAL_BYTES_V1=17_891_328`，再用有界增量 canonical-CBOR decoder 消费恰好该长度，不得按未验证长度预分配。active epoch 的 namespace/key/value cap 只限制新写入，不能在治理下调后拒绝旧状态中仍低于 v1 绝对上限的认证记录。frame 和 8-byte 长度前缀都可以跨 chunk。非空 stream 从 offset 0 按字节切分：除末 chunk 外 payload 恰为 1 MiB，`chunk_index` 从 0 连续，`first_byte_offset=chunk_index*1_048_576`，`chunk_count=ceil(stream_byte_length/1_048_576)`。空状态固定为 record/byte/chunk count 全为 0 且不携带 chunk。

chunk 和 manifest 身份按以下无环顺序计算：

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
        DomainHash("SNAPSHOT_CHUNK_ROOT", network_id, ledger_id, U64BE(0))
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

非空 chunk tree 奇数层复制末节点。每个非空 `SnapshotChunk` 必须携带 `siblings_bottom_up`：路径长度由 manifest `chunk_count` 反复上取整除以 2 直到 1 唯一决定，不得超过 64；每层左右位置只由 `core.chunk_index` 对应 bit 推导，不接受 envelope 自报 direction；奇数复制层的 sibling 必须等于当前 hash。接收者先验证 `0 <= chunk_index < chunk_count`、精确路径长度和每层复制/左右规则，再从 `snapshot_chunk_leaf_i` 自底向上重建并包装 `SNAPSHOT_CHUNK_ROOT(U64BE(chunk_count)||tree_top)`；只有精确等于 manifest `chunks_root` 才能在 staging 写入该 chunk。

先由 core hashes 得到 `chunks_root` 和 `snapshot_manifest_id`，再把 manifest ID 与路径装入每个 `SnapshotChunk` envelope，因此不形成哈希环；每个 chunk 的 `manifest_id` 必须逐字节等于已验证 ID，不得跨 manifest 拼接。signer bitmap/subset 和导出者签名不进入 manifest ID。

Snapshot API 将最终性证明作为独立传输 envelope 返回，不把证书 signer subset 塞入 manifest：

```text
GetSnapshotManifestResponse {
    manifest: SnapshotManifest
    finality_proof: FinalityProof
}

GetCheckpointSnapshotManifestResponse {
    manifest: SnapshotManifest
    checkpoint_finality_proof: CheckpointFinalityProof
}

GetSnapshotChunkRequest {
    manifest_id: Hash32
    chunk_index: uint64
}

GetSnapshotChunkResponse {
    chunk: SnapshotChunk
}
```

客户端必须在发请求前按本地 trust-root 类型选择 `GetSnapshotManifest` 或 `GetCheckpointSnapshotManifest`；两种响应是不同 Schema，未知 proof 字段直接拒绝，不能在验证失败后改试另一方法。导入者调用对应的 `VerifyFinalityProof` 或 `VerifyCheckpointFinalityProof` 后，再要求其 Header/statement 的 network、ledger、epoch、height、`FinalizedBlockID`、finality ID、state root、block MMR root、epoch emitted Vertex count/root、ValidatorSet hash 和 ProtocolConfig hash 与 manifest 的所有 `target_*` 字段逐项匹配，且 `target_height>=1`。`target_block_mmr_peaks` 还必须满足 MMR 规范 level/覆盖组合，并以 `leaf_count=target_height` 重算出 `target_block_mmr_root`；只有 root 而缺少 peaks 的 snapshot 不能继续追加后续块。

随后逐 chunk 验证 core hash、`siblings_bottom_up` inclusion 和 manifest root，通过后才写 staging；完整下载后再验证连续 offset、framing、record count 和 key-hash 严格顺序，从空 SMT 重建全部 present leaves，并要求结果精确等于 `target_state_root`。断点至少保存 manifest ID、最后 proof-verified chunk、下一 byte/record 游标、framed-decoder 边界与部分 SMT staging generation；重启时重验 manifest/proof 后幂等续传。staging 未完成时不得改变 active state。状态 Snapshot 自身不恢复 emitted exact set：full Validator 必须完成下一节的同 target checkpoint，并以 29-field `SnapshotInstallMarkerV1` 原子切换 state/MMR/derivation/cursor；state-only 节点必须写独立 26-field `QuerySnapshotInstallMarkerV1` 后才可切只读 state/MMR，保持 `SYNCING_DERIVATION`。两种 marker 都 hash-chain 到 previous active marker；marker fsync 后/pointer 前崩溃必须 roll-forward。Snapshot 自身不授予最终性，即使来自多数 peer 也不能替代精确匹配的 `FinalityProof`。

### 9.6 DAG derivation checkpoint sync

当前 epoch 中途恢复的 Validator 通过 `/finalweave/1/dag-derivation-checkpoint/sync` 获取协议第 18.4 节冻结的 `DAGDerivationCheckpointManifest` 与 `DAGDerivationCheckpointChunk`。manifest 必须绑定同一认证 Header 的 network、ledger、epoch、height、FinalizedBlockID、finality ID、committed slot、epoch emitted count/root；编码固定为严格升序、无重复的 raw Hash32 串，1 MiB 分块，chunk envelope 上限 `1_050_823` bytes。客户端逐块验证 manifest ID、core hash、Merkle inclusion、index/offset/长度，再从 epoch 空 sparse set 重建完整 exact set；缺失或额外一个 ID 都会改变 count/root并拒绝。

API 对基础与 checkpoint trust root 使用互斥响应，不能由服务端选择或 fallback：

```text
GetDAGDerivationCheckpointManifestResponse {
    manifest: DAGDerivationCheckpointManifest
    finality_proof: FinalityProof
}

GetCheckpointDAGDerivationCheckpointManifestResponse {
    manifest: DAGDerivationCheckpointManifest
    checkpoint_finality_proof: CheckpointFinalityProof
}

GetDAGDerivationCheckpointChunkRequest {
    manifest_id: Hash32
    chunk_index: uint64
}

GetDAGDerivationCheckpointChunkResponse {
    chunk: DAGDerivationCheckpointChunk
}
```

验证顺序固定为：本地 trust-store 预选 proof 类型并验证 → 精确匹配 target Header → 验证 manifest/chunks → 重建 sparse set root/count → fsync derivation generation。不能先接受 peer 自报的 emitted set 再补 proof。wire checkpoint 不进入 Header，避免 `Header -> FinalizedBlockID -> manifest -> Header` 环；它也不证明历史 Direct/Indirect 决策，仍需同步未决窗口与必要 `DAGCommitWitness`。

状态 Snapshot 与 checkpoint target 完全一致后，full-validator 安装把 state generation、MMR peaks、derivation generation 和 `certified_resume_cursor=Header.committed_slot` 放入同一个 `SnapshotInstallMarkerV1` 事务。marker fsync 后崩溃必须 roll-forward；仅安装 Snapshot 的查询节点由 `QuerySnapshotInstallMarkerV1` 线性化且不得签任何协议消息，也不能作为 H+1 certified publication 的 previous anchor。最新 validator-resumable Snapshot 对应的 checkpoint payload必须一起保留；若 payload 被合法裁剪，该 Snapshot 降级为 query-only，不得静默恢复签名能力。

### 9.7 DAG catch-up

Validator 恢复签名能力前还需补齐安全窗口内的 BatchAC、Vertex、stable-slot 决策和 attestation 状态。仅同步最终 Header 不足以安全创建新 Vertex。

## 10. Proof-carrying query

### 10.1 FinalityProof

外部最终查询统一返回字段 `finality_proof`，其验证入口为：

```text
VerifyFinalityProof(trust_anchor, finality_proof)
```

以 Genesis 为根的基础证明 schema 固定为：

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

验证者从带外期望的 network、ledger 与 `genesis_reference` 开始，调用完整参数的规范入口 `ValidateGenesisCertificate`：在分配前强制 policy/certificate count+byte 绝对边界；验证 policy signer key ID/strict key/排序/正 weight/threshold 和 approvals；调用 epoch-0 ValidatorSet/ProtocolConfig 结构验证并重算其 hash；按协议无条件重算 `epoch0_seed`、空 MMR root、policy hash 与 statement reference。任何未知/重复 signer、错派生字段/reference/domain/key generation、无效签名或 weight 溢出都拒绝，不能因 reference 匹配而跳过。proof 不携完整 Genesis state manifest，故 state root 的信任明确来自带外 reference；实际安装则必须调用 `ValidateGenesisInstallation` 从完整 bundle 重建 state root并验证 Feature/Gas bundle。

随后按顺序处理 old epoch `e` 的每个 transition：先要求 seal statement 的 old epoch 等于 `e`，调用 `ValidateValidatorSet(next_validator_set,e+1,expected_network_id)`，再使用“当前的旧 `ValidatorSet`”Consensus Key 验证 seal 上恰好 q 个不同 signer，核对 next validator/config hash，并调用 `ValidateProtocolConfigStructure(next_protocol_config,next_validator_set)` 校验无需执行对象即可判定的结构、范围和引用约束，最后才把 set/config 切换为下一跳的当前集合。old epoch 必须从 0 连续递增到 Header 的目标 epoch；零个 transition 只对目标 epoch 0 有效；只携带目标 `ValidatorSet` 而缺少中间旧集合公钥无法验证多跳签名，必须拒绝。

到达目标 epoch 后，重算 `target_feature_set` 和 `target_gas_schedule` 的 `(network_id,ZERO_ID)` 内容 ID，要求分别等于目标 `ProtocolConfig` 中的两个 hash，并调用 `ValidateExecutionConfigBundle(target_protocol_config,target_feature_set,target_gas_schedule,target_validator_set)` 验证全部 typed feature parameters、gas bounds 及跨对象约束。中间 epoch 的 FeatureSet/GasSchedule 不在每个 transition 中重复内嵌；仅验证目标最终性时不需它们，但从 Genesis 做全历史重放时必须按每个已认证 `ProtocolConfig` 的内容 hash 另行流式获取并验证完整中间对象。缺少它们意味“暂不具备全历史重放能力”，不意味目标 `FinalityProof` 无效。

`transitions` 允许增量 canonical-CBOR 解码、分段获取和已验证前缀缓存。本地 `api.maxInclusionProofBytes` 只约束单个 Merkle/SMT/MMR/status inclusion-proof 响应，不属于 `ProtocolConfig`，也不约束从 Genesis 增量验证的 epoch transition chain；节点可背压或分页，不得因目标 epoch 较高或本地单响应字节上限把密码学有效的链判为无效。

得到目标集合后，再验证 Header 的规范 hash/`FinalizedBlockID`、恰好 q 个不同 Ed25519 Consensus Key signer，以及 `FinalityStatement` 对 Header ID、state root、当前 block MMR root、validator/config hash 的精确绑定。`merkle_proofs` 必须按 `(tree_kind,index,item_hash)` 排序且唯一，每个期望 root 只能取自同一 Header、Receipt 或被该 Header 认证的对象。查询响应为方便读取而重复携带的 Header 必须与 `finality_proof.finalized_block_header` 逐字节相同。`DAGCommitWitness` 只用于全节点同步、审计和争议分析，不是公共 proof 必选字段。终态证明字段固定为 `finality_proof`；任何本地“finalized=true”布尔值都不能替代它。

### 10.2 CheckpointFinalityProof

Checkpoint 路径的 wire schema 逐字段复用协议第 15.1 节：

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

`VerifyCheckpointFinalityProof(expected_anchor_id,proof)` 先重算 `CHECKPOINT_TRUST_ANCHOR` ID，并要求等于 proof 与本地只读 trust store 的两个 ID；随后验证 anchor 的 set/config/bundle hashes 和以 `leaf_count=height` 重算的 MMR peaks/root。验证状态从 anchor epoch/set/config 开始，transitions 的首个 `old_epoch` 必须等于 anchor epoch，之后严格连续并始终使用当前旧集合验证 seal；第一跳 final height 不得早于 anchor，等高时 block/state/MMR 必须相同。到达目标 epoch 后验证目标 FeatureSet/GasSchedule bundle、Header/statement 和 q 个当前 Consensus Key signer；目标高度不得早于 anchor，等高目标必须逐项等于 anchor。最后验证排序唯一的 `merkle_proofs`。

Checkpoint 是显式增加的运营信任假设。`GetCheckpointFinalityProof` 可以传输 anchor 内容，却没有权限安装它；安装或轮换期望 anchor ID 必须走独立治理/离线审批接口并留下审计记录。节点不得从“多数 peer 返回相同 anchor”、HTTPS 成功或 proof 内自报 ID 推导信任，也不得在 checkpoint verifier 失败后回退到基础 verifier。Checkpoint 路径只能证明 anchor 之后的最终性；若需审计更早历史，仍须保存并验证 Genesis 基础链。

### 10.3 交易与收据

```text
FinalizedTransactionResponse {
    transaction
    transaction_index
    transaction_proof: MerkleProof
    receipt
    receipt_proof: MerkleProof
    finalized_block_header
}
```

普通查询响应故意不内嵌未类型化的最终性证明。SDK 必须先按本地 trust mode 调用并验证 `GetFinalityProof` 或 `GetCheckpointFinalityProof(expected_anchor_id)`，取得一个已验证 Header context；随后要求响应的 `finalized_block_header` 与该 context 的 Header 逐字节相同，再按以下顺序验证：交易/收据规范 hash，`MerkleProof` 的 tree kind、index、item hash 和位置绑定 path，Receipt 与 tx id/index/height 的对应，以及 Header roots。若所选最终性证明也携带同一 inclusion proof，则两份必须逐字段相同；否则查询中的具名 proof 自身验证到已认证 Header 即已充分。服务端不能把任一种 proof 塞入未登记字段，SDK 也不能在验证一种 trust path 失败后改试另一种。

### 10.4 状态查询

```text
StateQueryResponse {
    namespace
    key
    value optional
    sparse_merkle_proof
    finalized_block_header
}
```

不存在证明与存在证明使用同一深度 256 SMT 规则，并且该 Header 必须先以上述调用方选定的基础或 checkpoint 最终性方法验证。`SparseMerkleProof` 必须带 `schema_version`、原始 `namespace/key`、可选 `value` 以及恰好 256 个 `siblings_top_down`；验证者重算 `STATE_KEY(namespace,key)`，按 key hash 从 MSB 到 LSB 选择路径，并从 `present_leaf(0x01 || key_hash || value_hash)` 或 `empty[256]` 开始，按 depth `255..0` 逆向与 sibling 重建带 `U16BE(depth)` 的 node，最后包装 `STATE_ROOT(tree_depth=256,tree_top)` 并与已认证 Header `state_root` 比较。

`STATE_VALUE` 显式区分 `{presence:0}` 与 `{presence:1,value}`；因此空 byte-string 是“存在且值为空”，不是删除。删除将 SMT 叶恢复为 `empty[256]`，`StateChange` 的缺失一侧则使用 `absent_value_hash`。同一 key hash 对应不同原始 namespace/key 是碰撞安全事件，节点必须 `SAFETY_HALT`。服务节点索引只负责找到数据，不能替代 root proof。

### 10.5 事件查询

事件查询必须区分两条证明路径：

- `BLOCK_EVENT` proof 以连续全局索引 `g` 验证 `{transaction_index:i,event_index:j,event}` 对 Header `event_root` 的成员关系；块事件只能按 `(i ASC,j ASC)` 展平，使用 `BLOCK_EVENT_ITEM/BLOCK_EVENT_LEAF/BLOCK_EVENT_NODE/BLOCK_EVENT_ROOT` domain。
- `PER_TX_EVENT` proof 以局部索引 `j` 验证 `event` 对该 `Receipt.event_root` 的成员关系，使用 `EVENT/EVENT_LEAF/EVENT_NODE/EVENT_ROOT` domain；该 Receipt 还必须通过 receipt proof 绑定到 Header `receipt_root`。

如果响应同时返回两条路径，其 `{i,j,event}` 必须完全相同。空 per-tx 列表和空 block 列表各自使用自己 ROOT domain 的 count-zero root；不得把 per-tx roots 的列表当作 Header event root，也不得按 topic/emitter 重排。topic/account 索引仅用于定位候选，不是认证数据。

## 11. 交易提交、状态与证据

### 11.1 SubmitTransaction

请求必须传输完整 `TransactionEnvelope { intent, signer_policy, signatures }`。Gateway 重算 `signer_policy_hash`、`tx_intent_hash` 和 `tx_id`；不得丢弃 SignerPolicy，也不得改写签名语义中的 `gas_limit`、`fee_limit`、`priority_class`、`payload_type` 或 `authorized_access_scope`。Gateway 以 active verified bundle 执行同一 payload/Feature static registry；v1 非零 `fee_limit` 或非零 `priority_class` 直接 `STATIC_INVALID`。租户 QoS 由已认证 API principal 的链外队列/配额实现，不能信任用户自报 priority，也不能改变 canonical occurrence 顺序；本地估算 Gas 或资源不足不能替代共识 occurrence filter。

`intent.sender` 是 32-byte `AccountAddress`。POLICY_V1 地址按下式生成，且地址在后续 policy 轮换中保持不变：

```text
AccountAddressCore {
    schema_version: uint16
    address_scheme: POLICY_V1
    creation_salt: Hash32
    initial_signer_policy_hash: Hash32
}

account_address = DomainHash(
    "ACCOUNT_ADDRESS", network_id, ZERO_ID,
    canonical(AccountAddressCore)
)
```

Account key ID、SignerPolicy hash 和 `AccountAddress` 都是 `(network_id,ZERO_ID)` scoped；交易 Intent、签名摘要、nonce 和账户状态仍绑定真实 `LedgerID`。`creation_salt` 允许同一初始策略创建不同地址，且同一地址可显式加入多个 Ledger，但交易不能因此跨账本重放。账户状态固定位于 UTF-8 namespace `finalweave/v1/account/meta`、`finalweave/v1/account/nonce` 和 `finalweave/v1/account/auth`，三者 StateKey 的原始 key 都是该 32-byte `AccountAddress`，且必须全部存在或全部不存在。

Gateway 应使用当前最终状态的完整 `AccountMetadataState/AccountAuthState/AccountNonceState` 做 admission 预检，拒绝明显自造、未激活、已撤销或残缺状态；但这只是缓存时点判断。普通交易不携 AccountAddressCore，也不在每次提交时重新派生地址，权威判定由高度 h 的 occurrence filter 使用父 state root 认证的完整三元组和块开始授权策略完成，且 h 内策略轮换固定在 h+1 生效。

对不存在账户，唯一入口是 `payload_type=1` 的 `ACCOUNT_CREATE_V1`：payload 必须是 canonical `CreateAccountPayloadV1{schema_version:1,address_core}`，`nonce=0`，用户 `authorized_access_scope` 为空，重算地址等于 sender，Envelope policy hash 同时等于 core/Intent 承诺且 signatures 达到该 policy threshold。Gateway 可据此做无状态自证预检，并查询当前 tip 的 meta/auth/nonce 三项均不存在；最终 filter 仍以父认证状态为准。协议 resolver 注入三个保留 key 的 `EXACT WRITE` system access，Gas 由 operation `0x00010001` 的完整固定 trace 计量。通过预检的原生创建 winner 原子写 immutable meta、auth、nonce 并置 `next_nonce=1`；普通模块和用户 scope 不得写/删除三个 namespace。其他不存在账户、已存在/残缺账户上的创建、或同块第二个创建均为 `AUTH_INVALID_OCCURRENCE`。`ACCEPTED` 因而不承诺该 Envelope 将获授权或成为 nonce winner；最终认证无效 occurrence 不进 transaction tree、不产 Receipt、不耗 nonce。

RPC 超时表示结果未知。客户端重试同一规范交易并查询 tx id，不能立即更换 nonce 或把 `ACCEPTED` 当最终成功。

本地请求预算必须满足 `api.maxRequestBytes >= checked_add(max_transaction_bytes,65_536)`；启动时若不满足则 SubmitTransaction endpoint 不得进入 ready。该式是 canonical binary 请求的最低护栏，不允许把 HTTP JSON/base64/multipart 的 wire bytes 与 canonical envelope bytes 混为一谈：这些表示的 raw request 上限必须使用经 checked arithmetic 计算的表示层膨胀预算，或在独立、受限的流式解码器中先消除 framing/base64，再对 canonical `TransactionEnvelope` 执行 `max_transaction_bytes` 校验。任何 canonical envelope 不超过协议上限的交易，都不能仅因 JSON/base64/multipart 膨胀而被本地 transport cap 拒绝；流式路径仍须同时限制压缩后输入、解压输出、字段数和临时磁盘，不能靠无限 buffering 实现兼容。

### 11.2 稳定状态

```text
UNKNOWN
PENDING
FINALIZED_SUCCESS
FINALIZED_FAILED
EXPIRED
REPLACED
```

`FINALIZED_FAILED` 只表示真正进入 transaction tree、消费 nonce 并执行后的业务失败/revert；static-invalid、auth-invalid、stale、future、duplicate、nonce conflict、窗口过滤和 `BLOCK_CAP` 都没有 Receipt，不能伪装成失败终态。

### 11.3 本地诊断阶段

`PENDING` 可附：

```text
MEMPOOL
BATCHED
DA_CERTIFIED
DAG_REFERENCED
SLOT_SUPPORTED
ORDER_FINAL
EXECUTED_LOCAL
FINALITY_CERTIFIED
COMMITTING
```

它们来自单节点观察，可以回退、跳跃或消失，不是共识事实。只有带可验证 terminal evidence 的稳定状态不可回退。

### 11.4 Terminal evidence

- `FINALIZED_SUCCESS/FAILED`：原交易、nonce-consuming Receipt、两类 inclusion proof、Header 和 `finality_proof`；
- `REPLACED`：queried envelope、窗口内 `candidate_height` 的具名 authorization context（candidate执行前父state anchor/proof、candidate epoch bundle/可选activation、sender meta/auth/nonce三份SMT proof），再加同sender/nonce、不同tx id的最终winner、nonce-consuming Receipt、inclusion proofs和终态`finality_proof`；winner成功或业务失败都证明替换；
- `EXPIRED`：先以同样的candidate parent-state context证明queried envelope在某候选块开始时确由sender active policy授权，再提供满足`tip.height > valid_until_height`的Header、终态`finality_proof`及该Header `state_root`下固定nonce key的SMT proof。present value必须解码`AccountNonceState{schema_version:1}`并证明`next_nonce <= queried nonce`；non-inclusion只允许已由authorization context完整自证的`ACCOUNT_CREATE_V1`，普通不存在账户交易不能取得终态；
- `UNKNOWN/PENDING`：不得伪造 terminal proof。

若 `next_nonce > queried nonce`，不得返回 EXPIRED，必须寻找原交易 finality 或同 slot winner 的 replacement 证据。若 winner 已最终，`REPLACED` 优先于 loser 后来的过期诊断。

authorization context与终态proof必须属于同一具名evidence schema。普通路径父proof精确认证`candidate_height-1`；TRUST_ROOT_STATE只允许Genesis首块或预置checkpoint的下一块。candidate与父同epoch时bundle匹配父context；跨epoch首块必须带terminal proof chain中唯一activation transition，已sealed parent不能用旧epoch config伪造H+1。普通账户要求父状态完整meta/auth/nonce三元组、candidate height active policy/signatures匹配且`next_nonce<=nonce`；nonce超过本地future gap仍可能在同块前序winner推进后合法，不得拒绝。账户创建要求父状态三项non-inclusion及完整创建规则。历史状态/config已裁剪且Archive不可得时返回`PROOF_UNAVAILABLE/HISTORY_PRUNED`，不能使用candidate post-state或退化为自签envelope。

账户 `next_nonce` 严格单调、不可删除或重置，并进入状态根和快照。因此不需要也禁止引入 `nonce_winner` 表、历史 tx-id seen-set、intent outcome map 等会因裁剪失去安全语义的共识状态。`tx_location` 仅是可重建查询索引。

## 12. API 表面

最小方法：

```text
SubmitTransaction
GetTransactionStatus
GetTransactionStatusEvidence
GetCheckpointTransactionStatusEvidence
GetFinalizedBlockByHeight
GetFinalityProof
GetCheckpointFinalityProof
GetTransaction
GetReceipt
GetState
GetEvents
GetLedgerStatus
GetValidatorSet
GetProtocolConfig
GetFeatureSetByHash
GetGasScheduleByHash
GetSnapshotManifest
GetCheckpointSnapshotManifest
GetSnapshotChunk
GetDAGDerivationCheckpointManifest
GetCheckpointDAGDerivationCheckpointManifest
GetDAGDerivationCheckpointChunk
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

所有读 API 接受明确 consistency：`FINALIZED` 是默认；`LOCAL_ORDERED` 和 `LOCAL_EXECUTED` 仅限授权诊断，并必须返回非最终标记且不附伪造 proof。

`GetFinalityProof` 必须支持 server-streaming/分段传输从 Genesis 开始的超长 `transitions`；`GetCheckpointFinalityProof` 使用独立方法并显式绑定请求方已预置的 anchor ID。分段边界只是传输 framing，不产生另一种 proof schema。客户端增量解码并持久化已验证的 old-epoch 前缀，重连后从该前缀后继续；服务端不得将本地 `api.maxInclusionProofBytes` 用于拒绝合法 epoch chain。全历史 replay 通过 `GetFeatureSetByHash/GetGasScheduleByHash` 按已认证 `ProtocolConfig` 的 network-scoped hash 获取中间对象。

跨账本 API 的信任方向必须固定：relayer 先从已验证的目标 Header/config 取得 active `CrossLedgerSourcePolicyV1`，再把精确 `destination_policy_id/trust_root_kind/trust_root_id` 带入 `GetCrossLedgerSourceProof`。source 服务可以返回匹配的 `CrossLedgerProofEnvelopeV1` 或 `HISTORY_PRUNED`，但不能换 root kind、返回“最新 checkpoint”或让响应安装 policy。目标 Validator/SDK 必须以 active policy 重新验证；proof envelope hash 只作传输审计，source event ID 与 consumption key 必须独立重算。

`GetCrossLedgerConsumption` 与普通 state query 一样不内嵌未类型化 target finality proof：响应携 target Header、consumed value/SMT proof，以及可选成功 transaction/Receipt/Event paths；调用方先按自己的 target trust mode 验证同一 Header。`CONSUMED` 要求 state inclusion。

`GetCrossLedgerExpiredUnconsumedEvidence` 请求显式绑定 expected target network/ledger、`destination_policy_id`与`source_event_id`；响应返回规范 `CrossLedgerExpiredUnconsumedEvidenceV1`，含完整 source proof、一个 message window 内且 FeatureSet列出 exact policy的历史 policy Header/FeatureSet、一个严格晚于 until 的 tip Header，以及 tip state root下 consumption-key non-inclusion。它不内嵌任意类型 target finality proof；客户端必须为历史 Header和 tip Header分别调用自己已经选定的 `GetFinalityProof` 或 `GetCheckpointFinalityProof`，验证得到的两个 Header context逐字节匹配后，要求 evidence重算的 policy/source-event IDs等于请求 expected值，才用历史 policy验证 source proof并用 tip验证 non-inclusion。policy后来删除不影响该路径；反之，仅 current policy inactive、索引 miss、relayer未见、历史 context不在 message window或任一 Header未独立最终验证都不能伪装成 `EXPIRED_UNCONSUMED`。服务端缺历史 FeatureSet/source Body时返回 `HISTORY_PRUNED`，不能换用新 policy/checkpoint。

终态证据同样使用两个显式方法：`GetTransactionStatusEvidence` 只返回含基础 `FinalityProof` 的 `TransactionStatusEvidence`；`GetCheckpointTransactionStatusEvidence` 必须在请求中携带调用方已经预置的 `expected_anchor_id`，只返回含 `CheckpointFinalityProof` 的 `CheckpointTransactionStatusEvidence`。响应中的 anchor、proof 类型或字段形状不能改变请求方信任模式，两个方法不能互相 fallback，也不能把一种 evidence 缓存在另一种 domain/ID 下。

分页 token 绑定 ledger、查询条件、finalized height 和排序键并签名/MAC；索引追赶期间返回明确 `INDEX_LAGGING`，不得悄悄漏项。

稳定错误码至少区分：规范解码、签名、权限、资源上限、未找到、尚未最终、证明不可用、`HISTORY_PRUNED`、节点同步中、索引落后、限流、内部安全停机。`HISTORY_PRUNED` 只表示当前服务节点已按非共识 `LocalHistoryPolicy` 裁掉对象，可附带不受信任的 Archive URI；它不能替代 FINALIZED 状态或 proof。SDK 从 locator 获取后仍完整验证 Header/FinalityProof/inclusion proof。错误字符串不作为 SDK 控制流。

## 13. SDK 验证器

SDK 必须默认验证而非仅解析：

```text
VerifyFinalityProof
VerifyCheckpointFinalityProof
VerifyTransactionInclusion
VerifyReceiptInclusion
VerifyEventInclusion
VerifyStateProof
VerifyTransactionStatusEvidence
VerifyCheckpointTransactionStatusEvidence
VerifyDAGDerivationCheckpoint
VerifyCheckpointDAGDerivationCheckpoint
VerifyCrossLedgerSourceProof
DeriveCrossLedgerSourceEventID
DeriveCrossLedgerConsumptionKey
VerifyCrossLedgerConsumedState
VerifyCrossLedgerConsumptionResponse
VerifyCrossLedgerExpiredUnconsumedEvidence
```

验证器拒绝未知 schema/algorithm、重复 signer、非规范 bitmap、错误 epoch、错误 network/ledger 和多余的互斥证据变体。沿 Genesis 路径缓存的新 epoch 验证状态必须来自已验证 config/validator transition；安装或轮换运营 checkpoint anchor 则必须走独立授权的 trust-store 管理流程。两者都不能由普通 API 响应替换。

`VerifyCrossLedgerExpiredUnconsumedEvidence` 的调用参数必须包含请求方 expected target、policy ID、source event ID、历史 `VerifiedTargetHeaderContext` 与 tip `VerifiedTargetHeaderContext`；它不能只接收一个自描述 evidence对象。两个 context可以分别来自基础或调用方预置 checkpoint trust mode，但各自的 proof类型必须在调用前确定，响应不能触发 fallback。

## 14. 分区、背压与降级

- 少于 q 个 Validator 可通信：不能形成新的 BatchAC、stable progress 或 FinalityCertificate；已最终查询保持可用；
- DA 正常但 DAG 落后：限制新 Batch，优先 Vertex/父对象；
- 排序正常但执行落后：对 DAG 输入施加 backpressure，优先执行和 attestation；
- attestation 聚合落后：重传相同签名对象，不重新签 digest；
- 状态提交慢：保留证书和 PreparedExecution，公开游标不越过原子提交；
- 查询流量过载：先拒绝历史/复杂查询，不能挤占 P0/P1。
- 跨账本 proof 洪泛：先做 bounded outer parse、target账户鉴权、tx窗口/exact nonce、policy/relayer/RequiredGas/success-reserve前缀，再进入独立低优先级 verifier pool；canonical scan与source verifier都按 authenticated containing Vertex occurrence sponsor保留work份额并另设 shared余量，Batch author、relayer、peer和slot proposer不得替代sponsor。f个恶意Vertex作者反复引用任何Batch也不能耗用honest sponsor reserve。不得挤占 DAG/finality，也不得把本地 queue drop解释为链上无效。

## 15. 可观测性

至少暴露：

- `finalweave_p2p_message_total{protocol,result}`；
- `finalweave_p2p_inflight_bytes{protocol,ledger}`；
- `finalweave_orphan_vertices{ledger}`；
- `finalweave_sync_height{ledger,stage}`；
- `finalweave_finality_attestation_lag{ledger}`；
- `finalweave_query_proof_verify_seconds{type}`；
- `finalweave_tx_progress_total{stage}`；
- `finalweave_peer_invalid_object_total{type}`；
- `finalweave_cross_ledger_proof_bytes{source_ledger,root_kind}`；
- `finalweave_cross_ledger_relayer_lag_heights{source_ledger,target_ledger}`；
- `finalweave_cross_ledger_consumption_total{result}`。

账户、tx id 和 peer id 不作为高基数 metric label。安全调查标识进入受控结构化日志。

## 16. 验收清单

- [ ] 所有核心 protocol id 有独立消息上限、fuzz corpus 和权限策略。
- [ ] PeerHello 只有 7-field core/3-field envelope，不携应用 nonce；TLS 1.3 双向 certificate、ALPN `finalweave-p2p/1`、leaf Peer key 和每次握手的新 `tls_exporter` 全部绑定且 QUIC/TCP 语义一致。
- [ ] 每条 Validator/P0/P1 消息都以 `(ledger_id,message_epoch,validator_set_hash,peer_id,protocol_id)` 动态授权；epoch 激活原子失效旧 cache，连接和 stream 都不保存全局 Validator 权力。
- [ ] 本地 P2P key/certificate/ALPN/mutual-certificate/frame/NONE-only 配置在 listener、readiness 和 signer 开放前完成原子校验，失败热重载不产生部分切换。
- [ ] P2P v1 拒绝除 `[NONE]` 外的压缩列表；HTTP Gateway 或未来版本所有启用压缩的消息都有有限 `MAX_CANONICAL_BYTES`，由 streaming counter 在首个越界 byte、decoder/staging/声明长度分配之前中止；没有上限的消息不得启用压缩。
- [ ] 大 fragment 不阻塞 Vertex、attestation 和 certificate。
- [ ] DAG orphan 有界且缺失依赖可恢复。
- [ ] CausalInput manifest/chunk 使用固定 framed stream、1 MiB 分块、逐 chunk proof、可背压断点续传；不完整 stream 绝不形成 Header/attestation。
- [ ] Header/snapshot/body 从恶意来源获得时仍由 proof/root 拒绝。
- [ ] Snapshot manifest/chunk 使用固定 framed stream、1 MiB 分块和 manifest ID，且由本地 trust-root 类型选定的基础 `FinalityProof` 或 `CheckpointFinalityProof` 与全部 target 字段逐项匹配。
- [ ] CausalInput/Snapshot canonical envelope 在 P2P v1 由原始字节 bounded reader、在 HTTP Gateway/未来压缩 profile 由 streaming decompressor/running counter 最多读取 `MAX+1`；第 `1_050_824` byte 不进入 parser/staging，任何声明长度驱动的大对象分配都在上限验证之后，P2P 路径不实例化解压器。
- [ ] `api.maxInclusionProofBytes` 只限制单个 inclusion proof，长 epoch transition chain 可增量获取；SubmitTransaction 的本地 transport 预算不会拒绝协议有效的 canonical envelope。
- [ ] API 和 SDK 只使用 `finality_proof`。
- [ ] 普通 SubmitTransaction 只按认证账户三元组授权；`ACCOUNT_CREATE_V1` 是不存在账户唯一自证例外，meta/auth/nonce 原子建立且同块不可立即使用。
- [ ] 九个 progress stage 在文档、API enum 和指标中一致。
- [ ] 四种 terminal evidence 严格互斥并符合 `next_nonce` 规则。
- [ ] 不存在 `nonce_winner` 或历史 seen-set 依赖。
- [ ] 分区和慢执行只停止进展，不产生冲突最终结果。
