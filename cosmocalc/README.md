# cosmocalc — 宇宙学红移、哈勃距离与谱线证认服务

纯服务端计算组件：上游提交光谱观测数据，服务算出**红移量**、**退行速度**与低红移近似下的**共动距离**，并判定结果是否落在非相对论线性区。每次核算的请求与结果都会持久化，可供历史查询。在此之上服务还持有一份**静止波长线表**并能对只给观测峰的光谱做**自动谱线证认**（配对、歧义与回放），证认走独立入口、独立存储，不污染核算历史。

## 快速开始

```bash
docker compose up --build
```

服务监听 `:8080`，PostgreSQL 随之启动并自动建表（核算历史、自备线表、证认报告均随库持久，重启后仍在）。无 `DATABASE_URL` 时服务退化为内存存储（便于本地开发，历史/线表/报告不持久）。

```bash
# 本地开发（需 Go 1.22+）
go test ./...        # 运行全部自动化测试
go run ./cmd/server  # 内存存储模式启动
```

## 物理约定（固定，不混用）

| 量 | 单位 |
|---|---|
| 距离 | Mpc（兆秒差距） |
| 速度 | km/s |
| 哈勃常数 | km/s/Mpc |
| 波长 | 任意单位，静止/观测一致即可（仅比值参与计算） |

- 红移定义：`z = λ_obs / λ_rest − 1`（**静止波长做分母**）。`λ_obs > λ_rest` 为红移（z > 0），反之为蓝移（z < 0），z = 0 时速度、距离严格为零。
- 低红移线性区（默认关系 `cosmological_linear`）：`v = c·z`，`D = v / H₀`，c = 299792.458 km/s。
- 可选相对论多普勒关系（`relativistic: true`，标签 `relativistic_doppler`）：`β = ((1+z)²−1)/((1+z)²+1)`，反变换 `z = √((1+β)/(1−β)) − 1`。两套关系是独立代码路径，由开关显式选择，绝不混合。
- 线性阈值固定为 **z = 0.1**：超过后仍返回线性近似值，但响应带 `beyond_linear_threshold: true` 与 `warning`。
- **蓝移不做距离查询**：z < 0 时返回 422 结构化错误 `blueshift_distance`，绝不冒充朝前方的正距离。

## API

### `POST /api/v1/redshift` — 计算红移

输入三选一：`rest_wavelength`+`observed_wavelength` / `redshift` / `velocity_kms`；可选 `relativistic`（默认 false）。

```bash
curl -X POST localhost:8080/api/v1/redshift \
  -d '{"rest_wavelength":656.28,"observed_wavelength":675.9684}'
```
```json
{"redshift":0.03,"velocity_kms":8993.77,"shift_type":"redshift","relation":"cosmological_linear"}
```

### `POST /api/v1/distance` — 计算距离与速度并判定线性区

在红移接口输入之上**必须**提供 `hubble_constant`。

```bash
curl -X POST localhost:8080/api/v1/distance -d '{"redshift":0.5,"hubble_constant":70}'
```
```json
{"redshift":0.5,"velocity_kms":149896.229,"shift_type":"redshift",
 "relation":"cosmological_linear","hubble_constant":70,"distance_mpc":2141.3747,
 "linear_regime":false,"beyond_linear_threshold":true,"linear_threshold":0.1,
 "warning":"redshift exceeds the linear-regime threshold; linear approximation returned but no longer reliable"}
```

### `POST /api/v1/batch` — 批量计算

```bash
curl -X POST localhost:8080/api/v1/batch -d '{"items":[
  {"id":"h-alpha","rest_wavelength":656.28,"observed_wavelength":675.9684,"hubble_constant":70},
  {"id":"approaching","rest_wavelength":600,"observed_wavelength":500,"hubble_constant":70}
]}'
```
每项独立返回 `ok` + `result` 或结构化 `error`，单项失败不影响其他项。**每条谱线（含失败项）都会和单条接口一样作为一条独立的 `distance` 历史记录落库**，因此可按 `type=distance` 逐条检索；批量接口本身不产生额外的汇总记录。

### `GET /api/v1/history?type=distance&limit=50&offset=0` — 历史查询

按类型（`redshift`/`distance`）过滤，按时间倒序返回持久化的请求与结果。

### `GET /api/v1/demo` — 预置算例

Hα 线（静止 656.28 nm）在 675.9684 nm 被观测（z = 0.03），H₀ = 70 km/s/Mpc。手算：v = c·z = 8993.77 km/s，D = v/H₀ = **128.48 Mpc**，与服务返回值一致。

## 静止波长线表

服务自带一份常见光学发射线表 `builtin_optical_emission`（单位 nm），至少含 Hα 656.28、Hβ 486.13、[OIII] 500.7，并收录氢巴耳末系其余线、[OII]、[OIII] 495.9、[NII]、[SII]、He I 等多条线，使得**单条观测峰对全表会落到多个不同红移**（即单峰必然歧义）。自带表只读。

### `GET /api/v1/catalogs` / `GET /api/v1/catalogs/{id}`

列出自带表与全部自备表，或取回单张表（含每条线的稳定 `id`、`rest_wavelength`、`wavelength_unit`）。

### `POST /api/v1/catalogs` — 登记自备表

自备表持久化、重启后仍在。每张表必须声明唯一 `id` 与全表统一的 `wavelength_unit`；每条线需要稳定 `id` 与正的静止波长。

```bash
curl -X POST localhost:8080/api/v1/catalogs -d '{
  "id":"bench","wavelength_unit":"nm",
  "lines":[{"id":"HI_HA","rest_wavelength":656.28},
           {"id":"HI_HB","rest_wavelength":486.13},
           {"id":"OIII_5007","rest_wavelength":500.7}]}'
```

### `PATCH /api/v1/catalogs/{id}/lines/{lineId}` — 改正一条线的静止波长

只对自备表生效（自带表 400 `builtin_catalog_read_only`）。改动对**下一次**证认生效；已经开始的证认使用当次快照，不受影响。

## 谱线证认

### `POST /api/v1/identify` — 对一组观测峰自动证认

输入同一个天体的一组观测峰（**只有观测波长，没有静止波长、没有红移**），表来源二选一：`catalog_id`（自带/已登记表）或请求体内一次性的 `catalog`（**本次证认专用，绝不与自带表混线**）。观测峰与表必须声明同一 `wavelength_unit`，字面不一致直接拒绝，不做任何隐式换算。

配对规则：把每条峰配到表中**互不相同**的一个身份，使得按 `z = λ_obs/λ_rest − 1` 与默认线性关系 `v = c·z` 算出的各条速度，任意两条之差 **≤ 200 km/s**。证认开始即对所用表做**当次快照**（深拷贝），计算期间表被改动也不影响本次；两张不同的表同时证认各用各的线。

- 唯一解（200，`status:"unique"`）：给出共同红移、每条峰的身份/静止波长/各自红移，并保证身份两两不同。
- 歧义（200，`status:"ambiguous"`）：存在两套让全部峰配对、但共同红移相差超过 200 km/s 的解时，候选全部列出（单峰对多线表属于歧义，不是对不上）。
- 对不上（**422** `no_consistent_match`，带 `report_id`）：不存在让全部峰落入同一窗口的双射，绝不返回凑合的红移。

```bash
curl -X POST localhost:8080/api/v1/identify -d '{
  "wavelength_unit":"nm","catalog_id":"builtin_optical_emission",
  "hubble_constant":70,
  "peaks":[{"observed_wavelength":675.9684},
           {"observed_wavelength":500.7139},
           {"observed_wavelength":515.721}]}'
```
标杆星系（与 `/demo` 同一条，z=0.03）唯一认定共同红移 **0.03**，身份依次为 Hα、Hβ、[OIII]；给 `hubble_constant` 时唯一且非蓝移的结果附带与 `/distance` 完全一致的距离（H₀=70 → **128.48 Mpc**，线性区判定一致）。共同红移为负会标 `blueshift`；此时距离按现有规矩以 `blueshift_distance` 拒绝，不冒充正距离。

### 证认报告与回放

无论 unique / ambiguous / no_match 都落一份可按 id 取回的报告，报告内嵌当次**快照**（用过哪些身份、哪些静止波长）：

- `GET /api/v1/identifications?limit=&offset=` — 报告列表（新→旧，独立于核算历史）。
- `GET /api/v1/identifications/{id}` — 取回报告。
- `POST /api/v1/identifications/{id}/replay` — 仅依据报告快照重算；即使此后表已被改/删，回放仍复现原身份与共同红移。回放不会再写一份报告，也不读线表注册表。

**历史隔离**：证认报告存在独立的表与接口。`GET /api/v1/history`（无论是否带 `type=redshift|distance`）都不会出现证认报告；升级前落下的核算历史按原类型、内容、条数照常筛出。现有的 `/redshift`、`/distance`、`/batch` 只接受已配对输入，行为完全不变。

### `GET /healthz` · `GET /status` — 运行状态

`/status` 返回运行时长、累计请求数、数据库连通性、线性阈值与单位约定，供监控采集。

## 错误响应

所有非法输入返回结构化错误并区分类型：

```json
{"error":{"type":"non_positive_hubble_constant","message":"...","field":"hubble_constant"}}
```

| type | HTTP | 含义 |
|---|---|---|
| `invalid_body` | 400 | 请求体不是合法 JSON |
| `missing_field` | 400 | 缺少必要字段（如 hubble_constant、成对波长缺其一） |
| `non_positive_wavelength` | 400 | 静止/观测波长非正 |
| `non_positive_hubble_constant` | 400 | 哈勃常数非正 |
| `redshift_out_of_range` | 400 | z ≤ −1 或非有限值 |
| `velocity_out_of_range` | 400 | 相对论模式下 \|v\| ≥ c 或非有限值 |
| `blueshift_distance` | 422 | 对蓝移（z < 0）发起距离查询（含证认附带距离） |
| `no_consistent_match` | 422 | 谱线证认：不存在让全部峰落入同一速度窗口的双射（响应带 `report_id`） |
| `empty_peaks` | 400 | 证认峰列表为空（配对前拦截） |
| `missing_peak_field` | 400 | 某条峰缺 `observed_wavelength` |
| `non_positive_observed_wavelength` | 400 | 观测峰波长非正（与核算的 `non_positive_wavelength` 区分） |
| `missing_wavelength_unit` | 400 | 观测峰或线表未声明波长单位 |
| `wavelength_unit_mismatch` | 400 | 观测峰与线表声明的单位不一致 |
| `missing_catalog` | 400 | 证认既没给 `catalog_id` 也没给内联 `catalog` |
| `empty_catalog` | 400 | 线表为空（含内联表） |
| `missing_catalog_id` / `duplicate_line_id` / `missing_line_id` / `non_positive_line_wavelength` | 400 | 登记表结构非法 |
| `catalog_id_conflict` | 400 | 自备表 id 已被占用或占用了自带表保留 id |
| `catalog_not_found` / `line_not_found` / `report_not_found` | 404 | 引用的表/线/报告不存在 |
| `builtin_catalog_read_only` | 400 | 试图改正自带表的线 |
| `internal` | 500 | 服务内部错误 |

## 测试覆盖

`go test ./...`（含 `-race` 验证）覆盖全部关键行为：

- 红移定义：观测波长加倍 → z′ = 2z + 1（而非 2z）；静止波长为分母
- 距离与哈勃常数成反比（H₀ 加倍 → D 减半）
- 零红移 → 零速度、零距离
- 超线性阈值被显式标记并附警告
- 蓝移被标记为 blueshift 并以 422 拒绝，绝不返回距离
- 相对论开关与默认线性关系互不混用（z=1：线性 v=c，相对论 v=0.6c；往返变换自洽）
- 批量计算混合项（成功/蓝移/非法 H₀）各自独立返回
- 历史持久化：请求落库、按类型过滤、倒序
- 批量中每条谱线（含失败项）都作为独立的 `distance` 记录落库，可按类型逐条检索；批量与单条并发混发时历史条数恰好等于核算次数，不丢不重不串
- 并发 64 路请求结果互不串扰、历史记录不重不漏

## 结构

```
cmd/server/          入口：装配存储与 HTTP 服务，优雅退出
internal/calc/       纯计算核心（红移/速度/距离，零依赖，并发安全）
internal/lines/      静止波长线表：自带表、自备表校验、注册表、当次快照
internal/identify/   纯证认引擎：双射配对 + 200 km/s 速度窗 + 歧义聚类（零 I/O）
internal/service/    编排层：核算管线 + 证认（校验/快照/距离附带/报告/回放）
internal/store/      持久化：PostgreSQL（生产）+ 内存（测试），三类数据分表
internal/wiring/     ctx 适配：把带 context 的 Postgres 仓储接到无 ctx 端口
internal/api/        HTTP 处理器、结构化错误、状态端点
Dockerfile           多阶段构建（非 root 运行）
docker-compose.yml   一键启动服务 + PostgreSQL（健康检查、数据卷）
```

存储分三类、互不串表：`computations`（红移/距离核算历史，行为不变）、`line_catalogs`（自备线表）、`identification_reports`（证认报告 + 快照）。

## 证认相关测试（`go test ./...`，含 `-race`）

- 三条标杆峰 675.9684/500.7139/515.721 nm 唯一认定 z=0.03，身份为 Hα/Hβ/[OIII]，各线 z=0.03
- 证认得到的"静止+观测"配对回丢现有 `/redshift` 仍得 0.03；H₀=70 距离与 `/distance`、`/demo` 一致（约 128.48 Mpc，线性区判定一致）
- 单峰 675.9684 对多线表报歧义并列出候选（含 Hα@z=0.03），不偷偷唯一化
- 把 500.7139 改成 520 三峰同送 → 422 结构化对不上，仍留可查报告
- 证认报告不出现在 `/history`（不带类型过滤也不出现），按类型计数不变
- 证认进行中改表不影响当次快照；下一次证认才用新表
- 按报告回放，表被改掉之后仍复现原身份与 z=0.03（含 JSONB 往返模拟重启）
- 两张不同的（内联）表并发证认互不渗线，内联表不落注册表
- 蓝移证认可标识，但附带距离按 `blueshift_distance` 拒绝
- 原有全部行为保持：红移定义、距离反比、零红移、线性阈值、相对论开关、批量逐条落库、历史分页新旧顺序、预置算例、并发不串扰

