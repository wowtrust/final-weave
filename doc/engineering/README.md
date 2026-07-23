# FinalWeave 工程规范

本目录把 FinalDAG-C 协议落到可实现、可恢复、可运维的节点工程。协议基线是：独立 `BatchAC` 保证数据可用性，已由作者签名但无独立顶点证书的元数据 DAG（uncertified）通过 restricted round jump、sticky support 和 direct/skip 决策形成 stable slot prefix；稳定前缀先派生本地 `BlockDerivationCandidate`，节点确定性执行后才生成完整 `FinalizedBlock`，并在后续 DAG Vertex 中搭载 `ExecutionAttestation`；`q=2f+1` 个 Ed25519 attestation 聚合为 `FinalityCertificate`，外部验证统一使用 `FinalityProof`。

推荐阅读顺序：

1. [执行与状态](./01-execution-and-state.md)：规范串行语义、确定性并行执行、收据与状态根。
2. [存储、快照与裁剪](./02-storage-snapshot-and-pruning.md)：签名前 WAL、stable-prefix 游标、原子提交、快照恢复。
3. [网络、同步、查询与 API](./03-network-sync-query-and-api.md)：P2P 协议、分层同步、proof-carrying query 与交易状态。
4. [安全、治理与运维](./04-security-governance-and-operations.md)：密钥隔离、参数治理、事故响应与运行手册。
5. [测试、发布与性能](./05-testing-release-and-performance.md)：模型检查、故障注入、差分测试、基准和发布门禁。

## 工程不可破坏的边界

- `BatchAC` 只证明 Batch 可恢复，不证明顺序或最终性。
- DAG support 只由合法 Vertex 的强父边产生；本地到达顺序、缓存命中和墙钟时间不是投票。
- 同槽被精确引用的全部sibling必须经crash-consistent dependency promotion进入因果闭包；未引用旁路只占固定硬上限quarantine，不能用“保存所有有效签名对象”制造无界义务。
- stable slot prefix 只决定规范输入顺序；公开交易成功、收据和状态证明必须有 `FinalityProof`。
- `ExecutionAttestation` 只能在相同排序输入、父状态和执行根已持久化后签署。
- 并行执行必须与按 `tx_index` 串行执行逐字节等价。
- 普通交易自包含签名必须同时匹配块开始时父认证 meta/auth/nonce 完整三元组；不存在账户只允许 `ACCOUNT_CREATE_V1` 自证原子创建，同块策略轮换或新建账户最早在下一高度生效。
- 每个raw occurrence先向containing signed DAGVertex author（occurrence sponsor）收scan费；account/governance昂贵静态与鉴权后缀只能在cheap window/policy-hash/exact-nonce/block-reserve通过后运行，并向同一sponsor收费。全部n个sponsor拥有独立通用prefilter reserve，余量shared；Batch author只绑定来源。
- `next_nonce` 是重放和 slot-winner 判断的认证状态；不得增加会被裁剪的历史 seen-set 或 `nonce_winner` 共识表。
- 网络来源只影响可用性，密码学证明决定正确性。
- 任何本地优化都不得改变规范编码、哈希、签名、排序、收据、状态根或证明验证结果。

## 文档优先级

字段、哈希和签名对象以 `../protocol/` 为最高规范；本目录定义实现约束。若工程描述与协议 Schema 冲突，必须先修正文档和测试向量，不能由代码自行选择一种解释。
