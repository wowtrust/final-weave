# ADR-005：证明驱动查询

- 状态：Accepted
- 日期：2026-07-23
- 关联需求：FR-PROOF-*、FR-EXEC-010、SAFE-009

## 背景

Full、Archive、Gateway 和 Relayer 可能被攻破或只保存部分历史。FinalDAG-C 还明确分开“DAG 顺序已提交”和“执行摘要已获 quorum 背书”；如果 API 只返回节点声称的状态，客户端会把内部进度误认为不可逆业务结果。

## 决策

对外最终查询优先返回可独立验证的证明：

- Transaction：原交易、tx Merkle path、Receipt path、FinalizedBlockHeader、FinalityProof；
- State：Sparse Merkle inclusion/non-inclusion proof、FinalizedBlockHeader、FinalityProof；
- Finalized checkpoint：ordered/state/receipt/event roots、FinalityCertificate、DAG 顺序见证或规范压缩；
- Cross-ledger message：event proof、source FinalityProof、ValidatorSet/ProtocolConfig transition chain 与目标 epoch 的完整 FeatureSet/GasSchedule；
- `REPLACED`：同 sender/nonce 的最终 winner receipt 及证明；
- `EXPIRED`：finalized tip `> valid_until_height` 与固定账户 nonce key 的认证状态证明；present state 必须满足 `next_nonce <= nonce`，non-inclusion 表示该地址尚未建立 nonce 状态。

`DAGCommitWitness` 只能用于内部排序/同步诊断，不能单独把交易、收据或状态标记为 FINALIZED。多 peer 响应用于可用性，不替代证书。

系统不维护永久 historical tx-id seen set 或 `nonce_winner` 状态。终态重放安全来自认证账户 nonce state 或其规范 non-inclusion；裁剪后仍可用状态证明建立 EXPIRED/REPLACED 语义。

## 安全与信任影响

- Query/Archive/Gateway/Relayer 可以不受信任；
- 客户端仍需可信 Genesis/checkpoint、ValidatorSet/ProtocolConfig transition 与目标 epoch 的完整 FeatureSet/GasSchedule；
- proof verifier 必须校验 epoch、domain、validator set、`q` 个不同 ExecutionAttestation 和所有 root；
- pending、DAG committed、本地 executed 和 attested 但未聚合状态不能得到最终证明；
- 证书 envelope 的 signer 子集/字节不得作为协议随机种子或唯一语义 ID。

## 正面结果

- 可安全裁剪并从不受信服务按需取数；
- SDK、监管方和跨账本目标可离线验证；
- API 明确排序最终与执行最终边界；
- 恶意响应最多影响可用性，不能伪造最终状态。

## 代价与风险

- proof 增加响应大小、索引和客户端复杂度；
- ValidatorSet/ProtocolConfig/完整 FeatureSet/GasSchedule 历史和必要 DAG witness 需长期归档；
- range/聚合查询不总有紧凑证明；
- proof schema 与压缩规则需要版本化和 Fuzz；
- 两阶段最终性使外部确认晚于内部 DAG commit。

## 备选方案

- 信任固定 RPC：仅可作为应用本地策略，不标记 Verified；
- 多节点多数返回一致：拒绝，副本和控制域可能相关；
- DAGCommitWitness 直接作为状态最终：拒绝，它不认证执行 root；
- 所有节点保存全历史：可选部署，不是信任机制；
- 永久 tx-id seen set：拒绝，会造成无界认证状态并与 snapshot/pruning 复杂耦合。

## 验证方式

- SDK/跨语言 proof vectors；
- 恶意 Merkle/SMT path、FinalityStatement、attestation bitmap/signature 负例；
- 不同 epoch/config replay；
- `ORDER_FINAL`、`EXECUTED_LOCAL`、`FINALITY_CERTIFIED` 不得通过 API 冒充稳定 `FINALIZED_*` 终态；
- REPLACED/EXPIRED 的 next_nonce 边界测试；
- 裁剪、Archive 取数和跨账本重放测试。
