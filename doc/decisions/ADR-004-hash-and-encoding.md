# ADR-004：完整 32 字节域隔离哈希与确定性编码

- 状态：Accepted
- 日期：2026-07-23
- 关联需求：NFR-COR-*、SEC-006、protocol schema

## 背景

共识节点必须对相同对象产生相同字节和哈希。截断哈希、无域隔离签名、非规范 map 顺序和未知字段会增加碰撞、重放和跨实现分歧风险。

## 决策

- 共识哈希使用完整 SHA-256 32 字节；
- 哈希输入包含固定 magic、object domain、schema version、NetworkID、LedgerID 和规范 payload；
- 共识对象使用 RFC 8949 确定性 CBOR 严格子集；
- 禁止浮点、无限长度、重复 map key 和未知共识字段；
- API 使用 Protobuf，但 API 序列化不直接参与共识哈希；
- Merkle leaf/node/empty root 使用不同固定域；
- 所有对象发布机器可读 test vectors。

## 安全与信任影响

- 防止跨网络、跨账本和跨对象签名重放；
- 避免实现语言 map 顺序造成分叉；
- unknown feature 必须通过版本激活，而不是静默忽略。

## 正面结果

- 长期安全余量；
- 易于跨语言实现；
- proof 和对象 ID 规则统一；
- 可通过 golden vectors 审核。

## 代价与风险

- 自定义严格 CBOR profile 需要更多验证器；
- 相比截断哈希多占少量存储；
- schema 演进必须严格治理；
- Protobuf 与内部对象需要显式转换。

## 备选方案

- SSZ：可行，但 v1 团队需额外工具链；
- deterministic Protobuf：unknown fields 和 schema 约束仍需额外规范；
- BLAKE3：性能好，但 v1 优先采用更普遍的 SHA-256；
- 20 字节截断：拒绝。

## 验证方式

- 跨语言 vectors；
- 非规范编码 negative corpus；
- 每字段 mutation tests；
- fuzz parser resource bounds；
- domain replay tests。

