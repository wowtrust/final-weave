# FinalWeave 架构决策记录

ADR（Architecture Decision Record）记录会影响协议、安全、兼容性或长期工程结构的决策。

## 状态

- Proposed：讨论中；
- Accepted：已接受，是规范的一部分；
- Rejected：未采用；
- Superseded：被新 ADR 取代；
- Deprecated：仍兼容但不建议继续使用。

## 优先级

Accepted 且未被取代的 ADR 高于普通设计说明。ADR 不能仅凭代码实现被隐式修改。

## 何时必须写 ADR

- 改变共识算法或 quorum；
- 改变 slot、round-jump、稳定前缀或执行背书规则；
- 改变对象 schema、编码、哈希或签名域；
- 改变交易顺序或执行语义；
- 改变状态根或证明格式；
- 改变 validator set/epoch 规则；
- 引入新的信任假设；
- 改变持久化安全状态；
- 引入不兼容网络协议；
- 替换核心存储、状态机或密码算法；
- 扩展跨账本安全模型。

## ADR 模板

```markdown
# ADR-NNN：标题

- 状态：Proposed
- 日期：YYYY-MM-DD
- 决策人/评审人：
- 关联需求：

## 背景
## 决策
## 安全与信任影响
## 正面结果
## 代价与风险
## 备选方案
## 实施和迁移
## 验证方式
```

## 当前决策

- [ADR-001：许可型多账本](ADR-001-permissioned-multi-ledger.md)
- [ADR-002：BatchAC + FinalDAG-C 直接 DAG 共识](ADR-002-finaldag-c-direct-dag.md)
- [ADR-003：预设顺序可串行化的确定性并行执行](ADR-003-deterministic-speculative-parallel-execution.md)
- [ADR-004：32 字节域隔离哈希与确定性编码](ADR-004-hash-and-encoding.md)
- [ADR-005：证明驱动查询](ADR-005-proof-carrying-query.md)

## 项目级复杂度准入

FinalWeave 允许为功能、安全、性能或可恢复运维增加复杂度，但 ADR 必须写出可测收益、不变量、资源上限、故障/恢复路径、观测方法和回退边界。仅以“更先进”或单一正常路径 benchmark 为理由的复杂化不能接受。
