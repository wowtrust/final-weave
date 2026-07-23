# FinalWeave 首次贡献教程：规范哈希与交易无状态校验

> 目标读者：完成前三篇教程、准备提交第一个 Go PR 的开发者  
> 项目状态：绿地实现；本文路径、接口和代码是建议模板，只有被 ADR/规范和代码评审接受后才成为项目事实  
> 推荐任务：实现一个小而完整的安全边界，而不是第一次就修改 FinalDAG-C 的决定规则  
> 上一篇：[03-development-environment-and-codebase.md](03-development-environment-and-codebase.md) ｜ 下一篇：[05-debugging-testing-and-glossary.md](05-debugging-testing-and-glossary.md)

上一章把 Alice 的交易拆进 `types`、`codec`、`crypto`、`mempool`、`execution` 和 `consensus` 等目标包。本章选择最靠内、最适合作为第一项贡献的边界：让 Alice 的 `KVPut("device/42/status", "online")` 在所有实现中得到同一规范意图哈希，并在进入昂贵网络阶段前完成无状态校验。

## 1. 本次贡献的目标

我们以两个相邻但可拆分的任务为例：

1. **任务 A：TransactionIntent 的规范哈希**；
2. **任务 B：TransactionIntent 的无状态校验**。

理想情况下分成两个 PR：先建立稳定的类型、编码和哈希契约，再增加校验器。每个 PR 都需要：

- 引用冻结规范或 ADR；
- 明确输入、输出、错误和非目标；
- 先提交测试向量和失败测试；
- 有最小实现、Fuzz 属性和安全评审清单；
- 不依赖网络、数据库或系统时间；
- 不改变未在范围内的协议字段。

完成后，新人应该学会 FinalWeave 最重要的开发习惯：

> 共识关键代码不是“让测试变绿”就结束，而是把规范、不变量、字节、错误和故障行为一起固定下来。

## 2. 开始编码前的停止条件

以下任一项未完成时，不要实现稳定哈希：

- TransactionIntent schema 未冻结；
- 确定性 CBOR profile 未冻结；
- 域隔离 transcript 未冻结；
- network/ledger ID 的长度和语义未冻结；
- 字段缺省值与未知字段策略未冻结；
- 没有至少一组可人工/独立复核的 test vector；
- 无法确定 schema 升级是否改变哈希。

此时正确贡献是补 ADR、状态表和 test-vector format，而不是选择自己喜欢的序列化方式。

无状态校验也有停止条件：字段上限、有效窗口、启用交易类型、signer policy hash 和 Envelope 签名规则必须来自协议配置，不能散落成开发者个人常量。

## 3. 写一张规格卡

在 issue 或[核心协议规范](../protocol/README.md)的对应章节中先写类似规格卡：

```text
Spec ID: FW-TX-INTENT-001
Object: TransactionIntent schema_version=1

Canonical bytes:
  Deterministic CBOR profile FW-CBOR-001

Digest:
  SHA-256 through FW-HASH-001 transcript
  domain = TX_INTENT
  network_id and ledger_id are explicit transcript fields

Validation:
  schema/network/ledger/type/sender/nonce/window/resources/payload/policy
  no state access
  no clock access
  no silent normalization

Output:
  [32]byte TxIntentHash

Errors:
  typed stable categories; no payload/key material in messages

Non-goals:
  signature verification
  nonce lookup
  balance/permission check
  mempool replacement
  transaction execution
```

如果团队不能对这张卡达成一致，代码也不可能形成跨节点一致性。

## 4. 建议文件切分

这些是 **[目标]** 路径，创建前先确认阶段 A/B 骨架已经批准：

```text
pkg/types/hash.go
pkg/types/id.go
pkg/types/transaction.go
pkg/codec/encoder.go
pkg/crypto/domain.go
pkg/crypto/digest.go
pkg/mempool/stateless_validate.go
pkg/mempool/stateless_validate_test.go
pkg/codec/transaction_vector_test.go
pkg/codec/fuzz_test.go
specs/vectors/tx-intent-v1/
```

权威目标目录见[实施路线](../04-implementation-roadmap.md)。TransactionIntent 的具体类型/哈希代码可在 `pkg/types`、`pkg/codec` 和 `pkg/crypto` 间按依赖规则拆分；无状态 admission 校验放在 `pkg/mempool`。不要额外创建另一套 `tx` 模型。

不要为了避免包设计而把所有内容放进 `utils.go`。也不要让 `pkg/types` 或 `pkg/mempool` 直接引用 gRPC message。

## 5. 先设计强类型

概念性类型：

```go
// [示例] 真实类型需经过 API 评审。
type Hash [32]byte
type NetworkID [32]byte
type LedgerID [32]byte
type AccountAddress [32]byte
type Height uint64
type Nonce uint64
type Gas uint64

type Intent struct {
    SchemaVersion         uint16
    NetworkID             NetworkID
    LedgerID              LedgerID
    Sender                AccountAddress
    Nonce                 Nonce
    ValidFrom             Height
    ValidUntil            Height
    GasLimit              Gas
    FeeLimit              uint64
    PriorityClass         uint16
    PayloadType           uint16
    AuthorizedAccessScope []AuthorizedAccessEntry
    Payload               []byte
    MemoHash              *Hash
    SignerPolicyHash      Hash
}
```

设计问题：

- `[]byte` 字段是否在构造时复制，避免调用者后续修改？
- slice getter 是否返回副本或只读 iterator？
- typed 业务 builder 是否先把 KVPut 转成规范 payload bytes，避免在共识对象中使用 `any`？
- 零值是否有效？若无效，构造器如何强制？
- 最大长度在哪层检查？
- address 与 validator ID 是否能在编译期区分？

共识对象应尽量不可变。若 `Intent` 内部 slice 在计算哈希后还能被外部修改，就会出现“对象 ID 与对象内容不一致”。

## 6. 任务 A：规范哈希接口

### 6.1 分开编码与哈希

建议让哈希器依赖窄接口：

```go
// [示例]
type IntentEncoder interface {
    EncodeIntent(Intent) ([]byte, error)
}

type IntentHasher struct {
    encoder IntentEncoder
    digests DomainDigester
}

func (h IntentHasher) Hash(v Intent) (Hash, error)
```

好处：

- codec 可独立跑 conformance vectors；
- hash transcript 可独立测试；
- 测试能验证错误传播；
- 未来增加对象类型时不会复制拼接逻辑。

不建议：

```go
// 错误方向：隐式 JSON、忽略错误、截断哈希、没有域。
func (v Intent) Hash() []byte {
    b, _ := json.Marshal(v)
    sum := sha256.Sum256(b)
    return sum[:20]
}
```

### 6.2 哈希不变量

必须写进测试和评审描述：

1. 相同逻辑对象产生完全相同规范字节和 Hash；
2. 不同 network/ledger 不共享 intent hash；
3. 任一被覆盖字段变化都会改变规范字节；
4. `TX_INTENT` 字节不能被当作 `DA_ACK`、`DAG_VERTEX` 或 `EXECUTION_ATTESTATION` 的有效 digest；
5. 编码失败时不返回可使用的 Hash；
6. 输出始终完整 32 字节；
7. 非规范输入不能通过“解码后重编码”被悄悄接受为原始签名对象；
8. 哈希函数无全局状态、无时钟、无随机源；
9. test vector 跨平台、跨进程一致；
10. schema version 变化受明确升级规则控制。

### 6.3 错误返回

不要用零 Hash 表示失败：

```go
// [示例]
func (h IntentHasher) Hash(v Intent) (Hash, error) {
    canonical, err := h.encoder.EncodeIntent(v)
    if err != nil {
        return Hash{}, fmt.Errorf("encode transaction intent: %w", err)
    }
    digest, err := h.digests.Sum(DomainTxIntent, v.NetworkID, v.LedgerID, canonical)
    if err != nil {
        return Hash{}, fmt.Errorf("digest transaction intent: %w", err)
    }
    return digest, nil
}
```

调用者必须检查 error。内部错误可以 wrap 原因，但外部 API 映射到稳定错误码，且不回显敏感 payload。

## 7. 先写 test vectors

### 7.1 向量内容

每个规范向量至少包含：

```text
name
spec_version
human_readable_fields
canonical_cbor_hex
domain_transcript_hex or transcript fields
expected_tx_intent_hash_hex
expected_error (negative vector only)
```

推荐覆盖：

- 最小合法交易；
- 每个整数的边界值；
- 空 authorized access scope 与多元素规范顺序；
- UTF-8/任意 bytes 的明确区分；
- payload 最大合法长度；
- 不同 network、ledger、nonce、有效期；
- 非法未知字段；
- 非最短整数编码；
- map key 非规范顺序；
- 超长 byte string；
- 不支持 schema version。

### 7.2 不要自证正确

如果测试向量由被测 `EncodeIntent` 和 `Hash` 自动生成，再由同一代码读取，它只能证明代码自洽。

可信向量流程：

1. 规范作者给出字段和期望 transcript；
2. 一份简单 reference tool 生成候选字节；
3. 第二位开发者用独立实现或人工拆解复核；
4. 安全评审确认域、长度、版本和边界；
5. 向量作为只读协议资产提交；
6. 正式实现只消费向量，不能覆盖 expected 文件。

## 8. 哈希测试驱动步骤

### 8.1 第一个失败测试

```go
// [示例]
func TestIntentHasher_VectorMinimalV1(t *testing.T) {
    vector := loadVector(t, "minimal-v1")
    intent := intentFromVector(t, vector)

    got, err := newTestHasher(t).Hash(intent)
    require.NoError(t, err)
    require.Equal(t, vector.ExpectedHash, hex.EncodeToString(got[:]))
}
```

先确认测试因“未实现”失败，而不是 fixture 根本没有被执行。

### 8.2 表驱动字段变化测试

```go
// [示例]
func TestIntentHasher_CoversConsensusFields(t *testing.T) {
    base := validIntent(t)
    baseHash := mustHash(t, base)

    tests := []struct {
        name   string
        mutate func(*Intent)
    }{
        {"schema version", func(v *Intent) { v.SchemaVersion++ }},
        {"network", func(v *Intent) { v.NetworkID[0] ^= 1 }},
        {"ledger", func(v *Intent) { v.LedgerID[0] ^= 1 }},
        {"sender", func(v *Intent) { v.Sender[0] ^= 1 }},
        {"nonce", func(v *Intent) { v.Nonce++ }},
        {"valid from", func(v *Intent) { v.ValidFrom++ }},
        {"valid until", func(v *Intent) { v.ValidUntil++ }},
        {"gas limit", func(v *Intent) { v.GasLimit++ }},
        {"fee limit", func(v *Intent) { v.FeeLimit++ }},
        {"priority class", func(v *Intent) { v.PriorityClass++ }},
        {"payload type", func(v *Intent) { v.PayloadType++ }},
        {"authorized access", func(v *Intent) { v.AuthorizedAccessScope = anotherScope(t) }},
        {"payload", func(v *Intent) { v.Payload = anotherPayload(t) }},
        {"memo hash", func(v *Intent) { v.MemoHash = anotherMemoHash(t) }},
        {"signer policy hash", func(v *Intent) { v.SignerPolicyHash[0] ^= 1 }},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            changed := deepCopyIntent(base)
            tt.mutate(&changed)
            require.NotEqual(t, baseHash, mustHash(t, changed))
        })
    }
}
```

这个测试不是数学上的碰撞证明，而是防止实现漏掉字段；字段表必须随冻结 Schema 一一对应，不能只抽测几个“典型字段”。`TransactionEnvelope` 另建覆盖测试，逐一改变完整 `SignerPolicy`、每份签名的 `key_id`、算法和签名字节，并验证策略哈希不匹配、签名重排、重复 signer 与 threshold 不足都会被拒绝或改变 tx_id。

### 8.3 错误传播测试

使用返回 sentinel error 的 fake encoder，断言：

- Hash 返回 error；
- 结果是不可使用零值；
- error chain 支持 `errors.Is`；
- 不 panic；
- 不继续调用 digester。

### 8.4 域隔离测试

对完全相同 canonical payload，分别用 `TX_INTENT`、`DA_ACK`、`DAG_VERTEX` 域计算，结果必须不同。还要确认 network/ledger 变化会改变结果。

## 9. 任务 B：无状态校验器

### 9.1 纯函数边界

无状态校验不访问数据库、mempool、网络或 wall clock：

```go
// [示例]
type ValidationLimits struct {
    NetworkID               NetworkID
    LedgerID                LedgerID
    EnabledSchema           set.Set[uint16]
    EnabledPayloadTypes     set.Set[uint16]
    MaxTransactionBytes     uint32
    MaxValidityWindowHeights uint64
    MaxAccessScopeEntries   uint32
    MaxAccessScopeBytes     uint32
    MaxExecutionGasPerBlock Gas
    PayloadRules            PayloadRuleSet // 从 active FeatureEntry.parameters_cbor typed decode，并由完整 FeatureSet hash 认证
}

type StatelessValidator struct {
    limits ValidationLimits
}

func (v StatelessValidator) ValidateIntent(intent Intent) error
```

配置在构造器中先自校验并深复制。不能让调用者运行中修改 map/slice，导致同一节点不同时刻规则漂移。

### 9.2 校验顺序

建议按成本从低到高，并保证错误优先级固定：

1. schema version；
2. network ID；
3. ledger ID；
4. 固定长度与 enum；
5. 高度窗口和整数溢出；
6. Intent/Envelope 总字节、authorized access scope 数量与字节上限；
7. 规范顺序和重复项；
8. payload 的类型专属结构；
9. memo hash 和 signer policy hash 格式；
10. 独立 Envelope 校验阶段再检查签名数量、顺序、公钥、签名和策略满足关系。

固定错误优先级有助于跨实现 conformance，也避免攻击者通过错误差异探测过多内部状态。

### 9.3 错误类型

```go
// [示例]
type ValidationCode uint16

const (
    CodeUnsupportedSchema ValidationCode = iota + 1
    CodeWrongNetwork
    CodeWrongLedger
    CodeUnknownTxType
    CodeInvalidValidityWindow
    CodePayloadTooLarge
    CodeAccessScopeNotCanonical
)

type ValidationError struct {
    Code  ValidationCode
    Field FieldID
    Cause error
}
```

外部消息可以说 `payload violates the activated payload rule`，但不要包含 payload 内容、完整地址列表或签名字节。

### 9.4 避免验证器“修复”输入

以下都应拒绝，而不是静默修改：

- authorized access scope 未排序；
- Envelope 中 signer 重复；
- 非规范 Unicode 标识符；
- 多余未知字段；
- 非最短整数编码；
- `valid_from_height > valid_until_height`；
- 超限 memo 截断后继续。

若一个节点排序后接受、另一个节点直接拒绝，就会出现签名字节和协议语义分叉。

## 10. 无状态校验测试矩阵

| 类别 | 正例 | 反例 | 边界 |
|---|---|---|---|
| schema | 当前激活版本 | 0、未知未来版本 | 激活集合首尾 |
| network/ledger | 精确匹配 | 每个字节位变化 | 全零 ID 是否禁用 |
| type | 固有 `ACCOUNT_CREATE_V1=1`、`ACCOUNT_POLICY_ROTATE_V1=2`、`LEDGER_RECONFIGURE_V1=3`；Feature 启用的 `CROSS_LEDGER_SEND_V1=4`/`CONSUME_V1=5` 与 `KV_PUT_V1=16`/`KV_DELETE_V1=17` | 未知、未激活类型；把某 payload schema 放入其他 type | 1/2/3/4/5/16/17 与 `uint16` 最大值 |
| sender | 普通交易为精确 32-byte AccountAddress；create 时由 core 重算匹配 | 长度错误；create 的 salt/core/policy hash 与 sender 错配 | 普通交易不重新派生地址 |
| nonce | 协议允许范围 | 保留值（如有） | 0、MaxUint64 |
| validity | from <= until | from > until | 最大合法窗口及 +1 |
| gas/fee/priority | `gas_limit` 在 `(0,max_execution_gas_per_finalized_block]` 且 v1 `fee_limit=0`、`priority_class=0` | gas 超块上限；任何非零 fee/priority；解码溢出 | gas 1/块上限；fee/priority 0/1/各自最大值 |
| payload | 满足固有/active FeatureSet 登记的类型 Schema；KV 允许空 value | KV 空 key、系统 namespace、违反该类型规则 | 类型规则声明的最大长度及 +1 |
| authorized access scope | 排序、唯一、EXACT/PREFIX 合法 | 逆序、重复、非法 mode | 0、最大项数及 +1 |
| hash commitments | `signer_policy_hash` 与可选 `memo_hash` 为 Hash32 | 解码阶段错误长度 | 是否禁止零值只能由规范决定 |

签名数量、重复 signer、签名顺序和 M-of-N 满足关系属于 `TransactionEnvelope` 的无状态校验矩阵，应作为紧随其后的独立任务，不能偷偷塞进 Intent 哈希函数。

`ACCOUNT_CREATE_V1` 还需要一张单独矩阵：payload 必须是唯一 canonical `CreateAccountPayloadV1`，nonce 为 0，用户 `authorized_access_scope` 为空，Envelope policy hash 同时匹配 core 与 Intent，signatures 达到 initial policy threshold，Gas 按 operation `0x00010001` 的完整固定 trace 精确预检。父状态 meta/auth/nonce 全无、残缺三元组停机、同块 created-set、mandatory resource reserve 和三项原子写属于有状态 occurrence filter/执行器测试，不应被误塞进这个纯无状态函数。普通交易没有 core；其 sender 归属要由父 state root 认证的完整三元组和 active policy 判断。

本章范围内的每个错误用`errors.As`断言code/field，不要只比较易变的完整字符串。做到这里已经满足任务B；下面只是帮助你看懂后续工作如何承接本次无状态边界，**不属于本次PR的验收条件，也不应顺手实现**。

### 10.1 后续进阶测试地图（非本次贡献）

接着为 occurrence filter 写一组“工作只在对应预算后发生”的测试。先耗尽某occurrence sponsor的scan reserve/shared，用decoder、tx-hash、SMT和crypto spy证明`PREFILTER_SCAN_CAP`只会固定chunk比对verified source bytes；随后让scan通过，再构造stale nonce、future nonce、过期窗口、active policy hash错配和明显超出Body reserve的Envelope，断言它们不会调用Ed25519、strict-key或治理bundle worker。对exact nonce candidate必须先按`PrefilterExpensiveWorkCostV1`扣款并原子写`PrefilterAttemptV1{STARTED}`；即使三类field counter为0，固定base也使worker cost至少为1。再令`P<n`，分别从Vertex author 0、P与n-1注入最大合法模板，证明份额数组按全部n个sponsor建立；让f个恶意Vertex作者用坏签名或反复引用同一诚实Batch耗尽shared后，诚实sponsor的本地预验交易仍可使用其独占reserve。

最后对scan/common/source三类状态机逐点崩溃：charge与版本化STARTED必须同生共灭，origin cursor、同时含Batch作者与containing Vertex sponsor的source binding、`sponsor_author_index`和receipt必须重验，恢复重跑worker但不重扣，本地timeout或磁盘错误不能写成INVALID。完成occurrence时，in-flight scan receipt必须恰好一次并入completed-scan sponsor-indexed reserved/shared累计量，再与全部common attempt receipts重算通用spend；source spend由全部proof-attempt receipts重算。任一漏算、双算、cursor越过STARTED、VALID artifact digest损坏或active bundle ID错配都只能回滚到完整occurrence边界（没有则从块首重扫），绝不能从中间游标把预算清零。

有状态执行测试不能只断言最终错误码。要为七个 payload 的所有激活组合固定完整 GasEvent 顺序，并构造同一 READ/WRITE 同时 scope 越权、resource 越界且 Gas 差 1 的用例，断言优先级为 `ACCESS_SCOPE_VIOLATION > STATE_LIMIT_EXCEEDED > OUT_OF_GAS`。普通 winner 的 nonce commit 不应出现在 GasEvent 列表，但 success/FAILED/OOG 的 StateChange 中都必须出现；账户创建成功 result必须含 meta/auth/nonce三项，CrossLedger CONSUME成功必须含 consumed/nonce两项。KV DELETE还要比较规范 tombstone sizing；跨账本测试则必须从 target active policy开始验证 source transaction/Receipt/两条 Event path，并证明并发 replay无 Receipt、无 nonce消费。还要用 crypto spy证明未授权账户、stale/future nonce与 `gas_limit=1` 的最大 proof不会进入 source verifier，并以 f个恶意Vertex sponsor持续 proof spam或反复引用诚实Batch，验证honest-sponsor保留份额仍能推进；过期状态fixture必须提供窗口内历史 policy context和窗口后 tip context两份独立最终性证据。完整 proof fixture应按协议第六篇单独实现，不能在这个无状态校验练习里用 `valid=true` 布尔值替代。

## 11. Fuzz 测试

### 11.1 解码器属性

```go
// [示例]
func FuzzDecodeIntentNeverPanics(f *testing.F) {
    for _, seed := range canonicalAndMalformedSeeds() {
        f.Add(seed)
    }
    f.Fuzz(func(t *testing.T, data []byte) {
        _, _ = DecodeIntent(data)
    })
}
```

还应通过测试 harness 限制输入大小、执行时间和内存。`never panics` 只是最低要求。

### 11.2 Round-trip 属性

对合法内部对象：

```text
Decode(Encode(x)) == x
Encode(Decode(canonical_bytes)) == canonical_bytes
```

对非规范但可解析字节，严格解码器应拒绝，而不是返回对象后重新编码。

### 11.3 确定性属性

同一对象重复编码和哈希多次必须一致；并发调用也必须一致且通过 race detector。

### 11.4 校验器属性

随机字节和随机组合不能：

- panic；
- 分配与声明长度不成比例的内存；
- 进入无限循环；
- 修改输入；
- 改变 validator 配置；
- 返回 code 之外的含敏感原文错误。

不要写“任意两个不同对象哈希一定不同”的绝对 Fuzz 属性；密码学哈希理论上存在碰撞。应写明确字段覆盖和规范向量测试。

## 12. 最小实现顺序

建议提交顺序：

1. 增加规格卡和 vector schema；
2. 增加 positive/negative vectors；
3. 增加 vector runner；
4. 增加失败的 codec/hash tests；
5. 实现最小 codec profile；
6. 实现统一 DomainDigester；
7. 实现 IntentHasher；
8. 增加 mutation/domain/error tests；
9. 增加 Fuzz corpus；
10. 独立复核向量；
11. 另一个 PR 增加 ValidationLimits；
12. 增加校验表格的失败测试；
13. 最小实现 StatelessValidator；
14. 增加 fuzz/race/conformance；
15. 更新协议符合性矩阵。

在实现过程中，不顺手增加网络广播、mempool 或签名钱包。

## 13. 本地验证命令

只有相应 Go module 和包创建后，以下标准命令才适用：

```bash
go test ./pkg/types ./pkg/codec ./pkg/crypto ./pkg/mempool
go test -race ./pkg/types ./pkg/codec ./pkg/crypto ./pkg/mempool
go test -run TestIntentHasher -count=100 ./pkg/codec
go test -fuzz FuzzDecodeIntent -fuzztime 60s ./pkg/codec
go vet ./...
go test ./...
```

若仓库后来提供 `make test-unit` 等包装命令，以 CI 的单一事实来源为准。不要在 PR 描述中声称运行了尚未存在或实际未执行的命令。

## 14. Review Checklist

### 14.1 规范与兼容性

- [ ] PR 引用了准确 spec/ADR ID；
- [ ] schema、域和 transcript 没有由实现临时决定；
- [ ] 共识字段全部进入规范编码；
- [ ] 未知字段和未知版本默认拒绝；
- [ ] 没有 JSON、Go 内存布局或 map 遍历进入哈希；
- [ ] 输出使用完整 32 字节；
- [ ] 变更 test vector 时说明是否协议破坏性升级；
- [ ] 不同 network/ledger/object domain 不能重放。

### 14.2 Go 安全性

- [ ] 所有 slice/map 输入已复制或明确只读所有权；
- [ ] 整数加减乘使用 checked 语义；
- [ ] 错误未被忽略；
- [ ] 不使用全局可变配置；
- [ ] 不读取时钟、随机数、环境变量或网络；
- [ ] 并发调用通过 race detector；
- [ ] 对恶意长度先检查再分配；
- [ ] 不在 error/log 中输出敏感 payload。

### 14.3 测试

- [ ] 有正例、反例、边界和字段 mutation；
- [ ] 有跨实现/独立复核 test vector；
- [ ] 有 codec、hash、validation 错误传播测试；
- [ ] 有 Fuzz seeds 和属性；
- [ ] 测试不会覆盖或重新生成 expected vector；
- [ ] `go test ./...`、race、vet 结果记录完整；
- [ ] 没有依赖测试执行顺序或本地时区。

### 14.4 API 语义

- [ ] 零值和 nil 行为明确；
- [ ] typed error code 稳定；
- [ ] Validate 不修改输入；
- [ ] 编码与验证职责没有互相隐藏；
- [ ] SDK 预检和节点验证共享规范，但节点不信任 SDK；
- [ ] 注释区分 intent hash 和 tx ID。

## 15. PR 描述模板

```markdown
## Summary
Implements FW-TX-INTENT-001 transaction-intent hashing.

## Invariants preserved
- deterministic canonical bytes
- complete 32-byte digest
- object/network/ledger domain separation
- no mutable global state

## Out of scope
- signatures
- mempool admission
- stateful nonce validation

## Test vectors
- minimal-v1
- max-boundaries-v1
- wrong-schema-negative-v1

## Verification
- go test ...
- go test -race ...
- go test -fuzz ...
- independent vector review: <review reference>

## Security notes
...

## Compatibility
No released schema exists / or exact upgrade impact.
```

描述应列出真实运行结果；Fuzz 时间、平台和随机 seed（失败时）要可复现。

## 16. 评审时的演示

用五分钟演示：

1. 打开规范卡，指出每条规则落在哪个测试；
2. 运行 minimal vector；
3. 修改 ledger ID 一个 bit，展示 hash 变化；
4. 提交未排序 authorized access scope，展示稳定错误码；
5. 展示 malformed corpus 不 panic；
6. 说明代码没有访问数据库、网络和时间；
7. 指出未来签名验证在哪里接入，为什么不在本 PR。

如果无法从规范追到测试再追到实现，说明贡献边界还不清晰。

## 17. 新人容易犯的实现错误

| 错误 | 后果 | 修正 |
|---|---|---|
| `json.Marshal` 后哈希 | 跨语言和字段表示不稳定 | 确定性 CBOR profile |
| 只哈希 payload | 跨账本重放、nonce 未绑定 | 覆盖完整意图与域 |
| 错误时返回零 Hash、nil | 调用者误用零值 | 返回 typed error |
| `sort` 输入后接受 | 签名字节被静默改变 | 要求规范输入或在签名前显式构造 |
| shallow copy intent | 哈希后 slice 可变 | 构造时深复制并限制 getter |
| 测试自己生成 expected | 只能证明自洽 | 独立固定向量 |
| 用当前高度做“无状态”校验 | 结果随节点状态变化 | 放到有状态执行阶段 |
| 校验只在 SDK 做 | 恶意客户端绕过 | 每个接入/执行节点独立验证 |
| Fuzz 任意大声明长度 | OOM 而非有效测试 | 解码前设全局和字段上限 |
| 一个 PR 同时改 schema 和实现 | 无法判断错在规范还是代码 | 先规范/向量，再实现 |

## 18. 练习

### 练习 1：分层

把以下检查分到规范解码、无状态校验、有状态校验、执行四类：

- CBOR 整数不是最短编码；
- ledger ID 与节点请求账本不符；
- Alice 当前 nonce 是 19，但交易 nonce 是 18；
- 本题fixture的active `max_state_key_bytes=1 KiB`，而KV key超过该上限；
- Alice 没有写该 namespace 的权限；
- KV 写入触发 gas 消耗并产生 event。

<details>
<summary>答案提示</summary>

依次：规范解码；无状态校验；有状态校验；相对于题设active bundle的静态校验；有状态校验；执行。`1 KiB`只是本题fixture，不是v1硬编码常量；实现必须读取并认证active ProtocolConfig/Feature中的component cap。实际管线可在不同阶段重复低成本检查，但协议语义归属要清楚。

</details>

### 练习 2：评审伪代码

```go
func Validate(v *Intent) error {
    sort.Slice(v.AuthorizedAccessScope, func(i, j int) bool {
        return compareAccessEntry(v.AuthorizedAccessScope[i], v.AuthorizedAccessScope[j]) < 0
    })
    if v.ValidUntil-v.ValidFrom > maxWindow {
        return errors.New("bad window")
    }
    return nil
}
```

找出至少四个问题。

<details>
<summary>答案提示</summary>

修改了输入；静默规范化可能改变签名字节；比较函数若未覆盖 `(scope_kind,mode,namespace,key_or_prefix)` 就不是协议全序；减法可能在 `until < from` 时无符号下溢；`maxWindow` 可能是全局可变量；错误没有稳定 code/field；没有检查重复授权项；指针 nil 行为不明确。

</details>

### 练习 3：测试向量变更

某开发者为“修复编码”修改了 expected hash，所有测试重新通过。你在评审中要问什么？

<details>
<summary>答案提示</summary>

编码是否违反已冻结规范；是实现 bug 还是协议升级；所有共识对象和签名是否受影响；是否需要 schema/feature/epoch 升级；向量是否由独立实现复核；已生成交易和 checkpoint 如何处理；为什么不能修实现而必须改 expected。不能把更新快照当成正确性证据。

</details>

## 19. 完成定义

首个贡献只有满足以下条件才完成：

- 规范、向量、测试和实现互相可追踪；
- reviewer 能独立复算至少一个向量；
- 所有错误和边界均有确定行为；
- Fuzz/race/vet/全量测试通过；
- 没有超出 issue 的网络、存储或共识副作用；
- 文档明确哪些接口已成为事实，哪些仍是规划；
- 你能解释该 PR 防止了哪类分叉、重放或拒绝服务问题。

下一篇将教你在模块增多后如何定位故障、设计测试和安排前 30 天成长路径：[05-debugging-testing-and-glossary.md](05-debugging-testing-and-glossary.md)。
