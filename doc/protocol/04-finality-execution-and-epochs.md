# FinalWeave v1 最终性、执行证明与 Epoch 规范

> 协议：FinalDAG-C v1
> 前置：[数据模型与密码学](01-data-model-and-cryptography.md) · [BatchAC 与 DAG](02-data-availability-and-blockdag.md) · [共识与排序](03-finaldag-consensus.md)

## 1. 目标

FinalDAG-C 的 DAG decider 先产生唯一的 proposer slot 决策前缀。本文定义如何把该前缀转换为：

- 连续 FinalizedBlock 高度；
- 唯一 transaction/receipt/state roots；
- 可由轻客户端验证的 FinalityCertificate；
- 不依赖本地缓存判断的交易终态；
- 安全的 epoch close、恢复、同步和 GC。

关键分层：

```text
DAG decision
  决定唯一输入顺序

Deterministic execution
  计算唯一状态结果

ExecutionAttestation
  Validator 对结果签名

FinalityCertificate
  q 个签名证明最终块和状态根
```

DAGCommitWitness 可以证明排序推导过程，但不是公共 FinalityProof 的必选部分。

## 2. 从 COMMIT slot 派生 FinalizedBlock

### 2.1 高度

```text
创世初始状态 = height 0
第一个 COMMIT slot 派生的普通块 = height 1
后续普通块 = parent.height + 1
```

SKIP slot 不占高度。epoch 切换不重置高度。

### 2.2 Delta

对按全局顺序输出的 `COMMIT(P)`：

```text
VertexDelta(P) = Past(P) - GloballyEmittedVertices
```

按以下顺序线性化：

```text
(round ASC, author_index ASC, vertex_id ASC)
```

然后依次展开：

```text
Vertex
  -> availability_references 原始数组顺序
  -> BatchBody transactions 原始数组顺序
```

规范 CBOR 已冻结数组顺序。不能用 Batch 到达时间、AC signer 或 fragment provider 改变顺序。

实现必须按[数据模型第 13.1 节](01-data-model-and-cryptography.md#131-canonical-causal-input-stream)把整个 `VertexDelta` 及其全部 occurrence 编成规范 causal input stream，并在执行前或边执行边验证对应 manifest/chunks。内存、消息和数据库事务上限只能触发磁盘 spill、背压、暂停或更换数据源；不得截断 `Past(P)`、跳过剩余 Vertex/Batch，或把“本地装不下”解释成共识有效输入无效。ordered vertex root、occurrence filter 和后续 winner roots 都消费同一条已验证 stream。

同一 stream 中的全部 VertexID 还必须在签 attestation 前原子插入当前 epoch 的 copy-on-write `EpochEmittedVertexSet` generation，并按[数据模型第 13.3 节](01-data-model-and-cryptography.md#133-epochemittedvertexset)重算 Header 的累计 count/root。父 generation 中任何 ID 已存在、delta 内重复、缺失 non-membership path、count 溢出或 root 不一致都会拒绝候选。单块 `ordered_vertex_root` 不能替代该跨块 exact set，Snapshot 也不能单独恢复它。

### 2.3 Equivocation 和重复

同一 `(epoch,round,author)` 多个 Vertex：

- `VertexDelta` 保留 `Past(P)` 中所有不同 VertexID，并统一按 `(round,author_index,vertex_id)` 排序；
- committed proposer 自然作为其中一个 Vertex 保留，不覆盖或删除同槽其他签名对象；
- 每一份双签对象都可以另外形成 evidence，但 evidence 不替代其规范 payload occurrence；
- 相同 VertexID 只由 `GloballyEmittedVertices` 排除一次，不按作者、BatchID 或 tx_id 预去重。

这是共识输入闭包的一部分：不同实现不能因为本地“先看到哪一个 equivocation”而选择不同 payload。父数组、每 Vertex 引用数和对象字节均有协议上限；超出因果闭包之外的旁路 gossip 不进入该块。

这里的“因果闭包”只包含已经从精确 VertexID 引用递归提升到 dependency store 的对象。纯旁路 sibling quarantine 和其确定性最小 evidence pair 不参与 `Past(P)`；但一个已驱逐 sibling 若后来被接受的 Vertex、证书/witness 或 anchor 引用，必须按 ID 重拉、完整验证并永久提升，闭包完成前候选不得 commit 或签 attestation。由此，quarantine 的本地到达/驱逐差异不改变最终 ordered stream，而 Byzantine author 也不能靠无限未引用双签制造无限共识存储义务。

同一 BatchID 或相同 tx_id 的后续 occurrence 不自动成为历史状态。它们仍进入 occurrence filter，并由当前 FinalizedBlock 的规范单遍规则决定是否是 winner。

## 3. Occurrence filter

### 3.1 权威输入

过滤只读取：

- 当前 FinalizedBlock height；
- 父 state root 下每个 sender 的 `AccountMetadataState/AccountAuthState/AccountNonceState` 完整三元组或三项全无证明，并拒绝任何残缺三元组；
- 完整三元组中认证的 address metadata、`next_nonce`，以及按当前 height 从 auth state 解析出的 active signer policy hash；
- 当前块中此前已接受 winner 导致的 working next-nonce 增量、accepted tx-id 集合和 created-account 集合；
- occurrence 的规范交易字节和签名。

不得读取共识状态中的历史 tx-id、intent seen、nonce winner 或本地 Bloom 命中。FinalWeave v1 不维护这些状态。

### 3.2 单遍算法

v1 把 occurrence filter 中会消耗、却不进入 Receipt Gas 的输入扫描和通用昂贵前缀统一计为 prefilter verification work。扫描费必须在解析完整 Envelope 之前、只凭已验证因果位置给出的 occurrence sponsor和frame长度收取；sponsor固定为承载该 AvailabilityReference 的已签名 DAGVertex 作者，而不是被引用Batch的作者。昂贵后缀费在cheap winner前缀通过后向同一 sponsor收取：

```text
PREFILTER_HASH_CHUNK_BYTES_V1             = 1_024
PREFILTER_SUFFIX_BASE_UNITS_V1            = 1
PREFILTER_STRICT_PUBLIC_KEY_UNITS_V1      = 16
PREFILTER_ED25519_SIGNATURE_UNITS_V1      = 64
PREFILTER_COMPLEX_ENTRY_UNITS_V1          = 4

PrefilterScanWorkCostV1(causal_occurrence_item_canonical_length) =
  ceil_div(causal_occurrence_item_canonical_length,
           PREFILTER_HASH_CHUNK_BYTES_V1)

PrefilterExpensiveWorkCostV1(tx) = checked_sum(
  PREFILTER_SUFFIX_BASE_UNITS_V1,
  EmbeddedStrictEd25519PublicKeyOccurrences(tx)
    * PREFILTER_STRICT_PUBLIC_KEY_UNITS_V1,
  TargetAndGovernanceSignatureOccurrences(tx)
    * PREFILTER_ED25519_SIGNATURE_UNITS_V1,
  EmbeddedComplexRegistryEntryOccurrences(tx)
    * PREFILTER_COMPLEX_ENTRY_UNITS_V1
)

PrefilterVerificationWorkCostV1(item_length,tx) = checked_add(
  PrefilterScanWorkCostV1(item_length),
  PrefilterExpensiveWorkCostV1(tx)
)
```

`CausalOccurrenceItem` 的 frame 长度在增量解析其 TransactionEnvelope 前已经由已验证 causal source和8-byte frame前缀确定；该 source 同时给出 containing Vertex作者（sponsor）与Batch作者，二者都从已验证对象重算且可以不同。`MaxCausalOccurrenceItemCanonicalBytesV1(config)`用完整tagged `OCCURRENCE(CausalOccurrenceItem)`、最大宽度ordinal/reference/transaction index、完整BatchID与恰好`max_transaction_bytes`的Envelope placeholder执行deterministic-CBOR sizing。scan cap失败时仍须流式消费、逐字节比对已验证Vertex/Batch source并推进manifest cursor，但不得解码Envelope字段、计算tx ID、查SMT或启动crypto；规范分类为`PREFILTER_SCAN_CAP`。这把每字节放大后的parse/hash/state工作设为硬上限，但不伪称免除了验证任意有限causal delta所必需的线性stream I/O；后者只能由固定chunk、spill、sponsor-fair fetch和背压有界化空间并最终排空。

v1 的昂贵字段遍历表固定如下；只列出的typed field path产生occurrence units，字节相同也不去重：

| 计数器 | v1 field path |
|---|---|
| strict public key | `envelope.signer_policy.signers[*].public_key`；`ACCOUNT_POLICY_ROTATE_V1.new_signer_policy.signers[*].public_key`；`LEDGER_RECONFIGURE_V1.next_validator_set.validators[*].{dag_public_key,consensus_public_key,peer_public_key}` |
| target/governance signature | `envelope.signatures[*].signature`；`LEDGER_RECONFIGURE_V1.approvals[*].signature` |
| complex registry entry | `envelope.signer_policy.signers[*]`；`ACCOUNT_POLICY_ROTATE_V1.new_signer_policy.signers[*]`；`LEDGER_RECONFIGURE_V1.next_validator_set.validators[*]`、`next_feature_set.entries[*]`、`next_gas_schedule.entries[*]` |

`CROSS_LEDGER_CONSUME_V1.payload.proof_envelope` 的全部后代都明确排除在上述三类occurrence计数之外；它们的source keys/signatures/transition registry由本篇后述独立source-proof scheduler计量。完整CausalOccurrenceItem bytes仍收一次scan费，因为流式frame/canonical/source比对不是source密码学验证。未来payload/Feature若引入新的key、signature或typed registry path，必须先升级此遍历表和最大模板；未知路径不得被反射式“递归统计”，也不得静默零成本激活。

suffix base使每次真正进入worker并创建attempt map项都至少消耗1 unit；即使未来某个合法bounded outer恰好没有上述三类field occurrence，也不能形成零成本dispatcher、registry查找或exact-map增长路径。

`MaxSinglePrefilterVerificationWorkV1(config)` 不依赖当前 ValidatorSet大小；重配置可把小集合变成v1允许的253成员集合。令`C=PrefilterScanWorkCostV1(MaxCausalOccurrenceItemCanonicalBytesV1(config))`、`B=PREFILTER_SUFFIX_BASE_UNITS_V1`、`S=config.max_account_signers`、`A=config.max_signatures_per_transaction`，用checked arithmetic计算：

```text
BaseMax = C + B + S*16 + A*64 + S*4
RotateMax = C + B + (2*S)*16 + A*64 + (2*S)*4
ReconfigureMax = C + B
  + (S + 3*253)*16
  + (A + 1_024)*64
  + (S + 253 + 256 + 65_536)*4

MaxSinglePrefilterVerificationWorkV1(config) =
  max(BaseMax, RotateMax, ReconfigureMax)
```

这些是保守upper-bound templates：即使FeatureSet/GasSchedule各自极值因当前`max_transaction_bytes`不能同时装入，仍使用上述绝对entry上限，避免求解内容组合优化问题。ProtocolConfig必须令per-sponsor reserve不小于该值且不超过v1 hard cap；所有实现冻结相同golden。新增payload若不能给出field-path和最大公式就不能激活。

扫描开始时为ValidatorSet全部`n`个 occurrence sponsor各保留`prefilter_verification_work_reserve_per_occurrence_sponsor`，shared pool等于总cap减`n*reserve`。`TryChargePrefilter(sponsor,cost)`先用该sponsor剩余reserve，不足部分才用shared；只有两者足以覆盖完整cost时才原子扣款，失败完全不扣。每个raw occurrence先独立向其sponsor收scan费；通过cheap前缀后才向同一sponsor收suffix费。per-sponsor reserve覆盖一个最大合法item的两段总和，所以shared耗尽时，尚未花费份额的honest sponsor仍可完整验证一次最大候选。Batch author不控制另一个Vertex是否重引其Batch，因而绝不能作为费用归因；同一BatchAC被Byzantine Vertex反复引用时只会消耗那些Vertex作者自己的份额。

以下是本地可恢复checkpoint schema；它们不进入Header或共识树，但所有实现必须按字段顺序以deterministic CBOR编码并纳入checkpoint checksum。`status`固定为`1=STARTED,2=VALID,3=INVALID`：

```text
CausalOccurrenceCursorV1 {
  schema_version: uint16                 // 1
  causal_input_manifest_id: Hash32
  item_ordinal: uint64                   // 整个CausalInputItem序列的zero-based index
  occurrence_ordinal: uint64             // OCCURRENCE子序列的zero-based index
  frame_start_byte_offset: uint64        // 8-byte frame length prefix的起始offset
}

OccurrenceScanSourceBindingV1 {
  schema_version: uint16                 // 1
  cursor: CausalOccurrenceCursorV1
  vertex_ordinal: uint64
  availability_reference_index: uint32
  batch_id: Hash32
  batch_transaction_index: uint32
  item_canonical_length: uint64          // 不含8-byte frame prefix
  containing_vertex_author_index: uint16 // occurrence sponsor
  batch_author_index: uint16             // 仅绑定来源，不用于费用归因
}

PrefilterChargeReceiptV1 {
  schema_version: uint16                 // 1
  sponsor_author_index: uint16
  reserved_units: uint64
  shared_units: uint64
}

OccurrenceScanAttemptV1 {
  schema_version: uint16                 // 1
  status: uint16                         // 只允许STARTED
  origin_occurrence_cursor: CausalOccurrenceCursorV1
  source_binding_hash: Hash32
  sponsor_author_index: uint16
  item_length: uint64
  scan_work_cost: uint64
  charge_receipt: PrefilterChargeReceiptV1
}

PrefilterAttemptV1 {
  schema_version: uint16                 // 1
  status: uint16
  origin_occurrence_cursor: CausalOccurrenceCursorV1
  sponsor_author_index: uint16
  work_cost: uint64                      // 只含expensive suffix
  charge_receipt: PrefilterChargeReceiptV1
}

source_binding_hash = DomainHash(
  "OCCURRENCE_SCAN_SOURCE_BINDING", network_id, ledger_id,
  canonical(OccurrenceScanSourceBindingV1)
)
```

cursor四元组必须与已验证`CausalInputManifest`、framed-decoder progress和前序item/occurrence计数逐项一致；source binding的其余字段必须与已验证 containing Vertex/AvailabilityReference/BatchHeader/BatchBody重算一致，`containing_vertex_author_index`必须等于该Vertex的签名作者并作为唯一sponsor，`batch_author_index`必须等于BatchHeader作者。receipt要求`sponsor_author_index`匹配外层记录与source binding sponsor，且checked `reserved_units + shared_units == cost`。未知schema/status、字段缺失/多余、计数或offset错配都使checkpoint不可恢复，不能“尽量继续”。

checkpoint另存按sponsor索引的`completed_scan_reserved_units[0..n-1]`与`completed_scan_shared_units` checked累计量，不永久保存每个已完成occurrence的scan receipt。完成当前occurrence时，必须在同一原子事务把in-flight receipt并入这两个累计量、清除`OccurrenceScanAttemptV1`并推进cursor。恢复校验时，common prefilter的逐sponsor/shared总spend必须恰好由“completed scan累计量 + 可选in-flight scan receipt + `attempted_prefilter_tx_results`全部receipt”重算，remaining与total也必须由初始reserve/cap反推一致；每份receipt的sponsor还必须重验到origin containing Vertex签名作者。孤立扣款、重复receipt或只改累计量都使checkpoint损坏。

scan扣款本身也必须可恢复。filter checkpoint至多保存一个`OccurrenceScanAttemptV1`；scan扣款与该记录原子提交，durable cursor继续指向当前occurrence。恢复时逐项重验绑定并从item开头重扫但不再次扣费。只有完整验证/分类该occurrence后，才原子推进cursor并清除in-flight记录。若扣款与记录都未durable，就连同预算回滚到前一个completed-occurrence checkpoint；绝不允许只保留一侧。scan cap失败不扣款，可从旧checkpoint重新执行纯stream compare；它不得留下伪造的已验证进度。

昂贵suffix的exact map值不是一个裸布尔值，而是上述`PrefilterAttemptV1`。新attempt在调用worker前写`STARTED`并绑定当前causal occurrence cursor；invalid不退款，cache结果不改变逻辑扣款，suffix cap loser不写attempt。相同tx ID承诺完整Envelope，后续occurrence每次仍向各自containing Vertex sponsor收scan费，但复用已有terminal suffix结果；如果它此前因scan/suffix cap没有真正尝试，则可由另一sponsor的独占份额重试。诚实Vertex作者只引用已本地完整预验的Batch occurrence并公平调度，这是在f个恶意sponsor耗尽自身份额/shared时仍推进合法交易的活性前提。

```text
FilterOccurrences(height, ordered_occurrences, parent_state):
  working_next_nonce = lazy map backed by parent_state
  account_view = ResolveCompleteAccountTriples(parent_state, height)
  accepted_tx_ids_in_block = empty set
  created_accounts_in_block = empty set
  attempted_prefilter_tx_results = exact map tx_id -> PrefilterAttemptV1
  prefilter_sponsor_remaining[0..n-1] =
    config.prefilter_verification_work_reserve_per_occurrence_sponsor
  prefilter_shared_remaining = checked_sub(
    config.max_prefilter_verification_work_per_finalized_block,
    n * config.prefilter_verification_work_reserve_per_occurrence_sponsor)
  completed_scan_reserved_units[0..n-1] = 0
  completed_scan_shared_units = 0
  inflight_occurrence_scan = ABSENT
  winners = []
  reserved_gas = 0
  body_and_mandatory_write_reservation = EmptyBlockReservation(height)

  for occurrence in ordered_occurrences:
    source = VerifiedOccurrenceSourceAtCursor(occurrence.cursor)
      else CANDIDATE_INVALID
    sponsor = source.containing_vertex_author_index
    batch_author = source.authenticated_batch_author_index
    require sponsor in [0,n) and batch_author in [0,n) and
            sponsor == VerifiedContainingDAGVertex(
              source.vertex_ordinal).author_index
      else CANDIDATE_INVALID
    item_length = source.causal_occurrence_item_canonical_length
    scan_work = PrefilterScanWorkCostV1(item_length)
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
      "OCCURRENCE_SCAN_SOURCE_BINDING", network_id, ledger_id,
      canonical(source_binding))
    if inflight_occurrence_scan is ABSENT:
      scan_charge = TryChargePrefilter(sponsor, scan_work)
      if scan_charge failed:
        StreamCompareSourceBytesWithoutEnvelopeDecode(source)
          else CANDIDATE_INVALID
        mark PREFILTER_SCAN_CAP
        continue
      inflight_occurrence_scan = OccurrenceScanAttemptV1(
        schema_version=1,
        status=STARTED,
        origin_occurrence_cursor=occurrence.cursor,
        source_binding_hash=source_binding_hash,
        sponsor_author_index=sponsor,
        item_length=item_length,
        scan_work_cost=scan_work,
        charge_receipt=scan_charge)
      AtomicallyCheckpointChargeAndInflightScanWithoutAdvancingCursor()
    else:
      require inflight_occurrence_scan.status == STARTED and
              inflight_occurrence_scan.origin_occurrence_cursor == occurrence.cursor and
              inflight_occurrence_scan.source_binding_hash == source_binding_hash and
              inflight_occurrence_scan.sponsor_author_index == sponsor and
              inflight_occurrence_scan.item_length == item_length and
              inflight_occurrence_scan.scan_work_cost == scan_work
        else RECOVERY_STATE_CORRUPT

    // 只做bounded canonical结构、固定长度/数量、network/ledger与便宜字段检查；
    // 不做Ed25519、strict public-key、governance approval或完整next-bundle验证。
    if !BoundedCanonicalStructureAndCheapContextValid(occurrence, source):
      mark STATIC_INVALID
      continue

    tx = occurrence.transaction

    if tx.valid_from_height > height:
      mark FUTURE_HEIGHT
      continue

    if tx.valid_until_height < height:
      mark EXPIRED_OCCURRENCE
      continue

    if tx.tx_id in accepted_tx_ids_in_block:
      mark DUPLICATE_OCCURRENCE
      continue

    account = account_view.get(tx.sender)
    is_create = tx.payload_type == ACCOUNT_CREATE_V1

    if is_create:
      if account is PRESENT or
         tx.sender in created_accounts_in_block or
         !CheapCreateAccountContextPrecheckV1(tx, parent_state):
        mark AUTH_INVALID_OCCURRENCE
        continue
      next = 0
    else:
      if account is MISSING or
         tx.signer_policy_hash != account.active_policy_hash:
        mark AUTH_INVALID_OCCURRENCE
        continue
      next = working_next_nonce[tx.sender]

    if next == UINT64_MAX:
      mark NONCE_EXHAUSTED
      continue

    if tx.nonce < next:
      mark STALE_OR_DUPLICATE_NONCE
      continue

    if tx.nonce > next:
      mark FUTURE_NONCE
      continue

    if len(winners) >= config.max_transactions_per_finalized_block:
      mark BLOCK_CAP
      continue

    remaining_gas = checked_sub(
      config.max_execution_gas_per_finalized_block,
      reserved_gas
    ) else EXECUTION_HALT

    if tx.gas_limit > remaining_gas:
      mark BLOCK_CAP
      continue

    tx_index = len(winners)
    prepared = TryPrepareCheapPayloadCandidateAndBlockReservation(
      body_and_mandatory_write_reservation,
      tx,
      height,
      tx_index,
      active_bundle,
      parent_state
    )
    if prepared is deterministic static/policy/window reject:
      mark prepared.reject_class
      continue
    if prepared exceeds config block/body/write/event/return caps:
      mark BLOCK_CAP
      continue
    next_reservation = prepared.next_reservation

    attempt = attempted_prefilter_tx_results.get(tx.tx_id)
    if attempt is ABSENT:
      suffix_work = PrefilterExpensiveWorkCostV1(tx)
      suffix_charge = TryChargePrefilter(sponsor, suffix_work)
      if suffix_charge failed:
        mark PREFILTER_VERIFY_CAP
        continue
      attempt = PrefilterAttemptV1(
        schema_version=1,
        status=STARTED,
        origin_occurrence_cursor=occurrence.cursor,
        sponsor_author_index=sponsor,
        work_cost=suffix_work,
        charge_receipt=suffix_charge)
      attempted_prefilter_tx_results[tx.tx_id] = attempt

    if attempt.status == STARTED:
      require attempt.origin_occurrence_cursor == occurrence.cursor and
              attempt.sponsor_author_index == sponsor
        else RECOVERY_STATE_CORRUPT
      result = RunChargedCanonicalStaticAuthAndGovernanceSuffix(
        tx, height, account_view, active_bundle, governance_policy)
        else LOCAL_EXECUTION_PAUSE_WITHOUT_CURSOR_ADVANCE
      attempt.status = result // VALID或INVALID；charge不再改变

    if attempt.status == INVALID:
      mark STATIC_OR_AUTH_INVALID_OCCURRENCE
      continue

    // CROSS_LEDGER_CONSUME 在这里继续执行其独立、同样按n个sponsor分片的
    // source-proof scheduler；使用prepared tentative context，失败则不成为winner。
    if is_cross_ledger_consume and
       !RunCrossLedgerChargedSuffix(prepared.cross_ledger_context,...):
      continue

    // 唯一接受条件：nonce == next_nonce
    winners.append((tx_index, tx))
    accepted_tx_ids_in_block.add(tx.tx_id)
    // tx.nonce < UINT64_MAX 且 tx.nonce == next，故 checked 加一不会溢出；
    // 接受 UINT64_MAX-1 后得到永久耗尽哨兵 UINT64_MAX。
    working_next_nonce[tx.sender] = checked_add(next, 1)
    reserved_gas = checked_add(reserved_gas, tx.gas_limit)
      else EXECUTION_HALT
    body_and_mandatory_write_reservation = next_reservation
    if is_create:
      created_accounts_in_block.add(tx.sender)

  return winners
```

伪代码中的每个`mark ...; continue`与winner完成点都隐含`FinishOccurrenceAndCheckpoint`：先确认没有属于当前cursor的`STARTED` common/source worker，再把in-flight scan receipt checked并入对应sponsor/shared completed-scan累计量，把全部budget、attempt map、working set与派生状态原子写入completed-occurrence checkpoint，最后清除`inflight_occurrence_scan`并把cursor推进一项。scan cap路径没有in-flight receipt，累计量不变。实现可以批量提交多个已完成occurrence，但不能把cursor提交到任何in-flight记录之后；批量提交失败必须整体回滚到上一个durable checkpoint。

`TryPrepareCheapPayloadCandidateAndBlockReservation` 不执行任何Ed25519、strict-key或完整registry worker。普通交易生成Failure reserve，账户创建/SEND生成完整success reserve；CONSUME还必须从bounded outer与active policy完成policy/relayer声明cap、RequiredGas、声明message-window reject-only检查、tentative consumption key和完整success reserve，但此时不读consumed SMT、不验证target signatures或source proof。这样protocol06的昂贵顺序与通用filter完全一致：明显无资格/放不下的proof不会先烧common reserve。

`RunChargedCanonicalStaticAuthAndGovernanceSuffix` 必须完成此前延后的全部工作：SignerPolicy strict public-key/key-ID/threshold、每份account signature、payload registry与typed参数、access/Gas/resource static规则；重配置还要验证全部governance approvals、next ValidatorSet strict keys、ProtocolConfig、FeatureSet/GasSchedule和四个hash。扣款后实际field-path count必须与`PrefilterExpensiveWorkCostV1`逐项一致，不一致是`EXECUTION_HALT`。CROSS_LEDGER proof subtree只做已登记的bounded outer规则，source crypto留给独立scheduler。

attempt的扣款与`STARTED`若进入durable scan checkpoint必须原子出现；checkpoint cursor仍指向`origin_occurrence_cursor`所标识的当前未完成occurrence。恢复见`STARTED`时必须逐字节确认同一tx bytes与cursor，随后重跑suffix且不再次扣逻辑work，得到terminal状态后才可推进cursor；cursor与origin不一致是checkpoint损坏。若崩溃前两者都未durable，则回到更早checkpoint并从其旧budget重扫。crypto provider超时、worker取消、磁盘错误或进程资源不足不是`INVALID`，只能保持/回滚到`STARTED`并暂停该candidate，禁止形成Header或attestation；只有确定性协议验证失败才能写`INVALID`。`PREFILTER_SCAN_CAP`、`PREFILTER_VERIFY_CAP`、static/auth invalid都不进树、不产Receipt、不耗nonce；cap不是永久无效，相同交易以后重新出现仍可重试。

当 active FeatureSet 激活 `CROSS_LEDGER_V1` 时，上述状态机按[跨账本异步消息规范](06-cross-ledger-async-messaging.md)增加一个不可省略的 overlay。SEND 与 CONSUME 都使用逐字节可计算的完整成功结果预留，不能沿普通交易的 Failure reserve进入 winner后再降级为 FAILED。CONSUME 是通用 `CanonicalAndStaticValid` 的规范拆分例外：第一阶段只流式完成 target Envelope/proof outer canonical、schema、长度、计数与 union 上限，绝不能在目标账户认证、交易窗口、accepted tx-id、`nonce == next`、RequiredGas 与成功 reserve 之前执行 source Finality/Merkle/signature 验证。

扫描开始还初始化 `working_consumption_keys` exact set、`cross_ledger_proof_attempts` exact map，以及按 containing signed DAGVertex 的 sponsor author index分片的 source-proof sponsor reserve/shared pool。map值固定为：

```text
CrossLedgerProofAttemptV1 {
  schema_version: uint16                 // 1
  status: uint16                         // STARTED | VALID | INVALID
  origin_occurrence_cursor: CausalOccurrenceCursorV1
  sponsor_author_index: uint16
  work_cost: uint64
  charge_receipt: CrossLedgerProofChargeReceiptV1
  verified_consume: optional CanonicalVerifiedConsumeV1
  verified_consume_digest: optional Hash32
}
```

`verified_consume`与其domain-separated digest恰在`VALID`时同时存在，`STARTED/INVALID`时都必须缺失；checkpoint恢复会重算digest。target Envelope/account signatures、声明policy/relayer cap、RequiredGas、声明窗口reject-only、tentative key与完整success reserve已经分别由上面的cheap准备和通用charged prefilter完成。固定后缀只接收`prepared.cross_ledger_context`，不得再次运行这些前缀：

```text
require charged target static/account/governance prefilter == VALID
tentative = prepared.cross_ledger_context
tentative_key = tentative.consumption_key
sponsor = VerifiedContainingDAGVertex(occurrence.vertex_ordinal).author_index
proof_attempt = cross_ledger_proof_attempts.get(tx_id)
if proof_attempt exists and
   proof_attempt.origin_occurrence_cursor != occurrence.cursor:
  require proof_attempt.status != STARTED else RECOVERY_STATE_CORRUPT
  mark DUPLICATE_CROSS_LEDGER_ATTEMPT
  continue
if parent consumed state PRESENT or tentative_key in working set:
  if proof_attempt is ABSENT: mark CROSS_LEDGER_REPLAY
  else: RECOVERY_STATE_CORRUPT
if proof_attempt is ABSENT:
  proof_work = ProofAndSourceSignatureGas(tentative)
  proof_charge = TryChargeCrossLedgerProofSponsorThenShared(sponsor, proof_work)
    else CROSS_LEDGER_VERIFY_CAP
  proof_attempt = CrossLedgerProofAttemptV1(
    schema_version=1,
    status=STARTED,
    origin_occurrence_cursor=occurrence.cursor,
    sponsor_author_index=sponsor,
    work_cost=proof_work,
    charge_receipt=proof_charge,
    verified_consume=ABSENT,
    verified_consume_digest=ABSENT)
  cross_ledger_proof_attempts[tx_id] = proof_attempt
if proof_attempt.status == STARTED:
  require proof_attempt.origin_occurrence_cursor == occurrence.cursor and
          proof_attempt.sponsor_author_index == sponsor
    else RECOVERY_STATE_CORRUPT
  proof_result = VerifyCrossLedgerSourceProof(...)
    else LOCAL_EXECUTION_PAUSE_WITHOUT_CURSOR_ADVANCE
  if proof_result is deterministic invalid:
    proof_attempt.status = INVALID
  else:
    proof_attempt.status = VALID
    proof_attempt.verified_consume = Canonicalize(proof_result)
    proof_attempt.verified_consume_digest =
      DomainHash("CROSS_LEDGER_VERIFIED_CONSUME", network_id, ledger_id,
                 canonical(proof_attempt.verified_consume))
if proof_attempt.status == INVALID:
  mark STATIC_INVALID
  continue
verified_consume = VerifyStoredConsumeArtifactAndDigest(proof_attempt)
  else RECOVERY_STATE_CORRUPT
require authenticated message window contains current height

key = verified_consume.consumption_key
if key != tentative_key: EXECUTION_HALT
parent_value = ReadParentState("finalweave/v1/cross-ledger/consumed", key)
if parent_value malformed or key/value binding mismatches: EXECUTION_HALT
if parent_value PRESENT or key in working_consumption_keys:
    mark CROSS_LEDGER_REPLAY
    continue
```

sponsor reserve 的单份大小是 active policies 的最大单 proof verification Gas；`n=ValidatorSet.validators.length`，bundle validation要求 `n * max_single <= max_execution_gas_per_finalized_block`，余量才进入 shared pool。这里必须使用全部 Validator sponsor数 n，不能误用每轮 proposer slot数 P。扣款先用 containing Vertex sponsor自己的保留份额，不足部分才用 shared；Batch作者、relayer和gossip peer都不参与归因。invalid proof不退款，`CROSS_LEDGER_VERIFY_CAP`不写attempt map。相同完整tx_id一旦真正得到terminal attempt，后续通过更早通用cheap gates并到达source scheduler的occurrence标记`DUPLICATE_CROSS_LEDGER_ATTEMPT`，不重复密码学工作；若原tx已成为winner，则更早的accepted-tx-id gate规范分类为`DUPLICATE_OCCURRENCE`。`proof_charge + STARTED`的checkpoint原子性、cursor固定、恢复不重扣、local failure暂停和terminal状态规则与通用attempt完全相同；cursor不得越过`STARTED`。诚实Vertex作者只引用已本地完整预验的 CONSUME并公平调度，因此 f个Byzantine sponsor即使反复引用他人Batch、持续烧光自己的份额与shared pool，也不能消耗honest sponsor的保留份额。

声明 message window与tentative key尚未认证，只能用于reject-only：窗口外、parent present或working-key present时，该Envelope无论proof有效还是无效都不可能按其声明身份成为winner，故可安全短路；absent不能证明可消费。source proof成功后必须重算相同window/key并复查working set，任何不一致都是实现分叉而非用户失败。

CONSUME 使用完整成功结果预留而不是普通 `FailureResultReserve`；只有 proof验证、authenticated message window、parent/working replay检查全部通过且 occurrence 真正成为 winner 后，才把 key 加入 `working_consumption_keys` 并推进账户 nonce。其 native transition只能成功写 consumed marker + nonce、发出一个 Event与 SUCCESS Receipt；本地失败停止 attestation。proof/policy/window无效、replay、`DUPLICATE_CROSS_LEDGER_ATTEMPT`、`CROSS_LEDGER_VERIFY_CAP` 与 `BLOCK_CAP` occurrence都不进 transaction/receipt/event tree、不耗 nonce。相同 source event 的不同 relayer、proof envelope或合法证书 signer subset不得绕过该 exact set/SMT key。

不进行块内二次回扫。例如 nonce 11 occurrence 先出现、nonce 10 后出现且父 next_nonce 为 10：nonce 11 在本块被跳过，nonce 10 成为 winner；nonce 11 只有以后再次进入 Batch 才有机会执行。

`ResolveCompleteAccountTriples` 只解释父状态中以同一 AccountAddress 为 raw key 的 `AccountMetadataState/AccountAuthState/AccountNonceState` 完整三元组；三项只出现一项或两项是认证状态损坏，必须 `EXECUTION_HALT`。普通交易使用其中版本化 auth state；高度 `h` 成功执行的策略轮换最早以 `effective_height=h+1` 生效，因此本块所有 occurrence 使用同一个块开始认证视图。不存在账户只有 `ACCOUNT_CREATE_V1` 可按地址 core、Envelope initial policy、`nonce=0` 和三项不存在证明完成自证；创建 winner 记入块内集合，所以同块后续创建或普通交易不能利用尚未提交的新状态。其他认证失败不消费 nonce，防止任意攻击者用自造 signer policy 抢占账户序号。

`TryReserveFailureBodyAndMandatoryWrites` 是[执行注册表规范](05-execution-registry-gas-and-resource-metering.md)的精确函数：普通交易预留 Envelope、字段宽度最大的 FAILED result 与 nonce protocol write；创建交易预留完整成功 result 和 meta/auth/nonce 三项；跨账本 CONSUME预留完整成功 result、consumed/nonce两项 StateChange、一个 Event与 return data。所有 canonical-length、array-header 和 write-byte 增量 checked。加入后超出 `max_finalized_block_body_bytes` 或 mandatory block-write budget 才标记 `BLOCK_CAP`；节点不能用本地 RPC/message/memory 上限替代它。

块上限按 occurrence 单遍确定性应用，不拆分一个 COMMIT slot，也不截断尚未扫描的输入。账户 `next_nonce == UINT64_MAX` 时所有 occurrence 都标记 `NONCE_EXHAUSTED`，不产生 Receipt 且永不再推进。`reserved_gas` 使用 winner 的签名 `gas_limit`，不是最终 `gas_used`；减法和加法均 checked。因剩余容量不足被标记 `BLOCK_CAP` 的 occurrence 不消费 nonce，后续更小 gas 的同 nonce occurrence仍可成为 winner。ProtocolConfig 必须保证两个块上限大于 0；`gas_limit > max_execution_gas_per_finalized_block` 属于静态无效，避免产生永远不能执行的交易。

### 3.3 树和 Receipt

只有 winners：

- 进入 transaction tree；
- 获得连续 `tx_index=0..m-1`；
- 执行业务状态机；
- 产生 SUCCESS 或 FAILED Receipt；
- 无论业务成功或失败都消费 nonce。

`ACCOUNT_CREATE_V1` 是上述最后一条的受控特例：所有 payload/address/policy/scope/gas 确定性错误都在 winner 选择前拒绝；一旦成为 winner，原生转移必须成功并在同一 journal 原子写 meta/auth/nonce 三项，其中 `next_nonce=1`。本地存储、运行时或 journal 失败属于 `EXECUTION_HALT`，不能生成一张没有认证 nonce 状态的 `FAILED` Receipt。普通 winner 的业务失败仍按原规则消费既有账户 nonce。

以下 occurrence 不进入 transaction tree 且不产生 Receipt：

- static invalid；
- account authentication invalid；
- future height；
- expired；
- duplicate；
- nonce exhausted；
- stale nonce；
- future nonce；
- nonce-conflict loser；
- block transaction/gas cap。

稳定 slot prefix 到执行器的接口固定为：

```text
StableSlotPrefix
  -> canonical CausalInputManifest/chunks
  -> canonical FinalizedBlockBody skeleton
  -> occurrence filter / winner tx indexes
  -> PreparedExecution
  -> FinalizedBlockBody + FinalizedBlockHeader
```

`PreparedExecution` 只是本地、可丢弃的优化载体。只有重算 canonical Body/Header 并通过后续 attestation 的结果具有协议意义。

## 4. 规范串行语义

FinalWeave 的唯一权威执行语义是：

```text
state_0 = parent finalized state

account_view = ResolveCompleteAccountTriples(state_0, height)

for tx_index = 0 .. winners.length-1:
  (state_{i+1}, receipt_i) = ExecuteSerial(state_i, tx_i)

state_root = Root(state_m)
```

所有实现必须能够关闭并行优化，以单线程或逻辑单线程解释器得到相同结果。

payload/Feature/Gas operation 注册表、每个 operation 的 input/output byte、remaining-budget 扣费、OOG、`fee_limit=0`、硬资源 cap 和 FinalizedBlockBody failure reserve 全部以[执行注册表、Gas 与资源计量规范](05-execution-registry-gas-and-resource-metering.md)为准。串行 `ExecuteSerial` 必须产生该规范的有序 GasEvent/resource trace；并行路径只能证明与它相等，不能另定义计费语义。

成功的 signer-policy 轮换写入版本化 `AccountAuthState`，其 `effective_height` 固定为当前高度加一；失败或回滚不得改变认证状态。该延迟不延迟普通业务状态，只隔离“谁有权签下一笔交易”这一过滤前提。

确定性约束：

- 禁止读取墙钟、系统随机数、线程调度和本地文件；
- v1 FinalizedBlock 没有 timestamp 或 randomness 字段，因此 VM 不暴露区块时间/随机数 opcode；未来新增必须先冻结来源、Schema 和 domain；
- map 迭代必须按协议 key 排序；事件按确定性发出顺序，单交易状态变化按 `(namespace,key_hash)` 排序；
- 整数溢出、除零、内存越界和 gas 耗尽使用固定错误码；
- 业务 FAILED 回滚业务写集，但 nonce 消费和 Receipt 写入保留；
- 外部调用只能访问确定性、版本化的链上能力。

## 5. 性能执行：exact access 依赖图

并行执行只是一项必须证明与规范串行等价的优化。

### 5.1 授权范围与 ExactObservedAccess

每个 winner 必须区分两个对象：

```text
AuthorizedAccessScope {
  entries: [AuthorizedAccessEntry]
  // 从 TransactionIntent.authorized_access_scope 规范化得到的只读视图
}

ExactObservedAccess {
  read_keys
  write_keys
  system_keys
}
```

`AuthorizedAccessEntry` 的冻结 Schema 位于[数据模型规范](01-data-model-and-cryptography.md)。`AuthorizedAccessScope` 是 `TransactionIntent.authorized_access_scope` 的规范化只读视图，属于签名语义，决定用户授权 VM 访问什么；它不是调度 hint。`ExactObservedAccess` 是本次确定性执行实际使用的精确读写集，用来构造依赖图和验证 MVCC 结果。

ExactObservedAccess 必须是 exact，而不是可能漏 key 的 hint。来源可以是：

- 签名 scope 中的 exact key；
- 对合法 PREFIX scope 做确定性 preflight/resolution；
- 固定原生或系统交易类型的规范 resolver；
- 无法提前精确解析时进入 serial barrier，并在权威串行执行中记录实际集合。

必须分类处理访问差异：

1. 用户交易尝试访问 `AuthorizedAccessScope` 之外的 key 或 mode：产生固定错误 `ACCESS_SCOPE_VIOLATION`，回滚业务写，保留 nonce 消费并产生 FAILED Receipt；所有实现必须在尝试读取未授权值或应用未授权写之前失败。
2. 合法 PREFIX scope 解析出的 key 仍在授权范围内，但 preflight 的推测读写集与实际执行不同：丢弃该 index 起的推测 suffix，触发第 7 节至多一次权威串行回退。
3. 原生/系统 resolver 的结果违反其规范声明，或相同规范输入在节点间得到不同 resolver 结果：这是实现或协议确定性失效，节点进入 `EXECUTION_HALT`，不得把它编码成普通业务失败，也不得签 ExecutionAttestation。
4. 无法证明精确访问关系的合法操作：执行前就放入 serial barrier，不通过乐观重试猜测冲突。

`max_exact_observed_access_keys_per_tx` 计算 `read_keys ∪ write_keys ∪ system_keys` 中不同完整 `ConsensusStateKey` 的数量；同一 key 同时读写只计一次，hash 碰撞后仍按原始 namespace/key 比较。resolver/preflight 在执行前已经确定超限，或 instrumented 执行即将加入第 `limit+1` 个新 key 时，必须在读取该 key 的值或应用写入前返回登记错误 `STATE_LIMIT_EXCEEDED`：回滚业务 journal、保留 winner 的协议写、产生 FAILED Receipt并消费 nonce。它不是 static-invalid、scope violation、serial fallback 或本地资源错误。实际集合未超限但预测集合错误仍按第 2 类 suffix fallback；本地缓存/物理 SMT nodes 不计入该上限。

namespace/key/value、逻辑 read/write bytes、Event count/bytes、return data、call count/depth、block writes/events 和完整 Body 同样受 ProtocolConfig 与 v1 absolute caps 约束。每项都必须在读取值或分配/追加/写 journal 前 checked 扣减；普通 winner 超限确定性产生 `STATE_LIMIT_EXCEEDED` 并消费 nonce。选择 winner 前先为其 Envelope、最大失败结果和 mandatory protocol writes 预留空间；连失败结果都放不下时才是无 Receipt、无 nonce 的 `BLOCK_CAP`。精确 reserve 算法见执行注册表规范，节点不得用本地 message/memory limit 截断。

因此“scope 越权”和“调度预测失配”绝不能合并成一个错误分支。前者是确定性用户失败；后者是可回退的本地优化失败；规范 resolver 分叉则是致命安全错误。

### 5.2 依赖边

对 `i<j`，若满足任一条件，建立 `i -> j`：

```text
W_i intersects R_j
W_i intersects W_j
R_i intersects W_j
System_i conflicts System_j
sender_i == sender_j
```

边永远从较小 tx_index 指向较大 tx_index，因此图必为 DAG，并保留规范串行冲突顺序。

无 exact access set 的交易视为与所有尚未认证 transaction prefix 冲突。

### 5.3 图构建伪代码

```text
BuildDependencyGraph(winners):
  graph = vertices 0..m-1
  last_readers = map key -> set(tx_index)
  last_writer = map key -> tx_index

  for j in 0..m-1:
    access = ExactObservedAccess(j)

    for key in access.read_keys:
      if last_writer contains key:
        add edge last_writer[key] -> j
      last_readers[key].add(j)

    for key in access.write_keys:
      if last_writer contains key:
        add edge last_writer[key] -> j
      for i in last_readers[key]:
        add edge i -> j
      last_readers[key].clear()
      last_writer[key] = j

    add sender/system ordering edges

  return graph
```

实现可以用 key-range、shard 或稀疏索引优化构图，但输出边的传递语义必须一致。

## 6. 有界 MVCC

### 6.1 版本

推测执行使用：

```text
VersionedValue {
  state_key
  writer_tx_index
  physical_value_or_delete_marker // 本地artifact；不是共识tombstone value
}
```

交易 `j` 对 key 的读取必须选择：

```text
max writer_tx_index < j
```

且该 writer 是依赖图中已完成的祖先；不存在则读 parent state。

### 6.2 界限

epoch 配置冻结：

- `execution_parallelism`；
- `mvcc_max_versions`；
- `mvcc_max_bytes`；
- `mvcc_max_retries`；
- `max_dependency_edges`。

任一上限触发后不得扩张到无界内存或反复重试。节点标记并行 suffix 失败，进入一次串行回退。

### 6.3 推测结果

每个结果记录：

```text
SpeculativeResult {
  tx_index
  exact_read_versions
  write_set
  receipt_core_without_roots
  gas_used
  access_set_observed
}
```

推测完成不等于可签 FinalityCertificate。

## 7. tx-index prefix certification

这里的 prefix certification 是本地确定性验证步骤，不是 Validator 签名证书。

### 7.1 认证条件

从 `tx_index=0` 开始，只能连续认证。交易 `i` 可加入 `certified_prefix`，当且仅当：

1. `0..i-1` 已认证；
2. `ExactObservedAccess` 与实际访问完全相同，或该交易已经在 serial barrier 中权威执行；
3. 所有实际访问均在 `AuthorizedAccessScope` 内；若 receipt 为 `ACCESS_SCOPE_VIOLATION`，确认没有读取或写入 scope 外状态；
4. 每个 read version 是串行顺序下最新的 `<i` writer；
5. 所有依赖祖先已认证；
6. gas、错误码、事件和 write set 均满足 VM 规则；
7. 应用 write set 后增量 root 与规范计算一致；
8. Receipt 的 tx_index、nonce 和状态变化根正确。

```text
CertifyPrefix(results):
  prefix = -1
  state = parent_state

  for window_start in range(0, m, config.tx_index_prefix_size):
    window_end = min(m, checked_add(window_start,
                                    config.tx_index_prefix_size))

    for i in window_start..window_end-1:
      if !ValidateSpeculativeResult(i, state, results[i]):
        return (prefix, state, i)

      state = ApplyResultInSerialIndexOrder(state, results[i])
      prefix = i

    PersistCertifiedPrefixCheckpoint(prefix, state)
    ReleaseMvccVersionsOlderThan(prefix)

  return (prefix, state, m)
```

`tx_index_prefix_size` 只冻结认证 checkpoint/资源释放窗口，不能让实现并行跳过窗口内 index、提前认证后续 window 或改变首个失败 `j`。最后一个 window 可以更短；checked addition 溢出是 `EXECUTION_HALT`。不同合法 worker 调度仍必须逐 index 得到同一 prefix 和最终结果。

### 7.2 一次权威串行回退

若第一个失败 index 为 `j`：

```text
AuthoritativeFallback(j, state_after_prefix):
  discard all speculative results j..m-1

  for i in j..m-1:
    execute tx_i exactly once with ExecuteSerial
    apply result immediately

  return final state and receipts
```

每个 FinalizedBlock 最多发生一次这种 suffix 回退。禁止：

- 多轮乐观重试直到“碰巧成功”；
- 在不同机器上使用不同 retry count；
- 丢弃已认证 prefix 后从随机 snapshot 重跑；
- 因并行失败改变 transaction index 或 winner 集。

若权威串行执行自身不确定或崩溃，节点进入 `EXECUTION_HALT`，不得签 attestation。

## 8. Root 构造

执行完成后按[数据模型规范](01-data-model-and-cryptography.md)构造：

- ordered vertex root；
- transaction root；
- receipt root；
- block event root；
- state root；
- Header 中的 parent block MMR root。

FinalizedBlockHeader 的 parent block ID、height、committed slot 和 proposer VertexID 必须来自连续 decider 前缀。

先用上述 roots 和父 MMR root 计算 FinalizedBlockID，再把 `{height, FinalizedBlockID}` 的 `BLOCK_MMR_LEAF` 追加到父 MMR peaks，得到包含当前块的 `block_mmr_root`。该新 root 进入 FinalityStatement，而不回填 Header。下一高度 Header 的 `parent_block_mmr_root` 必须等于本高度已认证的 `block_mmr_root`。这两步顺序消除了 `Header hash -> MMR leaf -> Header root` 的自引用。证书 envelope 永不进入 MMR。

## 9. ExecutionAttestation

Validator 只有完成以下步骤后才能签：

1. 决策前缀连续且未越过 UNDECIDED slot；
2. 所需 BatchBody 已恢复并验证；
3. VertexDelta 和 occurrence 顺序规范；
4. filter winners 唯一；
5. 并行结果通过全部 prefix certification，或已完成一次权威串行回退；
6. 重算所有 roots 和 FinalizedBlockID；
7. 父 height/state 与本地已认证连续状态一致；
8. 在 Consensus signer 的同一安全锁域检查该 epoch 未关闭，或待签 height 不超过已锁定 final height；
9. 写入 `EXECUTION_ATTESTATION_INTENT`；
10. fsync；
11. 用 Consensus Key 签 `EXECUTION_ATTESTATION` digest。

Attestation 搭载在后续 DAGVertex 的 `execution_attestations[]` 中，也可以通过有界同步响应重传。它不是新的排序消息。

### 9.1 防双签槽位

```text
(network_id, ledger_id, epoch, height, signer_index)
```

相同槽位只允许一个 `FinalityStatement`。若本地发现不同 digest：

- 停止 Consensus Key；
- 进入 `SAFETY_HALT`；
- 导出两个 block derivation 和 DAG witnesses；
- 不以“较新软件版本”为由覆盖 WAL。

## 10. FinalityCertificate

聚合器按 `finality_id` 收集 attestations：

1. 验证 statement 与 FinalizedBlockHeader；
2. 验证当前 epoch ValidatorSet；
3. 按 signer index 去重；
4. 收齐 `q` 个；
5. 选择任意合法、恰好 `q` 个签名；
6. 按 index 升序构造 bitmap 和数组。

不同 signer subset 是同一语义 FinalityCertificate。任何节点不得把 envelope hash 用作 block ID、父引用、MMR leaf、epoch seed或查询结果身份。

### 10.1 最终性含义

FinalityCertificate 含 `q` 个 Consensus Key 签名，至少 `f+1` 个来自诚实 Validator。诚实 Validator 只对唯一 DAG 决策前缀和确定性执行结果签名，并在同 height 防双签。因此两个冲突 FinalizedBlock 不可能都取得有效证书。

### 10.2 Ed25519 v1

v1 使用：

- signer bitmap；
- 恰好 `q` 个独立 Ed25519 签名；
- 按 ValidatorIndex 排序；
- 可选批量验证，但失败后必须逐签名定位。

BLS aggregate 或 threshold signature 仅是未来研究项。启用前必须设计 PoP、rogue-key 防御、DKG/resharing（若为 threshold）、密钥恢复、epoch 轮换、审计归责和新 domain。

## 11. FinalityProof

以 Genesis 为根的基础证明：

```text
FinalityProof {
  schema_version: uint16
  finalized_block_header: FinalizedBlockHeader
  finality_certificate: FinalityCertificate
  validator_set_proof: ValidatorSetProof
  merkle_proofs: [MerkleProof]
}
```

`EpochTransitionProof`、`ValidatorSetProof` 的唯一 wire schema 以[数据模型规范第 15 节](01-data-model-and-cryptography.md)为准：transition 携 seal、next ValidatorSet/ProtocolConfig；proof 只在末尾携目标 epoch 的完整 FeatureSet/GasSchedule，不在每一跳重复大执行配置对象。

以运营方预置 checkpoint 为根时，不修改或裁剪上述基础 Schema，而使用[数据模型规范第 15.1 节](01-data-model-and-cryptography.md#151-独立-checkpoint-信任根与证明)的独立 `CheckpointTrustAnchor/CheckpointFinalityProof` 和 `VerifyCheckpointFinalityProof(expected_anchor_id,proof)`。该验证器从本地只读 trust store 匹配 anchor ID，验证 anchor 完整配置 bundle/MMR peaks 后，才从 anchor epoch 的当前 ValidatorSet 开始逐跳验证；不可信响应中的 anchor 不能自证或更新信任根。

验证：

1. 从带外 `genesis_reference` 开始调用 `ValidateGenesisCertificate`，完整验证 governance policy 与 approvals；再调用 `ValidateValidatorSet(set,expected_epoch,expected_network_id)` 和 `ValidateProtocolConfigStructure`，按 `EpochTransitionProof` 逐跳使用旧 ValidatorSet 验证 EpochSeal，并验证每跳 next ValidatorSet/ProtocolConfig 后再切换，直到 Header epoch；
2. 对目标 config/set/FeatureSet/GasSchedule 调用 `ValidateExecutionConfigBundle`；检查严格 `n=3f+1`、`4<=n<=253` 与 `q=2f+1`；
3. 重算 FinalizedBlockID；
4. 检查 FinalityStatement 的 block ID、state/config/validator hashes 精确匹配 Header；
5. 需要验证历史累积时，从已认证父 MMR peaks 追加当前 `BLOCK_MMR_LEAF`，重算 Statement 的 `block_mmr_root`；
6. 验证 bitmap 和 `q` 个 Consensus Key 签名；
7. 验证内含 transaction/receipt 等有序树 Merkle proof；state 与历史累计查询分别使用响应中的独立 `SparseMerkleProof` / `BlockMMRProof`，以同一 Header/Statement 的 root 校验，不塞进 `merkle_proofs`。

DAGCommitWitness 不属于公共证明。全节点可以在审计/同步模式请求它，以复核 direct/indirect decision，但轻客户端信任经过 ValidatorSetProof 验证的 `q` 执行签名。

## 12. 交易终态

### 12.1 FINALIZED_SUCCESS / FINALIZED_FAILED

证明必须包含：

- 原 TransactionEnvelope 的 transaction tree proof；
- 同 tx_index Receipt proof；
- SUCCESS 或 FAILED 状态；
- 对应 FinalityProof。

FAILED 是 nonce-consuming winner 的业务失败，不包括 occurrence 过滤跳过。

### 12.2 REPLACED

在证明替换结果前，必须用具名 `TransactionAuthorizationContextV1` 或 checkpoint 变体选择窗口内 `candidate_height`，并证明 candidate 执行前的父状态与当时配置。普通父路径的 FinalityProof 必须精确指向 `candidate_height-1`；首块可使用本地 Genesis/checkpoint trust-root state。三份 SMT proof只验证到该父 root，不能使用candidate同高post-state。普通账户三项都存在，按candidate height解析active policy并验证signatures，且父状态`next_nonce <= queried.nonce`；`max_future_nonce_gap`仅是mempool资源规则，不进入终态。`ACCOUNT_CREATE_V1`要求三项non-inclusion及完整自证。跨epoch首块还必须携带terminal proof chain中对应activation transition和next bundle；sealed parent不得伪装为same-epoch。普通不存在账户、残缺三元组或历史父状态/配置已裁剪时不能生成REPLACED，后者返回`PROOF_UNAVAILABLE/HISTORY_PRUNED`。

结果证明还必须包含另一个不同 tx_id：

- sender 与 queried transaction 相同；
- nonce 相同；
- winner transaction inclusion proof；
- winner 的 nonce-consuming Receipt proof；
- 对应 FinalityProof。

不允许用 mempool 替换记录、fee bump、seen cache 或“节点记得另一笔交易”返回稳定 REPLACED。

### 12.3 EXPIRED

证明必须包含：

- 与 12.2 相同的 candidate parent-state authorization context，证明 queried Envelope 在窗口内某候选块开始时确由 sender 授权；
- 一个 final tip，其 `height > queried.valid_until_height`；
- tip 的 FinalityProof；
- tip state root 下固定 namespace `finalweave/v1/account/nonce`、raw key 为 queried sender 的账户 nonce SMT proof；
- present value 必须规范解码为 `AccountNonceState{schema_version:1}` 且 `next_nonce <= queried.nonce`；non-inclusion 只允许 queried Envelope 已由历史授权 proof 证明为合法 `ACCOUNT_CREATE_V1`。普通不存在账户交易不能取得 EXPIRED。

若 present value 的 `next_nonce > queried.nonce`，该 nonce 已被消费。服务必须寻找原交易 proof 或同 sender/nonce 的不同 winner Receipt，不能返回 EXPIRED；non-inclusion 不能被当成编码后的 `next_nonce=0`。

### 12.4 互斥和优先级

查询端验证优先级：

1. 原交易 FINALIZED_SUCCESS/FAILED；
2. queried Envelope 的历史授权上下文有效，且存在不同 winner 的 REPLACED；
3. queried Envelope 的历史授权上下文有效、满足高度条件，且 nonce proof 为合法创建 non-inclusion 或 present `next_nonce<=queried.nonce` 的 EXPIRED；
4. 否则 PENDING 或 UNKNOWN。

终态一旦有有效证明不可回退。

## 13. Epoch close

### 13.1 触发

Epoch close 有两个互斥触发来源：治理重配置与协议自动 rollover。只有[执行注册表规范](05-execution-registry-gas-and-resource-metering.md)中 `LEDGER_RECONFIGURE_V1` 的 SUCCESS Receipt 已随所在 FinalizedBlock 获得 FinalityCertificate，才可以提前请求 governance reconfiguration。普通账户签名、治理 approvals、四对象完整 bundle、target epoch 和 pending-state 检查缺一不可；FAILED 或仅进入 mempool/DAG 的请求不触发治理关闭。请求所在 FinalizedBlock 定义：

- next ValidatorSet；
- next ProtocolConfig；
- 与 next ProtocolConfig 内容 hash 精确匹配的完整 next FeatureSet；
- 与 next ProtocolConfig 内容 hash 精确匹配的完整 next GasSchedule；
- 可选 migration manifest（v1 只允许缺失）。

为防止单个 epoch 的 authenticated emitted set、DAG checkpoint 和 ACK-fragment 生命周期在持续出块时无限增长，v1 另外固定两条不可由治理提高的 rollover 触发线：`EPOCH_FINALIZED_BLOCKS_MAX_V1=65_536` 与 `EPOCH_EMITTED_VERTEX_ROLLOVER_TRIGGER_V1=4_194_304`。令 `epoch_base_height=0`（epoch 0）或上一 `EpochSealStatement.final_height`，候选 C 的 `epoch_block_ordinal=checked_sub(candidate_height,epoch_base_height)`。在 stable COMMIT 候选完整 delta 已验证、但分配高度/公开 ORDER_FINAL/签 attestation 之前，若以下任一成立，C 必须成为自动 closing candidate：

```text
epoch_block_ordinal == EPOCH_FINALIZED_BLOCKS_MAX_V1

parent_epoch_emitted_vertex_count
  < EPOCH_EMITTED_VERTEX_ROLLOVER_TRIGGER_V1
and candidate_epoch_emitted_vertex_count
  >= EPOCH_EMITTED_VERTEX_ROLLOVER_TRIGGER_V1
```

`epoch_block_ordinal > MAX` 无效并 `SAFETY_HALT`，因为正确实现应已在等号处落 reservation。emitted threshold 是 rollover 触发线而非单块 delta 的拒绝 cap：C 可以有限地越过阈值，避免 Byzantine 大但协议有效的 causal delta 把系统卡在“无法关闭”的状态。activation 不能只查 C 的父状态：C 自身也可能含合法 `LEDGER_RECONFIGURE_V1` 并在确定性执行后写 pending state。自动 close 必须先锁 C，再以 C 的完整 post-state 决定；若其中有 target epoch 正确的 pending reconfiguration就激活该 action，否则 next ValidatorSet 由当前 descriptors/keys/weights 复制并把 epoch 加一，next ProtocolConfig/FeatureSet/GasSchedule 逐字节复用当前认证对象。这种 same-config rollover 不需要伪造治理 approvals。

本规则按已提交块而不是 wall clock 或 DAG round 触发：GST 可以任意晚到，协议不会因为耗尽有限 closing-round 尾巴而永久停机；没有新 COMMIT 时累计 emitted set 也不增长。未决 DAG/Batch bytes 由第 18 节的高低水位背压限制生产，而不是改变对象有效性。

节点在共享 signer/fence 锁域持久化精确的关闭来源：

```text
EpochClosingIntentV1 {
  schema_version: uint16
  old_epoch: Epoch
  trigger_kind: uint16       // 1=GOVERNANCE_EARLY, 2=AUTOMATIC_BOUND
  trigger_reference: Hash32  // GovernanceActionID 或自动触发 C 的 derivation digest
  activation_kind: uint16    // 1=PENDING_RECONFIG, 2=SAME_CONFIG_ROLLOVER
  next_validator_set_hash: Hash32
  next_protocol_config_hash: Hash32
  next_feature_set_hash: Hash32
  next_gas_schedule_hash: Hash32
}
```

字段按顺序使用 storage deterministic-CBOR key `1..9`。同一 old epoch 只能激活一个 intent；相同字节重放幂等，不同 trigger/next bundle 冲突即 `SAFETY_HALT`。它是安全存储 schema，不是 peer 自报的共识对象；最终由 closing FinalityCertificate 与 EpochSealCertificate 认证结果。

```text
epoch_closing_intent_hash = SHA256(
  U32BE(len("FINALWEAVE_EPOCH_CLOSING_INTENT_V1")) ||
  ASCII("FINALWEAVE_EPOCH_CLOSING_INTENT_V1") ||
  network_id || ledger_id ||
  U64BE(len(canonical(EpochClosingIntentV1))) ||
  canonical(EpochClosingIntentV1)
)

DerivationCandidateIdentityV1 {
  schema_version: uint16
  network_id: NetworkID
  ledger_id: LedgerID
  epoch: Epoch
  candidate_height: Height
  parent_block_id: Hash32
  committed_slot: ProposerSlot
  proposer_vertex_id: Hash32
  causal_input_manifest_id: Hash32
}

derivation_candidate_digest = SHA256(
  U32BE(len("FINALWEAVE_DERIVATION_CANDIDATE_V1")) ||
  ASCII("FINALWEAVE_DERIVATION_CANDIDATE_V1") ||
  U64BE(len(canonical(DerivationCandidateIdentityV1))) ||
  canonical(DerivationCandidateIdentityV1)
)
```

candidate identity 的 9 个字段同样用 integer key `1..9`，version 为 1。manifest 必须已经覆盖完整 delta，candidate height/parent/slot/proposer 必须来自连续 stable prefix；任何字段尚未确定时不得写 closing reservation。上述两个 hash 是 storage safety identity，不替代 FinalizedBlockID 或 wire certificate。

Snapshot 不属于该 action，也不阻塞 closing frontier、EpochSeal 或新 epoch readiness。它在最终状态发布后按恢复策略异步生成；本地 snapshot 失败只能触发告警、重试或从其他节点复制，不能改变已经认证的重配置结果。

### 13.2 Closing Vertex

诚实 Validator 观察到请求 FinalityCertificate 后：

- 停止为旧 epoch 构建新 Batch；
- 不再把新 AvailabilityReference 放入旧 epoch Vertex；
- 在所有后续旧 epoch Vertex 设置 `epoch_closing=true`；
- 继续创建 Vertex、参与 decider 和传播执行 attestation。

已存在但未引用的交易返回本地 mempool，等待新 epoch 重新 Batch。

### 13.3 Closing frontier

旧 epoch 的治理提前 closing candidate 是稳定 slot 前缀中第一个满足以下条件的 committed proposer `C`：

- `Past(C)` 中含 `q` 个不同作者、`epoch_closing=true` 的 Vertex；
- 从上次已输出 slot 到 C 的所有更早 slot 都已 COMMIT 或 SKIP。

自动 closing candidate 则是按 13.1 节两个确定性触发式命中的第一个 stable COMMIT；它不再等待 q 个预先标记 closing 的 Vertex，因为 C 的完整 parent count/delta、连续 epoch ordinal 和父认证状态已经由所有 attesters逐项验证。若同一 C 同时满足治理 q-closing frontier 与自动边界，且 pending action 有效，`trigger_kind=GOVERNANCE_EARLY`、`activation_kind=PENDING_RECONFIG`；否则自动规则决定。所有节点从同一 stable prefix、父 Header 与状态得到同一选择。

识别 candidate 不等待 C 的执行或 FinalityCertificate，但自动分支的 activation kind 必须等 C 执行完才知道。slot stable-prefix 派生、旧 epoch 高度分配与 Consensus signer 必须共用一个串行 fence 锁域；在给 C 分配下一连续高度、公开其 `ORDER_FINAL` 或为它/任何后继创建 attestation intent 前，先 append 并 fsync：

```text
EPOCH_CLOSING_RESERVATION {
  old_epoch
  closing_slot
  proposer_vertex_id
  candidate_height
  derivation_candidate_digest
}
```

reservation 立即把旧 epoch 最后可分配高度锁为 candidate height，但尚不选择 next bundle。节点只允许确定性执行完全匹配的 C。执行产生 Body/Header/post-state 后，若 post-state 含 C 自身或更早块写入的有效 pending reconfiguration，构造 `PENDING_RECONFIG` intent；否则构造 `SAME_CONFIG_ROLLOVER` intent。然后在创建 `EXECUTION_ATTESTATION_INTENT` 前依次 append+fsync `EpochClosingIntentV1` 和最终 fence：

```text
EPOCH_CLOSING_FENCE {
  old_epoch
  reservation_record_hash
  closing_intent_hash
  closing_slot
  proposer_vertex_id
  candidate_height
  derivation_candidate_digest
}
```

reservation 与 fence 都是协议冻结的安全 WAL record。reservation 已拒绝分配任何 `height > candidate_height`；final fence 另外锁定 post-state 派生的唯一 next bundle，之后才允许为完全匹配 C 的该高度签 attestation。C 之后的 slot 可以为审计继续作 DAG 决策，但不得派生账本高度；节点停止创建普通旧 epoch Vertex，仅允许重传已有 Vertex、C 的 attestation 和 seal vote。所有诚实节点因稳定前缀、完整确定性执行和“第一个”规则得到同一 C/intent；不得用本地证书到达时间改选。

C 派生的 FinalizedBlock 获得 FinalityCertificate 后，第 14.3 节 certified publication 在同一 fence/signer 锁域验证 slot、proposer、height 与 fence 完全一致，并原子把它提升为：


```text
EPOCH_CLOSED(old_epoch, final_height, final_block_id, closing_intent_hash)
```

其中 `final_height == candidate_height`。reservation、intent、fence、`EPOCH_CLOSED`、`EXECUTION_ATTESTATION_INTENT` 与 `EpochSealVoteLock` 共享同一串行锁域。若写 reservation 前已经存在更高高度分配/intent，reservation 后出现 C+1，或 final fence 后 intent/post-state不匹配，说明实现违反串行化，必须 `SAFETY_HALT`。崩溃后有 reservation、无 intent/fence时只能幂等重建并重执行同一 C，再从其 post-state重建相同 intent；有 fence、无 closed record时只能认证/发布同一 C，不能撤销或选择后继 block。

治理提前关闭时，quorum intersection 保证 close frontier 的因果过去覆盖所有能够在关闭前形成稳定 DAG 认证路径的诚实数据。自动边界关闭则以该确定性 C 的完整 Past 为最终输入；尚未进入其 Past 的交易或 Vertex 不获最终性，交易回到新 epoch 重提，不能借“节点曾看见”扩展 closing state。

### 13.4 EpochSeal

旧 Validator 验证 closing frontier、`EpochClosingIntentV1`、最终状态和下一配置后：

1. 取得完整 next ValidatorSet、ProtocolConfig、FeatureSet 和 GasSchedule，调用 `ValidateValidatorSet(next_validator_set,old_epoch+1,network_id)`、`ValidateProtocolConfigStructure(next_protocol_config,next_validator_set)` 与 `ValidateExecutionConfigBundle(next_protocol_config,next_feature_set,next_gas_schedule,next_validator_set)`，并重算四个内容 hash；
2. 要求两个执行对象内容 ID 精确等于 next ProtocolConfig 字段，再要求四个 hash 精确等于 closing intent 与 seal statement；`PENDING_RECONFIG` 验证治理 action/approvals，`SAME_CONFIG_ROLLOVER` 则要求 next set 仅把 epoch checked 加一且 descriptors/keys/weights逐字节不变、config/Feature/Gas 与当前对象逐字节不变；
3. 在 signer 锁域核对 `EPOCH_CLOSED` 与 statement 的 final height/block 完全一致；
4. 确认不存在更高旧 epoch ExecutionAttestation intent/signature；
5. 按下式重算 `next_epoch_seed` 并构造唯一 EpochSealStatement；调用方若提供不同 seed 必须拒绝，不能覆盖或签名；
6. 写入 EpochSealVoteLock；
7. fsync；
8. 用 Consensus Key 签 EpochSealVote；
9. 通过控制 gossip 或最后的 DAG 附件传播。

收齐旧 ValidatorSet 恰好 `q` 个签名形成 EpochSealCertificate。

EpochSealVote 的签名摘要和证书语义 ID 均以 signer-subset 无关的 statement 为输入：

```text
DomainHash(
  "EPOCH_SEAL_STATEMENT", network_id, ledger_id,
  canonical(EpochSealStatement)
)
```

```text
next_epoch_seed = DomainHash(
  "EPOCH_SEED", network_id, ledger_id,
  canonical({
    old_epoch,
    final_height,
    final_block_id,
    final_state_root,
    next_validator_set_hash,
    next_protocol_config_hash
  })
)
```

seed 不包含 EpochSeal signer subset。

验证拆成两个不可混淆的谓词：

- `ValidateEpochSealStatementIntrinsic(statement,next_validator_set,next_protocol_config,expected_network_id,expected_ledger_id,expected_old_epoch)` 只依赖 wire proof 中确实存在的对象；它检查 schema/context/epoch，调用 set/config 结构谓词，重算 next set/config hash 和上述 `next_epoch_seed` 并逐字节匹配。聚合或接收 `EpochSealCertificate`、`FinalityProof`/`CheckpointFinalityProof` 每一跳和新 epoch 读取证书时都调用它。
- `ValidateEpochSealAuthorization(statement,closing_publication,closed_record,closing_intent,optional_pending_action,current_bundle,next_validator_set,next_protocol_config,next_feature_set,next_gas_schedule)` 供旧 signer 和本地 epoch activation 使用；它先调用 intrinsic，再验证 closing publication 的 final block/state/MMR、durable `EPOCH_CLOSED`、closing intent 唯一性与触发式。`PENDING_RECONFIG` 分支验证治理 action/approvals及 next bundle；`SAME_CONFIG_ROLLOVER` 分支拒绝 action 并验证上述 epoch+1/逐字节复用规则。全部匹配后才允许写 `EpochSealVoteLock`。

proof verifier 没有旧节点的本地 closed record、closing intent/current bundle 或 optional pending action，绝不能伪造参数调用第二个谓词；它依靠旧集合 q 个有效签名认证这些授权前置条件。任何单字段 seed 篡改即使已有 q 个对错误字节的签名也不能通过 intrinsic。验证器不得把老组签署的自报随机数当作 epoch seed。

### 13.5 新 epoch 启动

新 Validator 必须：

- 用旧 ValidatorSet 验证旧 EpochSealCertificate，并对 statement 调用 `ValidateEpochSealStatementIntrinsic`，包括逐字节重算 `next_epoch_seed`；若本节点持有完整旧 closing publication/intent/current bundle/optional action，则额外运行 authorization 谓词作纵深校验，但公共 proof 缺少这些本地历史 payload 不推翻已通过 intrinsic 与 q 签名的 seal；
- 取得完整 ValidatorSet、ProtocolConfig、FeatureSet 和 GasSchedule，调用成员/结构/bundle 三个谓词，重算 set/config hash，并核对两个执行内容 ID；
- 导入或重放到 final state root；
- 验证 final block MMR；
- 初始化新 epoch synthetic genesis；
- 从 round 1 开始创建 Vertex。

新 epoch 首个普通 FinalizedBlock 高度是旧 epoch final height + 1。旧、新 epoch 的 DAG quorum 和 BatchAC 绝不能混合。

若新组不足 `q` 个节点就绪，系统安全停机，不能退回旧组继续产生新高度。

## 14. WAL 与崩溃恢复

### 14.1 Durable records

每个 ledger 至少持久化：

```text
OwnBatchSlot
DA_ACK_LOCK
VERTEX_SIGN_INTENT
VERTEX_REJOIN_INTENT
RestrictedJumpCursor
LastEmittedSlot
SlotDecisionCheckpoint
DerivationStateGenerationV1
DerivedHeight
FinalizedBlockHeader
CausalValidationRecordV1
CertifiedCoreGenerationV1
CertifiedPublishMarkerV1
HistorySegmentManifestV1
HistoryGCRecordV1 chain
EXECUTION_ATTESTATION_INTENT
FinalityCertificate
EpochClosingIntentV1
EPOCH_CLOSING_RESERVATION
EPOCH_CLOSING_FENCE
EpochSealVoteLock
EpochSealCertificate
EPOCH_CLOSED
SafetyHaltState
```

`CODEWORD_VERIFIED`、`DA_ACK_LOCK`、`VERTEX_SIGN_INTENT`、`VERTEX_REJOIN_INTENT`、`EXECUTION_ATTESTATION_INTENT`、`EpochClosingIntentV1`、`EPOCH_CLOSING_RESERVATION`、`EPOCH_CLOSING_FENCE`、seal/closed、receipt与`BATCH_AUTHOR_INTENT`类型都使用存储规范冻结的`SafetyRecordV1` kind 1..16；实现可以增加非安全索引或checkpoint，但不得用含义模糊的通用`SIGN`记录替代typed slot/payload。Batch author intent锁`(epoch,author,batch_seq)`与BatchID，并要求AUTHOR_BODY retention manifest在签名前durable；rejoin intent绑定epoch/author/target finality/prior own ID/new round；一旦fsync，旧own branch永久abandoned。reservation在C执行前锁高度，intent/fence在C post-state后锁next bundle，顺序不可交换。

### 14.2 Sign-before-send 纪律

所有 DAG Key 和 Consensus Key 签名：

```text
Validate
  -> append SIGN_INTENT(unique_slot, digest)
  -> fsync
  -> call signer/HSM
  -> append SIGNATURE or deterministic-recovery marker
  -> fsync
  -> publish
```

HSM 已签但进程未记录 signature 时，只能用相同 digest 恢复或重新请求确定性 Ed25519 签名，不能改变消息。

### 14.3 两阶段耐久化与原子最终发布

本地排序/执行完成和对外 finalized 是两个不同阶段。

#### 阶段 A：Prepared execution

节点可以在 staging namespace 或带状态标记的 WAL 中耐久化：

- stable slot decision prefix、emitted Vertex delta，以及不可见的 copy-on-write `DerivationStateGenerationV1`（target committed slot、累计 count/root 与 authenticated sparse-set nodes）；
- 已验证的 `CausalInputManifest`、manifest ID、chunks 或可重建位置、下一 byte/item/Vertex/occurrence 游标，以及增量 tree leaf 临时文件校验信息；
- `PreparedExecution`；
- canonical FinalizedBlockBody/Header 候选；
- 尚未应用到权威状态的 state write batch；
- transaction/receipt/event 临时索引；
- prepared height；
- `EXECUTION_ATTESTATION_INTENT` 和已产生的本机签名。

`DerivationStateGenerationV1` 必须在 attestation intent 前完整 fsync，并由 `DurablePreparedRecord` 绑定；它不能原地修改父 generation，也不能因为候选失败而影响 active exact set。只有获得匹配 FinalityCertificate 并原子发布后，候选 generation 才与 state/MMR/public cursor 一起激活。

这些记录只用于崩溃恢复、重算比对和收集 attestations。它们不得：

- 推进公开 `finalized_height`；
- 改变查询使用的权威 state root；
- 返回 `FINALIZED_*`、REPLACED 或 EXPIRED；
- 被 snapshot、跨账本消费或 epoch close 当作最终状态；
- 覆盖仍由上一 FinalityCertificate 认证的 canonical state。

#### 阶段 B：Certified atomic publish

收齐并验证同一 FinalityStatement 的 `q` 个签名形成 FinalityCertificate 后，以下内容必须在一个数据库事务中提交，或通过具有等价原子恢复语义的 commit marker 发布：

- 不可裁剪的 `CertifiedCoreGenerationV1`：FinalizedBlockHeader、FinalityCertificate、canonical `CausalInputManifestCore`、全部 counts/roots、父链/MMR/epoch/config 引用、finalized slot、认证 derivation cursor、epoch emitted count/root、active derivation generation ID、terminal-status 和核心 checksum 清单；
- canonical FinalizedBlockBody，以及本次初始发布的 transaction/result/Receipt/event、ordered-Vertex proof、DAG witness 或 causal source payload；这些可裁剪对象必须各自封装为内容寻址 `HistorySegmentV1`，其 immutable manifest/segment ID 进入核心；
- state write batch、新 canonical state root、当前 MMR peaks/root、权威 state generation 与 authenticated derivation generation 的原子切换；
- public `finalized_height` 和 finalized block ID；
- snapshot cursor 及终态查询可见性。

异步 transaction/receipt/event/query indexes 可在发布后追赶，不进入核心 checksum；在索引追平前必须扫描仍在线的权威 segment/状态或明确报告索引滞后。作为初始 publication 一部分的 Body 和其他 segment payload 仍必须在首次公开时与核心、状态、证书一起原子可见，不能用“可裁剪”作为先发布后补数据的理由。

对外事实源是这个已提交的 certified publication record，而不是 prepared height、Header 文件或单个 ExecutionAttestation。

每条 certified publication 无论由单数据库事务还是 commit marker 发布，都必须持久化以下本地恢复记录；它不是 wire 共识对象，也不产生新的账本 ID：

这里的 `GenerationID` 是 storage schema v1 的本地 32-byte opaque identifier，由 CSPRNG 在创建 staging generation 前生成并在同一 Ledger 存储域内保持唯一；碰撞或复用必须 `SAFETY_HALT`。它只用于定位和原子切换，不参与任何 wire hash、签名或共识排序。

```text
CausalValidationRecordV1 {
  schema_version: uint16
  generation_id: GenerationID
  causal_input_manifest_id: Hash32
  causal_chunks_root: Hash32
  stream_byte_length: uint64
  chunk_count: uint64
  item_count: uint64
  ordered_vertex_count: uint64
  occurrence_count: uint64
  consumed_stream_byte_length: uint64
  consumed_chunk_count: uint64
  consumed_item_count: uint64
  consumed_ordered_vertex_count: uint64
  consumed_occurrence_count: uint64
  target_committed_slot: ProposerSlot
  epoch_emitted_vertex_count: uint64
  epoch_emitted_vertex_set_root: Hash32
  derivation_state_generation_id: GenerationID
  ordered_vertex_root: Hash32
  transaction_root: Hash32
  receipt_root: Hash32
  event_root: Hash32
  state_root: Hash32
  finalized_block_id: Hash32
  block_mmr_root: Hash32
  validation_complete: bool
  generation_content_checksum: Hash32
}
```

`schema_version` 必须为 1，27 个字段按代码块顺序使用 deterministic-CBOR integer key `1..27`，`validation_complete` 必须为 true；五个 consumed 值必须分别精确等于 manifest 的长度/计数，manifest ID 与 `causal_chunks_root` 必须从同一已验证 `CausalInputManifestCore` 重算。`target_committed_slot`、epoch emitted count/root 必须分别等于 Header 的同名事实，`derivation_state_generation_id` 必须定位到能重建该 root/count、已在 attestation 前 fsync 的不可变 generation。该 canonical manifest core 是 `CertifiedCoreGenerationV1` 的永久元数据并受 `generation_content_checksum` 覆盖，不属于可删除的 raw chunks/source。五个派生 root、FinalizedBlockID 与 block MMR root 必须逐项等于核心中的 Header/FinalityStatement。

`generation_content_checksum` 只覆盖 storage schema 冻结的不可裁剪核心 entries：canonical manifest core、Header、FinalityCertificate、counts/roots、父链/MMR/epoch/config 引用、发布游标/终态事实，以及本次初始 `HistorySegmentManifestV1`/segment ID。它明确排除本 validation record、commit marker、Body/Receipt/Event 等 segment payload、raw causal/DAG/Batch source、历史 SMT/proof payload、查询索引、物理 state/SST 文件、可变 active pointer 和 `HistoryGCRecordV1`；marker 另行绑定 validation record 自身 checksum。合法 history GC、Archive 迁移、索引重建和 compaction 因而绝不能改变该 checksum。

若存储不能用一个事务覆盖上述所有逻辑域，等价 commit-marker 实现必须使用不可变核心 generation：先把完整 state write batch、authenticated derivation generation、`CertifiedCoreGenerationV1`、证书、`CausalValidationRecordV1`、全部初始 HistorySegment manifest/payload/active ref 写到尚不可见的 staging 并 fsync，再写本地 `CertifiedPublishMarkerV1`。其完整 31-field schema、字段顺序、`HistorySegmentRefV1` 数组、tagged previous publication marker 与 domain-separated marker hash 由[存储规范第 7.3 节](../engineering/02-storage-snapshot-and-pruning.md#73-耐久-publication-marker-与-historygcrecord)冻结；不得用“至少绑定”扩展 map 参与同一 v1 hash。该 marker 是本地恢复格式，不是 wire 共识对象，也不得代替 FinalityCertificate。

marker fsync 是 commit-marker backend 的 publication 线性化点；它一旦 durable 就不能回滚。运行时在开放查询、snapshot、epoch close 或下一高度前还必须把可重建 `active_generation` pointer 原子指向它。若断电发生在 marker fsync 后、pointer 替换前，恢复器验证完整 marker/core/证书/segments/state/derivation 后必须幂等 roll-forward pointer，不能丢弃 generation；写下一 marker 前也必须先完成该步骤。所有在线读路径仍从 pointer 开始，不能扫描“最高 prepared height”。崩溃留下的无 marker generation/segment 才是垃圾；有 marker 但核心/证书/checksum/roots 不匹配，或初始 segment 在没有 matching durable GC record 时 missing/corrupt，都是损坏并 `SAFETY_HALT`。同一 `(ledger,height,previous_publication_marker_kind,previous_publication_marker_hash)` 不同 marker hash 也是存储分叉；逐字节相同重放才幂等。单数据库 backend 则以含 commit record 与 pointer 的同一事务 commit 为线性化点。

`HistorySegmentManifestV1`、segment content checksum/Merkle root/ID 和 `HistoryGCRecordV1` 的精确 storage schema 与 domain-separated hash 见[存储规范第 7.2、7.3 节](../engineering/02-storage-snapshot-and-pruning.md#72-内容寻址的-historysegment)。GC 必须先在 append-only hash chain 中 durable 写入与 marker、height、kind、segment ID 完全匹配的 record；该 fsync 是逻辑裁剪线性化点，之后才能删除 bytes。record 前缺失是 corruption，record 后无论 bytes 是否残留都视为 `HISTORY_PRUNED`。这些本地记录不改变任何 wire ID、Header root 或共识状态。

崩溃后要么上述全部对外可见，要么全部仍不可见。禁止出现 state root、Receipt 或 finalized height 已公开，但 FinalityCertificate 尚未原子关联的状态。

### 14.4 恢复检查

启动时验证：

- own Batch/ACK/Vertex 无同槽冲突；
- `EXECUTION_ATTESTATION_INTENT` 无同 height 冲突；
- EpochSealVoteLock 无同 epoch 冲突；
- 每个公开 FinalizedBlock 都有匹配的有效 FinalityCertificate；
- certified publication records 的 parent/height 连续；
- canonical state root 与最后一条 certified publication 一致；
- public finalized/query cursor 不超过最后一条 certified publication；
- Header 认证的 committed-slot checkpoint、epoch emitted count/root 与 active derivation generation 一致；trailing SKIP 不得伪装成认证 cursor；
- 每条 publication 都有 marker/事务原子绑定的 `CausalValidationRecordV1` 和 `CertifiedCoreGenerationV1`；记录为 complete，manifest/chunks root、全部 consumed counts、派生 roots、不变的核心 generation checksum 与 FinalityCertificate/Header 精确一致；
- History GC record chain 的 sequence/previous hash 连续，每条 record 指向同一 publication 核心已注册的 segment manifest/ID；
- prepared records 不得被终态查询或 snapshot 枚举为 finalized。

恢复时：

- 只有 prepared record、尚无证书：若尚无 publish marker，任何复用或继续收集 attestations 之前都必须从原始 manifest/chunks 与 DAG/Batch source 完成覆盖整个 stream/source 的验证；可从已认证、校验和完整的 durable chunk checkpoint 续验，但 checkpoint 不能把未覆盖输入视为已验证，并始终保持外部不可见；
- 已有完整证书、发布事务未提交：仍无 marker，必须验证全部 staging、原始 causal stream/source 和派生结果后重放同一原子发布；
- 发布事务已有 commit marker：先验证 marker chain、FinalityCertificate、`CausalValidationRecordV1` 自身 checksum、record 与核心内 canonical manifest/Header 的字段关系、核心清单与 `generation_content_checksum`；再枚举 `initial_history_segment_ids`，对每项按固定优先级判定：存在 matching durable GC record 则标记 `HISTORY_PRUNED` 且不要求 payload；否则 payload 必须存在并重算 manifest ID、content checksum/root 及相应 Header/Receipt/MMR/SMT 承诺；missing/corrupt 且无 record 是 storage corruption，进入 `SAFETY_HALT`。随后才能以 certified publication 为事实源幂等补发仍可得的事件并重建非权威缓存；尤其不得要求已有 GC record 的 causal chunks 或 DAG/Batch source 仍在本地；
- 发现公开状态没有证书或与证书 statement 不匹配：进入 `SAFETY_HALT`。

发现冲突进入 `SAFETY_HALT`，禁止自动选择“较新”记录。

## 15. 同步

### 15.1 信任链

新节点从可信创世开始验证：

```text
Genesis
  -> EpochSealCertificate chain
  -> each-hop ValidatorSet / ProtocolConfig
  -> target FeatureSet / GasSchedule
  -> FinalityCertificate
  -> snapshot or block replay
```

### 15.2 快速同步

1. 从本地选定的 Genesis 或显式 checkpoint trust root 开始，逐跳获取并验证 EpochSealCertificate、next ValidatorSet 与 next ProtocolConfig；中间跳不要求 FeatureSet/GasSchedule；
2. 获取目标高度的基础 `FinalityProof` 或独立 `CheckpointFinalityProof`，取得并验证目标 epoch 的完整 FeatureSet/GasSchedule，调用 `ValidateExecutionConfigBundle`，再验证 Header、validator-set chain 与 FinalityCertificate；此步认证 FinalityStatement 中签署的 `block_mmr_root`，但没有 peaks 时不声称已经重建 MMR state；
3. 获取与该 proof 各目标字段完全一致的 v1 `SnapshotManifest`，逐项匹配 state/MMR/epoch emitted count/root 并重算 manifest ID；full-validator 恢复还必须获取同一 target 的 `DAGDerivationCheckpointManifest`，逐项匹配 block/finality/committed-slot/emitted 字段并重算其 ID；
4. 可从多个 peer 并行取两类 chunks。P2P v1 以 bounded raw reader最多接受各对象的 `1_050_823` bytes，只探测第 `1_050_824` byte 判定越界且不得实例化解压器；HTTP Gateway Content-Encoding 或未来明确版本启用压缩时，才用 streaming decompressor/running counter 对解压输出执行同一 `MAX+1` 边界。第一个越界 byte 不得进入 parser/staging；任何依据声明 payload/sibling 长度的分配都必须在证明其不超过硬上限后发生。随后逐块验证 manifest ID、core hash、chunk tree root、连续 offset 和固定 1 MiB framing；peer 数量或多数意见不增加可信度；
5. 验证 Snapshot 的 MMR peaks 规范，并以 `leaf_count=target_height` 重算出第 2 步已由 FC 认证的目标 block MMR root；在不可见 state generation 中增量解码全部记录，检查 record count、key-hash 严格顺序，从空 SMT 重建并要求精确等于 proof/manifest 的 state root；
6. 流式重组 checkpoint 的严格升序、无重复 VertexID 串，从该 epoch 空 sparse set 重建 `DerivationStateGenerationV1`，要求 count/root 精确等于 proof、Snapshot 与 checkpoint manifest；少/多/重复 ID、错序、缺 chunk 或 root 错配都禁止 Validator readiness；
7. 在同一 per-ledger active-marker 锁中 fsync generation、MMR peaks、manifest、proof bundle 与导入清单。full-validator 写第 7.3 节完整 29-field `SnapshotInstallMarkerV1`；state-only 查询节点写独立 26-field `QuerySnapshotInstallMarkerV1`，不得伪填 checkpoint/generation 字段。两者都以 `install_sequence + previous_active marker kind/hash/height` CAS 串行。marker fsync 是安装线性化点：之前崩溃可丢弃 staging，之后即使 active pointer 尚未切换，恢复也必须沿唯一 chain 幂等 roll-forward，不能回滚、丢弃或任选最高 target；再原子切换相应指针。full 模式同时切 state、derivation、MMR、finalized 与 `certified_resume_cursor=Header.committed_slot`；query 模式只切 state/MMR/query cursor，永久标记 `SYNCING_DERIVATION` 且不能作为后续 publication-chain anchor。已有 active state 不得切换到更低高度；同 target 的 QUERY→FULL 是唯一允许的等高升级；
8. 从 snapshot height 后按父 ID、高度、MMR、emitted exact set 和配置激活点连续重放 FinalizedBlock；H+1 的本地 `CertifiedPublishMarkerV1` 用 tagged previous hash 引用高度 H 的 snapshot-install marker。只有 replay 实际跨入某个 epoch 时，才按该 epoch 已认证 ProtocolConfig 的内容 hash 获取并验证其中间 FeatureSet/GasSchedule，缺对象时暂停 replay，不能推翻已经验证的目标最终性；
9. 获取当前 epoch unresolved DAG window；
10. 验证 BatchAC、Vertex 和 DAGCommitWitness；
11. 从认证 `Header.committed_slot` 恢复 decider；trailing SKIP 只能重算，不能推进 GC；
12. 按 restricted jump 补齐必要 own Vertex 后再参与。

节点不能只相信远端的 `current_round` 或 `committed_height` 数字。

### 15.3 DAG 同步摘要

```text
DAGSyncSummary {
  epoch
  highest_complete_round
  gc_boundary_round
  per_round_author_bitmap
  unresolved_slot_range
  known_vertex_ids_root
}
```

摘要只用于发现缺失对象，不是最终性证明。

## 16. GC 与保留

### 16.1 DAG GC

令 `highest_complete_round` 使用第三篇 `Complete(r)` 的本地已验证定义。v1 的 inclusive GC round 边界固定为：

```text
gc_boundary_round =
  if highest_complete_round < dag_gc_rounds:
    0
  else:
    checked_sub(highest_complete_round, dag_gc_rounds)
```

round 从 1 开始，因此 boundary 为 0 时没有 Vertex 仅因窗口到期而可删；只有 `r <= gc_boundary_round`，等价于 checked `highest_complete_round-r >= dag_gc_rounds`，才满足下面的窗口条件。peer 自报 round、未完成依赖的 future round 或最高 observed round 均不能推进该边界。

round `r` 的 Vertex 只有满足全部条件才可从在线 DAG store 删除：

- 已有 FinalityCertificate 认证的 Header，其 `committed_slot.proposer_round >= checked_add(r,dag_gc_rounds)`；该 Header 证明到该 slot 的连续决策前缀，不能用本地 trailing SKIP 或 `highest_complete_round` 单独代替；
- 协议已拒绝 gap `>= dag_gc_rounds` 的新 own/weak parent，因此上述认证 cursor 之后的 wire-valid Vertex 不能新建指向 r 的边；所有在此之前已知且可能依赖它的 slot 已决定并输出；
- 对应 publication、decider checkpoint 和 authenticated emitted-set generation 已持久化；
- 同步/审计所需 DAGCommitWitness 已归档；
- `r <= gc_boundary_round`，恰好命中边界时即满足 inclusive `dag_gc_rounds` 安全窗口；
- 不再是 restricted jump、pinned repair、snapshot/checkpoint export 或 epoch close 依赖。

checked `r+dag_gc_rounds` 溢出表示本 epoch 内永不满足，不能 wrap。节点本地 `highest_complete_round` 仍决定时间窗口下界，但只有它与上述 certified cursor 条件同时成立才可删；迟到的旧 Vertex 若不能取得完整父闭包只是不完整对象，不能迫使恢复已合法裁剪的数据。

### 16.2 Batch GC

v1 不给 AvailabilityReference 设引用高度或 round 过期：同一 BatchAC 可以被本 epoch 的任意较晚 Vertex 再次引用。因此“第一次被某块消费”绝不是安全的 fragment 释放锚。每个 Batch 唯一的认证 `terminal_height` 是其所属 epoch 的 durable `EPOCH_CLOSED.final_height`；关闭前即使已经消费过、当前没有已知引用或本地 DAG 窗口已推进，也不存在证明未来合法 Vertex 不会再次引用它。epoch 尚未关闭时没有 terminal height，不能按接收时间、首次消费高度、peer 高度或本地未见引用删除。

```text
retention_release_height =
  checked_add(terminal_height, batch_retention_heights)

retention_elapsed =
  public_finalized_height >= retention_release_height
```

该比较为 inclusive：恰好等于 release height 时窗口才到期。checked addition 溢出表示本账本剩余高度内永不到期，必须继续保留，不能 wrap 或饱和后提前删除。

fragment 只有满足全部条件才可删除：

- Batch 所属 epoch 的 closing FinalizedBlock、FinalityCertificate 与同一原子 publication 中的 `EPOCH_CLOSED` 均已验证，且已存在上述 `terminal_height` 与 `retention_elapsed`；
- 所有早于等于 closing cursor、仍会被审计/同步读取的已认证 causal publication 已持久化其 Batch disposition；未获 publication marker 的 prepared 候选不得借 GC 继续复用；
- block body/archive 已达到部署 `LocalHistoryPolicy` 的冗余目标；
- 部署的争议和查询服务窗口已过；
- 不再被 pinned DAG witness、repair session、snapshot/export 或 Archive 上传引用。
- 对每个本地 `BatchRetentionManifestV1(AUTHOR_BODY|ACK_FRAGMENT)`，已经按精确 terminal/release/public heights append+fsync 唯一 `BatchGCRecordV1`；该 record 而非首次 publication marker 是 Batch obligation 的释放线性化点。

`LocalHistoryPolicy` 不是共识对象，只能延长保留，不能缩短 `batch_retention_heights` 或其他协议前置条件。ACK signer 在这些条件前删除自己的固定 fragment 是协议违规。代价是一个长期不关闭的 epoch 必须持续保留其 ACK fragments；若未来要缩短，必须另行协议化 Batch reference expiry，不能用本地启发式 GC。

`BatchRetentionManifestV1/BatchGCRecordV1` 的 exact schema、content/manifest/record hash与chain规则见存储规范§5.1。它们独立于 publication-bound HistorySegment：从未被消费的Batch也有registry entry。record前缺失bytes是损坏，record后残留bytes只作幂等清理；启动从全部无GC record的manifest重建保留counter。

每条 publication 的 `CertifiedCoreGenerationV1`、核心 checksum 清单、canonical manifest core、Header、FinalityCertificate、validation record/checksum、publish marker、全部初始 `HistorySegmentManifestV1`/ID 和全部 `HistoryGCRecordV1` 永久保留。Body、Receipt/Event/proof payload、ordered-Vertex 叶/路径、DAG witness、历史状态证明、原始 causal chunks、临时 leaf files 与已消费 DAG/Batch source 只能作为内容寻址 HistorySegment 裁剪；它们从不进入 `generation_content_checksum`，因此 segment 在线状态变化不会使已发布核心 checksum 失效。

对某一受管 segment，GC worker 必须在互斥锁内重验 marker 注册、manifest ID、content checksum/root、协议 root、所有协议保留窗口、未决引用和部署归档策略，然后先 append+fsync 唯一 `HistoryGCRecordV1`，再删除 payload。record 的 durable append 是逻辑裁剪线性化点：record 已存在时返回 `HISTORY_PRUNED`，即使 bytes 尚残留；record 不存在时 payload 必须 present 且验证通过，missing/corrupt 必须撤销该 Ledger Validator readiness 并 `SAFETY_HALT`，不能事后补 record 掩盖损坏。GC 重试按 segment ID 幂等，不得改变 core、marker、manifest 或 checksum。

其中 `CAUSAL_SOURCE` 还要求 matching publication 已有完整 record/core/FC、manifest consumed counts 和全部派生 roots 的复验；满足后删除 source，重启按第 14.4 节验证 core 与 marker/GC chain，不回退为要求已合法裁剪的 source。没有 publish marker 的 prepared/staging 输入不适用此规则，仍须从完整 manifest/chunks/source 验证或整体丢弃。

### 16.3 Proof retention

至少长期保留且不得包装成可裁剪 segment：

- FinalizedBlockHeader；
- FinalityCertificate；
- ValidatorSetProof/EpochSeal 链；
- 被保留 snapshot 的 manifest、匹配 FinalityProof 和重建所需 chunks，或明确可用的 archive locator；
- MMR peaks；
- 每个已发布高度的 HistorySegment manifest/ID 与 GC record。transaction/receipt/state proof 的 payload/节点可按已声明策略在线保留或由 Archive 重建；本地已裁剪时必须返回 `HISTORY_PRUNED`，不能伪造空 proof。

DAGCommitWitness 可以转移到冷归档，但必须满足部署/合规 `LocalHistoryPolicy` 的审计可用期；该时长不进入区块有效性或 epoch 治理 hash。

## 17. 安全性

### 17.1 排序安全

由[第三篇](03-finaldag-consensus.md)的 sticky support、direct/indirect 决策和 undecided-prefix barrier 保证所有诚实节点输出相同 slot 前缀。

### 17.2 执行安全

occurrence filter 仅依赖高度、规范顺序和认证 next_nonce。并行路径的 exact access graph、MVCC 和 prefix certification 必须等价于串行；否则一次权威串行回退重新建立唯一结果。

### 17.3 最终性安全

两个不同 FinalityStatement 若处于同一 height，各需 `q` 个签名。quorum 相交至少 `f+1`，至少一个诚实 Validator 必须违反 `EXECUTION_ATTESTATION_INTENT`。因此在故障假设内不可能同时有效。

### 17.4 Epoch 安全

新 epoch 只从旧组 `q` 签名的 EpochSeal 启动。seal 绑定最终 height、block、state、下一 ValidatorSet、配置和 seed；不同 signer subset 不改变 statement。旧新 quorum 不混合。

## 18. 活性与性能

### 18.1 正常路径

BatchAC 在 DAG 前并行形成。FinalDAG-C 正常 direct commit 使用 proposal/support/decision 三次元数据传播。执行完成后，attestation 搭载在下一批 DAGVertex 中；通常再一个 DAG round 可收集 `q` 个签名。

### 18.2 故障路径

- primary crash：timer 后 DAG 继续，slot direct skip 或被后继 anchor 解决；
- Batch 作者失败：无 BatchAC 就不进入 DAG；
- fragment signer 拒绝：至少 `k` 个诚实 signer 可恢复；
- 执行节点落后：不阻塞 DAG 排序，但 FinalityCertificate 等待 `q` 个完成执行的节点；
- 并行冲突过多：一次串行 suffix fallback，保持活性和一致性；
- 新 epoch 就绪不足：安全停机。

### 18.3 背压

定义 `public_finalized_height` 为最后一条已经原子发布的 certified publication height；`highest_ordered_candidate_height` 为 stable slot prefix 已连续派生并 durable 保存 `BlockDerivationCandidate` 的最高 height，不存在未发布 candidate 时等于 public height。必须保持前者不大于后者，并用 checked subtraction 计算：

```text
execution_lag = checked_sub(
  highest_ordered_candidate_height,
  public_finalized_height
)
```

当 `execution_lag >= max_execution_lag_heights` 时，诚实作者必须停止创建新 Batch，并在新 Vertex 中令 `availability_references=[]`；恰好等于上限即进入背压，只有降到 `<` 才恢复 payload。它仍必须创建/转发 control-only Vertex，执行 dependency repair、causal/execution/finality 同步、传播 attestation、形成 FC 和完成 epoch close，不能因背压阻断消除 lag 所需的控制路径。接收者不得把 Byzantine Vertex 携带 payload 仅因本地 lag 判为 wire-invalid，仍按协议对象上限验证。

causal input spool、chunk、增量 leaf 文件或 snapshot staging 达到本地磁盘水位时，节点必须优先保留共识控制消息并暂停新 Batch/reference 或 bulk 同步；可以清理可重建的未认证 staging 后重做，但不得截断当前 COMMIT 的规范输入、跳过 chunk 或发布部分结果。

`execution_lag` 只覆盖已经派生高度，不能发现最早 undecided slot 长时间阻塞、尚无 block candidate 时的 payload 积累。每个生产 Validator 还必须配置并持久化非共识 `BacklogBackpressurePolicyV1`：

```text
BacklogBackpressurePolicyV1 {
  undecided_slot_depth_low: uint64
  undecided_slot_depth_high: uint64
  verified_unemitted_dag_bytes_low: uint64
  verified_unemitted_dag_bytes_high: uint64
  retained_referenceable_batch_bytes_low: uint64
  retained_referenceable_batch_bytes_high: uint64
  control_dag_bytes_low: uint64
  control_dag_bytes_high: uint64
  reserved_control_bytes: uint64
}
```

每项必须`0 < low < high`，checked counters在对象durable写入/GC时更新：`undecided_slot_depth`是最高已验证proposer slot与最早未决slot之间的连续slot数；`verified_unemitted_dag_bytes`对尚未进入认证emitted set、且仍可能进入未来Past的唯一完整DAGVertex及非Batch依赖字节计一次；`retained_referenceable_batch_bytes`唯一口径是存储规范§5.1 registry公式：对每个没有`BatchGCRecordV1`的本地AUTHOR_BODY或ACK_FRAGMENT obligation manifest，累加`canonical_stream_byte_length + len(canonical(manifest))`。同一Batch的两个obligation分别计数，其自包含stream重复BatchHeader字节也有意重复；不做另一个“按BatchID/content ID去重”的实现分支，物理压缩不改变值。首次certified消费绝不能扣减，只有release条件全部满足且BatchGCRecord durable后才扣减；`control_dag_bytes`统计进入payload backpressure后新产生、尚未由certified cursor覆盖的control-only Vertex/WAL bytes。counter checksum或registry/GC chain重算不一致均撤销readiness，不能清零继续签名。

任一 counter `>= high` 或 execution lag 达上限即进入 `PAYLOAD_BACKPRESSURE`；只有全部 counter `<= low` 且 execution lag `<` 上限才退出，避免抖动。进入后诚实 Validator 必须：

- 停止构建新 Batch，停止为尚无 `DA_ACK_LOCK` 的新业务 Batch 签 ACK，不在 Vertex 增加 AvailabilityReference；因此最多 f 个 Byzantine signer不能继续形成新的 BatchAC；
- 继续重传已锁 ACK/fragment、修复已认证依赖、创建无新 payload 的 parent/control Vertex、执行 decider/causal/execution、传播 attestation/FC、rejoin proof、closing 与 EpochSeal；
- 为 control 路径保留 `reserved_control_bytes` 和独立队列；bulk query/snapshot/archive 首先降级，未认证可重建 staging 可清理；
- 对远端协议有效 payload 只做限流/quarantine/稍后拉取，不因本地 watermark 把它永久判 wire-invalid。

Validator readiness 必须证明本机 high watermarks 与 control reserve 至少能容纳当前 ProtocolConfig 的一个最大 Vertex、一个最大 Batch repair、q 个父依赖和一个最大允许 execution candidate 的流式工作集；做不到就不启用 Validator。阈值可以因硬件不同而不同，因为它只限制诚实生产，不改变 canonical order/validity；q 个诚实节点同时背压仍能靠 control DAG 排空已有 backlog并在低水位恢复。

有限 `reserved_control_bytes` 不能伪称可承受 GST 前无限异步期。若 `control_dag_bytes >= control_dag_bytes_high` 或物理 control reserve 即将耗尽，节点进入 `CONTROL_STORAGE_PAUSE`：撤销 `dag_production_ready`，停止签新的 DAGVertex 与新 ACK/Batch，但继续转发已有对象、dependency repair、执行已稳定输入、通过独立 finality control protocol发送/聚合已有 ExecutionAttestation/FC、完成已落 fence 的 closing/seal，并保持查询。它不把远端对象判无效，也不删除 Safety WAL。只有 control bytes 降到 low、存储校验通过且重新完成 readiness challenge 后才恢复 pacemaker；若决定当前最早 slot 确实还需要新 Vertex而本机无空间，安全地保持暂停并告警扩容/Archive迁移，不能以覆写未决历史换取活性。部分同步保证从资源可承载且至少 q 个 production-ready 节点的时刻开始适用，不承诺有限磁盘支撑任意长的 GST 前生产。

## 19. 攻击面

| 攻击 | 防御 |
|---|---|
| AC signer subset 操纵 | 语义 ID 只含 statement |
| Finality signer subset 操纵 | FinalizedBlock/MMR/seed 不含 envelope |
| 错误 codeword | ACK 前恢复、重编码、root 校验 |
| future/stale occurrence poison | occurrence 级跳过，不产 Receipt |
| causal input 截断或跨 manifest 拼块 | 固定 byte framing、chunk root、完整计数与 publish marker |
| 伪造 REPLACED | 必须给同 sender/nonce 不同 winner Receipt |
| 伪造 EXPIRED | 必须给最终 tip，以及固定 nonce key 的合法 non-inclusion 或 `next_nonce<=nonce` present proof |
| access scope 越权 | 读取未授权值前返回 `ACCESS_SCOPE_VIOLATION`，FAILED Receipt 消费 nonce |
| 合法 PREFIX 预测失配 | 丢弃推测 suffix，至多一次权威串行回退 |
| MVCC 内存攻击 | 固定版本、字节、边和 retry 上限 |
| 并行结果分叉 | prefix certification 和串行 oracle |
| attestation 双签 | durable `EXECUTION_ATTESTATION_INTENT` 和 HSM policy |
| epoch overlap | closing frontier、EpochSeal、旧新 quorum 隔离 |
| 投毒或混装 snapshot | 匹配 FinalityProof、chunk tree、严格 key 顺序并从空 SMT 重建 root |
| GC 后无法举证或恢复误判 | core/Finality/epoch/MMR 与 segment manifest/GC record 长期保留；payload 由 Archive 提供并独立验 root |

## 20. 必测场景

- height 0 创世和 height 1 首块；
- 一次输出多个 COMMIT slot 时高度连续；
- future/stale/duplicate/expired 不进树且无 Receipt；
- FAILED winner 消费 nonce；
- 接受 `nonce=UINT64_MAX-1` 后进入耗尽哨兵，后续 occurrence 全部 `NONCE_EXHAUSTED` 且无溢出；
- causal item/frame 长度前缀跨 chunk、超大单 transaction 跨多个 chunk、截断/追加字节和跨 manifest chunk 均产生确定结果；
- `CausalValidationRecordV1` 的 manifest/root/count/derived-root/core generation checksum 任一错配都拒绝发布；合法 history GC 前后 checksum 必须逐字节不变；
- marker 后 segment present 时验证 content checksum/root 与协议 root；missing+matching durable GC record 得到 `HISTORY_PRUNED` 并正常恢复核心；missing/corrupt 且无 record 必须停机；GC record fsync 后、bytes 删除前崩溃按已裁剪幂等收敛；marker 前删除或不完整 record 绝不能公开；
- 同 sender/nonce REPLACED proof；
- REPLACED/EXPIRED 的 `candidate_height`、TRUST_ROOT_STATE/FINALIZED_PARENT、父状态 meta/auth/nonce 三份 proof及candidate bundle；覆盖active policy切换、`nonce > admission future-gap`仍可证明、错误network/ledger/trust-root类型、跨epoch activation、sealed parent伪造same-epoch、残缺账户三元组和历史proof已裁剪时的`PROOF_UNAVAILABLE/HISTORY_PRUNED`；
- 同块创建后普通交易、同块较早nonce推进后的post-state都不能反向为该块制造授权；proof必须落到`candidate_height-1`父root；
- EXPIRED 的 `next_nonce==nonce`、`next_nonce<nonce`，以及 nonce key non-inclusion 的已合法自证过期账户创建；自签 victim sender 的普通不存在账户交易即使终态 nonce non-inclusion 也必须拒绝；
- `next_nonce>nonce` 时拒绝 EXPIRED；
- access set 扩大触发一次 suffix fallback；
- MVCC version/bytes/edge 超限；
- prefix 0 失败和中间 prefix 失败；
- 不同线程调度产生相同 roots；
- 同 statement 不同 FinalityCertificate signer subset；
- WAL 在每个 fsync 边界崩溃；
- closing request、q closing vertices、frontier 和 EpochSeal；
- 新 epoch 同步失败安全停机；
- EpochTransitionProof 的 set/config 缺失或篡改时拒绝切换；FinalityProof 的目标 FeatureSet/GasSchedule 缺失、typed parameter 无效或内容 ID 错配时拒绝；全历史重放缺任一中间 epoch 执行配置时只能停止重放，不能误报最终性证明无效；
- 快速同步只要求每跳 set/config/seal 与目标完整 bundle；FC 先认证 MMR root，只有 snapshot peaks 验证后才得到可追加 MMR state；
- 空/非空 snapshot、frame 跨 chunk、chunk 重排/缺失/重复、record 乱序/重复/遗漏、root 不匹配和导入每个 fsync kill point；
- `execution_lag` 在 `limit-1/limit/limit+1` 的 payload 背压边界、`gc_boundary_round` 等号边界、Batch retention release 等号边界及全部 checked overflow；控制-only Vertex/attestation/finality 在背压期间仍推进；
- DAG/Batch GC 后轻证明仍可验证；
- archive DAGCommitWitness 可复核 direct 与 indirect 决策。
