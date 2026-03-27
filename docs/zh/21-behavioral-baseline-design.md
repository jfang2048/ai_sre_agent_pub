# 行为基线与重复性 Burst 判别

English version: [docs/en/37-behavioral-baseline-design.md](../en/37-behavioral-baseline-design.md)

这份说明专门解释 controller 侧的 workload 行为判别层。实现主要在：

- [`../../backend/internal/controller/agentcore/behavioral_memory.go`](../../backend/internal/controller/agentcore/behavioral_memory.go)
- [`../../backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go)
- [`../../backend/internal/controller/agentcore/workflow_eventization.go`](../../backend/internal/controller/agentcore/workflow_eventization.go)
- [`../../backend/internal/controller/agentcore/evidence_contract.go`](../../backend/internal/controller/agentcore/evidence_contract.go)

它要解决的是一类很常见、也最容易把值班人折腾烦的误报：

- build worker 编译几分钟，CPU 打满
- deployment helper 重启时短时间刷很多日志
- backup 或 artifact upload 在固定时间段冲高网络
- ML workload 在训练或推理阶段拉高 GPU 利用率和显存

如果系统只看阈值，这些都会像“新事故”一样被重复报出来。

## 为什么不再做第二套行为历史存储

这个仓库已经有长期历史来源：

- ingest 里的有界热历史
- 可选的 TSDB / timeseries 路径，通过现有 `MetricHistoryProvider` 暴露给 workflow

再引入一套行为 profile 数据库，代价并不小：

1. 会复制长期事实
   运维排障时会立刻遇到一个烦人的问题：到底该信 TSDB 历史，还是信 behavior profile。

2. 会增加运维面
   新 store 就意味着 retention、损坏恢复、schema 演进、配置和启动顺序都要再维护一套。

3. 会让派生摘要和真实历史逐渐脱节
   一旦 suppression 的依据不是直接可回放的历史窗口，而是另一份二次加工结果，解释成本就会上升。

这里并不需要那样的复杂度。这个分类器真正需要的是：

- 读长窗口历史
- 和当前 burst 做比较
- 给 workflow 一个更接近生产语义的分类

## 最终设计

当前实现遵守一个简单原则：

- 长期历史由现有 `MetricHistoryProvider` 提供
- 如果这个 provider 背后连的是 TSDB，那 TSDB 就是长期事实来源
- 分类器只保留一个很小的内存 cache，避免同一轮 workflow 反复查同一段历史

这里没有第二套 profile store，也没有单独的行为数据库。

## 它为什么能工作

因为这个问题本质上是“上下文判别”，不是“再存一份历史”。

判别时真正要回答的是：

- 当前值相对 recent baseline 偏离了多少
- 相对更长窗口的 habitual baseline 偏离了多少
- 这个 burst 是否在同一 workload 上反复出现
- 是否经常出现在相近时段
- 这次 burst 有没有伴随真正的下游损伤

只要现有历史路径能回答这些问题，就已经足够。多做一层长期存储，并不会让答案本质上更对，只会让系统更重。

## 决策流

当前 workflow 的判别顺序很直接：

1. 从当前 `NodeSnapshot`、labels 和 top-process 上下文推导 workload 身份
2. 通过 `MetricHistoryProvider` 拉取同一 collector 的长窗口历史
3. 从当前窗口提取活跃信号
4. 对每个信号计算：
   - 当前值
   - recent baseline
   - long-window baseline
   - 历史高水位
   - recurrence 次数
   - 小时级时间桶匹配
5. 收集 corroboration：
   - 错误日志 / log burst
   - service latency 回归
   - runtime / eBPF 行为异常
   - security finding
   - deployment / change context
6. 输出分类、理由和 cross-signal support
7. 把结果反馈到 risk score、trend summary 和 evidence

重点不在“把 burst 藏起来”，而在“让 workflow 改变解释方式”。

## 当前分类语义

### `expected_recurring_burst`

适用情况：

- 历史上已经反复出现
- 最近和长窗口都能找到相近形态
- 没有明显下游损伤

含义：

- 保留原始 telemetry
- 但 workflow 不把它当成新的硬故障

### `suspicious_deviation`

适用情况：

- 历史不足
- 或者确实偏离了既有模式
- 但还没有足够强的额外证据把它升级成相关性事故

含义：

- 保守保留
- 不提前 suppress

### `correlated_anomaly`

适用情况：

- 资源信号本身可能不是最极端
- 但日志、延迟、runtime 或 security 证据开始同向恶化
- 或者出现 GPU pinned memory 这类“单看 utilization 看不出来”的异常组合

含义：

- 这是“熟悉 burst + 明显损伤证据”的组合
- 应该升级，但不必把所有这类场景都夸大成最强等级

### `confirmed_anomaly`

适用情况：

- 超过历史高水位很多
- 或者出现 OOM、node eviction、GPU driver fault 这类硬故障信号

含义：

- 不做 suppression
- 明确作为高可信 incident 继续推进

## 工业场景与回归测试

下面这些场景已经被加入
[`../../backend/internal/controller/agentcore/behavioral_memory_test.go`](../../backend/internal/controller/agentcore/behavioral_memory_test.go)
的回归测试。

这些回归不是只测一层：

- 一部分是表驱动分类测试，直接压分类器本身
- 一部分是 `EvaluateJointRisk(...)` 级别的回归，确保 suppression 或 escalation 真的会反映到最终 `JointRiskAssessment`

新增的 workflow 级回归专门盯住几类最容易在真实系统里退化的情况：

- deployment log burst 明明是历史常态，但最终 `JointRiskSignal` 里又被抬回去
- deployment / startup 期间的 latency warmup 需要明确解释成 deploy 上下文，而不是一律退回“历史不足”；只有同样形状重复足够多次后，才允许完全压成历史常态
- 内存已经出现 OOM kill 证据，却还被当成普通波动
- CPU 偏差本身不算离谱，但延迟和错误已经一起变坏，这时不该还把它当作“只是相关”
- 加上 peer 对比后，单个副本的异常不能再被“这类 workload 平时就会 burst”轻易盖过去，而整组副本一起变忙时又不该被误判成单点事故

### CPU 场景

#### 1. build / compile worker 的预期 CPU burst

真实场景：

- 编译、链接、镜像构建会短时间打满 CPU
- 但流程本身是健康的

期望输出：

- `expected_recurring_burst`

为什么：

- 这类 workload 的价值在于吞吐，不在于 CPU 看起来平稳
- 如果历史上已经反复出现且没有错误和延迟损伤，继续报 incident 没意义

#### 2. 流量突增 + autoscaling 滞后的 CPU spike

真实场景：

- 请求量突然升高
- CPU 先上去，扩容稍后跟上
- 错误率仍低，延迟还在预算内

期望输出：

- 有充分历史时偏向 `expected_recurring_burst`
- 历史不足时保守留在 `suspicious_deviation`

为什么：

- 这更像容量边界被短暂碰到，而不是服务本身异常

#### 3. runaway CPU / busy loop

真实场景：

- CPU 突然飙升
- 没有对应的已知周期性 workload
- 也没有健康的 deploy/batch 背景

期望输出：

- 至少 `suspicious_deviation`
- 如明显超过历史高水位，可直接 `confirmed_anomaly`

#### 4. deployment 相关的短时 CPU spike

真实场景：

- rollout、restart、JIT、classloader 或 sidecar init 带来短时 CPU 跳升

期望输出：

- 历史上常见且时间短时，降级为 `expected_recurring_burst`

为什么：

- restart 或 rollout 常常会短时间重建缓存、预热类加载器、恢复连接池
- 如果历史上已经多次健康出现同样形状，就不该反复把它升级成 incident

### 内存场景

#### 5. 启动预热引起的内存上升

真实场景：

- 缓存填充、模型加载、classloader 预热会让内存快速上升
- 之后会稳定下来

期望输出：

- `expected_recurring_burst`

#### 6. 渐进式内存泄漏

真实场景：

- 内存在更长窗口里持续爬升
- 问题不只是瞬时尖峰，而是斜率一直不对

期望输出：

- `suspicious_deviation` 或 `confirmed_anomaly`

为什么：

- 这不是健康 recurring burst
- 自适应基线不应该把 leak 正常化

#### 7. OOM 风险 / OOMKilled

真实场景：

- 内存逼近 limit
- pressure 上升
- 日志出现 OOM kill、进程重启或 kill 信号

期望输出：

- `confirmed_anomaly`

为什么：

- 一旦已经出现 kill 证据，这就不是普通 warmup 或缓存填充了
- 新增的 workflow 级回归也会检查最终 `memory_pressure` 的 `JointRiskSignal` 仍然保持触发，而不只是分类器内部判断为异常

#### 8. 节点级内存压力 / eviction

真实场景：

- 节点整体进入内存压力
- 调度、驱逐、placement 都开始受影响

期望输出：

- `confirmed_anomaly`

为什么：

- 这已经是基础设施级故障，不应该因为 workload 以往也吃内存就被降级

### GPU 场景

#### 9. 训练 / 推理任务的预期 GPU burst

真实场景：

- GPU utilization 和显存使用在作业窗口内一起上升
- 过去也多次出现，且作业健康

期望输出：

- `expected_recurring_burst`

#### 10. 非预期的 GPU 显存打满

真实场景：

- GPU memory 突然冲高
- 没有对应的已知训练或推理模式

期望输出：

- `suspicious_deviation` 或更高

#### 11. GPU utilization 很低，但显存一直被占住

真实场景：

- 显存没有释放
- utilization 却很低
- 常见于卡住的 workload、泄漏或清理失败

期望输出：

- `correlated_anomaly`

理由通常会提到：

- `gpu_memory_pinned`
- stuck workload

#### 12. GPU driver / hardware fault

真实场景：

- XID、page retirement、GPU reset、job health fail 之类的信号出现

期望输出：

- `confirmed_anomaly`

为什么：

- 这是硬故障证据，不能只看 utilization 曲线像不像平时

#### 13. GPU 压力叠加用户可见损伤

真实场景：

- GPU 很忙
- service latency 上升
- 错误日志也开始变多

期望输出：

- `correlated_anomaly` 或 `confirmed_anomaly`

### 多信号上下文场景

#### 14. 反复 burst，但没有下游损伤

真实场景：

- CPU 或网络周期性尖峰
- 没有 error-rate 上升
- 没有 log anomaly
- 没有 latency 回归

期望输出：

- 随着历史变厚，逐渐趋向 `expected_recurring_burst`

#### 15. 相同 burst 模式，这次却带来损伤

真实场景：

- burst 形状本身是熟悉的
- 但这次错误率、日志或 latency 开始一起恶化

期望输出：

- `correlated_anomaly`
- 如果同时超过历史高水位或伴随硬故障，再升到 `confirmed_anomaly`

#### 16. backup / artifact upload 的网络 burst

真实场景：

- 固定时间窗口内 throughput 上升
- 网络忙，但业务本身没有受伤

期望输出：

- `expected_recurring_burst`

#### 17. deployment 相关 log burst

真实场景：

- rollout / restart 会短时间写出很多日志

期望输出：

- 健康且重复时降级
- 如果错误文本变重、出现 restart-loop 迹象，再升级

为什么：

- deployment 路径本来就容易产生大量 warn 或 startup 日志
- 这次新增的 workflow 回归不是只喂一个假的 log 序列，而是走了真实的索引日志查询和 TSDB 历史比对路径，确保最终输出也会降级

#### 18. 指标偏差一般，但日志 / 延迟证据很强

真实场景：

- CPU 或内存本身没有离谱到一眼看出事故
- 但日志、延迟、runtime 证据都在同一方向变坏

期望输出：

- `correlated_anomaly` 或 `confirmed_anomaly`

为什么：

- 如果用户已经能感知到延迟和错误一起变坏，就不应该还把资源信号仅仅看成“背景噪声”
- 当前 workflow 回归里，这个场景会把 `cpu_pressure` 提升到 `confirmed_anomaly`，同时 `service_latency` 也会作为独立信号继续保持触发

### Peer 对比场景

#### 19. 只有一个副本很热，其他副本正常

真实场景：

- 这个 service 平时本来就存在一定 burst 历史
- 但这次只有一个 replica 明显冲高
- 同一服务的其他副本还在各自正常范围内

期望输出：

- 不应再被压成 `expected_recurring_burst`
- 至少保持 `suspicious_deviation`
- 解释里应指出它相对 peers 是孤立异常

为什么：

- peer 对比是区分“整个服务都忙”与“这个副本单独异常”最便宜也最直接的办法

#### 20. 同组副本一起进入相同 burst

真实场景：

- 同一服务的多个 replica 同时出现短时 CPU burst
- 延迟和日志仍然健康
- 这类 burst 在历史里也已经出现过

期望输出：

- `expected_recurring_burst`
- 解释可以明确说明 same-service peers 也在经历同样的 burst

为什么：

- 一整组副本一起变忙，通常比单个副本单独冲高更像健康负载
- 这里复用的是当前 fleet snapshot，不需要再引入第二套长期历史存储

## 示例输出形状

```json
{
  "signal_id": "cpu_pressure",
  "classification": "correlated_anomaly",
  "reason": "Recurring CPU burst now aligns with error-log burst and service latency regression",
  "cross_signal_support": [
    "error_log_burst",
    "service_latency_regression"
  ]
}
```

这里最重要的是三个字段：

- `classification`：workflow 应该把这个信号当成多严重
- `reason`：给 operator 的可读解释
- `cross_signal_support`：把升级依据显式列出来，便于事后复盘

## 评估入口

这套设计对应的具体回归数据放在：

- [`../../eval_data/anomaly_cases.json`](../../eval_data/anomaly_cases.json)

评估方法和当前测得的结果放在：

- [`19-testing-and-evaluation.md`](19-testing-and-evaluation.md)

这样拆分是刻意的：

- 这篇文档负责讲设计、边界和取舍
- 评估文档负责讲当前实现到底测到了什么、还有什么没测到

## 代价与边界

当前方案的收益是：

- 不引入第二套长期历史存储
- collector 热路径没有额外重负
- 可以直接用真实历史窗口解释 suppression

仍然存在的边界包括：

- trace seasonality 还不够丰富
- 目前只有当前快照级别的 peer comparison，还没有做更长窗口的历史 peer 对比
- workload identity 如果持续抖动，recurrence 证明会变弱
- 小时级时间桶足够便宜，但仍然是粗粒度模型

这套设计的目标不是“完全消灭误报”，而是把最常见、最烦人的重复性误报压下去，同时保持对真实基础设施故障和多信号事故的敏感度。
