# FinalWeave 调试、测试与开发进阶手册

> 目标读者：已经读完前四章，准备实现、测试或值守 FinalWeave 的开发者  
> 协议基线：FinalDAG-C v1  
> 文档状态：设计基线；指标名、命令和目录在实现落地时以版本化契约冻结  
> 上一篇：[第一次贡献教程](04-first-contribution-tutorial.md) ｜ 路线入口：[学习路线](00-learning-path.md)

这一章不把概念拆成一张术语表。我们继续追踪 Alice 的同一笔交易，并故意让系统在每一层都出一次故障。每次都按同一条路径工作：

1. 先说清用户看到的症状；
2. 再找到能够证实或排除某一层的对象；
3. 把现场缩成可重复的确定性测试；
4. 修复后同时验证安全性、活性和性能没有退化。

这样学到的不是一组名词，而是一种可以直接用于开发和值班的证据方法。

## 1. 从 Alice 的一次超时开始

假设 ledger-demo 有 4 个 Validator：

~~~text
n = 4
f = 1
q = 3
k = 2
P = 2
~~~

其中：

- n=3f+1，最多容忍 f 个 Byzantine 故障；
- q=2f+1，是 BatchAC、DAG 图样和最终执行认证使用的不同成员门槛；
- k=f+1，是 BatchBody 的纠删码恢复门槛；
- P 是每轮 proposer slot 数，生产默认值为 2，合法范围为 1..q，只能在 epoch 边界变更。

Alice 提交：

~~~text
sender = Alice
nonce = 18
valid_until_height = 900
operation = KVPut("device/42/status", "online")
~~~

SDK 得到 tx_id，但等待终态超时。这个现象本身只说明客户端没有及时得到可验证结论。它不证明交易失败，也不证明节点已经丢失交易。

正常情况下，这笔交易会依次经历：

~~~text
Gateway 接纳
  -> 本地 Mempool
  -> BatchBody 与 BatchAC
  -> DAGVertex 引用
  -> proposer slot 的 COMMIT 或 SKIP 决定
  -> 稳定 slot 前缀与 FinalizedBlock
  -> occurrence filter 与确定性执行
  -> ExecutionAttestation
  -> FinalityCertificate
  -> 原子公开状态
  -> FinalityProof 查询
~~~

调试的第一原则由此产生：不要从“交易失败”开始猜原因，要先找到最后一个有证据的边界。

## 2. 两套状态必须分开

### 2.1 公共状态回答用户结论

查询 API 使用以下稳定枚举：

~~~text
UNKNOWN
PENDING
FINALIZED_SUCCESS
FINALIZED_FAILED
EXPIRED
REPLACED
~~~

其中 UNKNOWN 和 PENDING 不是终态证明，只表示当前服务没有给出终态证据。其余四种结论必须附带可独立验证的证据：

- FINALIZED_SUCCESS 或 FINALIZED_FAILED：原交易、transaction inclusion、nonce-consuming Receipt、receipt inclusion 和 FinalityProof；
- REPLACED：先选择窗口内candidate height，以candidate执行前的父state anchor/proof、candidate bundle/可选epoch activation和sender meta/auth/nonce三份SMT proof证明原交易确由active policy授权，再给出同sender、同nonce、不同tx_id的最终winner、nonce-consuming Receipt、两个inclusion proof和终态FinalityProof；
- EXPIRED：先完成同样的candidate parent-state授权证明，再给出height大于valid_until_height的最终tip、它的FinalityProof，以及该tip下固定账户nonce key的SMT proof；nonce state存在时必须证明`AccountNonceState{schema_version:1,next_nonce<=queried.nonce}`，non-inclusion只支持父状态已完整自证的过期`ACCOUNT_CREATE_V1`。

若 present SMT proof 表明 next_nonce 大于查询 nonce，该 nonce 已被消费。服务端必须继续寻找原交易终态或另一个 winner，不能把它解释为 EXPIRED。non-inclusion 不能被解码成伪造的零值账户状态，也不能替攻击者自签的普通不存在账户交易制造终态。

一笔 tx_id 最多有一张 Receipt。业务失败、revert 或 out-of-gas 仍会消费 nonce；窗口外、future、stale、已经有 winner 后的重复 occurrence 和 nonce 冲突 loser 不进入 transaction tree，也没有 Receipt。注意不能在扫描前按 tx_id 去重：一次较早的 future occurrence 被跳过后，同 tx_id 的较后 occurrence 仍可能在游标推进后成为 winner。

### 2.2 本地阶段只回答卡在哪里

PENDING 可以附加本地 progress_stage：

~~~text
MEMPOOL
BATCHED
DA_CERTIFIED
DAG_REFERENCED
SLOT_SUPPORTED
ORDER_FINAL
EXECUTED_LOCAL
FINALITY_CERTIFIED
COMMITTING
~~~

这些阶段可以因重启、缓存裁剪、重打包或观察节点不同而回退、跳跃或消失。它们适合诊断，不适合触发发货、结算或跨链动作。

例如某节点显示 Alice 已到 ORDER_FINAL，只说明该节点已得到不可跨越的稳定顺序；只有 q 个相同执行结果形成 FinalityCertificate，且状态、区块和索引完成原子公开后，外部才得到 FINALIZED_SUCCESS 或 FINALIZED_FAILED。

## 3. 一张证据地图

先把用户症状映射到可以检查的对象：

| 边界 | 能证明已经到达的最小证据 | 还不能证明什么 |
|---|---|---|
| 接入 | tx_id、规范交易字节、接纳结果 | 已被排序或执行 |
| Mempool | 节点本地 entry 与 lane | 全网都知道 |
| Batch | BatchID、BatchHeader、BatchBody | 数据在故障模型内可恢复 |
| 可用性 | 合法 BatchAC 和 q 个唯一 ACK signer | 已进入 DAG 或最终顺序 |
| DAG | 有效 DAGVertex 引用该 BatchID | 对应 slot 已经决定 |
| slot 支持 | sticky support 的可复算路径 | 可以越过更早未决 slot |
| 排序 | 连续稳定 slot 前缀和可选 DAGCommitWitness | 状态根已被外部认证 |
| 本地执行 | FinalizedBlockHeader、Receipt 和本地 roots | q 个 Validator 得出同一结果 |
| 执行认证 | q 个 Ed25519 ExecutionAttestation 形成 FinalityCertificate | 数据库已经原子公开 |
| 用户终态 | FinalityProof 与交易专用 Merkle/SMT proof | 无 |

调试时从左到右找第一个缺失边界。没有 BatchAC 时研究 slot 决定毫无帮助；已经有 FinalityCertificate 却查不到 Receipt，则应该检查原子公开和索引，而不是修改 DAG 规则。

## 4. 先保存现场，再尝试恢复

任何重启、清缓存或重同步之前，至少保存：

~~~text
network_id, ledger_id, epoch
node_id, validator_index, build_id, config_hash
tx_id, BatchID, VertexID, FinalizedBlockID
local_round, highest_complete_round
oldest_undecided_slot, last_emitted_slot, committed_height
BatchAC signer bitmap and validation result
own Vertex signing locks
ExecutionAttestation signing locks
WAL sequence and fsync status
state_root, receipt_root, transaction_root
trace_id and monotonic event sequence
~~~

墙钟只用于把不同进程的日志大致对齐，不能判断交易有效性、slot 顺序或最终性。协议位置使用 epoch、round、slot、height 和对象 ID。

以下动作会破坏关键证据，除非已经完成只读导出，否则不要执行：

- 删除 WAL 后重启 Validator；
- 手工改 last emitted slot 或 next_nonce；
- 把本机 state root 改成多数节点的值；
- 清空 DAG 后声称故障无法复现；
- 用降低 q 的私有构建验证“是否只是门槛问题”。

## 5. 可观测性必须沿证据边界设计

### 5.1 结构化日志

所有组件共享一组低歧义字段：

~~~text
timestamp
monotonic_sequence
level
component
event
node_id
validator_index
network_id
ledger_id
epoch
round
slot_round
slot_rank
height
tx_id
batch_id
vertex_id
finalized_block_id
peer_id
error_code
duration_ms
build_id
config_hash
trace_id
~~~

字段只在适用时出现。安全事件不得采样，普通重复拒绝可以只保留首条详细日志并用计数器聚合。

Alice 的 BatchAC 卡住时，一条合格日志类似：

~~~json
{
  "level": "warn",
  "component": "availability",
  "event": "da_ack_refused",
  "ledger_id": "ledger-demo",
  "epoch": 7,
  "round": 314,
  "batch_id": "b_...",
  "validator_index": 2,
  "error_code": "CODEWORD_REENCODE_MISMATCH",
  "build_id": "fw-...",
  "trace_id": "tr_..."
}
~~~

不得记录私钥、seed、KMS token、完整机密 payload 或未脱敏的安全 WAL。完整签名通常也没有必要进入普通日志；保存 digest、signer index 和证据归档引用即可。

### 5.2 指标回答趋势，不追踪单笔交易

建议指标族：

~~~text
finalweave_mempool_entries{ledger,lane}
finalweave_mempool_rejected_total{ledger,reason}
finalweave_batch_body_bytes{ledger}
finalweave_da_reconstruction_total{ledger,result}
finalweave_da_ack_total{ledger,result}
finalweave_batch_ac_seconds{ledger}
finalweave_dag_vertex_total{ledger,result}
finalweave_dag_round{ledger,kind}
finalweave_dag_oldest_undecided_round{ledger}
finalweave_dag_slot_decision_total{ledger,decision,path}
finalweave_dag_restricted_jump_total{ledger,result}
finalweave_dag_future_quarantine_entries{ledger}
finalweave_dag_unreferenced_sibling_quarantine_entries{ledger}
finalweave_dag_unreferenced_sibling_quarantine_bytes{ledger}
finalweave_dag_dependency_promotion_total{ledger,result}
finalweave_dag_dependency_fetch_pending{ledger,author}
finalweave_order_final_height{ledger}
finalweave_execution_local_height{ledger}
finalweave_execution_seconds{ledger,path,result}
finalweave_execution_speculation_total{ledger,result}
finalweave_execution_authoritative_reexecution_total{ledger,reason}
finalweave_prefilter_work_spent{ledger,pool,sponsor_author_index}
finalweave_prefilter_occurrence_total{ledger,result,payload_type}
finalweave_prefilter_suffix_seconds{ledger,kind}
finalweave_prefilter_checkpoint_rebuild_total{ledger,reason}
finalweave_cross_ledger_proof_verify_seconds{ledger,root_kind}
finalweave_cross_ledger_occurrence_total{ledger,result}
finalweave_cross_ledger_proof_cache_hit_ratio{ledger}
finalweave_finality_attestation_total{ledger,result}
finalweave_finality_certificate_height{ledger}
finalweave_public_height{ledger}
finalweave_storage_wal_fsync_seconds{ledger,record}
finalweave_storage_atomic_publish_seconds{ledger,result}
finalweave_proof_verification_total{ledger,type,result}
~~~

tx_id、BatchID、VertexID 和 peer_id 不能作为 Prometheus label，否则攻击者可以制造无界时间序列。具体对象通过日志、trace exemplar 或受认证的诊断查询关联。

### 5.3 Trace 连接一次旅程，但不进入共识对象

Alice 的 trace 可以包含：

~~~text
sdk.submit
gateway.admit
mempool.insert
batch.build
availability.encode
availability.verify_codeword
availability.collect_ack
dag.build_vertex
dag.derive_support
dag.decide_slot
order.linearize_delta
execution.build_dependency_graph
execution.speculate
execution.certify_prefix
finality.sign_attestation
storage.atomic_publish
query.build_finality_proof
sdk.verify_finality_proof
~~~

trace context 只服务运维关联，绝不能进入规范编码、签名 digest、对象 ID、proposer schedule 或排序 tie-break。来自 peer 的 trace header 也必须限长、校验并在信任边界重新生成。

### 5.4 诊断快照

受认证、限流且默认不暴露公网的 debug snapshot 应包含：

- build、协议版本、config hash、ValidatorSet hash 和 P；
- local round、完整 frontier 摘要、future quarantine 用量；
- 每个未决 slot 的 direct、indirect 和依赖缺口摘要；
- last emitted slot、order final、executed、certificate 和 public 四个游标；
- Batch disposition、缺失 shard、BatchAC 验证结果；
- own-Vertex、ACK 和 ExecutionAttestation 的签名锁摘要；
- WAL、快照、状态库、索引和归档健康状态；
- 最近固定数量的拒绝码和安全停机原因。

摘要必须可确定排序。不要直接打印 Go map，否则同一状态可能产生不同文本，妨碍节点对比。

## 6. 十分钟分诊 Alice 的 PENDING

~~~mermaid
flowchart TD
    A["Alice 只得到 PENDING"] --> B{"服务端知道规范 tx_id？"}
    B -- "否" --> C["检查编码、签名、网络与接入"]
    B -- "是" --> D{"找到 BatchID？"}
    D -- "否" --> E["检查 Mempool lane、限额与选择器"]
    D -- "是" --> F{"BatchAC 有效？"}
    F -- "否" --> G["检查重建、重编码、持久化与 q 个 ACK"]
    F -- "是" --> H{"有效 Vertex 已引用？"}
    H -- "否" --> I["检查父节点、作者签名槽与传播"]
    H -- "是" --> J{"对应 slot 已进入稳定前缀？"}
    J -- "否" --> K["检查 sticky support、最早 gap 与 restricted jump"]
    J -- "是" --> L{"本地执行已完成？"}
    L -- "否" --> M["检查 occurrence filter、依赖图与串行回退"]
    L -- "是" --> N{"已收 q 个相同 attestation？"}
    N -- "否" --> O["检查根分歧、签名锁与执行积压"]
    N -- "是" --> P{"原子公开且 proof 可验证？"}
    P -- "否" --> Q["检查 prepared record、数据库、索引与 proof builder"]
    P -- "是" --> R["返回可验证终态"]
~~~

下面把每条边展开成可以直接执行的排障步骤。

## 7. 接入、Mempool 与 nonce：不要过早下终态结论

### 7.1 SDK 与节点算出不同 tx_id

按以下顺序比较：

1. network_id、ledger_id、schema version 和 feature set；
2. 规范 CBOR 原始字节；
3. domain-separated digest；
4. sender、nonce、有效高度窗口、gas/fee/priority、payload type 和授权 access scope；
5. signer_policy_hash、完整 SignerPolicy、key_id、签名算法和签名覆盖范围；
6. 双方 build ID 与 test-vector version。

比较字节并报告第一个不同 offset，不要比较格式化结构体。格式化层可能隐藏缺省字段、整数宽度或数组顺序差异。

修复验证至少包含一个跨 SDK golden vector、一个单 bit 修改负向用例和一个未知字段或非规范编码拒绝用例。

### 7.2 Gateway 接纳不等于 nonce 已赢

Gateway 可以用本地状态预分类 ready、future 或 stale，但权威判断发生在 FinalizedBlock 的规范 occurrence 顺序中。最终过滤只读取：

- 当前派生 height；
- 父状态认证的账户 meta/auth/nonce 完整三元组或三项全无证明，以及从中解析的 active policy 和 next_nonce；
- 本块前面已接受 winner 导致的 next_nonce 增量、已接受 tx-id 和已创建账户集合；
- 当前 occurrence 的规范交易。

它不读取历史 tx_id seen-set、intent outcome map 或所谓 winner 表。Bloom、缓存和查询索引可以优化性能，但其命中不能成为共识拒绝依据。

给定父状态 Alice.next_nonce=18，规范 occurrence 顺序为：

~~~text
A: nonce 19
B: nonce 18
C: nonce 18, different tx_id
D: exact duplicate of B
~~~

单遍过滤结果是：

1. A 是 future，跳过且无 Receipt；
2. B 首次满足 nonce==next_nonce，进入 transaction tree，并把 working next_nonce 推进到 19；
3. C 此时 stale，跳过且无 Receipt；
4. D 是重复，跳过且无第二张 Receipt。

即使 B 业务执行失败，也会有 nonce-consuming Receipt，Alice.next_nonce 仍推进到 19。A 不会在同一块被二次回扫；若以后重新进入 Batch，才可能在新块执行。

再加一个必须测试的边界：若同一 nonce-19 交易 A 在 B 之前和 B 之后各出现一次，第一次 A 是 future，B 推进游标后，第二次 A 可以成为本块下一个 winner。预先按 tx_id 去重会把它错误删除并造成状态根分叉。

### 7.3 如何测试这一层

用表驱动测试固定 raw occurrence 顺序，覆盖：

- future height、expired height、future nonce、stale nonce；
- 相同 tx_id 多次出现；
- 同 sender/nonce 的不同交易；
- 第一名业务成功与业务失败；
- 多个 sender 交错；
- 快照恢复后 next_nonce 不倒退。

断言的不只是 winner 列表，还包括 transaction root、Receipt 数量、next_nonce 和每个被过滤 occurrence 的诊断原因。任何被过滤 occurrence 都不得获得伪造失败 Receipt。

### 7.4 验签 CPU 飙升但没有 Receipt

先看occurrence的两段计费。完整Envelope解码前，节点只凭已验证causal source中的frame长度和containing Vertex签名得到occurrence sponsor，再从该sponsor份额收scan work；BatchHeader作者只用于重算数据来源，绝不承担引用者的费用。`PREFILTER_SCAN_CAP`后只能固定chunk流式比对source，decoder、tx-hash、SMT和crypto调用都应为0。scan通过后，stale/future/expired、active policy hash不匹配或明显放不下block reserve的候选，应该在任何Ed25519、strict public-key或完整治理bundle worker之前短路。只有exact nonce且仍可能成为winner的candidate，才计算`PrefilterExpensiveWorkCostV1`并从同一sponsor份额、再从shared pool扣款。两个cap都没有Receipt也不耗nonce，只表示本块相应预算已满，不代表交易永久无效。

排障时同时导出：scan cursor/source binding/item length、BatchHeader认证的Batch作者、containing Vertex签名认证的`sponsor_author_index`、该sponsor reserve与shared剩余、completed-scan逐sponsor/shared累计量、scan/suffix work与charge receipt、in-flight scan、完整`tx_id`（若已解码）、common/source attempt的status与origin cursor、active bundle四个内容ID。不要只看本地crypto cache；cache命中可以降低真实CPU，却不能降低逻辑扣款。先用completed累计量、可选in-flight receipt和attempt receipts逐sponsor/shared重算spend；若重启后相同cursor的预算突然变大，检查checkpoint是否遗漏或重复并入receipt。若cursor越过STARTED、artifact digest损坏或无法验证完整occurrence边界，正确行为是回滚到上一个完整边界（没有则从本块causal stream开头重扫），而不是从中间清零继续。

回归测试至少让`P<n`，由Vertex author 0、P和n-1分别携带候选；全部n个occurrence sponsor都应有份额。再让f个恶意sponsor用坏签名/坏reconfiguration bundle耗尽自己的reserve与shared，诚实sponsor本地已完整预验、且公平引用的最大合法交易仍必须能使用其独占reserve推进。关键反例是：Byzantine Vertex作者反复引用诚实作者的旧大Batch；每次work都必须归给containing Vertex作者。若归给BatchHeader作者、committed proposer或gossip peer，攻击者就能转嫁成本并烧掉诚实reserve。

## 8. BatchAC：ACK 是持久化承诺，不是“收到一个 shard”

Alice 已经 BATCHED，但迟迟没有 DA_CERTIFIED。先检查每个 ACK signer 是否完整执行了以下顺序：

1. 收集至少 k=f+1 个带合法 proof 的不同 shard；
2. 重建完整 BatchBody；
3. 验证规范 framing、body hash 和 transaction root；
4. 从重建 Body 重新编码全部 n 个 shard；
5. 重算并核对 fragment root；
6. 持久化该 Validator 固定 index 的 shard；
7. 持久化 CODEWORD_VERIFIED；
8. 持久化相同 Batch 签名锁并 fsync；
9. 才能签署并发送 DA_ACK。

聚合器按 ValidatorID 去重，验证当前 epoch 成员、域、BatchID 和签名，收齐 q=2f+1 个合法 ACK 才形成 BatchAC。即使最多有 f 个 Byzantine signer，仍至少有 f+1 个诚实、不同的固定 shard holder。ACK envelope 的 signer 子集不进入 BatchID、排序种子或交易顺序。

### 8.1 Alice 的第一次故障

4 节点网络中，Batch producer 只收到 2 个合法 ACK 后崩溃。这里 k=2 只表示诚实 shard holder 有能力重建，不能代替 q=3 的 BatchAC 门槛。Alice 仍是 PENDING，本地最多显示 BATCHED。

重启后可能出现三类证据：

- WAL 已有相同 Batch 的 ACK 锁和完整持久化标记：只重发相同字节；
- shard 已落盘但 CODEWORD_VERIFIED 未完成：重新验证，不能直接签；
- 锁已落盘但签名响应未发出：签名服务只能为锁中的同一 digest 完成或重发。

### 8.2 最小可复现测试

对每一步注入崩溃：

~~~text
after k shards
after body reconstruction
after full re-encoding
after fixed shard write
after CODEWORD_VERIFIED write
after sign-lock write
after fsync
after signature
before broadcast
~~~

重启断言：

- 从不在验证或 fsync 之前发 ACK；
- 同一签名槽从不产生第二个 digest；
- 固定 shard 丢失时节点撤销 readiness，不假装可恢复；
- q 个合法 ACK 才形成 BatchAC；
- 任意 k 个与已验证码字一致的 shard重建同一 Body；
- 一片或一个 proof 被修改时验证失败。

修复通过后，再跑大 Batch 基准，确认完整重编码没有出现意外的多次复制、无界内存和磁盘写放大。

## 9. DAG 接纳：一个作者每轮只能留下一个签名字节串

有效 DAGVertex 是小型元数据。它引用已验证 BatchAC，带指向本作者最高 lower-round Vertex 的 own parent、至少 q 个上一轮不同作者的 strong parents，以及受限 weak parents。每个 Validator 在同一 ledger、epoch、round 最多签一个 DAGVertex。

接收 Vertex 时依次验证：

1. network、ledger、epoch、round 和作者；
2. 作者签名和规范字节；
3. strong parent 的轮次、数量与作者去重；
4. own parent 链；
5. weak parent 上限和确定顺序；
6. 所有 BatchAC 引用；
7. restricted round-jump 合法性；
8. 对象、依赖和 future quarantine 资源上限。

这里没有额外的 Vertex 认证往返。下一轮 Vertex 的父边就是可复算的隐式支持。

### 9.1 同轮双发

若 Byzantine 作者在同一 round 广播两个不同 Vertex：

- 两个签名对象都作为 equivocation evidence 保留；
- 若二者都进入某 committed proposer 的包含自身因果过去，则两个不同 VertexID 的 payload 都按全局三元组顺序展开，不能只留本地先见者；
- 不能把它们计作两个作者；
- 本地确定性规则不得依赖谁先到达；
- 诚实节点自己的 WAL 只能恢复并重发原字节，绝不能选择第二份“更好”的 Vertex。

测试时把两个对象以所有到达顺序送给不同节点，最终 frontier、支持推导、稳定前缀和证据摘要必须一致。

## 10. FinalDAG-C：从边推导决定，而不是从消息数量猜最终性

### 10.1 sticky support

对过去 slot，节点从当前 Vertex 的父引用开始执行确定性 DFS：

1. own parent；
2. strong parents，按 author index、VertexID 排序；
3. weak parents，按 round、author index、VertexID 排序。

遇到该 slot 的第一个候选 proposer Vertex 就返回它；遍历结束仍没有则返回空。own parent 优先使诚实作者一旦支持某个候选，后续不能切换到同 slot 的冲突候选。

这是测试中最容易被并发优化破坏的地方。并行抓取父对象可以提高 I/O，但“第一个”的选择必须由规范顺序而不是 goroutine 完成顺序决定。

### 10.2 三个 round 的 direct commit

设候选 P0 位于 round r：

~~~text
round r:     P0 出现在一个 proposer slot
round r+1:   q 个不同作者 Vertex 支持 P0
round r+2:   某 Vertex 的 q 个 strong parents 支持 P0，因此它认证 P0
round r+2:   q 个不同作者 Vertex 都认证 P0
~~~

最后一个条件成立时，该 slot direct commit。这里的“认证”是 DAG 图样，不是另一个传输 envelope。

direct skip 的条件不同：round r+1 有 q 个不同作者 Vertex 对该 slot 都找不到候选。超时、本地没有看到或 proposer 很慢本身都不能直接产生 SKIP。

### 10.3 indirect decision 与最早 gap

若 round r 的 slot 经 direct 规则仍是 UNDECIDED，节点查找全局 slot 顺序中 proposer round 大于 r+2、不是 DirectSkip、且状态为 UNDECIDED 或 COMMIT 的第一个后继 anchor：

- anchor 仍未决，旧 slot 也保持未决；
- anchor 已 COMMIT，且只从它的签名因果历史 `Past(A)` 算出的旧候选集合恰有一个认证图样，则旧 slot COMMIT；
- anchor 已 COMMIT但没有该图样，则旧 slot SKIP。

本地在 `Past(A)` 外额外看到的 proposer equivocation 不能改变间接结果；若 anchor 历史内竟算出两个认证候选，节点进入 SAFETY_HALT，而不是按到达时间任选一个。

所有 slot 按 round 升序、proposer rank 升序输出。扫描遇到第一个 UNDECIDED 必须停下，即使后面的 slot 已有漂亮的 direct commit 图样，也不能越过 gap 公开。

### 10.4 每个决定怎样变成区块

每个 COMMIT slot 精确取 `Past(current proposer) - GloballyEmittedVertices`，再按以下顺序线性化；不能只减去紧邻上一个 committed proposer 的 Past，因为两个连续 proposer 不保证互为祖先：

~~~text
(round ASC, author_index ASC, VertexID ASC)
  -> VertexCore 中 availability_references 的原始签名承诺顺序
  -> BatchBody 中 transactions 的原始数组顺序
~~~

每个 COMMIT slot 派生一个连续 FinalizedBlock。创世 height 为 0，第一个普通块为 1。SKIP slot 不占高度；一次追赶决定多个 slot 时，也按全局 slot 顺序逐块派生，不能合并成一个高度。

### 10.5 restricted round-jump 是强制活性规则

节点从 curr 追赶到 target 时，对每个被跨越的 r' 按升序检查：

~~~text
if r' >= 3
and decision round r'-2 still has a slot blocking the stable prefix:
    obtain q valid parents for r'-1
    build the local Vertex for r'
    persist exact bytes and signing intent
    fsync
    sign and broadcast
~~~

父节点不足时必须同步，不能继续跳到 target。若该 round 已签过，只能重发 WAL 中的同一 Vertex。far-future 对象先进入按 peer、epoch 和字节数限制的 quarantine。

另一个容易混淆的 cache 是 unreferenced-sibling quarantine。它只保存尚未被共识对象按 VertexID 引用的同槽双签 gossip，受每 slot 4 个、每 Ledger 65,536 个/64 MiB 的绝对上限，不参与 support。真正被 child、证书/witness 或 anchor 引用的 ID 走独立 dependency promotion：cache 未命中就重拉，递归闭包 fsync 完成前 root 一直 pending。排障时若看到“签名正确却没有 support”，先查它究竟是旁路 sibling、待提升依赖，还是已闭合 DAG object，不要靠手工把 quarantine 文件复制进 DAG store。

任意跳轮不是一种“更激进的性能模式”；它存在永久不提交的活性反例，因此从创世起禁止。

### 10.6 这一层的确定性仿真

至少构造以下 schedule：

- 正常 r、r+1、r+2 direct commit；
- q 个 no-support Vertex 的 direct skip；
- direct 均未决定，后继 committed anchor 触发 indirect commit；
- 同样场景但 anchor 因果历史缺少认证图样，触发 indirect skip；
- 更晚 slot 已决定，但更早 slot 仍 UNDECIDED，断言 public cursor 不前进；
- primary 静默，timer 只影响何时创建 Vertex，不替代决定证据；
- proposer equivocation 与所有消息重排；
- 落后节点需要补多个中间 round；
- 删除 restricted-jump 分支后重现永久停滞 seed，再恢复规则验证进展。

每个失败必须输出 seed、规范事件序列、每个节点的 slot map、own chain 和最早 gap。相同 seed 重跑必须得到逐字节相同的摘要。

## 11. 排序之后：并行执行必须证明串行等价

ORDER_FINAL 只冻结输入。FinalWeave 的规范语义始终是按 tx_index 调用串行 Apply：

~~~text
state_0 = parent finalized state
for tx_index from 0 to m-1:
    state_i+1, receipt_i = Apply(state_i, transaction_i)
~~~

生产实现为了性能使用 Preset-Order Serializable Parallel Execution：

1. 为每个 winner 得到 exact read、write 和隐式 system access；
2. 按 tx_index 构建只从小 index 指向大 index 的依赖图；
3. 无冲突分支并行推测执行；
4. MVCC 读取小于自身 index 的最新合法 writer；
5. 从 tx_index 0 开始连续做 prefix certification；
6. 若第一个不合格 index 为 j，丢弃 j 及其后推测结果；
7. 只进行一次权威串行 suffix 重执行。

每笔交易最多一次推测执行和一次权威执行。禁止无界 abort、反复乐观重试或因本机线程数不同改变重试次数。

无法提前证明 exact access 的动态或全局操作进入串行 barrier lane，功能仍然可用。系统必须注入 nonce、gas、权限、事件和其他隐式状态键。用户合约越过声明能力边界时按协议产生确定性错误；native 或系统 access resolver 与真实访问不一致属于实现安全故障，节点进入 EXECUTION_HALT，不能签执行结果。

调度顺序、worker 数量、推测失败和线程时间都不能影响 gas、Receipt、事件、state root 或任何共识字节。

### 11.1 state root mismatch 的处理

这是高优先级安全事件。节点立即停止该高度的 ExecutionAttestation，保存：

- parent FinalizedBlockID 和 parent state root；
- ordered Vertex、Batch 和 raw occurrence IDs；
- winner tx_id 与 tx_index；
- exact access、读取版本、write set、Receipt；
- prefix certification 停止位置；
- 串行 oracle 输出；
- VM、数据库、配置和 build ID。

先验证两个节点输入完全相同，再对 tx_index 前缀二分：

~~~text
比较前 N/2 笔后的中间 root
  -> 选择第一次产生分歧的一半
  -> 重复直到单笔交易和单个 key
~~~

常见根因包括 map 迭代顺序、本地时间、随机数、整数溢出、事件排序、错误读取 MVCC 版本、遗漏 system key、失败回滚不完整、不同快照或数据库损坏。

修复验证必须同时证明：

- 单线程 oracle 在所有架构得到相同字节；
- 并行结果逐字节等于 oracle；
- 从 0% 到 100% 冲突率都成立；
- worker 数、调度 seed 和消息到达顺序不改变 roots；
- 每笔交易的执行次数没有超过约束；
- 修复没有把所有负载永久退化到串行。

## 12. ORDER_FINAL 之后为何还不能向 Alice 宣布成功

每个 Validator 完成规范执行并持久化 PreparedExecution 后：

1. 重算 FinalizedBlockHeader 和所有 roots；
2. 在相同 ledger、epoch、height 的签名槽写入 ExecutionAttestation intent；
3. fsync；
4. 用 Consensus Ed25519 key 签名；
5. 在后续 DAGVertex 中搭载，或通过有界同步重传同一对象。

节点从 Header 派生绑定 FinalizedBlockID、MMR root、state root、validator-set/config hashes 的 `FinalityStatement`。收齐 q 个不同 Validator 对同一 Statement 的合法签名，形成 FinalityCertificate。它认证已经决定的顺序和执行结果，不重新选择顺序。

证书的语义身份不包含 signer subset 或 envelope hash。两个聚合器选择不同但合法的 q 个签名，仍然认证同一个 FinalizedBlock。

节点只有在 FinalityCertificate、区块、状态、Receipt、证明索引和 public cursor 原子可见后，才返回 FINALIZED_SUCCESS 或 FINALIZED_FAILED。崩溃恢复时：

- 已有 prepared record、尚未签名：核验锁后完成相同 digest；
- 已签名、尚未聚合：重发相同 attestation；
- 已有证书、尚未公开：继续同一原子发布，不生成新高度；
- 主状态已提交、索引落后：公共终态仍由 proof 决定，索引明确返回 INDEX_LAGGING 并可重建。

### 12.1 FinalityProof 与 DAGCommitWitness 不解决同一问题

面向钱包和应用的基础 FinalityProof 精确包含：

~~~text
finalized_block_header: FinalizedBlockHeader
finality_certificate: FinalityCertificate
validator_set_proof: ValidatorSetProof
merkle_proofs: [MerkleProof]
~~~

`ValidatorSetProof` 从 Genesis 连续验证到目标 epoch，并在末尾携目标完整 FeatureSet/GasSchedule。若钱包由运营方 checkpoint 起步，它使用名字和入口都不同的 `CheckpointTrustAnchor/CheckpointFinalityProof`，而且只信任本地预置的 anchor ID；服务端返回一个自称可信的 checkpoint 没有意义。

transaction/Receipt/Event 的有序树 path 可以位于相应 proof 的 `merkle_proofs`。状态 SMT proof 和历史 MMR proof 是查询 evidence 在其外层携带的独立 `SparseMerkleProof/BlockMMRProof`，分别连接同一 Header 的 state root 和同一 FinalityStatement 的 MMR root。不要把“证明包一起返回”误写成“所有证明都是 FinalityProof 字段”。

DAGCommitWitness 则保存 direct 或 indirect 决定所需的完整图样，适合全节点同步、审计、争议分析和模型测试。它可能很大，不是普通轻客户端证明的必选字段，也不进入 FinalizedBlockID。

## 13. 把 Alice 的事故完整走一遍

现在给 Alice 的交易注入一组连续故障：

1. Gateway 接纳并返回 tx_id；
2. producer 构建 Batch，但只取得两个合法 ACK 后崩溃；
3. 恢复后同一 Batch 收齐第三个 ACK，形成 BatchAC；
4. 一个有效 Vertex 引用 BatchID；
5. primary 静默，后继 Vertex 继续形成，Alice 所在 slot 暂时 UNDECIDED；
6. 一个更晚 committed anchor 的因果历史包含所需认证图样，旧 slot indirect commit；
7. 更早另一个 slot 尚未决定，public order cursor 暂时停住；
8. restricted round-jump 补出必要中间 Vertex，gap 最终决定；
9. Alice 成为 nonce winner，与 Carol 写不同 key 的交易并行推测；
10. Dave 的动态全局操作进入串行 barrier；
11. 一个 Validator 因 resolver bug 算出不同 root，停止签名；
12. 其余三个 Validator 得出相同结果，形成 FinalityCertificate；
13. 聚合节点在原子公开前崩溃，重启后从 prepared record 完成；
14. 查询节点返回 proof，但 transaction Merkle path 被篡改一位。

正确结论：

- 第 2 步只有 BATCHED，不是 DA_CERTIFIED；
- 第 4 步最多是 DAG_REFERENCED，不是最终性；
- 第 6 步已有 slot 决定，但第 7 步禁止越过更早 gap；
- 第 9 步的并行只是优化，结果必须等于 tx_index 串行 Apply；
- 第 11 步不能把少数 root 改成多数值；
- 第 12 步 q=3 足以形成 FinalityCertificate；
- 第 13 步公开前 Alice 仍不能得到终态；
- 第 14 步客户端必须拒绝整个结果，合法证书不能修复错误的包含证明。

这条链就是事故报告、回归测试和性能基准共享的主场景。

## 14. 故障分支手册

### 14.1 长期停在 MEMPOOL

先检查：

- entry 是否只存在于单个观察节点；
- 本地 lane 是 ready、future、deferred 还是 quarantine；
- 交易大小、配额、有效高度窗口和签名；
- Batch selector 的 sender 公平性和饥饿指标；
- 是否已经被重打包，而旧 entry 仅是滞后索引；
- 当前可验证 next_nonce，而不是缓存中的历史结论。

复现时冻结输入交易和 selector seed，不用 sleep 猜时序。修复后断言高流量 sender 不能永久饿死其他 sender，并测量选择器 CPU 与锁竞争。

### 14.2 BatchAC 一直缺一个 signer

按 Validator 列出：

~~~text
shards collected
body reconstruction
canonical body validation
full codeword re-encoding
fragment root validation
fixed shard persistence
CODEWORD_VERIFIED
sign lock fsync
DA_ACK digest and result
~~~

磁盘满、固定 shard 丢失、codeword 不一致、epoch 错误或 signer 重复都应拒绝 ACK。修复目标不是“让计数变成 q”，而是让每一个计入者都满足完整承诺。

### 14.3 round 增长但 stable prefix 不前进

查看：

- 最早 UNDECIDED slot，而不是最新 round；
- primary 与次级 proposer 是否按冻结 schedule 计算；
- sticky support DFS 是否在所有节点得到同一结果；
- q 个 support 和 q 个 decision-round认证 Vertex 是否来自不同作者；
- direct skip 是否真的有 q 个 no-support；
- 后继 anchor 是否距离足够且自身已经 COMMIT；
- restricted-jump 是否因父缺口停住；
- future quarantine 是否被洪泛占满。

不能通过人工把旧 slot 标为 SKIP 恢复服务。最小测试应保存完整 DAG 子图，并在任意输入顺序下重算同一 decision map。

### 14.4 后续 slot 已决定但高度不增长

这通常是最早 gap 在工作，而不是系统“忘了提交”。比较：

~~~text
oldest_undecided_slot
last_emitted_slot
later_decided_slots
restricted_jump_cursor
missing parent set
~~~

修复后必须证明 public cursor 从未越过 gap，并且 gap 决定后按 slot 顺序逐个派生连续高度。

### 14.5 ORDER_FINAL 增长而 FinalityCertificate 落后

依次检查：

- BatchBody 是否都能从 shard 恢复；
- execution queue 和 barrier lane 是否积压；
- prefix certification 首个失败 index；
- 权威串行 suffix 是否只运行一次；
- 节点 roots 是否一致；
- ExecutionAttestation sign lock 和 KMS；
- attestation 是否在后续 Vertex 中传播；
- 聚合器是否按 signer index 去重；
- 排序领先执行的背压是否生效。

如果 order-final height 无界领先 certificate height，必须限制新 Batch 和 DAG 引用，优先执行与认证。不能靠跳过动态交易保持吞吐。

### 14.6 已有证书但用户仍是 PENDING

检查 prepared execution、原子数据库事务、public cursor、proof index 和查询缓存。正确恢复过程必须满足：

- 旧 public height 永远完整可读；
- 新 height 要么全部可见，要么完全不可见；
- 索引可从 FinalizedBlock 和状态重建；
- 重试同一 publish 是幂等操作；
- 服务端不会仅因缓存命中返回无 proof 的终态。

### 14.7 FinalityProof 验证失败

独立 verifier 按以下顺序：

1. trusted network、ledger 和创世或已验证 epoch 链；
2. ValidatorSet、n=3f+1、q=2f+1；
3. FinalizedBlockHeader 规范编码和 ID；
4. FinalityCertificate signer bitmap、唯一性和 q 个 Ed25519 签名；
5. transaction leaf、tx_index 和 transaction root；
6. Receipt leaf、相同 tx_id 与 tx_index、receipt root；
7. 必要的 state 或 MMR proof；
8. REPLACED/EXPIRED的candidate height、父state trust/proof、candidate bundle/activation、meta/auth/nonce三份proof、active policy与`next_nonce<=nonce`；拒绝同高post-state和sealed-parent旧epoch伪造，再验证EXPIRED终态height及nonce边界，或仅对合法账户创建接受non-inclusion。

任一层失败就拒绝，不通过请求更多查询节点做“多数表决”覆盖密码学错误。

### 14.8 同步节点追不上

同步顺序是：

~~~text
trust anchor
  -> epoch / ValidatorSet transition
  -> continuous FinalizedBlockHeader + FinalityCertificate
  -> verified state Snapshot manifest and chunks
  -> same-target DAGDerivationCheckpoint manifest and chunks
  -> state root + MMR + committed slot + emitted count/root/exact set
  -> atomic full SnapshotInstallMarker
  -> post-snapshot block replay
  -> bounded recent DAG and Batch dependencies
  -> restricted-jump catch-up
  -> validator readiness
~~~

验证节点在根、WAL 签名锁、authenticated emitted exact set 和必要 DAG window 全部恢复前不能重新签 ACK、Vertex 或 ExecutionAttestation。只有 state Snapshot 时写 query-only marker 并保持只读；它不能锚定下一高度。DAGCommitWitness 可用于审计排序，但不能代替 Snapshot state-root 验证，也不能代替同 target derivation checkpoint 恢复 exact membership。

### 14.9 跨账本消息没有被消费或疑似重复

不要先查 relayer 日志猜结论，沿两条最终性边界分段：

```text
source SEND transaction/Receipt/Event 是否都被同一 source Header认证？
  -> target active FeatureSet 是否精确包含消息签入的 policy_id？
  -> proof kind/root 是否与该 policy逐字节相同？
  -> target账户鉴权、tx窗口/exact nonce、RequiredGas/success reserve是否先于source crypto？
  -> proof-work是否归到 authenticated containing Vertex sponsor且未误耗其他sponsor reserve？
  -> target当前高度是否在消息签名窗口内？
  -> consumption_key 在 target父 SMT是 absent还是 present？
  -> 同块 working set中是否已有更早 canonical winner？
  -> winner的 consumed state/nonce/Event/Receipt是否已原子发布？
```

source proof valid不等于 target已消费；本地 relayer未见也不等于未消费。`CONSUMED` 只看 target最终 state inclusion。`EXPIRED_UNCONSUMED` 需要两份独立验证的 target context：一份 Header高度在 message window内且其 FeatureSet认证 exact历史 policy，用它验证 source proof；另一份同 ledger tip高度严格大于 until，并在其 state root验证同一 key non-inclusion。当前 policy miss、tip高度或索引 miss单独都不够。若两个 relayer都声称成功，重算 source event ID与 consumption key，再验证 target transaction/Receipt roots：协议上最多一张 SUCCESS Receipt；另一个请求应是无 Receipt、无 nonce消费的 replay occurrence。

若 policy刚轮换，检查 old policy是否仍作为独立 entry保留，不能让 verifier把旧 ID映射到新 checkpoint。若 proof cache命中时通过、清空 cache后失败，这是安全故障：cache只能保存完整验证结果，不能提供 trust root或省略逻辑 Gas events。

若现象是“普通交易继续最终，但跨账本长期都是 `CROSS_LEDGER_VERIFY_CAP`”，按containing Vertex的`sponsor_author_index`拆分指标，并把Batch作者作为独立诊断维度。每个sponsor都应有独立max-single reserve，只有剩余工作使用shared pool；恶意sponsor的无效proof或重复Batch引用若降低了honest sponsor reserve，说明attribution或budget journal实现错误。再用crypto spy注入未授权账户、stale/future nonce和`gas_limit=1`的最大proof：source verifier调用必须为零。source预算扣款成功时必须原子写`CrossLedgerProofAttemptV1{STARTED,origin cursor,sponsor,cost,receipt}`；确定结果变为VALID/INVALID，VALID还带canonical artifact与digest。本块更晚、通过更早cheap gates并到达source scheduler的同tx-id复用terminal结论并归类`DUPLICATE_CROSS_LEDGER_ATTEMPT`，不得重复验签；已成为winner的同tx先由通用gate归类`DUPLICATE_OCCURRENCE`。因cap未尝试的tx-id不能写attempt map，之后仍可由另一sponsor份额重试；本地timeout/磁盘错误保持STARTED、cursor不推进，恢复重验但不重扣。

## 15. 测试体系从同一条旅程逐层放大

### 15.1 单元测试：把规则变成纯函数

优先提取：

- 规范编码、domain hash 和签名验证；
- q、k 和作者去重；
- BatchBody 重建与全码字核验；
- Vertex parent 和 resource validation；
- sticky support DFS；
- direct、indirect 与稳定前缀 decider；
- occurrence filter；
- exact dependency graph、MVCC read selection 和 prefix certification；
- FinalityCertificate 与 status evidence verifier。

纯函数不读墙钟、不访问真实网络、不依赖 map 顺序。错误返回稳定 code，不以日志字符串作为测试契约。

### 15.2 Conformance vectors：跨实现共享一份事实

固定以下对象的 canonical bytes、digest、ID、签名与负向变体：

~~~text
TransactionEnvelope
BatchHeader and BatchBody
Shard commitment and DA_ACK
BatchAC
DAGVertex
Slot decision witness
FinalizedBlockHeader and Body
Receipt and state changes
ExecutionAttestation
FinalityCertificate
FinalityProof
Epoch transition
~~~

每个 vector 同时写清 network、ledger、epoch、算法和 schema version。任何 golden byte 变化都是协议兼容性事件，不能用“更新测试快照”掩盖。

### 15.3 属性测试：验证无限输入族的不变量

核心性质包括：

~~~text
少于 q 个唯一合法成员永远不形成 BatchAC 或 FinalityCertificate
任意 k 个合法 shard 恢复同一 BatchBody
同一诚实作者同一 round 不产生两个 Vertex digest
sticky support 不随到达顺序改变
同一 slot 不同时 direct commit 与 direct skip
两个诚实节点的 emitted slot 序列互为前缀
任何 emitted 序列不跨越最早 UNDECIDED
同一 committed causal history 产生相同 Vertex delta 顺序
每个 COMMIT slot 恰好派生一个连续高度
SKIP 不派生高度
同一 tx_id 最多一张 Receipt
执行结果逐字节等于串行 oracle
每个 height 的诚实 signer 只认证一个 Header digest
不同合法 signer subset 不改变 FinalizedBlockID
终态 evidence 四种变体互斥
~~~

属性失败必须输出最小化输入和 seed，并永久加入回归库。

### 15.4 Fuzz：所有不可信字节都要经过

Fuzz 入口至少包括：

- API、P2P、WAL、snapshot 和 proof 解码器；
- canonical CBOR 拒绝器；
- shard proof、重建器与 re-encoder；
- DAGVertex、父集合和 weak-edge limits；
- DFS、causal history 和 witness verifier；
- signer bitmap 与证书验证；
- transaction、Receipt、SMT 和 Merkle proof；
- config、genesis、epoch transition；
- access set、依赖图和 VM input。

要求：

~~~text
never panic
bounded allocation
bounded recursion or iterative traversal
no acceptance of non-canonical alternate bytes
round-trip only for valid canonical objects
one-bit mutation of authenticated data fails
~~~

语料库加入所有生产事故最小样本，但先脱敏并移除秘密。

### 15.5 确定性协议模拟器：共识开发的主战场

模拟器虚拟化：

- 逻辑时间与 timer；
- 消息延迟、丢失、重复、乱序和选择性发送；
- 节点 crash、restart 和持久化状态；
- WAL、fsync、签名服务成功或失败；
- Byzantine 作者的双发、无效父、far-future 和 withholding；
- Batch shard 损坏与错误 ACK；
- 执行速度和 attestation 传播。

事件循环只有一个规范顺序。真实 goroutine 并发属于后续集成测试，否则失败无法稳定重放。

每条 simulation 同时断言：

- safety：不出现冲突稳定前缀、冲突公开高度或冲突合法证书；
- liveness：在故障不超过 f、网络进入部分同步且公平调度后，稳定前缀最终增长；
- bounds：quarantine、orphan、MVCC 和重试不超过配置。

### 15.6 Byzantine 测试

至少注入：

- 同作者同 round 两个 Vertex；
- 选择性向不同 Validator 发送不同父历史；
- 重复 signer、伪 signer、旧 epoch signature；
- q 个消息计数中混入同一 Validator 多连接；
- 错 shard、错 proof、错重编码 root、ACK 后删除固定 shard；
- sticky support 冲突诱导；
- far-future flooding 与依赖放大；
- 冲突 ExecutionAttestation；
- 篡改 Header、Receipt、transaction 和 state proof；
- 恶意查询节点返回更高但无效的 tip。

故障工具不得连接生产网络或持有生产 key。

### 15.7 Crash 与 recovery 矩阵

安全关键点逐一覆盖：

| 组件 | 崩溃点 | 重启后的唯一合法动作 |
|---|---|---|
| BatchAC signer | CODEWORD_VERIFIED 前 | 重新验证，不能 ACK |
| BatchAC signer | ACK 锁 fsync 后 | 完成或重发同一 digest |
| DAG author | own Vertex intent 前 | 尚未授权签名 |
| DAG author | intent fsync 后 | 只完成或重发原字节 |
| decider | decision WAL 前 | 从合法 DAG 重算 |
| decider | emitted cursor fsync 后 | 不重复或重排已输出 prefix |
| occurrence filter | scan/common/source `charge + STARTED` 后，或Finish归集scan receipt过程中 | 保持origin cursor，按版本化binding/receipt恢复并重跑但不重扣；归集、清in-flight、推进cursor全有或全无 |
| executor | speculative result 后 | 可丢弃并重算 |
| executor | prepared record fsync 后 | 重验并继续同一 Header |
| finality signer | attestation 锁 fsync 后 | 只完成或重发同一 digest |
| storage | atomic publish 中 | 回滚或幂等完成，不暴露半高度 |
| epoch close | seal lock fsync 后 | 只重发相同 epoch seal statement |

故障注入覆盖 write 前后、fsync 前后、rename 前后、数据库提交前后和响应发送前后。

### 15.8 多进程、网络与 chaos

真实进程验证：

- P2P framing、认证、限流和 backpressure；
- 4 节点与 7 节点网络；
- 节点滚动重启、KMS 暂停和磁盘只读；
- 3+1、2+2 分区与恢复；
- 延迟、丢包、乱序、带宽限制和连接抖动；
- snapshot bootstrap、跨 epoch sync 和 Validator rejoin；
- SDK 的 FinalityProof 独立验证。

2+2 分区不能形成 q=3 的新 BatchAC、稳定进展或 FinalityCertificate；恢复后应继续，而不是让任一分区按墙钟选“更新历史”。

## 16. 性能基准必须保护正确性语义

性能优先不等于只报告吞吐。每次基准记录：

~~~text
hardware, kernel, filesystem, Go version, build_id
n, f, q, k, P, epoch config hash
network latency, jitter, loss and bandwidth
transaction size and Batch target
sender distribution and nonce pattern
read/write key distribution and conflict rate
dynamic/barrier transaction ratio
state size, snapshot age and cache warmness
~~~

### 16.1 端到端延迟分解

至少测量：

- submit 到 BATCHED；
- BATCHED 到 DA_CERTIFIED；
- DAG_REFERENCED 到 SLOT_SUPPORTED；
- slot 首次可决定到 ORDER_FINAL；
- ORDER_FINAL 到 EXECUTED_LOCAL；
- EXECUTED_LOCAL 到 FINALITY_CERTIFIED；
- FINALITY_CERTIFIED 到 proof 可验证的公开终态。

同时报告 P50、P95、P99、最大 gap age 和超时率。只报“进入某节点 Mempool”的 TPS 没有意义。

### 16.2 工作负载矩阵

至少包含：

1. 独立账户、独立 key，验证并行上限；
2. 单热点 key，验证正确退化；
3. 同 sender 连续 nonce；
4. 大量 duplicate、future 和 stale occurrence；
5. 小比例动态全局操作，验证 barrier；
6. exact access 声明错误；
7. 大 Batch 与小 Batch；
8. 一个慢 Validator、一个慢磁盘；
9. primary 静默和次级 proposer 活跃；
10. P=1、默认 P=2、实验 P=q 的 epoch 隔离对比。

实验 P=q 不能在运行中临时打开，也不能仅凭一次吞吐结果升为生产默认。

### 16.3 并行执行指标

报告：

~~~text
dependency graph build time
ready width and critical path
speculative success ratio
prefix certification length
authoritative suffix length
barrier lane ratio
MVCC versions and bytes
worker utilization
serial oracle comparison cost
state and Receipt root time
~~~

性能修复必须证明功能不丢失、结果不变、资源仍有界。把未知 access 交易拒绝掉以提高图宽度，不是合格优化。

### 16.4 Soak 与容量边界

24 小时和 7 天 soak 观察：

- RSS、heap、goroutine、file descriptor；
- orphan Vertex、future quarantine、Batch shard；
- WAL、snapshot、proof archive 和索引增长；
- order-final 与 certificate/public height 差；
- GC pause、数据库 compaction 和磁盘写放大；
- peer queue、重传和带宽公平性。

GC 只有在 slot 已决定、FinalizedBlock 已派生、FinalityCertificate 已归档、proof 保留满足且 Batch disposition 已知时才能推进。不能按本地 round 或墙钟直接删除。

## 17. 发布门禁

协议或安全关键改动至少满足：

- 单元、conformance、属性和 race 测试通过；
- 修改过的所有不可信解码入口有 Fuzz；
- 确定性 simulation 回归 seed 全部通过；
- direct、indirect、gap 与 restricted-jump 永久回归通过；
- 4/7 节点 Byzantine 与分区场景通过；
- ACK、Vertex、attestation 和原子公开 crash matrix 通过；
- 串行 oracle 与并行执行跨架构一致；
- FinalityProof 由独立 SDK verifier 通过；
- 性能没有超过约定退化门槛，资源上限没有放宽；
- 协议文档、ADR、配置、指标和 test vectors 同步更新；
- 至少一位协议安全 reviewer 和一位实现 reviewer 批准。

推荐基础命令在对应包落地后执行：

~~~bash
go test ./...
go test -race ./...
go vet ./...
go test -fuzz FuzzName -fuzztime 60s ./path/to/package
~~~

不要用反复重跑直到绿色来处理偶发失败。保存 seed、调度和节点摘要，把它转成稳定回归。

## 18. 故障报告写成可执行规格

~~~markdown
# 标题

## 影响
网络、ledger、节点、用户可见状态、开始与结束时间。

## 被违反或被怀疑的不变量
例如：public cursor 不得越过最早 UNDECIDED slot。

## 稳定状态与本地阶段
Alice 的公共状态、每个观察节点的 progress_stage。

## 协议位置
network_id、ledger_id、epoch、round、slot、height。

## 对象
tx_id、BatchID、VertexID、FinalizedBlockID。

## 最小证据
规范字节 digest、父集合、decision map、WAL 记录、roots、proof。

## 最小复现
初始状态、Validator 数、seed、事件序列、故障注入点。

## Safety 判断
是否出现冲突稳定前缀、双签、错误 ACK、冲突 root 或错误终态。

## Liveness 判断
部分同步和故障界限恢复后是否继续；最早阻塞点是什么。

## 根因
第一条错误状态迁移，而不只是最后一个报错。

## 修复验证
新测试、旧回归、性能与资源边界。
~~~

把“重启后好了”视为现象，不视为根因。一个合格报告最终可以直接变成 simulator schedule、crash case 或 conformance vector。

## 19. 不要用这些方式“修好”系统

- 不要跳过签名、BatchAC、parent、root 或 proof 验证；
- 不要降低 q 或把连接数当唯一 Validator 数；
- 不要在最早 gap 前人工标记 SKIP；
- 不要关闭 restricted round-jump；
- 不要让远未来对象触发无界递归同步；
- 不要让 DAG 到达顺序影响 sticky support 或线性化；
- 不要把本地 seen cache 变成 replay 共识状态；
- 不要给被过滤 occurrence 生成失败 Receipt；
- 不要让推测调度、重试次数或线程数影响 gas；
- 不要用多数 root 覆盖少数节点分歧；
- 不要在 WAL fsync 失败后继续签 ACK、Vertex 或 ExecutionAttestation；
- 不要把 ORDER_FINAL 当用户终态；
- 不要用证书覆盖错误 Merkle proof；
- 不要用高基数指标追踪对象；
- 不要通过拒绝动态功能来制造漂亮性能数据。

## 20. 30 天成长路线：逐步接管 Alice 的旅程

这不是每天背一组词，而是让你逐段拥有同一条端到端路径。

### 第 1～3 天：能证明 Alice 提交了什么

阅读 [区块链基础](01-blockchain-foundations.md) 和 [交易生命周期](02-finalweave-transaction-lifecycle.md)。完成一份 TransactionEnvelope golden vector，能解释 tx_id、规范字节、签名和 Merkle leaf 的关系。

交付：一个跨实现 vector 和三个负向变体。

### 第 4～7 天：能解释为何 BatchAC 成立

手工推演 4 节点的 k=2、q=3，写一个 shard 重建与全码字重新验证测试，再加入 ACK 锁的崩溃点。

交付：BatchAC 状态图、crash matrix 和一份故障报告。

### 第 8～12 天：能从 DAG 重算 Alice 的支持路径

在纯函数中实现或审阅 parent validation、规范 DFS 和 sticky support。对同一子图随机排列输入一万次，摘要必须相同。

交付：support vectors、equivocation case 和 Fuzz corpus。

### 第 13～17 天：能解释为何 Alice 的 slot 已决定或仍未决

在确定性模拟器推演 direct commit、direct skip、两类 indirect decision 和最早 gap。随后删除 restricted-jump 分支观察活性反例，再恢复规则。

交付：可重放 seed、每节点 decision map 和 safety/liveness 说明。

### 第 18～22 天：能证明并行结果等于串行

实现小型 KV 串行 oracle，再加入 exact dependency graph、bounded MVCC 和 prefix certification。用 Alice、Carol 的无冲突写与 Dave 的 barrier 操作覆盖全部路径。

交付：跨 worker 数一致性测试、冲突率基准和一次 root mismatch 演练。

### 第 23～26 天：能独立验证 Alice 的终态

从可信 ValidatorSet 开始验证 FinalizedBlockHeader、q 个 Ed25519 attestation、transaction/Receipt inclusion 和状态 proof。分别构造 `FINALIZED_SUCCESS`、`FINALIZED_FAILED`、`REPLACED`、`EXPIRED`；其中 `SUCCESS/FAILED` 只属于 Receipt status，不是交易查询终态。

交付：独立 FinalityProof verifier 与单 bit 篡改测试。

### 第 27～29 天：能在崩溃后安全恢复

依次在 ACK、own Vertex、ExecutionAttestation 和 atomic publish 的 fsync 前后 kill 进程，证明重启只重发原字节且不暴露半高度。

交付：自动化 crash suite 和恢复 runbook。

### 第 30 天：反讲和接管

用 20 分钟从 Alice 的签名讲到 proof，期间由导师任意插入网络分区、磁盘满、最早 gap 或 root mismatch。你必须指出证据层、预期状态、复现方式和不得采用的“快捷修复”。

交付：一个可以进入主干评审的第二阶段 issue，以及对本文一处真实改进。

## 21. 综合练习

4 Validator 网络中发生以下事件：

1. Alice 的 nonce 18 交易进入 Batch；
2. 两个节点完成 codeword 验证并 ACK，第三个节点在 sign-lock fsync 前崩溃；
3. 重启后第三个节点从持久化 shard 重新完成验证，收齐 q 个 ACK；
4. BatchID 被 round 50 的 proposer Vertex 引用；
5. round 51 有三个不同作者 Vertex 支持它；
6. round 52 只有两个 Vertex 认证它；
7. round 53 的另一个 slot 已 direct commit；
8. round 49 的次级 slot 仍是 UNDECIDED；
9. 之后 gap 被 indirect skip，Alice 的 slot 也由后继 anchor indirect commit；
10. Alice 与 Carol 无冲突并行执行，Dave 进入 barrier；
11. 一个节点 state root 不同，另外三个形成 FinalityCertificate；
12. public store 提交成功，但查询返回的 Receipt proof 被篡改。

请回答：

1. 第 2 步能否形成 BatchAC，为什么？
2. 第三节点重启后何时可以 ACK？
3. 第 6 步能否 direct commit？
4. 第 7 步能否立刻派生并公开后续高度？
5. 第 9 步最终派生几个区块，高度怎样连续？
6. Dave 是否因为无法并行而失去功能？
7. root 不同的节点能否复制多数 root 后签名？
8. q 个签名已经存在，为什么客户端仍拒绝第 12 步？
9. 为这个场景各设计一个纯函数测试、simulation、crash test 和性能断言。

<details>
<summary>答案提示</summary>

1. 不能。n=4 时 q=3，两个 ACK 未达到门槛；k=2 是恢复门槛，不是 BatchAC 门槛。
2. 它必须重新确认至少 k 个 shard 可重建 Body，完成全 n shard 重编码与 fragment root 核验，持久化固定 shard、CODEWORD_VERIFIED 和签名锁并 fsync，才能签相同 Batch digest。
3. 不能。direct commit 需要 q=3 个 round 52 不同作者 Vertex 认证该候选。
4. 不能。全局输出必须停在 round 49 的最早 UNDECIDED gap。
5. 每个最终 COMMIT slot 各派生一个区块，SKIP 不占高度；按 slot 全局顺序逐个递增，不能把追赶结果合并。题目没有给出所有 slot 的最终 COMMIT 数，因此应从 decision prefix 逐项计算，而不是凭 round 数猜。
6. 不会。它进入串行 barrier lane，保持规范功能和串行语义，只失去该笔交易的并行机会。
7. 不能。它应停止该高度认证，保存输入并与串行 oracle 比较，从可信 snapshot 或重放恢复。
8. FinalityCertificate 认证 Header 中的 receipt root，不会把一条错误 Merkle path 变正确；客户端必须独立重算并拒绝。
9. 纯函数测试可覆盖 sticky support 或 occurrence filter；simulation 覆盖 gap 与 indirect decision；crash test 覆盖 ACK 锁；性能断言覆盖 barrier 存在时无界重试为零、独立交易仍并行且结果等于串行。

</details>

## 22. 入场检查

- [ ] 我先区分公共状态与本地 progress_stage；
- [ ] 我能从 tx_id 追到 BatchID、VertexID、slot、FinalizedBlockID 和 proof；
- [ ] 我能解释 k 与 q 为什么不可互换；
- [ ] 我知道 ACK 前的重建、重编码、持久化与签名顺序；
- [ ] 我知道每个作者每 round 最多签一个 Vertex；
- [ ] 我能手工推演 sticky support、direct、indirect 与最早 gap；
- [ ] 我知道 restricted round-jump 是强制活性条件；
- [ ] 我知道每个 COMMIT slot 一个区块、SKIP 无高度；
- [ ] 我能解释 raw occurrence、next_nonce、Receipt 和四种终态证据；
- [ ] 我能证明并行输出逐字节等于串行 oracle；
- [ ] 我知道每笔交易最多一次推测和一次权威执行；
- [ ] 我不会把 ORDER_FINAL 当成用户终态；
- [ ] 我能区分 FinalityProof 与 DAGCommitWitness；
- [ ] 我能从 source SEND Event proof 推导 SourceEventID，并用 target active policy、永久 consumed key 与 target proof判断唯一消费；
- [ ] 我会用虚拟时间和 seed 复现故障；
- [ ] 我会同时断言 safety、liveness、资源边界和性能；
- [ ] 我知道何时必须停止签名，而不是为了活性放宽校验。

达到这些标准后，你已经不只是“认识”FinalWeave 的组件，而是能沿一笔真实交易定位边界、保全证据、写出可重复测试并验证修复。

继续深入时使用：

- [FinalDAG-C 共识规范](../protocol/03-finaldag-consensus.md)
- [BatchAC 与元数据 DAG](../protocol/02-data-availability-and-blockdag.md)
- [最终性、执行证明与 Epoch](../protocol/04-finality-execution-and-epochs.md)
- [执行与状态工程](../engineering/01-execution-and-state.md)
- [测试、发布与性能](../engineering/05-testing-release-and-performance.md)
- [跨账本异步消息](../protocol/06-cross-ledger-async-messaging.md)
