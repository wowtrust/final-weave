# FinalWeave v1 FinalDAG-C 共识、排序与活性规范

> 共识：FinalDAG-C v1
> 前置：[数据模型与密码学](01-data-model-and-cryptography.md) · [BatchAC 与元数据 DAG](02-data-availability-and-blockdag.md)
> 后续：[最终性、执行证明与 Epoch](04-finality-execution-and-epochs.md)

## 1. 目标

FinalDAG-C 直接解释已由作者签名、但无独立顶点证书的元数据 DAG（uncertified）中的因果关系，以产生所有诚实节点一致的 proposer slot 决策前缀。

协议没有单独的链式提案、显式投票、超时证书或 Vertex 认证消息。每轮 Validator 签署的 DAGVertex 同时承担：

- threshold logical clock；
- 对旧 Vertex 的隐式 support；
- certificate pattern；
- 执行 attestation 传播；
- epoch close 信号传播。

独立 BatchAC 已在 Vertex 进入 DAG 前证明大批次可恢复，因此共识路径只处理小型元数据。

## 2. 系统参数

```text
n = 3f + 1
q = 2f + 1
4 <= n <= 253
1 <= proposer_slots_per_round P <= q
```

v1 默认：

```text
P = 2
```

`P=q` 只属于实验性能 profile，不是生产默认。增大 P 可以让更多 Vertex 在三轮内直接提交，但也会增加 Byzantine/慢 proposer 制造 undecided slot 的机会。

安全性在任意消息延迟下成立。活性依赖部分同步和本规范的 pacemaker、primary timer 与 restricted round jump。

## 3. Round 与重叠三轮 Wave

每个 epoch 的普通 round 从 1 开始。所有 Validator 每 round 至多签一个 Vertex。

对 proposer round `r` 的每个 slot：

```text
r      proposal round
r + 1  support round
r + 2  decision round
```

每个 round 都启动新的一组 proposer slots，因此 waves 彼此重叠：

```text
round 10: 提议 slot(10,*), 支持 slot(9,*), 决定 slot(8,*)
round 11: 提议 slot(11,*), 支持 slot(10,*), 决定 slot(9,*)
```

## 4. Proposer schedule

### 4.1 Schedule 输入

每个 epoch 的 schedule 只依赖：

- epoch seed；
- ValidatorSet；
- epoch 冻结的 `P`；
- round。

不得使用本地到达顺序、墙钟、证书 signer subset 或未最终 DAG 数据。

FinalDAG-C v1 不使用 reputation adjustment。性能/遗漏指标可以供治理决定下一 epoch 是否移除或轮换成员，但不能在同一协议版本中改变 proposer 映射。

### 4.2 基础算法

ValidatorSet 规范 index 为 `0..n-1`。下列函数的前置条件是普通 `round >= 1`；round 0 只存在 synthetic anchor token，绝不能调用 proposer schedule 或 `Slot`。令：

```text
cycle  = floor((round - 1) / n)
offset = (round - 1) mod n

rank_key(v) = DomainHash(
  "PROPOSER_SCHEDULE", network_id, ledger_id,
  canonical({epoch_seed, epoch, cycle, validator_id: v.validator_id})
)

permutation = validators sorted by (rank_key, validator_id)
proposer(round,j) = permutation[(offset + j) mod n]  for j=0..P-1
```

同一 round 的 P 个 proposer 必须不同。每个对齐的 n-round cycle 中，每位 Validator 恰好担任一次 primary；因此只要至多 f 个 Byzantine，每个完整 cycle 恰有至少 `q` 个诚实 primary，进而无限执行中存在无限多个诚实 primary。跨 cycle 的任意滑动 n-round 窗口不承诺同一计数。每 cycle 重新洗牌可以降低长期固定顺序带来的定向攻击窗口。

```text
Slot(r,j) = (epoch, r, j, proposer_index), require r >= 1
```

`Slot(r,0)` 是 primary。只有 primary 享有 pacemaker 等待保证；其他 slot 是不阻塞 round 的性能机会。

一个 DAGVertex `P` 当且仅当 `P.epoch/round/author_index` 分别等于某个 schedule slot 的 `epoch/proposer_round/proposer_index` 时，才是该 slot 的 proposal；`proposer_rank` 由 schedule 唯一恢复。非当选作者的 Vertex 仍是合法 DAG 元数据和 payload 来源，但 `slot(P)` 未定义，不能被 commit/skip decider 当作 proposal。

Schedule 是上述纯函数，不需要网络投票；节点至少提前计算并缓存 `schedule_lookahead_rounds`，缓存值必须可从 epoch seed 重算。任何改变 permutation、cycle 或 offset 公式的调整都需要新协议版本。

## 5. Vertex parent 规范

有效 round `r>1` Vertex 至少有 `q` 个 round `r-1` 不同作者 strong parents。`own_parent` 指向作者已签署的最高 lower-round Vertex；若它来自 `r-1`，可同时放入 strong set，否则只作为 sticky support 链的首个逻辑父引用。

逻辑访问顺序固定：

```text
1. own_parent
2. strong_parents，按 (author_index, vertex_id)
3. weak_parents，按 (round, author_index, vertex_id)
```

若 own parent 同时存在于 strong array，DFS 只访问一次。

本文统一使用包含自身、但只含已验证签名 `DAGVertex` 的因果过去：

```text
Past(V) := {V} union SignedDAGVertexParentClosure(V)
```

遍历到 round 1 的 `GenesisAnchorID` 时，该 parent edge 立即终止；anchor 是从已认证 epoch 上下文重算的 terminal token，不是 DAGVertex，因而永不属于 `Past(V)`、`VertexDelta`、`GloballyEmittedVertices` 或 ordered-vertex tree。任何其他不能解析为完整有效签名 DAGVertex 的 parent 都使依赖尚未完成，不能参与 decider。

因此 committed proposer 自己引用的 Batch 一定进入其 causal delta；若需要严格祖先集合，本文会显式写 `Past(V)-{V}`。

## 6. Support

### 6.1 定义

对 Vertex `B` 和 slot `s=(epoch, proposer_round, proposer_rank, proposer_index)`：

```text
SupportedProposal(B, s)
```

执行确定性 DFS：

1. 从 `B` 的 parents 开始，按第 5 节顺序深度优先；
2. 遇到属于 slot `s` 的 Vertex 时立即返回该 VertexID；
3. 已访问 VertexID 不重复访问；
4. synthetic GenesisAnchor 没有父或 proposal 语义，访问到它立即停止该分支；
5. 遍历完成仍未找到时返回 `⊥`。

```text
Supports(B, P) := SupportedProposal(B, slot(P)) == P.vertex_id
NoSupport(B, s) := SupportedProposal(B, s) == ⊥
```

support 是从签名 DAGVertex 及其完整因果历史派生的隐式投票，没有单独投票对象。

### 6.2 Sticky support

own parent 必须首先访问。因此诚实作者一旦在某 round 的 Vertex 中支持 slot `s` 的 proposal `P`，其以后所有 Vertex 的 own chain 都会先找到 `P`，不能改为支持同 slot 的另一个 equivocation。

这是一项安全关键规则：实现不得用并行 DFS 的非确定完成顺序选择“第一个”对象。

## 7. Certificate pattern

设 proposal `P` 位于 round `r`。

round `r+2` 的 Vertex `C` 认证 `P`，当且仅当 `C` 的完整强父集合中至少有 `q` 个 round `r+1`、不同作者 Vertex 支持 `P`：

```text
Certifies(C, P) :=
  count_distinct_authors({
    V |
      V in StrongParents(C) and
      V.round == r+1 and
      Supports(V,P)
  }) >= q
```

`C` 称为 `P` 的 certificate block。它只是 DAG pattern，不是证书 envelope。

一个 slot 至多有一个 proposal 能形成 certificate pattern。否则两个 `q` support 集合相交至少 `f+1` 个作者，其中至少一个诚实作者违反 sticky support。

## 8. Direct decision

### 8.1 Direct commit

```text
DirectCommit(P) :=
  count_distinct_authors({
    C |
      C.round == P.round + 2 and
      Certifies(C,P)
  }) >= q
```

DirectCommit 需要三层可验证对象：

```text
proposal P at r
  <- q supporters at r+1
  <- q certificate blocks at r+2
```

节点必须拥有用于计数的完整 Vertex 和依赖，不能仅相信 peer 报告的计数。

### 8.2 Direct skip

```text
DirectSkip(s) :=
  count_distinct_authors({
    V |
      V.round == s.proposer_round + 1 and
      NoSupport(V,s)
  }) >= q
```

这是 slot 级判定。即使已看到多个 equivocation，也只有当 `q` 个不同作者的 support-round Vertex 对该 slot 均返回 `⊥` 时才成立。

### 8.3 Direct decider

```text
TryDirectDecide(slot s):
  if DirectSkip(s):
      return SKIP

  for each proposal P observed for s in VertexID order:
      if DirectCommit(P):
          return COMMIT(P)

  return UNDECIDED
```

先检查 DirectSkip 与先检查 DirectCommit 不应改变合法 DAG 的结果；固定先 skip 便于实现和向量一致。

## 9. Indirect decision

Direct 规则无法决定时，必须使用后继 slot 的已决定 proposal 作为 anchor。不得因超时、本地缺失或认为 proposer 很慢而直接跳过。

### 9.1 全局 slot 顺序

```text
SlotKey(s) = (s.proposer_round, s.proposer_rank)
```

全网按 SlotKey 升序输出。

### 9.2 Anchor

对 slot `s`，其 proposal round 为 `r`。在当前从新到旧计算出的临时 decision sequence 中，anchor 是第一个满足以下条件的更高 slot：

- `anchor.proposer_round > r + 2`；
- anchor 状态不是 DirectSkip；
- anchor 当前是 `UNDECIDED` 或 `COMMIT`。

直接被 SKIP 的 slot 不能成为 anchor。

临时 sequence 的每项必须同时保存 `direct_decision` 与最终 `decision`，否则实现无法区分 DirectSkip 和后续间接 SKIP。`FirstAnchorAfter` 按 SlotKey 升序寻找 `proposer_round > r+2`、`direct_decision != SKIP` 且最终状态为 `UNDECIDED` 或 `COMMIT` 的第一项；不得跳过一个符合条件但尚未决定的近 anchor 去使用更远 committed anchor。

### 9.3 Certified link

若 anchor 已 `COMMIT(A)`：

```text
CertifiedLink(A,P) :=
  Past(A) 中存在 Vertex C，且 Certifies(C,P)
```

`Past(A)` 使用签名 parent edges，不含本地旁路缓存或只通过 gossip 得到但未被 A 引用的对象。

对 anchor `A` 和旧 slot `s`，定义：

```text
CertifiedCandidates(A,s) = {
  P |
    P in Past(A) and
    slot(P) == s and
    CertifiedLink(A,P)
}
```

候选集合只能从 `Past(A)` 及其完整依赖计算，不能使用“本节点还额外见过哪些 equivocation”的 gossip 视图。根据 sticky support 和 quorum intersection，合法执行中该集合大小至多为 1；若实现算出两个候选，说明安全假设、依赖完整性或实现已失效，必须进入 `SAFETY_HALT`。

### 9.4 间接规则

```text
TryIndirectDecide(slot s, decision_sequence):
  anchor = FirstAnchorAfter(s, decision_sequence)

  if anchor does not exist:
      return UNDECIDED

  if Decision(anchor) == UNDECIDED:
      return UNDECIDED

  assert Decision(anchor) == COMMIT(A)

  candidates = CertifiedCandidates(A,s)

  if len(candidates) == 1:
      return COMMIT(the only element of candidates)

  if len(candidates) > 1:
      SafetyHalt("multiple certified candidates for one slot")

  return SKIP
```

若 slot 存在 equivocation，anchor 因果历史中的 certificate pattern 唯一确定能被认证的 proposal；本地在 anchor 之外额外见到另一个 equivocation 不得改变结果。anchor 历史中没有 certificate link 就 SKIP。

## 10. 完整 decider 伪代码

```text
Complete(r) := 本地拥有 round r 至少 q 个不同作者的完整有效 Vertex，
               且 decider 所需的全部 parent/BatchAC 依赖已验证

highest_complete_round := max({r | Complete(r)})，不存在时为 0
```

`highest_complete_round` 可以因一次同步向前跃迁，但不能仅依据 peer 声称的 round 或缺依赖对象计算。`last_emitted_slot` 是已经 durable 派生的最大 SlotKey；epoch 初始使用独立 tagged 值 `BEFORE_FIRST_SLOT`。该 sentinel 不是 ProposerSlot，不具有 proposer/author/rank，也绝不能传入 `Slot()`。

```text
TryDecide(last_emitted_slot, highest_complete_round):
  if highest_complete_round == 0:
    return []

  sequence = empty deque
  lower_round = 1 if last_emitted_slot == BEFORE_FIRST_SLOT
                else max(1, last_emitted_slot.proposer_round)

  // 从新到旧推导状态，使较新 committed slot 可作为旧 slot anchor。
  for round from highest_complete_round down to
                 lower_round:
    for rank from P-1 down to 0:
      s = Slot(round, rank)

      direct_d = TryDirectDecide(s)
      d = direct_d
      if direct_d == UNDECIDED:
        d = TryIndirectDecide(s, sequence)

      sequence.push_front({slot:s, direct_decision:direct_d, decision:d})

  decided_prefix = []

  // 绝不能越过最早未决 slot。
  for record in sequence ascending:
    s = record.slot
    d = record.decision
    if last_emitted_slot != BEFORE_FIRST_SLOT and s <= last_emitted_slot:
      continue
    if d == UNDECIDED:
      break
    decided_prefix.append((s,d))

  return decided_prefix
```

每次新 Vertex、依赖或同步对象到达后可以重算未决后缀。已输出前缀必须不可变；若重算得到冲突结果，节点进入 `SAFETY_HALT`。

## 11. 从 slot 前缀到 Vertex 顺序

对每个按顺序输出的 `COMMIT(P)`：

```text
delta = Past(P) - GloballyEmittedVertices
```

GenesisAnchor terminal token 已由 `Past` 定义排除，因此不会进入 delta。delta 排序：

```text
(round ASC, author_index ASC, vertex_id bytes ASC)
```

同一 `(epoch, round, author)` 若有多个 equivocation，`Past(P)` 中每个不同 VertexID 都保留并按上述三元组排序；committed proposer 只是其中之一。每对冲突签名可以另外形成 evidence，但不得用“本地先见分支”删除某个 Vertex 的 payload occurrence。只有已经存在于 `GloballyEmittedVertices` 的相同 VertexID 才从 delta 排除。每次派生 block skeleton 时，在同一个 durable derivation transaction 中把完整 delta 加入该集合；崩溃恢复不得部分标记。由于所有 parent 的 round 都更低，上述排序仍保持父先于子。

`GloballyEmittedVertices` 不是可丢缓存，而是当前 epoch 的精确 authenticated set。每个已认证 Header 固定其累计 count/root；实现按[数据模型第 13.3 节](01-data-model-and-cryptography.md#133-epochemittedvertexset)用 copy-on-write generation 从父 root 验证 non-membership、插入整个 delta，并在签 ExecutionAttestation 前 fsync。缺 sparse-tree 节点不能当作“不存在”。epoch 边界从认证空 root 重新开始；SKIP 不修改集合，最后一个 COMMIT 后尚未由下一 Header 认证的 trailing SKIP 也不得推进可恢复游标。

SKIP slot 不直接产生 delta，但其 Vertex 以后仍可能通过其他 committed proposal 的 causal history被纳入。

每个 COMMIT slot 派生一个连续 FinalizedBlock，详细见[第四篇](04-finality-execution-and-epochs.md)。

## 12. Round pacemaker

### 12.1 普通推进门槛

节点只有取得至少 `q` 个 round `r-1`、不同作者、完整有效 Vertex 后，才可创建 round `r` Vertex。

“完整有效”包括：

- Vertex 签名和 parent 规则有效；
- 必需父历史已取得；
- AvailabilityReference 的 BatchAC 有效；
- 没有超过资源上限。

### 12.2 Primary proposal timer

当节点已取得 round `r` 的 `q` 个 Vertex、准备创建 `r+1` Vertex，但其中没有 `Slot(r,0)` primary proposal：

1. 启动不低于 ProtocolConfig `primary_timeout_ms` 的本地 primary timer；
2. timer 内收到有效 primary 时，必须让自己的 `r+1` Vertex 支持它；
3. timer 到期仍未收到时，允许创建不支持 primary 的 Vertex。

非 primary proposer 不触发等待。

### 12.3 Certificate timer

若节点已经看到 primary proposal，在准备 round `r+2` Vertex 时：

- 锁定本轮及时 primary parent 后，收到与其同作者约束兼容的 `q` 个支持前一 primary 的 round `r+1` Vertex，立即创建；
- 否则等待不低于 ProtocolConfig `certificate_timeout_ms` 的本地 certificate timer；
- 到期后只要已有合法 `q` strong parents 就继续推进。

timer 只影响活性，不能代替 DirectSkip 或任何安全证明。

### 12.4 自适应

timeout 可以依据已认证 peer RTT 分位数调整：

- primary 连续未直接决定时乘以有界退避因子；
- 连续成功后缓慢降低；
- epoch config 的 `primary_timeout_ms` / `certificate_timeout_ms` 是相应本地 timeout 的安全下界；本地可有运维软上限，但必须允许在持续失败时继续有界分段增长，否则无法建立 GST 后最终超过网络延迟的活性前提；
- 本地 timeout 差异不得改变 slot schedule 或决定规则。

## 13. Restricted round jump

### 13.1 禁止朴素跳轮

节点可能通过 future Vertex 的因果历史一次获得远高于本地 current round 的 `q` 父证据。它不得直接签 target round Vertex。

朴素 jump 可能让诚实节点跳过本应创建的中间 Vertex，使两轮前仍未决的 proposer 再也无法获得足够 decision patterns，破坏部分同步活性。

### 13.2 规范规则

定义：

```text
DecisionRound[x] == UNDECIDED
```

当且仅当 proposer round `x` 中存在任一 rank `j` 使 `Decision(Slot(x,j)) == UNDECIDED`。这是保守且全局可复算的定义；不得根据本地认为该 slot “暂时不在队首”而忽略它。已决定的后续 slot 不能把它变为 decided。

`curr` 是本地 durable WAL 中最高已签 own Vertex round；尚未签普通 Vertex 时为 0。`target` 是节点已验证足够 parent 链后希望签署的更高 round。从 `curr` 跳到 `target`：

```text
RestrictedJump(curr, target):
  require target > curr

  for r_prime from curr + 1 to target - 1:
    if r_prime >= 3 and
       DecisionRound[r_prime - 2] == UNDECIDED:

      WaitOrFetchAtLeastQParents(r_prime - 1)
      core = BuildVertexCore(r_prime)
      PersistOwnVertexIntent(core)
      Fsync()
      vertex = SignWithDAGKey(core)
      PersistAndBroadcast(vertex)

  WaitOrFetchAtLeastQParents(target - 1)
  core = BuildVertexCore(target)
  PersistOwnVertexIntent(core)
  Fsync()
  vertex = SignWithDAGKey(core)
  PersistAndBroadcast(vertex)
```

硬性要求：

- 中间 round 按升序处理；
- 若该 round 已签过，只能重发 WAL 中原 Vertex；
- 若不能为中间 round 取得 `q` 个合法父，不能继续 target；
- 创建中间 Vertex 时照常应用 primary/certificate parent 约束；
- 不允许生成没有合法父的“空补轮 Vertex”；
- restricted jump 决策和 own signing intent 必须持久化。

### 13.3 Future quarantine

若收到 `round > local_highest + max_future_round_gap`：

- 不直接递归获取无限历史；
- 放入按 peer/epoch 限额的 quarantine；
- 先获取同步 summary、FinalityCertificate 和受限 DAG window；
- 验证存在合法 q-parent 链后才执行 RestrictedJump。

future quarantine 与同槽 unreferenced-sibling quarantine 都只是有硬上限的旁路 cache，不是共识 DAG。任何 VertexID 后来被已接纳 Vertex、证书/witness 或 anchor 精确引用时，节点必须绕过旁路 cache 配额进入 author-fair dependency fetch，递归闭合并把每个验证成功的对象提升到 durable dependency store；闭包完成前不得对该 root 计算 support、direct/skip 或 commit。驱逐只会触发按 ID 重拉，不得把原 wire 对象变为无效。纯旁路 sibling 永不参与 `Support` DFS，因而不同节点的 quarantine 到达/驱逐顺序不能改变 slot 决策。

## 14. 活性论证

GST 后，令诚实消息延迟不超过 `Δ`，timeout 最终大于所需界。

### 14.1 Round 同步

每个诚实 Vertex 引用上一 round 至少 `q` 个作者，并且所有诚实作者持续 multicast。任意最领先诚实节点的 Vertex 最终到达其他诚实节点；restricted jump 保证落后节点补齐必要中间 Vertex。因此诚实节点持续进入更高 round。

### 14.2 诚实 primary

诚实 primary 进入 round 后立即 multicast。其他诚实节点的 primary timer 足够长，因此都在创建 support-round Vertex 前收到并支持它。

### 14.3 Certificate 和 direct commit

至少 `q` 个诚实 support Vertex 被所有诚实节点取得。certificate timer 足够长，因此至少 `q` 个诚实 decision-round Vertex 的 strong parents 包含 certificate pattern，DirectCommit 成立。

### 14.4 旧未决 slot

不能只以“无限多个诚实 primary”跳过最近未决 anchor；`FirstAnchorAfter` 必须保留第 6.4 节的 nearest-anchor 语义。v1 使用下面的有限排空论证。

令 `R_good` 为 GST 后第一个满足以下条件的轮次：诚实消息延迟已落入最终 timeout，诚实 primary、support/certificate Vertex、parent selection和restricted jump均满足14.1–14.3，因此每个诚实primary最迟在其`r+2`证据层形成DirectCommit。对任意待决定target round `r`，定义：

```text
x = max(r + 3, R_good)
b = 1 + n * ceil((x - 1) / n)   // 不早于 x 的 aligned cycle start
decision_horizon = b + 3*n + 1
```

proposer schedule保证每个长度n的aligned cycle中每个Validator恰好一次成为primary，故三个连续cycle共有至少`3q=6f+3`个诚实primary。任意长度`3n=9f+3`且不含三个连续诚实primary的H/B序列，每组三个位置最多两个H，因此至多`2n=6f+2`个H，矛盾。于是三个cycle内必存在连续轮次`k,k+1,k+2`的诚实primary，三者最迟分别在`k+2,k+3,k+4`取得DirectCommit，且`k+4 <= decision_horizon`。

FinalDAG-C的多slot nearest-anchor归纳义务固定为：在这三个连续primary已DirectCommit且所需DAG完整时，round `k`的primary解决`k-3`全部slot，`k+1`解决`k-2`，`k+2`解决`k-1`；再按SlotKey反向归纳，所有更早slot的近端anchor已不再UNDECIDED，最迟使用上述committed primary得到唯一CertifiedCandidates结论。因此：

```text
highest_complete_round >= decision_horizon
  => Decision(Slot(r,j)) != UNDECIDED, for every legal rank j
```

从x到该保守horizon少于等于`4n`轮，v1 `n<=253`时至多1012轮；这是GST后旧前缀的极端排空界，不改变诚实primary的三层正常fast path。`P=1/2/q`不改变论证，因为primary rank 0在同round按SlotKey先于secondary。完整证明仍必须在FinalWeave多slot可执行模型中成立；不能直接照搬单slot结论，也不能声称`k..k+2`的secondary已被这三个primary自动决定。

由于输出前缀停在第一个undecided，决定一旦可输出就不会被后续证据重排。若实现的模型无法证明上述nearest-anchor归纳或找到超过horizon仍未决的轨迹，必须阻止发布，不能改成跳过较近UNDECIDED anchor使用远端commit。

## 15. 安全性论证

### 15.1 同 slot 认证唯一

两个 proposal 若都形成 certificate pattern，各自需要 `q` 个 supporter。两 quorum 相交至少 `f+1`，至少一个诚实作者必须支持两者，与 sticky support 矛盾。

### 15.2 Direct commit/skip 互斥

DirectCommit 意味着 proposal 有 `q` support；DirectSkip 意味着有 `q` 作者在该 support round 对 slot 返回 `⊥`。两集合存在诚实交集，同一个签名 Vertex 不可能同时支持和不支持。

### 15.3 Direct commit 不会被间接 skip

DirectCommit 有 `q` certificate blocks。任何足够高、可成为 committed anchor 的因果前缀都必须通过 quorum intersection 链接到该 certificate pattern；否则需要诚实 own chain 丢弃已支持历史。因而后继 anchor 的 CertifiedLink 检查不能把已直接提交 proposal 判为 skip。

### 15.4 相同 anchor 得到相同间接决定

有效 Vertex 的 parent list 和完整因果历史由 VertexID 承诺。两个节点以同一 committed anchor 计算 `CertifiedCandidates(A,s)` 时输入相同；集合大小为一时提交同一 proposal，为零时都跳过。anchor 之外的本地 gossip 集合不参与计算。集合大小超过一是不可继续投票或签执行证明的安全违规，而不是任意选择分支。

### 15.5 全局前缀

所有节点使用相同 SlotKey，且在第一个 UNDECIDED 停止。已输出序列只能是另一个诚实节点已输出序列的前缀，不会出现相反顺序。

## 16. DAGCommitWitness

同步或审计方可以构造：

- DirectCommit：proposal、`q` supporters、`q` certificate blocks 及依赖；
- DirectSkip：slot 描述、`q` no-support Vertex；
- IndirectCommit：target proposal、committed anchor 和 CertifiedLink；
- IndirectSkip：committed anchor 及其完整受限因果证明，证明 `CertifiedCandidates(anchor,slot)` 为空；
- decided prefix：从上次 checkpoint 到目标 slot 的连续决策。

不存在证明通常不能仅靠短 inclusion proof 表达，因此 IndirectSkip witness 可以较大。它只服务审计和同步，不放入公共 FinalityProof。

## 17. 故障与攻击

| 场景 | 行为 |
|---|---|
| primary 崩溃 | timer 后 support round 继续，slot 可 DirectSkip |
| primary equivocation | sticky support 使至多一个 proposal 可认证 |
| 次级 proposer 慢 | 不触发 timer；可能 undecided，后继 anchor 解决 |
| Byzantine far-future Vertex | quarantine、依赖验证、restricted jump |
| round flooding | 每作者每 round 有界对象；peer 配额 |
| 弱边操纵 | 弱父作者不作为独立投票、也不计 strong-parent quorum；边仍按规范次序进入 causal Support DFS，并受排序/数量上限约束 |
| 缺失 Batch | 无有效 BatchAC 的 Vertex 无效 |
| 选择性传播 decision Vertex | direct 证据至少含诚实作者；后继诚实 anchor 最终传播决定 |
| 本机 decider 冲突 | `SAFETY_HALT`，导出 witness，不再签执行 attestation |

## 18. 实现状态

每个 ledger/epoch 至少维护：

```text
current_round
highest_complete_round
own_vertex_by_round
slot_decision_map
last_emitted_slot
emitted_vertex_set
primary_timer_state
certificate_timer_state
restricted_jump_cursor
dag_gc_boundary
```

普通索引缓存可重建；own signing intent、每个已发布 Header 认证的 last emitted slot、active `emitted_vertex_set` generation/count/root、已派生高度和安全停机状态必须 durable。当前 epoch exact set 不能依赖已可裁剪的 causal sidecar 或远端 peer 重建。中途快照恢复使用与同一 Header 匹配的 `DAGDerivationCheckpoint`；缺少它时节点保持 `SYNCING_DERIVATION` 且不可签名。WAL 细节见[第四篇](04-finality-execution-and-epochs.md)。

## 19. 必测性质

- 两个 equivocation 各被不同 Byzantine 节点传播；
- 同一 Byzantine slot 的无限未引用 sibling 只改变有界 quarantine/evidence cache，不改变 Support/Direct/Indirect；晚引用分支完成 dependency promotion 前保持 pending，完成后所有到达/驱逐排列给出同一决定；
- DirectCommit 与 DirectSkip 组合搜索不可同时成立；
- 对所有合法`f=1..84`和每cycle任意独立permutation，三个aligned cycles必出现连续三个诚实primary；保留“两周期不足”的n=4/n=7反例；
- 对`P=1,2,q`和每个target rank，强制`k..k+2` primary DirectCommit、其他slot任意DirectSkip/UNDECIDED/COMMIT，exact `TryDecide`必须在`b+3n+1`前决定所有round `<=k-1`；
- 覆盖整轮DirectSkip、anchor跳幅大于3、Byzantine primary+未决secondary、equivocation和不同到达顺序；删除restricted-jump的mutant必须复现活性失败，跳过最近UNDECIDED anchor的mutant必须触发安全反例；
- 多 proposer slot 下第一个 undecided 阻塞后续输出；
- direct 失败后不同到达顺序得到相同 indirect 结果；
- primary 崩溃不阻塞 round；
- P=1、默认 P=2 和实验 P=q；
- 极远 future round 的 restricted jump；
- `r_prime-2` 未决时必须补发中间 Vertex；
- 已签中间 round 的恢复只重发原字节；
- epoch 边界不引用旧 epoch Vertex；
- DAG GC 后 witness 同步仍能验证 checkpoint。

决策核心必须进行状态空间探索或等价机械化验证，特别覆盖 Byzantine equivocation、消息丢失、崩溃恢复、round jump 和多 slot 交错。
