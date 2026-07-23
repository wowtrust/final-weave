# ADR-003：预设顺序可串行化的确定性并行执行

- 状态：Accepted
- 日期：2026-07-23
- 关联需求：FR-EXEC-*、SAFE-007..009、LIVE-005

## 背景

FinalDAG-C 已给出唯一 canonical transaction index。若账本只用单线程 Apply，执行会在多核主机上成为瓶颈；若采用不受约束的乐观事务内存，高冲突会产生 abort storm，未知访问集交易还可能被迫失去功能兼容。

系统需要同时满足：使用可证明的独立性并行；结果严格等价于 canonical 串行顺序；任何合法交易都能执行；冲突和恢复成本有硬上限。

## 决策

规范语义始终是按 `tx_index=0..m-1` 串行 `Apply`。v1 生产默认采用以下混合执行器：

1. 验证交易提供或状态机静态推导的 **exact access set**；
2. 对能安全声明 exact access 的交易构建确定性依赖图；
3. 对可并行分支进行一次 optimistic MVCC 推测执行；
4. 按 `tx_index` 递增认证可提交前缀，读取只能观察前序 canonical 版本；
5. 冲突或版本验证失败时进行最多一次权威重执行；
6. 无法安全声明 exact access 的交易自动进入串行兼容 lane；
7. 每笔交易最多一次推测执行、最多一次权威重执行；
8. 串行执行器永久保留为 oracle、恢复模式、调试和资源压力回退；
9. worker 数、调度和推测完成顺序不进入状态或证明；
10. state、receipt、event、gas 和错误结果必须与串行 oracle 逐字节一致。

该方案吸收 Block-STM 的预设顺序并行思想，但不是纯 Block-STM，也不允许无界重试。

## Exact-access 可信边界

- 客户端声明只能作为提示，必须由状态机/VM 验证；
- 动态访问、反射、未知合约、升级边界或声明超限自动进入 serial lane；
- 声明遗漏不能导致错误并行；检测到未声明访问时该交易的推测结果无效并走权威串行路径；
- 访问集本身是否进入交易签名/收费由状态机 schema 定义，但执行正确性不能依赖不可信提示。

## 安全与信任影响

- canonical 串行 Apply 是唯一可审计规范；
- 并行执行是 refinement，不能创造新的合法状态；
- 串行 lane 保证通用功能，不要求所有应用可静态分析；
- 一次推测 + 一次权威重执行限制 CPU/内存放大和活锁；
- ExecutionAttestation 只签最终认证前缀，不能签中间推测状态。

## 正面结果

- 低冲突/exact-access 工作负载可利用多核；
- 高冲突或未知访问集自动安全退化；
- 不因性能优化拒绝合法状态机能力；
- 有串行 oracle 可做 differential testing 和恢复；
- 每交易最大执行次数明确，容量可预测。

## 代价与风险

- exact-access 验证、依赖图和 MVCC 有内存/CPU 成本；
- serial lane 可能阻塞后续依赖前缀；
- exact-access 覆盖率低或冲突高时收益接近零；
- 状态机/VM 必须正确报告实际读写，需严密 Fuzz/审计；
- 批次构成和 tx_index 会影响可并行度，但不能为性能改变已共识顺序。

## 备选方案

- 仅串行：最简单且保留为 oracle，但放弃多核吞吐；不作为生产默认。
- 纯乐观 STM/无界重试：高冲突成本不可控；拒绝。
- 强制所有交易声明访问集：牺牲通用功能；拒绝。
- 由调度器重新排列交易：改变 consensus order 语义；拒绝。
- 跨账户/对象分片共识：范围和跨分片语义过大；v1 不选。

## 实施和恢复

- 先完成串行 oracle 和 golden traces；
- exact-access verifier 与状态机版本绑定；
- 并行执行输出在开发/采样模式与 serial oracle 对比；
- 资源压力、bug quarantine 或恢复时可本地切 serial，不需 epoch，因为输出语义不变；
- 执行状态、ordered cursor、FinalizedBlockHeader/FinalityStatement 和 attestation intent 原子持久化。

## 验证方式

- 随机程序/状态/交易序列 differential test；
- 不同 worker、调度 seed 和缓存配置逐字节一致；
- 缺失/多报/动态访问集负例；
- 高冲突确保每笔不超过一次推测 + 一次权威重执行；
- serial-lane 与 parallel-lane 交界的前缀/版本测试；
- 每个执行/提交/签名边界 crash recovery；
- benchmark 同时报告 exact-access 覆盖率、冲突率、serial-lane 比例和净加速。

## 参考

- [Block-STM](https://arxiv.org/abs/2203.06871)
