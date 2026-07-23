# ADR-002：BatchAC + FinalDAG-C 直接 DAG 共识

- 状态：Accepted
- 日期：2026-07-23
- 关联需求：FR-DA-*、FR-DAG-*、FR-CONS-*、FR-JUMP-*、SAFE-002..006、LIVE-001..004

## 背景

FinalWeave 面向持续高吞吐、多组织验证者和较大批次。所有验证者应并行利用网络和 CPU，而不是先并行传播数据、再由单 leader 通过第二套提议/投票链排序。

直接 uncertified DAG 具有更低元数据开销和更短提交路径的潜力，但其 slot 决策、round 推进和活性证明比成熟线性 BFT 更复杂。IEEE S&P 2026 对 Mysticeti 的机械化分析还表明，诚实节点任意跳轮可产生无限不提交轨迹。

## 决策

v1 采用名为 **FinalDAG-C** 的工程协议基线：

1. 部分同步，`n=3f+1`，`q=2f+1`；
2. 大 Batch 使用独立 BatchAC：ACK 前以 `k=f+1` 重构、重编码、验证并持久化；
3. DAGVertex 是小元数据，每作者每 round 最多一个；
4. 非起点顶点有至少 `q` 个上一轮不同作者 strong parents；
5. DAGVertex 本身不使用 VertexAck/VertexCertificate；
6. strong edge 同时表达因果关系和作者的隐式支持；
7. 使用 Mysticeti 风格 proposer slot、`commit/skip/undecided`、direct/indirect decision；
8. 只有从前沿起连续已决定的全局 slot 稳定前缀可以输出；
9. 使用 restricted round-jump：跨过 `r'` 时，若 `DecisionRound[r'-2] == UNDECIDED`，先补发自己的 `r'` 顶点；
10. `proposer_slots_per_round` 默认 2，合法范围 `1..q`，仅 epoch 边界可变；`q` slots 只作为低故障实验 profile；
11. 每 round 只给 primary slot（slot 0）timer 活性保证；
12. 不存在 Proposal、Vote、QC、TC 或额外线性共识链；
13. DAGCommitWitness 只证明顺序；外部最终性由另一个执行背书证书建立；
14. 同 epoch 不允许动态切换共识或决策规则。

FinalDAG-C 是 FinalWeave 内部协议名称，不声称原创上述已发表基础，也不声称相关论文的证明自动覆盖本系统。

## 安全与信任影响

- quorum intersection、单轮单顶点和父边合法性是安全基础；
- BatchAC 只证明 Batch 可恢复，不证明 DAG slot 或执行；
- 不同本地 DAG 可见性下 direct/indirect decision 必须不产生 commit/skip 冲突；
- stable-prefix 规则防止越过前序 undecided slot；
- round-jump 限制是活性安全关键规则，须进入 WAL/恢复/同步路径；
- BatchAC、FinalDAG-C、execution finality 和 epoch 的组合产生新增形式化证明义务。

## 正面结果

- 所有验证者并行承担数据生产，不把完整 block body 集中到一个 leader；
- 小顶点的父边复用为共识支持，无额外顶点证书和投票链；
- Batch 数据可用性和 DAG 元数据推进各自有清晰对象；
- 单作者慢/故障时其他作者继续供数；
- ordered prefix 可流水送入执行器。

## 代价与风险

- DAG decision、祖先同步、稳定前缀和 GC 显著复杂；
- 任意 round-jump 这类局部优化可能破坏全局活性；
- secondary undecided slots 可形成 head-of-line；slots 越多风险越高；
- 所有作者并行出数据会提高每节点 ingress/egress 和验证成本；
- 内部 DAG commit 与外部执行最终性分层，API/运维更复杂；
- 系统必须自行完成组合模型、外部审计和真实 benchmark。

## 备选方案

- 线性 leader BFT：更成熟、易审计，但在本目标负载下增加 leader 路径和第二消息族；不选作 v1。
- certified DAG + 独立排序共识：模块化清楚，但顶点证书和排序证书重复；不选。
- 完全异步 Mahi-Mahi 类：故障模型与 v1 部分同步基线不同；研究保留。
- 动态 leader/DAG 混合：同 epoch 语义和证明复杂度过高；拒绝。
- 交易早最终路径：需重写 nonce/执行/proof 安全；v1 拒绝。

## 实施约束

- 规范先于 reactor；参考 decider、模型和生产 decider 三方差分；
- 复现任意跳轮不活跃轨迹并固化回归；
- authored vertex intent 必须签名前 durable；
- 配置不得改变阈值、稳定前缀或 restricted jump；
- 所有协议变更需新 ADR、模型、vectors 和新 epoch 激活。

## 验证方式

- slot 安全/活性模型及实现 refinement；
- 随机 DAG、不同可见性和 Byzantine equivocation 差分；
- S&P 2026 round-jump 反例与修复回归；
- 崩溃每签名点、不双签和双实例 fencing；
- 4/7/10 validator WAN、过载、分区和追赶长稳；
- 内存/CPU/磁盘随未决定 DAG 窗口的上界测试。

## 参考

- [Mysticeti](https://arxiv.org/abs/2310.14821)
- [IEEE S&P 2026 机械化分析](https://flint.cs.yale.edu/flint/publications/sp26.html)
- [机械化证明 artifact](https://zenodo.org/records/17345693)
- [Mahi-Mahi](https://arxiv.org/abs/2410.08670)
