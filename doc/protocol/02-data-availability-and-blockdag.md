# FinalWeave v1 BatchAC 与已签名、无独立顶点证书的元数据 DAG 规范

> 协议：FinalDAG-C v1
> 前置：[数据模型与密码学](01-data-model-and-cryptography.md)
> 后续：[FinalDAG-C 共识](03-finaldag-consensus.md)

## 1. 设计边界

FinalDAG-C 将大数据可用性和共识元数据传播明确分开：

```text
Transaction
  -> Mempool
  -> BatchBody
  -> Reed-Solomon fragments
  -> 重构、重编码和持久化
  -> BatchAC
  -> DAGVertex 只引用 BatchID/ac_id
  -> uncertified metadata DAG
```

BatchAC 证明交易批次在故障模型内可恢复。DAG 强边表达 Vertex 因果关系和隐式共识 support。两者不得混用：

- DAG parent 不能替代 BatchAC；
- BatchAC 不决定 proposer slot 是否提交；
- FinalDAG-C v1 不存在 VertexAck 或 VertexCertificate；
- AC signer subset 不影响 BatchID、VertexID 或排序。

这样可以在 DAG round 快速推进的同时，让 Batch Worker、fragment 网络和磁盘恢复在独立流水线中运行。

## 2. 故障门槛

每个 ledger/epoch：

```text
n = 3f + 1
q = 2f + 1
k = f + 1
parity_shards = 2f
```

最多 `f` 个 Validator 可以任意行为，包括：

- 为同一 Batch slot 创建不同 Header；
- 发送错误、不同或截断 fragment；
- ACK 后拒绝提供 fragment；
- 为同一 round 发布多个 Vertex；
- 选择性传播 Vertex 或依赖；
- 引用不存在、跨 epoch 或过期对象；
- 发送极远未来 round 消耗资源。

安全性不依赖及时网络；Batch 和 DAG 的持续活性依赖 GST 后至少 `q` 个诚实 Validator 能在有限时间内互通和持久化。

## 3. Mempool

### 3.1 Admission

节点按以下顺序处理提交：

1. 请求鉴权、速率、字节数和账本状态；
2. deterministic CBOR 和 schema；
3. NetworkID、LedgerID 和 feature；
4. 重算 `tx_intent_hash` 和 `tx_id`；
5. 验证账户签名和静态 signer policy，并以最新 FinalityProof 状态预检当前 active policy；
6. 检查 payload、gas、签名数和访问声明上限；
7. 对有效高度窗口做宽松检查；
8. 从最终 state root 读取认证 `next_nonce`；
9. `nonce < next_nonce` 可以在本地拒绝，但缓存不能替代状态 proof；
10. 进入 sender/nonce 队列。

Submit 只有：

```text
ACCEPTED
REJECTED
```

`ACCEPTED` 不表示广播、可用性、DAG support、排序或最终性。入口的 active-policy 结果也只是针对当前最终 tip 的 admission 建议；最终 occurrence 必须按其实际块高度的块开始认证状态重验。

### 3.2 本地索引

允许：

```text
by_tx_id
by_sender_nonce
ready_queue
future_queue
inflight_batch
```

Bloom/Cuckoo/seen cache 只能节约查询或广播。假阳性不得成为共识拒绝、重放判定或终态依据。协议状态不新增历史 tx-id、intent seen 或 nonce winner 映射。

### 3.3 稳定状态与诊断阶段

稳定 API 状态：

```text
UNKNOWN
PENDING
FINALIZED_SUCCESS
FINALIZED_FAILED
EXPIRED
REPLACED
```

本地诊断 `progress_stage` 可以为：

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

诊断阶段可回退、消失且不跨节点一致，绝不能驱动资产交付。终态证明见[第四篇](04-finality-execution-and-epochs.md)。

## 4. Batch 构建

### 4.1 选择

Batch Worker 可以使用 priority、进入时间、sender 公平性、组织配额和连续 nonce 做本地选择。策略不属于共识。

Batch 可以包含尚未到 `valid_from_height`、执行时已 stale、重复或冲突的 occurrence。它们不会 poison Batch；最终执行层按实际 FinalizedBlock height 和 `next_nonce` 过滤。

### 4.2 构建步骤

作者：

1. 选择 `1..max_batch_transactions` 个有序 TransactionEnvelope；在按 count 创建数组前先检查该范围；
2. 重新执行静态规范检查；
3. 构造 `BatchBody{schema_version:1,transactions}`；
4. 计算每个 tx_id 和 transaction root；
5. 用带 checked counter 的 canonical streaming encoder 编码完整 BatchBody；counter 最多接受 `max_batch_body_bytes` bytes，并只探测下一个输出 byte，探测到第一个越界 byte 时立即放弃且不把它写入 buffer；实现不得用可回绕的 `max_batch_body_bytes + 1` 表达式，也不得先构造无界内存 buffer；
6. 要求 `len(canonical(BatchBody)) <= max_batch_body_bytes`，并令 Header 的 `transaction_count` 和 `body_length` 分别精确等于实际数组长度与完整规范字节长度；
7. 计算 body hash；
8. 只有上述 count/bytes 检查通过后才以 v1 固定参数分配空间并做 Reed-Solomon 编码；
9. 为 fragments 构建 Merkle Tree；
10. 构造 BatchHeaderCore；
11. 在 Safety WAL 写入 kind 16 `BATCH_AUTHOR_INTENT`，锁定 own Batch slot 与精确 BatchID，并把 canonical body、`BatchRetentionManifestV1(AUTHOR_BODY)` 与 active ref 放入同一 durable group；
12. 全部 fsync 后才用 DAG Key 签 BatchHeader；
13. 给 Validator `i` 发送 index `i` 的 fragment，并开放 fragment repair。

完整尺寸的唯一口径是第一篇的 `len(canonical(BatchBody))`。压缩后字节、TransactionEnvelope 长度之和、payload 长度或语言对象估算都不能代替该值；`MaxSingleTxBatchCanonicalBytes` 的配置约束保证任一达到交易上限的单个合法 Envelope 仍有可编码 Batch。

Batch slot：

```text
(ledger_id, epoch, author_index, batch_seq)
```

一个诚实作者在同一 slot 只能签一个 BatchID。已签但未获 AC 的 Batch 可以继续重传，不能复用 batch_seq 创建新内容。

## 5. Reed-Solomon 编码

### 5.1 参数

```text
n = 3f + 1
k = f + 1
m = 2f
```

使用 GF(2^8) 时 `n <= 253`，因而 `f <= 84`。更大 ValidatorSet 需要新编码方案和协议版本。

### 5.2 规范布局

`RS_GF256_V1` 不绑定某个软件库，而冻结下列数学与字节布局：

```text
field                 = GF(2^8)
primitive_polynomial  = x^8 + x^4 + x^3 + x^2 + 1  // 0x11d
field_byte            = polynomial-basis coefficient byte 0..255
x_i                   = field element (i + 1), i=0..n-1
V[i,j]                = x_i^j, j=0..k-1
G                     = V * inverse(V[0..k-1, 0..k-1])
generator_matrix_id   = ASCII("FW_RS_GF256_V1_VANDERMONDE_11D")
zero_padding_rule     = 0  // 只在 canonical body 尾部补 0x00
shard_length          = ceil(body_length / k)
```

前 `k` 行的 G 必须是 identity，因此 fragments `0..k-1` 是 systematic data shards：把 canonical BatchBody bytes 只在尾部补零到 `k*shard_length`，再按连续等长区间切分。对每个 byte offset，fragment `i` 的 byte 是 `sum_j G[i,j]*data_fragment[j][offset]`。任何 `k` 个不同 index fragment 通过相应 G 子矩阵求逆恢复 systematic shards；恢复后严格截断到 Header 的 `body_length`，被截去区域必须全为 `0x00`。

fragment index 与当前 ValidatorIndex 一一对应，fragment bytes 长度必须恰好等于 `shard_length`。`coding_context_hash`、专用 fragment leaf/root 和 BatchID 的无环次序见第一篇。不同语言实现必须对相同 BatchBody 产生逐字节相同的全部 `n` 个 fragments；协议向量至少覆盖 `body_length mod k` 为 0/1/k-1、最大 n、任意 k 子集恢复及单字节错误拒绝。

### 5.3 为什么 ACK 前必须重构和重编码

单一 fragment 的 Merkle proof 只证明该 fragment 位于作者承诺的集合，不能证明所有 fragments 是同一有效 Reed-Solomon codeword。

若节点只验证自己的 Merkle path 就 ACK，Byzantine 作者可以承诺一组彼此不一致、无法恢复出 body 的 fragments，却仍收集 quorum ACK。

因此 FinalDAG-C v1 不把“收到一个 shard”定义为可用性投票。

## 6. Fragment 接收和 ACK

Validator `i` 收到 BatchHeader 和 fragment 后：

1. 以固定小 Header 上限解析 schema、network、ledger、epoch 和 slot；
2. 在 fragment dependency fetch、shard buffer 或 RS matrix 分配前，要求 `1 <= Header.transaction_count <= max_batch_transactions`、`0 < Header.body_length <= max_batch_body_bytes`，并以 checked arithmetic 计算 `shard_length=ceil(body_length/k)`；
3. 检查作者 DAG Key 签名，并检查 `n/k/m` 与该 epoch 配置；
4. 用 Header 导出的精确 `shard_length` 和 ValidatorSet 导出的精确 Merkle 层数有界解析 fragment；在为 `fragment_bytes` 或 path 分配前拒绝任何不等于该 shard 长度的声明、任何不等于该层数的 path count，再检查 fragment index 与 Merkle path；
5. 从作者或 peers 收集至少 `k` 个不同 index fragments；
6. 在受 `Header.body_length` 硬上限保护的内存或 spill writer 中恢复完整 BatchBody bytes；
7. 检查恢复字节长度、尾部零填充、Header `body_length` 和 canonical CBOR；
8. 要求实际 `len(canonical(BatchBody)) <= max_batch_body_bytes`，重算 `body_hash`，并要求实际字节数/哈希与 Header 完全一致；
9. 增量解码 transactions；读到实际 array count 后、分配交易容器前，要求 `1 <= actual_count <= max_batch_transactions` 且精确等于 Header `transaction_count`。对每项先用 bounded canonical reader 取得精确 envelope 边界，要求 `len(canonical(TransactionEnvelope)) <= max_transaction_bytes`，并验证 schema/tag/必需字段、SignerPolicy/signature/access 数组等协议绝对 count/bytes cap；不得先按未验证长度分配。此处不验证账户签名、nonce、有效窗口或业务语义，它们仍由 occurrence filter 处理；
10. 全部交易通过上述可表示性边界后，按原始数组顺序重算 transaction root；任何一项为 `max_transaction_bytes+1` 都使整个 Batch 不可 ACK，不能形成“BatchAC 有效但 causal stream 无法规范解码”的对象；
11. 由协议版本、`n/k`、body hash/length 和固定 RS profile 重算 `coding_context_hash`；
12. 重新编码出全部 `n` 个 fragments；
13. 重建并检查 fragment root；
14. 把自己固定 index 的规范 fragment 持久化，并检查 `fragment_index == signer_index`；
15. 持久化 `CODEWORD_VERIFIED(batch_id)`；
16. 持久化 `DA_ACK_LOCK`；
17. fsync；
18. 用 DAG Key 签 DA_ACK。

DA_ACK 的签名摘要是：

```text
DomainHash(
  "DA_ACK", network_id, ledger_id,
  canonical(DataAvailabilityAckCore)
)
```

任一步失败都不得签 ACK。实现可以缓存恢复结果或用 worker pool 并行，但不能降低上述验证语义。

### 6.1 DA_ACK_LOCK

防双签 key：

```text
(ledger_id, epoch, batch_author_index, batch_seq, ack_signer_index)
```

若相同 slot 已锁定不同 BatchID，节点必须拒绝新 ACK、保存 equivocation evidence，并报告安全事件。

## 7. BatchAC

聚合者收集同一 `DataAvailabilityStatement` 的 ACK：

1. 按 signer index 去重；
2. 验证每个 ACK 的 batch、slot 和 fragment index，并强制 `fragment_index == signer_index`；
3. 达到 `q` 后，选择任意合法的恰好 `q` 个 ACK；
4. 按 index 升序编码 bitmap 和 ACK 数组；
5. 发布 AvailabilityCertificate。

### 7.1 可恢复性证明

BatchAC 有 `q=2f+1` 个 signer，最多 `f` 个 Byzantine，因此至少 `f+1=k` 个诚实 signer：

- 已验证同一 BatchID；
- 已恢复并重编码同一 codeword；
- 分别持久化由 `fragment_index == signer_index` 一一映射决定的不同 fragment。

即使全部 Byzantine signer 拒绝服务，仍有 `k` 个不同诚实 fragments 可恢复 BatchBody。

### 7.2 signer-subset 规则

多个 AC envelope 可能有不同 signer subset。节点必须：

- 用 `ac_id=Hash(DataAvailabilityStatement)` 作为语义 key；
- 将所有有效 envelope 视为等价证明；
- DAGVertex 只引用 BatchID/ac_id；
- 不用 AC envelope hash 选择 parent、slot、epoch seed 或执行顺序；
- 可以保留 signer 覆盖更适合取数的 envelope，但这只是本地优化。

不存在“全网最小 q signer”一类依赖未来消息的规范化规则。

## 8. Batch repair 与读取

需要完整 BatchBody 的节点：

1. 解析 BatchHeader 后先执行 ACK 路径相同的声明 `transaction_count/body_length`、`n/k` 与 checked shard-length 上限检查；超限时不得请求 fragment；
2. 验证 BatchHeader 签名和任一合法 AC；
3. 优先向 AC signer 请求其固定 fragment；
4. 并行请求不同 index，避免单 peer 阻塞；
5. 收齐 `k` 个后在受限 writer 中恢复；
6. 重复 ACK 前对实际完整 canonical BatchBody bytes、实际 transaction count、每笔 TransactionEnvelope 的 `max_transaction_bytes` 与绝对子对象 cap、body hash、transaction root 和重编码的全部校验；
7. 对错误 fragment 生成 peer evidence，但不能把未认证网络错误直接当作链上惩罚证据。

请求必须设置 per-peer、per-batch、总字节和并发限制。fragment 响应在发送前按 Header root 本地复验，避免磁盘损坏传播。

## 9. 元数据 DAG Vertex 构造

### 9.1 round 1

每个 epoch 确定性派生恰好 `q` 个 synthetic round-0 anchor。它们不是签名 DAGVertex，不含 Batch 或 proposal 语义：

```text
previous_epoch_reference =
  genesis_reference                         if epoch == 0
  previous EpochSealStatement semantic ID  otherwise

GenesisAnchorID(i) = DomainHash(
  "DAG_GENESIS_ANCHOR", network_id, ledger_id,
  canonical({
    epoch,
    anchor_index: i,
    epoch_seed,
    validator_set_hash,
    protocol_config_hash,
    previous_epoch_reference
  })
) for i = 0 .. q-1
```

round 1 Vertex 的 `own_parent` 与 `rejoin_checkpoint` 必须为空，`strong_parents` 必须恰好是按 `anchor_index` 升序的全部 `q` 个 GenesisAnchorID，`weak_parents` 必须为空。接收者从已经认证的 Genesis/EpochSeal 上下文重算，不通过网络抓取 anchor，也不得把它解码成 DAGVertex、计作 proposer 或继续 DFS。GenesisAnchor 只是 terminal edge token，永不属于 `Past`、`VertexDelta`、`GloballyEmittedVertices` 或 ordered-vertex tree。该特殊规则只建立 epoch 初始 threshold clock；round `r>1` 才使用普通上一轮父规则。

round 1 与普通 round 的作者都必须在写签名意图前检查 `len(availability_references) <= max_batches_per_vertex`、strong/weak parent 数量上限、`execution_attestations/evidence_refs` 固定绝对 count cap，并用 64-byte signature placeholder 构造完整 `DAGVertex` 规范 envelope，要求 `len(canonical(DAGVertex)) <= max_vertex_bytes`；真实 Ed25519 signature 也是固定 64 bytes，签名后仍须复验相同完整长度。

round 1 与普通 round 的诚实作者在签名前还必须完整预验每个拟引用 Batch 中会展开的 transaction occurrence，并对待引用的合法 Batch公平调度。引用不要求 `BatchHeader.author_index == DAGVertex.author_index`；一旦当前 Vertex 被签名，其作者就不可撤销地成为这些 raw occurrence 的 sponsor，并承担通用 scan/common 与已激活 source-proof 调度的本作者份额。该职责属于诚实 signer/本地 readiness 规则；接收者不重放目标执行状态来判断 Vertex wire validity，但能从 Vertex签名和引用精确恢复 sponsor。

### 9.2 普通 round

作者创建 round `r>1` Vertex 前必须：

1. 取得至少 `q` 个 round `r-1`、不同作者的有效 Vertex；
2. 取得并验证自己已签署的最高 lower-round own Vertex；普通 gap 小于 `dag_gc_rounds` 时写 `own_parent` 且不写 rejoin。gap 已达窗口时，必须选择同 epoch、距新 round 小于窗口的 certified Header，构造并验证该 prior own ID 对 Header emitted root 的 256 层 membership/non-membership proof，先 fsync 唯一 `VERTEX_REJOIN_INTENT`，再令 `own_parent=null` 并写 `rejoin_checkpoint`；没有这种 checkpoint 时保持 not-ready，不能伪造 parent；
3. 选择规范 strong parent 集；
4. 选择有界 weak parents；
5. 只加入已经验证 BatchAC、且其相关 occurrence 已在本地按当前最终状态完成完整静态/账户/治理/跨账本预验的 AvailabilityReference；跨作者引用允许，但当前 Vertex 作者将成为这些 occurrence 的 sponsor，必须用自己的后续验证保留份额承担费用，并对待引用的合法 Batch公平调度；
6. 加入待传播的执行 attestation 和 evidence refs；
7. 写入 `epoch_closing`；
8. 检查 AvailabilityReference、strong/weak parent 数量，并以固定 signature placeholder 检查完整 signed-envelope `max_vertex_bytes`；
9. 持久化 `VERTEX_SIGN_INTENT` 和完整 core digest；
10. fsync；
11. 使用 DAG Key 签名、复验完整 envelope 长度并 multicast。

### 9.3 strong parent

要求：

- 来自恰好上一 round；
- 至少 `q`、至多配置上限，v1 默认上限 `n`；
- 按 `(author_index, vertex_id)` 排序；
- 同一作者最多一个 strong parent；
- 普通 own parent 指向作者最高 lower-round Vertex；若其 round 恰好为 `r-1`，它可以同时计入 strong parents，否则不能计入上一轮 quorum；
- round>1 必须恰好选择普通 own parent 或认证 rejoin checkpoint 之一。普通 parent 满足 checked `1 <= child.round-own_parent.round < dag_gc_rounds`；rejoin 的 proof、recent committed-slot gap 和 signer WAL 规则以第一篇为准，rejoin terminal ref 不计 strong quorum，也不进入 `Past`；
- `min(n,q+1) <= max_strong_parents <= n`；额外一个位置允许同一 Vertex 同时承担“支持上一轮及时 primary”和“为前一轮 primary 纳入 q 个 certificate supporters”两项义务；
- 先锁定 timer 内到达并已选定的上一轮 primary 作为 `timely_primary_parent`；若其作者存在 equivocation，该 parent 的 VertexID 也随本轮签名意图固定；
- 再为前一轮 primary 从支持者中按 `(author_index,vertex_id)` 选择与 `timely_primary_parent` 兼容的规范最小 q 个不同作者：同作者只能选择同一个 VertexID。存在兼容 q 集合时它们都是 required parents；不存在时不能声称已满足 certificate-parent 义务，只能按 certificate timer 的超时分支继续；
- required parents 的并集最多 `q+1` 个。放入集合后，其余候选按 `(author_index,vertex_id)` 填充到 `max_strong_parents` 或当前候选耗尽；对没有 required 身份的同作者 equivocation 使用 VertexID 最小者。完整 core 写入 WAL 后，迟到的更小 VertexID 不得改变或重签该 round。

primary-support 义务是诚实 signer/pacemaker 规则，不是接收者可从单个 Vertex 证明的 wire-validity 条件：构造 support-round Vertex 时，若 primary 在 timer 到期前有效到达，strong parents 必须包含已经锁定的 `timely_primary_parent`；构造 decision-round Vertex 时，只有在按上述同作者约束仍存在兼容的 `q` 个 primary supporters 时，才必须包含规范最小的兼容 q 集合。接收者只验证父签名、round、不同作者、规范排序和数量；本地 timer、候选兼容性和曾看到哪些迟到消息不能追溯改变对象有效性。

强边用于：

- threshold logical clock；
- support DFS；
- certificate pattern；
- direct/indirect 决策。

强边不证明所引用 Batch 的数据可用性；BatchAC 才证明。

### 9.4 weak parent

weak parent 可以引用更老 round 中尚未被 strong closure 覆盖的 Vertex。选择规则：

```text
(oldest round, author_index, vertex_id)
```

按该顺序填充到 `max_weak_parents`。弱边：

- 不计入 round quorum；
- weak-parent 作者本身不作为 direct support/skip 的独立计票者；但 weak edge 仍按 own/strong/weak 规范次序进入 Support DFS，其祖先可影响当前 Vertex 的 sticky support；
- wire-validity 固定要求 checked `1 <= child.round-parent.round < dag_gc_rounds`；等于窗口即无效。该共识可验证相对年龄取代节点各自不同的本地 GC cursor，不能以“我还没 GC”放宽；
- 可以使老 Vertex 的 Batch 最终进入某个 committed causal delta。

## 10. Vertex 接收

接收 Vertex 时按顺序：

1. 通过带 checked counter 的 bounded reader 增量解析 framing 与 deterministic CBOR；reader 最多接受 `max_vertex_bytes` bytes，并只探测下一个输入 byte，探测到第一个越界 byte 时立即拒绝；实现不得用可回绕的 `max_vertex_bytes + 1` 表达式，且在按任何 array count 分配前检查它能被剩余完整-envelope 字节上限容纳；
2. 要求完整 `len(canonical(DAGVertex)) <= max_vertex_bytes`、`len(strong_parents) <= max_strong_parents`、`len(weak_parents) <= max_weak_parents`、`len(availability_references) <= max_batches_per_vertex`、`len(execution_attestations) <= EXECUTION_ATTESTATIONS_PER_VERTEX_MAX_V1`、`len(evidence_refs) <= EVIDENCE_REFS_PER_VERTEX_MAX_V1`；这里的字节数包含 core 与 author signature。所有六个 count 都在 dependency fetch、签名验证或对应数组分配前检查；
3. 检查 network、ledger、epoch 和 `round>=1`；
4. 验证 author DAG Key 签名并重算 VertexID；
5. 检查 own/rejoin/strong/weak parent 格式；若 `round==1`，验证 own/rejoin/weak 为空且 strong 恰好为本地重算的 q 个 GenesisAnchorID，并跳过普通父 Vertex 获取；若 `round>1`，own parent 与 rejoin checkpoint 必须恰好存在一个；rejoin proof 数组声明 count 必须在分配前等于 256；
6. 仅当 `round>1` 时检查至少 `q` 个上一 round 不同作者，且同作者最多一个 strong parent；
7. 第 1–6 步通过只得到 `INTRINSIC_VALID`，还不是可用于 support 的完整 Vertex。若其精确 ID 已在某个已接纳 root/证书/witness/anchor 的 pending dependency set 中，则标记 `DEPENDENCY_REQUIRED`；否则 author-fair root scheduler 对每个 `(epoch,round,author)` 最多接纳一个尚无引用的 `CONSENSUS_ROOT_ADMITTED`。同槽其余纯旁路 sibling 在此停止深验证，只进入固定三重硬上限的 unreferenced-sibling quarantine；配额满时可驱逐或拒绝缓存，但只能返回 `RESOURCE_DEFERRED`，不能写永久 invalid；
8. 只有 `DEPENDENCY_REQUIRED` 或 `CONSENSUS_ROOT_ADMITTED` 才建立 durable dependency-promotion intent并取得全部签名父 Vertex。每个父对象递归执行同一 intrinsic/dependency 验证，完整成功并 fsync dependency store 后才原子关闭该精确引用边；遇到 round-1 anchor token 以外无法解析为签名 DAGVertex 的 parent ID 必须拒绝。resolved own/strong/weak parent 的 network、ledger、epoch 必须逐项等于 containing Vertex；普通 parent 的 author/round 还要满足对应边规则，own/weak gap 必须小于 `dag_gc_rounds`。若为 rejoin，则改为验证同 epoch Header/FinalityCertificate、recent committed slot 与 exact-set proof；rejoin ref 是 terminal control proof，不作为 DAG parent 继续 DFS；
9. 获取并验证每个 AvailabilityReference 的 BatchHeader 和任一 AC；Header、AC statement 的 network、ledger、epoch、batch ID/author/seq 必须彼此一致并逐项等于 containing Vertex 的 network/ledger/epoch，禁止新 epoch Vertex 引用旧 epoch 的合法 BatchAC；Batch author可以不同于 containing Vertex author，后者的已验证 `author_index` 固定成为该引用展开出的全部 occurrence sponsor，不能由 BatchHeader、gossip peer或本地到达顺序覆盖；
10. 取得每个完整 FinalityStatement并验证 execution attestation 的 Consensus Key签名；statement 的 network、ledger、epoch必须等于 containing Vertex，按 `(statement_id,signer_index)` 唯一，同 signer/height冲突只记 evidence、不重复计票。`evidence_refs` 只是作者签名的审计 hint：接收关键路径只验证 Hash32 数量/排序，不得同步解析或拉取其完整 evidence，因而随机/缺失/被驱逐的 ref 不影响 Vertex validity、support 或闭包。异步 evidence worker在共享 quarantine/evidence 硬预算内按 ID获取；若取得，则重算 ID并要求 evidence 的network/ledger/epoch等于 containing Vertex，否则只丢弃该 hint并记录作者质量事件；
11. 第 8–10 步和完整递归闭包全部成功后，原子提升 root、关闭 promotion intent 并插入共识 DAG store，才可触发 decider。此前 root 始终为 `PENDING_DEPENDENCY`，不能产生 support、direct/skip、commit、ordered output 或 attestation。

父对象或 AC 缺失时进入有界 dependency fetch lane，不能先作为 strong parent 使用。dependency lane 与旁路 quarantine 分账并预留容量；合法引用后来命中已驱逐 sibling 时，节点必须按 exact VertexID 先查 quarantine，再向引用来源、其他 Validator 与 Archive 执行有界 `dag/sync`。取回并完整验证后原子提升到 dependency store；提升对象不得降回 quarantine，只能在不再被任何 undecided slot、候选闭包、已认证恢复点或保留窗口引用后按 DAG GC 规则删除。暂时取不到依赖只让该分支保持 pending，不使引用它的 wire 对象无效，也不得阻止其他闭包完整的诚实 author lane 推进。

“已进入 author-fair 共识工作队列”不是“签名有效”的同义词。调度器对 `(ledger,epoch,author)` 保留独立 root/dependency 工作份额，旁路 sibling、单一 peer 或单一 Byzantine author 不能消耗诚实 author 的保留份额；否则攻击者可用一条 DAG key 构造无限 sibling，再用无限子分支绕过 quarantine。工作预算耗尽时保持 deferred/pending，不能改变任何共识判定。

## 11. 为什么 DAG 不再认证 Vertex

FinalDAG-C v1 对每个 Vertex 只有作者的一次 DAG Key 签名。下一轮 Vertex 的强边就是 support。

若加入 VertexAck/VertexCertificate，会产生：

```text
Vertex multicast
  -> q VertexAck
  -> certificate multicast
```

这会增加两次传播、每轮大量签名操作和 catch-up 验证开销，并重新把 DAG 变成显式认证 DAG。

FinalDAG-C 的安全性来自：

- own-parent-first 的 sticky support；
- `q` support 和 certificate patterns；
- direct/indirect slot 决策；
- 输出不能越过最早 undecided slot；
- FinalityCertificate 对最终执行前缀再认证。

因此实现不得私自增加“只有 Vertex 获得 q ACK 才能进入 DAG”的规则，否则会破坏协议时延和不同实现的活性互操作。

## 12. Equivocation

同一 `(epoch, round, author_index)` 的不同 VertexID 构成 equivocation。

```text
DAGEquivocationEvidence {
  schema_version: uint16
  first_vertex: DAGVertex
  second_vertex: DAGVertex
}
```

三个字段按展示顺序使用 deterministic-CBOR integer key `1..3`，`schema_version=1`。完整 evidence 的 active-config 上限为 checked `5 + 2 * max_vertex_bytes`，并且不得超过全局 `DAG_EQUIVOCATION_EVIDENCE_MAX_CANONICAL_BYTES_V1=33_554_437`；接收端先检查消息长度，再用 bounded incremental decoder 分别执行两次 Vertex 上限与签名验证，禁止按未验证嵌套长度一次性分配。

两份 Vertex 必须具有相同 `(network,ledger,epoch,round,author_index)`、不同 VertexID 和有效作者签名，并按 VertexID 升序放入 first/second。证据 ID 为：

```text
evidence_id = DomainHash(
  "DAG_EQUIVOCATION", network_id, ledger_id,
  canonical(DAGEquivocationEvidence)
)
```

`DAGVertex.evidence_refs` 引用该 ID，但只是可丢弃、非共识的审计 hint。它不能成为 DAGVertex 的 validity/dependency 条件，否则一个最多只有 32 KiB hash refs 的 Vertex 可强迫同步下载约 32 GiB evidence。接收者不得在 support 路径解析这些 ref；异步取得完整对象后才按本节验证并写有界 cache。只见到一份对象、无效签名或同 ID 重放都不是 equivocation evidence。

节点：

- 对已提升到 dependency store、可能进入 `Past(P)` 的冲突 Vertex，完整保留每个被引用分支及其 payload occurrence，不能用 evidence 替代；
- 对纯旁路 gossip，每个 slot 的本地 evidence cache 至多保留当前已验证集合中按 VertexID 升序最小的两份完整签名对象；看到更小 ID 时替换较大者，因此同一观察集合的 pair 与到达顺序无关。该 cache 仍计入 quarantine 的 per-slot、总对象和总字节硬预算，可整体驱逐，且不参与 support、slot decision、`Past(P)` 或 ordered output；
- 传播有界 evidence ref；
- 不把作者从当前 ValidatorSet 动态移除；
- 继续依照 support DFS 和 slot decider 处理；
- 在治理层记录惩罚候选。

动态剔除会改变 quorum，必须留到 epoch 边界。

## 13. DAG 公平性和抗审查

所有 Validator 都可以持续创建 Batch 和 Vertex，proposer slot 只决定哪些 Vertex 可以直接驱动最终 slot 前缀，不限制数据生产。

公平性机制：

- 每个作者的 Batch 和 Vertex 字节配额；
- weak-parent oldest-first；
- proposer schedule 轮转；
- 记录 accepted→BatchAC→DAG→finalized 延迟；
- finalized 历史上的持续遗漏可以成为下一 epoch 治理更换成员的证据；
- FinalDAG-C v1 proposer schedule 不读取 reputation 或本地性能分数。

协议保证诚实数据在部分同步和有界负载下最终可纳入，不保证某笔交易固定时间内必然最终。

## 14. 资源限制

ProtocolConfig 冻结影响对象合法性和共识资源边界的字段：

```text
max_transaction_bytes
max_batch_body_bytes
max_batch_transactions
max_batches_per_vertex
max_strong_parents
max_weak_parents
max_vertex_bytes
max_future_round_gap
```

`max_dependency_fetches`、`max_fragment_repairs`、`max_unresolved_batches`、每 peer 并发/字节等是本地抗 DoS 与同步预算，不进入 ProtocolConfig。达到本地预算时节点必须背压、换 peer、落盘或稍后重试，不能据此永久判定一个协议有效对象无效；Validator 若资源不足以持续满足链上上限，应撤销 readiness 而不是形成不同 DAG 语义。

Batch 的声明 count/body length 必须在 fragment fetch、RS matrix/shard buffer 分配前检查，恢复后再以实际完整 canonical BatchBody 复验；Vertex 必须以 bounded reader 在 dependency fetch 前同时检查数组 count 和完整 signed-envelope 字节。协议超限对象在解析或分配大内存前拒绝。任何 peer 都不能通过声明超大 count、Merkle path 或 future round 迫使节点分配对应容量。第一篇的 `MaxSingleTxBatchCanonicalBytes` 与 `MinRequiredVertexCanonicalBytes` 是所有实现共同的配置交叉约束，不能只在配置工具中提示。

## 15. Retention 与 GC 前置条件

Batch fragment 至少保留到满足全部条件：

- Batch 所属 epoch 的 closing FinalizedBlock、FinalityCertificate 与 `EPOCH_CLOSED` 已原子持久化；第一次消费、当前未见引用或 DAG round 前进都不能证明未来 Vertex 不再引用同一 BatchAC；
- 已按第四篇从该 `EPOCH_CLOSED.final_height` 唯一认证的 `terminal_height` 经过 ProtocolConfig 的 `batch_retention_heights`；
- 所有 publication/repair/export/pinned witness 对该 fragment 的引用均已结束；
- block body/archive 已达到部署 `LocalHistoryPolicy` 的冗余目标，且任何更长的合规/争议服务窗口也已结束。
- 对本地 AUTHOR_BODY/ACK_FRAGMENT 每项义务，独立 `BatchGCRecordV1` 已在 append-only hash chain 中 fsync；record 前不得删流中任一字节，record 后才释放 active ref。

后两类部署策略不进入协议 hash，也不能缩短前述链上 retention 或未决依赖条件；它们只会让运营方保留得更久。

DAGVertex 只有在 slot 已决定、相关 FinalizedBlock 已认证、同步 checkpoint 已覆盖且 unresolved 决策不再引用时才可 GC。完整规则见[第四篇](04-finality-execution-and-epochs.md)。

## 16. 测试要求

必须覆盖：

- 错误 fragment 仍有合法 Merkle path；
- `k` fragments 可恢复但重编码 root 不同；
- ACK 前崩溃和 fsync 后崩溃；
- BatchBody 总大小/count 合法但某笔 TransactionEnvelope 恰为 `max_transaction_bytes+1` 时，ACK 与 repair 都在 transaction root/签名/持久化前拒绝；恰等于上限可继续，invalid account signature 但结构/字节合法仍可进入 occurrence filter；
- 同一 BatchAC 在首次消费很久后被新 Vertex 再引用时 fragment 仍可恢复；epoch close 前即使无当前引用也禁止 GC，close 后在 inclusive release height 才允许；
- 同 Batch slot 两个 Header；
- 同 statement 不同 AC signer subset；
- ACK signer 拒绝 repair；
- Vertex 引用不存在或错误 AC；
- 同作者同 round equivocation：首个无引用 root 每 slot 只接纳一个；旁路 quarantine 在 `4 entries/slot`、`65_536 objects/Ledger`、`67_108_864 bytes/Ledger` 的 exact/MAX+1 有界，最小 evidence pair 对所有到达排列一致；
- sibling 先进入 quarantine、被驱逐、再由 honest child/已验证 witness 精确引用时能按 ID 重拉并原子提升；递归闭包完成前不产生 support/commit，完成后与依赖先到路径的 DAG/ordered root 一致；Byzantine child ancestry 只能消耗其 author 工作份额；
- `DAGEquivocationEvidence` 的 active `5+2*max_vertex_bytes` 与全局 `33_554_437` canonical bytes exact/+1，嵌套 Vertex 超限在分配前拒绝；
- strong parent 作者重复；
- weak edge 绕过 GC；
- 无 VertexAck/VC 时的选择性传播；
- `max_transaction_bytes` 边界 Envelope 放入单交易 Batch 时精确命中 `MaxSingleTxBatchCanonicalBytes`，配置少 1 byte 拒绝；Header 声明 count/body 超限时在 fragment fetch/RS 分配前拒绝，恢复后的实际 count/bytes 与 Header 不同也拒绝；
- `max_vertex_bytes == MinRequiredVertexCanonicalBytes(set)` 的 q+1-parent 完整 signed Vertex 接受，少 1 byte 的 ProtocolConfig 无效；AvailabilityReference 超 `max_batches_per_vertex` 或完整 envelope 多 1 byte 时在 parent/AC fetch 前拒绝；
- 大 Batch 恢复拥塞时 DAG metadata 仍能推进。
